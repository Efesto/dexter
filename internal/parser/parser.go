package parser

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	defmoduleRe    = regexp.MustCompile(`^\s*defmodule\s+([A-Za-z0-9_.]+)\s+do`)
	defRe          = regexp.MustCompile(`^\s*(defp?|defmacrop?|defguardp?|defdelegate)\s+([a-z_][a-z0-9_?!]*)\s*[\(|,|do|\s]`)
	defprotocolRe  = regexp.MustCompile(`^\s*defprotocol\s+([A-Za-z0-9_.]+)\s+do`)
	defimplRe      = regexp.MustCompile(`^\s*defimpl\s+([A-Za-z0-9_.]+)`)
	defstructRe    = regexp.MustCompile(`^\s*defstruct\s`)
	defexceptionRe = regexp.MustCompile(`^\s*defexception\s`)
	aliasRe        = regexp.MustCompile(`^\s*alias\s+([A-Za-z0-9_.]+)`)
	aliasAsRe      = regexp.MustCompile(`^\s*alias\s+([A-Za-z0-9_.]+)\s*,\s*as:\s*([A-Za-z0-9_]+)`)
	delegateToRe   = regexp.MustCompile(`to:\s*([A-Za-z0-9_.]+)`)
	delegateAsRe   = regexp.MustCompile(`as:\s*:?([a-z_][a-z0-9_?!]*)`)
)

type Definition struct {
	Module     string
	Function   string
	Line       int
	FilePath   string
	Kind       string
	DelegateTo string
	DelegateAs string // for defdelegate with as: — the function name in the target module
}

func ParseFile(path string) ([]Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	var defs []Definition
	var moduleStack []string
	aliases := map[string]string{} // short name -> full module
	inHeredoc := false

	for lineIdx, line := range lines {
		lineNum := lineIdx + 1
		trimmed := strings.TrimSpace(line)

		quoteCount := strings.Count(line, `"""`)
		if quoteCount > 0 {
			if quoteCount >= 2 {
				continue
			}
			inHeredoc = !inHeredoc
			continue
		}

		if inHeredoc {
			continue
		}

		if trimmed == "end" && len(moduleStack) > 1 {
			moduleStack = moduleStack[:len(moduleStack)-1]
		}

		currentModule := ""
		if len(moduleStack) > 0 {
			currentModule = moduleStack[len(moduleStack)-1]
		}

		// Track aliases
		if m := aliasAsRe.FindStringSubmatch(line); m != nil {
			aliases[m[2]] = m[1]
		} else if m := aliasRe.FindStringSubmatch(line); m != nil {
			parts := strings.Split(m[1], ".")
			shortName := parts[len(parts)-1]
			aliases[shortName] = m[1]
		}

		if m := defmoduleRe.FindStringSubmatch(line); m != nil {
			currentModule = m[1]
			moduleStack = append(moduleStack, currentModule)
			defs = append(defs, Definition{
				Module:   currentModule,
				Line:     lineNum,
				FilePath: path,
				Kind:     "module",
			})
			continue
		}

		if m := defprotocolRe.FindStringSubmatch(line); m != nil {
			currentModule = m[1]
			moduleStack = append(moduleStack, currentModule)
			defs = append(defs, Definition{
				Module:   currentModule,
				Line:     lineNum,
				FilePath: path,
				Kind:     "defprotocol",
			})
			continue
		}

		if m := defimplRe.FindStringSubmatch(line); m != nil {
			currentModule = m[1]
			moduleStack = append(moduleStack, currentModule)
			defs = append(defs, Definition{
				Module:   currentModule,
				Line:     lineNum,
				FilePath: path,
				Kind:     "defimpl",
			})
			continue
		}

		if currentModule != "" {
			if m := defRe.FindStringSubmatch(line); m != nil {
				kind := m[1]
				funcName := m[2]
				def := Definition{
					Module:   currentModule,
					Function: funcName,
					Line:     lineNum,
					FilePath: path,
					Kind:     kind,
				}
				if kind == "defdelegate" {
					def.DelegateTo, def.DelegateAs = findDelegateToAndAs(lines, lineIdx, aliases)
				}
				defs = append(defs, def)
				continue
			}

			if defstructRe.MatchString(line) {
				defs = append(defs, Definition{
					Module:   currentModule,
					Function: "__struct__",
					Line:     lineNum,
					FilePath: path,
					Kind:     "defstruct",
				})
			}
			if defexceptionRe.MatchString(line) {
				defs = append(defs, Definition{
					Module:   currentModule,
					Function: "__exception__",
					Line:     lineNum,
					FilePath: path,
					Kind:     "defexception",
				})
			}
		}
	}

	return defs, nil
}

// findDelegateTo searches the current line and up to 5 subsequent lines for a to: target,
// then resolves it via aliases.
func findDelegateToAndAs(lines []string, startIdx int, aliases map[string]string) (string, string) {
	end := startIdx + 6
	if end > len(lines) {
		end = len(lines)
	}

	var targetModule, targetFunc string
	for i := startIdx; i < end; i++ {
		if m := delegateToRe.FindStringSubmatch(lines[i]); m != nil && targetModule == "" {
			target := m[1]
			if resolved, ok := aliases[target]; ok {
				targetModule = resolved
			} else {
				targetModule = target
			}
		}
		if m := delegateAsRe.FindStringSubmatch(lines[i]); m != nil && targetFunc == "" {
			targetFunc = m[1]
		}
	}
	return targetModule, targetFunc
}

func IsElixirFile(path string) bool {
	extension := filepath.Ext(path)
	return extension == ".ex" || extension == ".exs"
}

func countArity(line string, funcName string) int {
	idx := strings.Index(line, funcName)
	if idx == -1 {
		return 0
	}
	rest := line[idx+len(funcName):]
	rest = strings.TrimSpace(rest)

	if len(rest) == 0 || rest[0] != '(' {
		return 0
	}

	depth := 0
	commas := 0
	hasContent := false
	for _, ch := range rest {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				if hasContent {
					return commas + 1
				}
				return 0
			}
		case ',':
			if depth == 1 {
				commas++
			}
		default:
			if depth == 1 && ch != ' ' && ch != '\t' {
				hasContent = true
			}
		}
	}
	return 0
}
