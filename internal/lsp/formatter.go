package lsp

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.lsp.dev/protocol"
)

//go:embed formatter_server.exs
var formatterScript string

type formatterProcess struct {
	cmd            *commandHandle
	stdin          io.WriteCloser
	stdout         io.ReadCloser
	mu             sync.Mutex
	formatterMtime time.Time // mtime of .formatter.exs when process started
}

// commandHandle wraps the process so we can check liveness.
type commandHandle struct {
	process *os.Process
	done    chan struct{}
}

func (fp *formatterProcess) alive() bool {
	select {
	case <-fp.cmd.done:
		return false
	default:
		return true
	}
}

func (fp *formatterProcess) Format(content, filename string) (string, error) {
	fp.mu.Lock()
	defer fp.mu.Unlock()

	// Build the entire request as a single buffer to avoid partial writes
	filenameBytes := []byte(filename)
	contentBytes := []byte(content)
	var req bytes.Buffer
	_ = binary.Write(&req, binary.BigEndian, uint16(len(filenameBytes)))
	req.Write(filenameBytes)
	_ = binary.Write(&req, binary.BigEndian, uint32(len(contentBytes)))
	req.Write(contentBytes)
	if _, err := fp.stdin.Write(req.Bytes()); err != nil {
		return "", fmt.Errorf("write request: %w", err)
	}

	var status byte
	if err := binary.Read(fp.stdout, binary.BigEndian, &status); err != nil {
		return "", fmt.Errorf("read status: %w", err)
	}
	var respLen uint32
	if err := binary.Read(fp.stdout, binary.BigEndian, &respLen); err != nil {
		return "", fmt.Errorf("read length: %w", err)
	}
	buf := make([]byte, respLen)
	if _, err := io.ReadFull(fp.stdout, buf); err != nil {
		return "", fmt.Errorf("read data: %w", err)
	}

	if status != 0 {
		return "", &FormatError{Message: string(buf)}
	}
	return string(buf), nil
}

// FormatError represents a formatting failure (e.g. syntax error in the source).
// The persistent process is still alive — this is not a protocol/crash error.
type FormatError struct {
	Message string
}

func (e *FormatError) Error() string {
	return e.Message
}

func (fp *formatterProcess) Close() {
	_ = fp.stdin.Close()
	_ = fp.cmd.process.Kill()
}

func (s *Server) startFormatterProcess(mixRoot, formatterExs string) (*formatterProcess, error) {
	scriptDir := filepath.Join(os.TempDir(), "dexter")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		return nil, fmt.Errorf("create script dir: %w", err)
	}
	scriptPath := filepath.Join(scriptDir, "formatter_server.exs")
	if existing, err := os.ReadFile(scriptPath); err != nil || string(existing) != formatterScript {
		if err := os.WriteFile(scriptPath, []byte(formatterScript), 0644); err != nil {
			return nil, fmt.Errorf("write formatter script: %w", err)
		}
	}

	var mtime time.Time
	if info, err := os.Stat(formatterExs); err == nil {
		mtime = info.ModTime()
	}

	elixirBin := filepath.Join(filepath.Dir(s.mixBin), "elixir")
	cmd := exec.Command(elixirBin, scriptPath, mixRoot, formatterExs)
	cmd.Dir = mixRoot

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start formatter: %w", err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	handle := &commandHandle{process: cmd.Process, done: done}

	// Wait for ready signal with a timeout — if the BEAM doesn't respond
	// within 10s, it's not going to.
	type readyResult struct {
		status byte
		err    error
	}
	readyCh := make(chan readyResult, 1)
	go func() {
		var status byte
		if err := binary.Read(stdout, binary.BigEndian, &status); err != nil {
			readyCh <- readyResult{err: err}
			return
		}
		var readyLen uint32
		if err := binary.Read(stdout, binary.BigEndian, &readyLen); err != nil {
			readyCh <- readyResult{err: err}
			return
		}
		readyCh <- readyResult{status: status}
	}()

	select {
	case r := <-readyCh:
		if r.err != nil {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("formatter ready: %w", r.err)
		}
		if r.status != 0 {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("formatter failed to initialize (status %d)", r.status)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("formatter startup timed out")
	}

	log.Printf("Formatter: started persistent process for %s (pid %d)", formatterExs, cmd.Process.Pid)

	return &formatterProcess{
		cmd:            handle,
		stdin:          stdin,
		stdout:         stdout,
		formatterMtime: mtime,
	}, nil
}

func (s *Server) getFormatter(mixRoot, formatterExs string) (*formatterProcess, error) {
	s.formattersMu.Lock()
	defer s.formattersMu.Unlock()

	if fp, ok := s.formatters[formatterExs]; ok && fp.alive() {
		// Restart if .formatter.exs has changed
		if info, err := os.Stat(formatterExs); err == nil && info.ModTime().After(fp.formatterMtime) {
			fp.Close()
			delete(s.formatters, formatterExs)
		} else {
			return fp, nil
		}
	}

	fp, err := s.startFormatterProcess(mixRoot, formatterExs)
	if err != nil {
		return nil, err
	}
	if s.formatters == nil {
		s.formatters = make(map[string]*formatterProcess)
	}
	s.formatters[formatterExs] = fp
	return fp, nil
}

