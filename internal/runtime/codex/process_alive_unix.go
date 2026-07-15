//go:build !windows

package codex

import (
	"errors"
	"syscall"
)

func processAlivePID(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func stopProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	// The app server is started with Setpgid, so its PID is the process-group ID.
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
