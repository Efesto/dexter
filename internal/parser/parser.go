package parser

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Shared regex patterns used by both the parser and the LSP.
var (
	AliasRe   = regexp.MustCompile(`^\s*alias\s+([A-Za-z0-9_.]+)`)
	AliasAsRe = regexp.MustCompile(`^\s*alias\s+([A-Za-z0-9_.]+)\s*,\s*as:\s*([A-Za-z0-9_]+)`)
	FuncDefRe = regexp.MustCompile(`^\s*(defp?|defmacrop?|defguardp?|defdelegate)\s+([a-z_][a-z0-9_?!]*)[\s(,]`)
	TypeDefRe = regexp.MustCompile(`^\s*@(typep?|opaque)\s+([a-z_][a-z0-9_?!]*)`)
)

var (
	DefmoduleRe    = regexp.MustCompile(`^\s*defmodule\s+([A-Za-z0-9_.]+)\s+do`)
	delegateToRe   = regexp.MustCompile(`to:\s*([A-Za-z0-9_.]+)`)
	delegateAsRe   = regexp.MustCompile(`as:\s*:?([a-z_][a-z0-9_?!]*)`)
	newStatementRe = regexp.MustCompile(`^\s*(defdelegate|defp?|defmacrop?|defguardp?|alias|import|@|end)\b`)
)