// findFormatterConfig walks from the file's directory up to the mix root,
// returning the path to the nearest .formatter.exs. This handles subdirectory
// configs (e.g. config/.formatter.exs with different rules than the root).
func findFormatterConfig(filePath, mixRoot string) string {
	dir := filepath.Dir(filePath)
	for {
		candidate := filepath.Join(dir, ".formatter.exs")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		if dir == mixRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join(mixRoot, ".formatter.exs")
}

// formatContent tries the persistent formatter, falling back to mix format.
func (s *Server) formatContent(mixRoot, path, content string) (string, error) {
	formatterExs := findFormatterConfig(path, mixRoot)
	fp, err := s.getFormatter(mixRoot, formatterExs)
	if err != nil {
		log.Printf("Formatting: persistent formatter unavailable, falling back to mix format: %v", err)
		return s.formatWithMixFormat(mixRoot, path, content)
	}

	start := time.Now()
	result, err := fp.Format(content, path)
	if err != nil {
		var formatErr *FormatError
		if errors.As(err, &formatErr) {
			// Source code error (e.g. syntax error) — process is still alive
			log.Printf("Formatting: %s failed: %s", path, formatErr.Message)
			return "", err
		}
		// Process likely died — evict and fall back
		s.formattersMu.Lock()
		delete(s.formatters, formatterExs)
		s.formattersMu.Unlock()
		fp.Close()
		log.Printf("Formatting: persistent formatter failed, falling back to mix format: %v", err)
		return s.formatWithMixFormat(mixRoot, path, content)
	}

	log.Printf("Formatting: %s (%s, persistent)", path, time.Since(start))
	return result, nil
}

func (s *Server) formatWithMixFormat(mixRoot, path, content string) (string, error) {
	if s.mixBin == "" {
		return "", fmt.Errorf("mix binary not found")
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := s.mixCommand(ctx, mixRoot, "format", "--stdin-filename", path, "-")
	cmd.Stdin = strings.NewReader(content)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Printf("Formatting: mix format failed for %s (%s): %v\n%s", path, time.Since(start), err, stderr.String())
		return "", err
	}
	log.Printf("Formatting: %s (%s, mix format)", path, time.Since(start))
	return stdout.String(), nil
}

// parseFormatError extracts line, column, and a clean message from an Elixir
// formatter error. Example input:
//
//	token missing on lib/foo.ex:246:4:\n     error: missing terminator: end
var formatErrorLineCol = regexp.MustCompile(`:(\d+):(\d+)`)
var formatErrorHintLine = regexp.MustCompile(`on line (\d+)`)

type formatErrorInfo struct {
	line, col uint32
	message   string
	hintLine  uint32 // 0 if no hint
	hint      string
}

func parseFormatError(msg string) formatErrorInfo {
	var info formatErrorInfo

	if m := formatErrorLineCol.FindStringSubmatch(msg); m != nil {
		if l, err := strconv.ParseUint(m[1], 10, 32); err == nil {
			info.line = uint32(l)
		}
		if c, err := strconv.ParseUint(m[2], 10, 32); err == nil {
			info.col = uint32(c)
		}
	}

	for _, part := range strings.Split(msg, "\n") {
		trimmed := strings.TrimSpace(part)
		if strings.HasPrefix(trimmed, "error:") && info.message == "" {
			info.message = trimmed
		}
		if strings.HasPrefix(trimmed, "hint:") {
			info.hint = trimmed
			if m := formatErrorHintLine.FindStringSubmatch(trimmed); m != nil {
				if l, err := strconv.ParseUint(m[1], 10, 32); err == nil {
					info.hintLine = uint32(l)
				}
			}
		}
	}

	if info.message == "" {
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			info.message = msg[:i]
		} else {
			info.message = msg
		}
	}
	return info
}

func (s *Server) publishFormatDiagnostic(uri protocol.DocumentURI, formatErr *FormatError) {
	if s.client == nil {
		return
	}
	info := parseFormatError(formatErr.Message)

	// LSP lines/cols are 0-based, Elixir's are 1-based
	line := info.line
	col := info.col
	if line > 0 {
		line--
	}
	if col > 0 {
		col--
	}

	diagnostics := []protocol.Diagnostic{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: line, Character: col},
				End:   protocol.Position{Line: line, Character: col},
			},
			Severity: protocol.DiagnosticSeverityError,
			Source:   "dexter",
			Message:  info.message,
		},
	}

	if info.hintLine > 0 {
		hintLine := info.hintLine - 1
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: hintLine, Character: 0},
				End:   protocol.Position{Line: hintLine, Character: 0},
			},
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "dexter",
			Message:  info.hint,
		})
	}

	_ = s.client.PublishDiagnostics(context.Background(), &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
	})
}

func (s *Server) clearFormatDiagnostics(uri protocol.DocumentURI) {
	if s.client == nil {
		return
	}
	_ = s.client.PublishDiagnostics(context.Background(), &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: []protocol.Diagnostic{},
	})
}

func (s *Server) closeFormatters() {
	s.formattersMu.Lock()
	defer s.formattersMu.Unlock()
	for _, fp := range s.formatters {
		fp.Close()
	}
	s.formatters = nil
}
