package upgrade

import (
	"errors"
	"os"
	"testing"
)

func TestRestartCSGHubServerIfConfigured(t *testing.T) {
	originalPID := csghubServerParentPID
	originalSignal := signalCSGHubServerParent
	t.Cleanup(func() {
		csghubServerParentPID = originalPID
		signalCSGHubServerParent = originalSignal
	})
	t.Setenv(csghubServerRestartModeEnv, csghubServerRestartMode)

	csghubServerParentPID = func() int { return 4321 }
	var gotPID int
	signalCSGHubServerParent = func(pid int) error {
		gotPID = pid
		return nil
	}

	got, configured, err := RestartCSGHubServerIfConfigured()
	if err != nil {
		t.Fatalf("RestartCSGHubServerIfConfigured() error = %v", err)
	}
	if !configured {
		t.Fatal("configured = false, want true")
	}
	if gotPID != 4321 {
		t.Fatalf("signaled pid = %d, want 4321", gotPID)
	}
	if !got.DaemonWasRunning || !got.Restarted {
		t.Fatalf("result = %#v, want restart requested", got)
	}
}

func TestRestartCSGHubServerIfConfiguredSkipsOtherModes(t *testing.T) {
	t.Setenv(csghubServerRestartModeEnv, "")
	got, configured, err := RestartCSGHubServerIfConfigured()
	if err != nil {
		t.Fatalf("RestartCSGHubServerIfConfigured() error = %v", err)
	}
	if configured {
		t.Fatal("configured = true, want false")
	}
	if got != (RestartResult{}) {
		t.Fatalf("result = %#v, want zero value", got)
	}
}

func TestRestartCSGHubServerIfConfiguredRejectsInvalidParent(t *testing.T) {
	originalPID := csghubServerParentPID
	originalSignal := signalCSGHubServerParent
	t.Cleanup(func() {
		csghubServerParentPID = originalPID
		signalCSGHubServerParent = originalSignal
	})
	t.Setenv(csghubServerRestartModeEnv, csghubServerRestartMode)

	csghubServerParentPID = func() int { return 1 }
	signalCSGHubServerParent = func(int) error {
		t.Fatal("signalCSGHubServerParent should not be called")
		return errors.New("unreachable")
	}

	_, configured, err := RestartCSGHubServerIfConfigured()
	if !configured {
		t.Fatal("configured = false, want true")
	}
	if err == nil {
		t.Fatal("RestartCSGHubServerIfConfigured() error = nil, want invalid parent error")
	}
}

func TestRestartCSGHubServerIfConfiguredReturnsSignalFailure(t *testing.T) {
	originalPID := csghubServerParentPID
	originalSignal := signalCSGHubServerParent
	t.Cleanup(func() {
		csghubServerParentPID = originalPID
		signalCSGHubServerParent = originalSignal
	})
	t.Setenv(csghubServerRestartModeEnv, csghubServerRestartMode)

	csghubServerParentPID = func() int { return os.Getpid() }
	signalCSGHubServerParent = func(int) error { return errors.New("signal failed") }

	_, configured, err := RestartCSGHubServerIfConfigured()
	if !configured {
		t.Fatal("configured = false, want true")
	}
	if err == nil || err.Error() != "signal failed" {
		t.Fatalf("RestartCSGHubServerIfConfigured() error = %v, want signal failed", err)
	}
}
