package lsp

import (
	"regexp"
	"strings"
	"unicode"
)

// ExtractExpression returns the full dotted expression around the cursor position.
// Line is the text content, col is 0-based character offset.
// For example, on "  Foo.Bar.baz(123)" with col=9, returns "Foo.Bar.baz".
func ExtractExpression(line string, col int) string {
	if len(line) == 0 {
		return ""
	}
	if col >= len(line) {
		col = len(line) - 1
	}
	if col < 0 {
		col = 0
	}

	isExprChar := func(b byte) bool {
		c := rune(b)
		return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '.' || c == '?' || c == '!'
	}

	// If cursor is not on an expression character, return empty
	if !isExprChar(line[col]) {
		return ""
	}

	start := col
	for start > 0 && isExprChar(line[start-1]) {
		start--
	}

	end := col
	for end+1 < len(line) && isExprChar(line[end+1]) {
		end++
	}

	return line[start : end+1]
}

// ExtractModuleAndFunction splits a dotted expression into module reference and optional function name.
// Uppercase-starting parts are module segments, the first lowercase part is the function.
// Returns ("Foo.Bar", "baz") for "Foo.Bar.baz", ("Foo.Bar.Baz", "") for "Foo.Bar.Baz",
// ("", "do_something") for "do_something".
func ExtractModuleAndFunction(expr string) (moduleRef string, functionName string) {
	var moduleParts []string
	for _, part := range strings.Split(expr, ".") {
		if len(part) == 0 {
			continue
		}
		if unicode.IsUpper(rune(part[0])) {
			moduleParts = append(moduleParts, part)
		} else {
			functionName = part
			break
		}
	}
	if len(moduleParts) > 0 {
		moduleRef = strings.Join(moduleParts, ".")
	}
	return
}

var (
	aliasAsRe    = regexp.MustCompile(`^\s*alias\s+([A-Za-z0-9_.]+)\s*,\s*as:\s*([A-Za-z0-9_]+)`)
	aliasMultiRe = regexp.MustCompile(`^\s*alias\s+([A-Za-z0-9_.]+)\.{([^}]+)}`)
	aliasSimpleRe = regexp.MustCompile(`^\s*alias\s+([A-Za-z0-9_.]+)`)
	importRe     = regexp.MustCompile(`^\s*import\s+([A-Za-z0-9_.]+)`)
	funcDefRe    = regexp.MustCompile(`^\s*(defp?|defmacrop?|defguardp?|defdelegate)\s+([a-z_][a-z0-9_?!]*)[\s(,]`)
)

// ExtractAliases parses all alias declarations from document text.
// Returns a map of short name -> full module name.
// Handles: "alias A.B.C", "alias A.B.C, as: D", "alias A.B.{C, D}".
func ExtractAliases(text string) map[string]string {
	aliases := make(map[string]string)
	for _, line := range strings.Split(text, "\n") {
		// alias A.B.C, as: D
		if m := aliasAsRe.FindStringSubmatch(line); m != nil {
			aliases[m[2]] = m[1]
			continue
		}
		// alias A.B.{C, D, E}
		if m := aliasMultiRe.FindStringSubmatch(line); m != nil {
			base := m[1]
			for _, name := range strings.Split(m[2], ",") {
				name = strings.TrimSpace(name)
				if len(name) > 0 && unicode.IsUpper(rune(name[0])) {
					aliases[name] = base + "." + name
				}
			}
			continue
		}
		// alias A.B.C
		if m := aliasSimpleRe.FindStringSubmatch(line); m != nil {
			fullMod := m[1]
			parts := strings.Split(fullMod, ".")
			shortName := parts[len(parts)-1]
			aliases[shortName] = fullMod
			continue
		}
	}
	return aliases
}

// ExtractImports parses all import declarations from document text.
// Returns a slice of full module names.
func ExtractImports(text string) []string {
	var imports []string
	for _, line := range strings.Split(text, "\n") {
		if m := importRe.FindStringSubmatch(line); m != nil {
			imports = append(imports, m[1])
		}
	}
	return imports
}

// FindFunctionDefinition searches the document text for a def/defp/defmacro/defmacrop
// matching the given function name. Returns the 1-based line number and true if found.
func FindFunctionDefinition(text string, functionName string) (int, bool) {
	for i, line := range strings.Split(text, "\n") {
		if m := funcDefRe.FindStringSubmatch(line); m != nil {
			if m[2] == functionName {
				return i + 1, true
			}
		}
	}
	return 0, false
}
