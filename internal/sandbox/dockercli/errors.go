package dockercli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"csgclaw/internal/sandbox"
)

type ExitError struct {
	Op       string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = "command failed"
	}
	if hint := errorHint(msg); hint != "" {
		msg += "\nHint: " + hint
	}
	if e.Op == "" {
		return fmt.Sprintf("docker exited with code %d: %s", e.ExitCode, msg)
	}
	return fmt.Sprintf("%s: docker exited with code %d: %s", e.Op, e.ExitCode, msg)
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

func wrapRunError(op string, result CommandResult, err error) error {
	if err == nil {
		return nil
	}
	stderr := strings.TrimSpace(string(result.Stderr))
	if isNotFound(stderr) {
		return fmt.Errorf("%s: %w: %w", op, sandbox.ErrNotFound, &ExitError{
			Op:       op,
			ExitCode: result.ExitCode,
			Stderr:   stderr,
			Err:      err,
		})
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) || result.ExitCode != 0 {
		return &ExitError{
			Op:       op,
			ExitCode: result.ExitCode,
			Stderr:   stderr,
			Err:      err,
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

func isNotFound(stderr string) bool {
	text := strings.ToLower(stderr)
	return strings.Contains(text, "no such container") ||
		strings.Contains(text, "no such object") ||
		strings.Contains(text, "not found")
}

func errorHint(stderr string) string {
	text := strings.ToLower(stderr)
	if strings.Contains(text, "exec format error") || strings.Contains(text, "enoexec") {
		return "Docker could not execute a binary inside the image. This usually means the image architecture does not match the host/container platform; use a multi-arch image or an image built for this host."
	}
	return ""
}
