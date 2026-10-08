// Package clipimg saves an image from the system clipboard to a temporary PNG
// file, and recognises pasted text that is really an image file path.
//
// The platform clipboard tools are run as child processes with a timeout.
// The per-OS files choose the tool and interpret its exit code.
package clipimg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNoImage means the clipboard holds no image (or no image the tool can read).
var ErrNoImage = errors.New("no image on the clipboard")

// ErrUnavailable means no supported clipboard tool is installed.
var ErrUnavailable = errors.New("no clipboard image tool found")

// clipTimeout bounds one clipboard read, tool start-up included.
const clipTimeout = 10 * time.Second

// invocation describes one clipboard tool run.
type invocation struct {
	name string
	args []string
	// env holds extra KEY=VALUE entries appended to the current environment.
	env []string
	// dest is the file the image is written to. When toStdout is set the
	// tool writes to stdout and the runner copies it into dest.
	dest     string
	toStdout bool
}

// runner runs one invocation. It returns the process exit code, or an error
// when the process could not be started or the context ended.
type runner func(ctx context.Context, inv invocation) (exitCode int, err error)

// runCommand and lookPath are variables so tests can replace them.
var (
	runCommand runner                       = execRunner
	lookPath   func(string) (string, error) = exec.LookPath
)

// Save reads the clipboard image and writes it as a PNG file in dir. An empty
// dir means os.TempDir()/deck-images. The directory is created with mode 0700.
// The returned path is absolute when dir is absolute.
//
// ErrNoImage is returned when the clipboard holds no image, and ErrUnavailable
// when no clipboard tool is installed.
func Save(ctx context.Context, dir string) (path string, err error) {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "deck-images")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("clipboard image dir: %w", err)
	}
	path, f, err := createUnique(dir, time.Now())
	if err != nil {
		return "", fmt.Errorf("clipboard image file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("clipboard image file: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, clipTimeout)
	defer cancel()

	if err := readClipboard(ctx, path); err != nil {
		os.Remove(path)
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		os.Remove(path)
		return "", ErrNoImage
	}
	return path, nil
}

// Available reports whether a supported clipboard tool is installed.
func Available() bool {
	_, ok := clipboardInvocation("")
	return ok
}

// readClipboard runs the platform tool for path and maps its result to the
// package errors. It does not check that the file is non-empty; Save does.
func readClipboard(ctx context.Context, path string) error {
	inv, ok := clipboardInvocation(path)
	if !ok {
		return ErrUnavailable
	}
	code, err := runCommand(ctx, inv)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("clipboard read: %w", ctxErr)
		}
		return fmt.Errorf("clipboard read: %w", err)
	}
	if code != 0 {
		if noImage(code) {
			return ErrNoImage
		}
		return fmt.Errorf("clipboard tool %s exited with code %d", inv.name, code)
	}
	return nil
}

// candidatePath returns the file name for attempt n. Attempt 0 has no suffix.
func candidatePath(dir string, now time.Time, n int) string {
	name := "deck-clip-" + now.Format("20060102-150405.000")
	if n > 0 {
		name += fmt.Sprintf("-%d", n)
	}
	return filepath.Join(dir, name+".png")
}

// createUnique creates an empty file with a timestamped name in dir. It never
// reuses an existing file.
func createUnique(dir string, now time.Time) (string, *os.File, error) {
	var lastErr error
	for n := 0; n < 100; n++ {
		p := candidatePath(dir, now, n)
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return p, f, nil
		}
		if !os.IsExist(err) {
			return "", nil, err
		}
		lastErr = err
	}
	return "", nil, lastErr
}

// execRunner is the runner used in production.
func execRunner(ctx context.Context, inv invocation) (int, error) {
	cmd := exec.CommandContext(ctx, inv.name, inv.args...)
	if len(inv.env) > 0 {
		cmd.Env = append(os.Environ(), inv.env...)
	}
	if inv.toStdout {
		f, err := os.OpenFile(inv.dest, os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return -1, err
		}
		defer f.Close()
		cmd.Stdout = f
	} else {
		cmd.Stdout = io.Discard
	}
	cmd.Stderr = io.Discard

	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return -1, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// imageExts are the extensions DetectImagePath accepts, lower case.
var imageExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
}

// DetectImagePath reports whether pasted text is a path to an existing image
// file. It accepts one line with surrounding whitespace and quotes, a file://
// URL, and the Windows PowerShell drag-and-drop form `& 'C:\path\x.png'`.
// On success it returns the cleaned absolute path.
func DetectImagePath(s string) (path string, ok bool) {
	if strings.ContainsAny(strings.TrimSpace(s), "\r\n") {
		return "", false
	}
	p := cleanPastedPath(s)
	if p == "" {
		return "", false
	}
	if !imageExts[strings.ToLower(filepath.Ext(p))] {
		return "", false
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return "", false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

// cleanPastedPath strips the wrappers a terminal or shell may add to a path.
func cleanPastedPath(s string) string {
	p := strings.TrimSpace(s)
	p = strings.TrimSpace(strings.TrimPrefix(p, "&"))
	p = trimQuotes(p)
	if len(p) >= len("file://") && strings.EqualFold(p[:len("file://")], "file://") {
		p = fileURLPath(p)
		p = trimQuotes(strings.TrimSpace(p))
	}
	return p
}

// trimQuotes removes one matching pair of single or double quotes.
func trimQuotes(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '\'' || first == '"') && first == last {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// fileURLPath converts a file:// URL to a local path. It drops a leading slash
// before a Windows drive letter, such as "/C:/x.png" to "C:/x.png".
func fileURLPath(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return strings.TrimPrefix(s[len("file://"):], "localhost")
	}
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}
