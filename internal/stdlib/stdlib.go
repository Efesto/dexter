package stdlib

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DetectElixirLibRoot tries to find the Elixir app directory that contains
// the standard library sources (e.g. .../lib/elixir).
//
// It first locates the `elixir` executable in PATH (symlink-resolved) and
// derives a likely install root (common with asdf/mise). If that fails, it
// shells out to `elixir` to ask the runtime where the :elixir application is
// installed.
//
// Returns ("", false) if Elixir isn't installed or sources aren't present.
func DetectElixirLibRoot() (string, bool) {
	// Allow overriding for non-standard installs / CI.
	if v := strings.TrimSpace(os.Getenv("DEXTER_ELIXIR_LIB_ROOT")); v != "" {
		if dirLooksLikeElixirLibRoot(v) {
			return v, true
		}
	}

	// Try to derive from the elixir executable location.
	if exeRoot, ok := deriveFromElixirExecutable(); ok {
		return exeRoot, true
	}

	// :code.lib_dir(:elixir) returns the application directory for Elixir.
	// On common installs this is already ".../lib/elixir".
	out, ok := runElixir(`IO.puts(:code.lib_dir(:elixir) |> to_string())`)
	if ok {
		candidate := strings.TrimSpace(out)
		if dirLooksLikeElixirLibRoot(candidate) {
			return candidate, true
		}
		// Some environments may return an ebin path; normalize if needed.
		if root := normalizeFromEbin(candidate); root != "" && dirLooksLikeElixirLibRoot(root) {
			return root, true
		}
	}

	return "", false
}

func deriveFromElixirExecutable() (string, bool) {
	exe, err := exec.LookPath("elixir")
	if err != nil || exe == "" {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err == nil && resolved != "" {
		exe = resolved
	}
	// Common layout: <prefix>/bin/elixir and <prefix>/lib/elixir/lib/*.ex
	prefix := filepath.Clean(filepath.Join(filepath.Dir(exe), ".."))
	candidate := filepath.Join(prefix, "lib", "elixir")
	if dirLooksLikeElixirLibRoot(candidate) {
		return candidate, true
	}
	return "", false
}

func runElixir(expr string) (string, bool) {
	cmd := exec.Command("elixir", "-e", expr)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return stdout.String(), true
}

func normalizeFromEbin(p string) string {
	clean := filepath.Clean(strings.TrimSpace(p))
	if clean == "" {
		return ""
	}
	if filepath.Base(clean) == "ebin" {
		return filepath.Dir(clean)
	}
	return ""
}

func dirLooksLikeElixirLibRoot(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	// A cheap signal that sources exist.
	if _, err := os.Stat(filepath.Join(dir, "lib", "enum.ex")); err == nil {
		return true
	}
	return false
}