type Definition struct {
	Module     string
	Function   string
	Arity      int
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

	type moduleFrame struct {
		name   string
		indent int // leading whitespace count when defmodule was found
	}

	lines := strings.Split(string(data), "\n")
	var defs []Definition
	var moduleStack []moduleFrame
	aliases := map[string]string{} // short name -> full module
	inHeredoc := false

	for lineIdx, line := range lines {
		lineNum := lineIdx + 1

		// Heredoc tracking — only scan for """ if line contains a double-quote
		if strings.IndexByte(line, '"') >= 0 {
			quoteCount := strings.Count(line, `"""`)
			if quoteCount > 0 {
				if quoteCount >= 2 {
					continue
				}
				inHeredoc = !inHeredoc
				continue
			}
		}

		if inHeredoc {
			continue
		}

		// Find first non-whitespace character for fast pre-filtering.
		// All patterns we match start with a keyword: def*, alias, @type, or end.
		trimStart := 0
		for trimStart < len(line) && (line[trimStart] == ' ' || line[trimStart] == '\t') {
			trimStart++
		}
		if trimStart >= len(line) {
			continue
		}
		first := line[trimStart]
		rest := line[trimStart:] // line content from first non-whitespace char

		// 'e' — check for "end" to pop module stack
		if first == 'e' {
			if len(moduleStack) > 0 && strings.TrimRight(rest, " \t\r") == "end" {
				if moduleStack[len(moduleStack)-1].indent == trimStart {
					moduleStack = moduleStack[:len(moduleStack)-1]
				}
			}
			continue
		}

		// Skip lines that can't match any pattern we care about
		if first != 'a' && first != 'd' && first != '@' {
			continue
		}

		currentModule := ""
		if len(moduleStack) > 0 {
			currentModule = moduleStack[len(moduleStack)-1].name
		}

		// 'a' — alias tracking
		if first == 'a' {
			if !strings.HasPrefix(rest, "alias") || (len(rest) > 5 && rest[5] != ' ' && rest[5] != '\t') {
				continue
			}
			resolveModule := func(s string) string {
				if currentModule != "" {
					return strings.ReplaceAll(s, "__MODULE__", currentModule)
				}
				return s
			}
			afterAlias := strings.TrimLeft(rest[5:], " \t")
			moduleName := scanModuleName(afterAlias)
			if moduleName == "" {
				continue
			}
			// Check for ", as:" pattern
			remaining := afterAlias[len(moduleName):]
			remaining = strings.TrimLeft(remaining, " \t")
			if strings.HasPrefix(remaining, ", as:") || strings.HasPrefix(remaining, ",as:") {
				asStr := remaining[strings.Index(remaining, "as:")+3:]
				asStr = strings.TrimLeft(asStr, " \t")
				asName := scanIdentifier(asStr)
				if asName != "" {
					resolved := resolveModule(moduleName)
					if !strings.Contains(resolved, "__MODULE__") {
						aliases[asName] = resolved
					}
				}
			} else {
				resolved := resolveModule(moduleName)
				parts := strings.Split(resolved, ".")
				shortName := parts[len(parts)-1]
				aliases[shortName] = resolved
			}
			continue
		}

		// '@' — type definitions (@type, @typep, @opaque)
		if first == '@' {
			if currentModule == "" {
				continue
			}
			var kind string
			var afterKw string
			if strings.HasPrefix(rest, "@typep") && len(rest) > 6 && (rest[6] == ' ' || rest[6] == '\t') {
				kind = "typep"
				afterKw = strings.TrimLeft(rest[6:], " \t")
			} else if strings.HasPrefix(rest, "@type") && len(rest) > 5 && (rest[5] == ' ' || rest[5] == '\t') {
				kind = "type"
				afterKw = strings.TrimLeft(rest[5:], " \t")
			} else if strings.HasPrefix(rest, "@opaque") && len(rest) > 7 && (rest[7] == ' ' || rest[7] == '\t') {
				kind = "opaque"
				afterKw = strings.TrimLeft(rest[7:], " \t")
			} else {
				continue
			}
			name := scanFuncName(afterKw)
			if name != "" {
				defs = append(defs, Definition{
					Module:   currentModule,
					Function: name,
					Arity:    ExtractArity(line, name),
					Line:     lineNum,
					FilePath: path,
					Kind:     kind,
				})
			}
			continue
		}

		// 'd' — defmodule, defprotocol, defimpl, def*, defstruct, defexception
		if !strings.HasPrefix(rest, "def") {
			continue
		}

		if name, ok := scanDefKeyword(rest, "defmodule"); ok {
			if !strings.Contains(name, ".") && currentModule != "" {
				name = currentModule + "." + name
			}
			currentModule = name
			moduleStack = append(moduleStack, moduleFrame{name: currentModule, indent: trimStart})
			defs = append(defs, Definition{
				Module:   currentModule,
				Line:     lineNum,
				FilePath: path,
				Kind:     "module",
			})
			continue
		}

		if name, ok := scanDefKeyword(rest, "defprotocol"); ok {
			currentModule = name
			moduleStack = append(moduleStack, moduleFrame{name: currentModule, indent: trimStart})
			defs = append(defs, Definition{
				Module:   currentModule,
				Line:     lineNum,
				FilePath: path,
				Kind:     "defprotocol",
			})
			continue
		}

		if name, ok := scanDefKeyword(rest, "defimpl"); ok {
			currentModule = name
			moduleStack = append(moduleStack, moduleFrame{name: currentModule, indent: trimStart})
			defs = append(defs, Definition{
				Module:   currentModule,
				Line:     lineNum,
				FilePath: path,
				Kind:     "defimpl",
			})
			continue
		}

		if currentModule != "" {
			if kind, funcName, ok := scanFuncDef(rest); ok {
				def := Definition{
					Module:   currentModule,
					Function: funcName,
					Arity:    ExtractArity(line, funcName),
					Line:     lineNum,
					FilePath: path,
					Kind:     kind,
				}
				if kind == "defdelegate" {
					def.DelegateTo, def.DelegateAs = findDelegateToAndAs(lines, lineIdx, aliases, currentModule)
				}
				defs = append(defs, def)
				continue
			}

			if strings.HasPrefix(rest, "defstruct ") || strings.HasPrefix(rest, "defstruct\t") {
				defs = append(defs, Definition{
					Module:   currentModule,
					Function: "__struct__",
					Line:     lineNum,
					FilePath: path,
					Kind:     "defstruct",
				})
			}
			if strings.HasPrefix(rest, "defexception ") || strings.HasPrefix(rest, "defexception\t") {
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

// scanModuleName reads a module name ([A-Za-z0-9_.]+) from the start of s.
func scanModuleName(s string) string {
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '.' {
			i++
		} else {
			break
		}
	}
	if i == 0 {
		return ""
	}
	return s[:i]
}

// scanFuncName reads a function/type name ([a-z_][a-z0-9_?!]*) from the start of s.
func scanFuncName(s string) string {
	if len(s) == 0 {
		return ""
	}
	c := s[0]
	if (c < 'a' || c > 'z') && c != '_' {
		return ""
	}
	i := 1
	for i < len(s) {
		c = s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '?' || c == '!' {
			i++
		} else {
			break
		}
	}
	return s[:i]
}

// scanIdentifier reads an identifier ([A-Za-z0-9_]+) from the start of s.
func scanIdentifier(s string) string {
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			i++
		} else {
			break
		}
	}
	if i == 0 {
		return ""
	}
	return s[:i]
}

// scanDefKeyword checks if rest starts with keyword (e.g. "defmodule") followed
// by whitespace and a module name. For defmodule/defprotocol, requires " do" after.
func scanDefKeyword(rest, keyword string) (string, bool) {
	if !strings.HasPrefix(rest, keyword) {
		return "", false
	}
	after := rest[len(keyword):]
	if len(after) == 0 || (after[0] != ' ' && after[0] != '\t') {
		return "", false
	}
	after = strings.TrimLeft(after, " \t")
	name := scanModuleName(after)
	if name == "" {
		return "", false
	}
	if keyword == "defimpl" {
		return name, true
	}
	remaining := strings.TrimLeft(after[len(name):], " \t")
	if remaining == "do" || strings.HasPrefix(remaining, "do ") || strings.HasPrefix(remaining, "do\t") || strings.HasPrefix(remaining, "do\r") {
		return name, true
	}
	return "", false
}

// funcDefKeywords is ordered longest-first to avoid prefix ambiguity
// (e.g. "defmacrop" before "defmacro", "defp" before "def").
var funcDefKeywords = []string{
	"defdelegate",
	"defmacrop",
	"defmacro",
	"defguardp",
	"defguard",
	"defp",
	"def",
}

// scanFuncDef checks if rest matches a function definition keyword followed by
// whitespace and a function name. Returns the kind, name, and true if matched.
func scanFuncDef(rest string) (string, string, bool) {
	for _, kw := range funcDefKeywords {
		if !strings.HasPrefix(rest, kw) {
			continue
		}
		after := rest[len(kw):]
		// Must be followed by whitespace
		if len(after) == 0 || (after[0] != ' ' && after[0] != '\t') {
			continue
		}
		after = strings.TrimLeft(after, " \t")
		name := scanFuncName(after)
		if name == "" {
			continue
		}
		// Verify next char is whitespace, '(', or ','
		afterName := after[len(name):]
		if len(afterName) > 0 {
			c := afterName[0]
			if c != ' ' && c != '\t' && c != '(' && c != ',' && c != '\n' && c != '\r' {
				continue
			}
		}
		return kw, name, true
	}
	return "", "", false
}

// findDelegateTo searches the current line and up to 5 subsequent lines for a to: target,
// then resolves it via aliases.
func findDelegateToAndAs(lines []string, startIdx int, aliases map[string]string, currentModule string) (string, string) {
	end := startIdx + 6
	if end > len(lines) {
		end = len(lines)
	}

	var targetModule, targetFunc string
	for i := startIdx; i < end; i++ {
		// A new statement on any line after the first means the current defdelegate ended
		if i > startIdx && newStatementRe.MatchString(lines[i]) {
			break
		}
		if m := delegateToRe.FindStringSubmatch(lines[i]); m != nil && targetModule == "" {
			target := m[1]
			// Resolve __MODULE__ directly in to: field
			if currentModule != "" {
				target = strings.ReplaceAll(target, "__MODULE__", currentModule)
			}
			if resolved, ok := aliases[target]; ok {
				// Exact alias match: "to: Services" where Services -> MyApp.HRIS.Services
				targetModule = resolved
			} else if parts := strings.SplitN(target, ".", 2); len(parts) == 2 {
				// Partial alias: "to: Services.Foo" where Services -> MyApp.HRIS.Services
				if resolved, ok := aliases[parts[0]]; ok {
					targetModule = resolved + "." + parts[1]
				} else {
					targetModule = target
				}
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

// ExtractArity counts the number of arguments in a function definition line.
// It finds the first parenthesized argument list after the function name and
// counts top-level commas, respecting nested parens/brackets/braces.
func ExtractArity(line string, funcName string) int {
	idx := strings.Index(line, funcName)
	if idx < 0 {
		return 0
	}
	rest := line[idx+len(funcName):]

	parenIdx := strings.IndexByte(rest, '(')
	if parenIdx < 0 {
		return 0
	}

	depth := 1
	commas := 0
	hasContent := false
	for _, ch := range rest[parenIdx+1:] {
		switch ch {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
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
		}
		if depth == 1 && ch != ' ' && ch != '\t' && ch != '\n' {
			hasContent = true
		}
	}

	if hasContent {
		return commas + 1
	}
	return 0
}

func IsElixirFile(path string) bool {
	extension := filepath.Ext(path)
	return extension == ".ex" || extension == ".exs"
}

// WalkElixirFiles walks root, skipping _build/.git/node_modules directories,
// and calls fn for each .ex/.exs file found.
func WalkElixirFiles(root string, fn func(path string, d fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == "_build" || base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !IsElixirFile(path) {
			return nil
		}
		return fn(path, d)
	})
}
