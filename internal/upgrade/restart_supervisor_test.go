package upgrade

import (
	"errors"
	"os"
	"testing"
)

func TestRestartSupervisorParentIfConfigured(t *testing.T) {
	originalPID := supervisorParentPID
	originalSignal := signalSupervisorParent
	t.Cleanup(func() {
		supervisorParentPID = originalPID
		signalSupervisorParent = originalSignal
	})
	t.Setenv(supervisorParentRestartModeEnv, supervisorParentRestartMode)

	supervisorParentPID = func() int { return 4321 }
	var gotPID int
	signalSupervisorParent = func(pid int) error {
		gotPID = pid
		return nil
	}

	got, configured, err := RestartSupervisorParentIfConfigured()
	if err != nil {
		t.Fatalf("RestartSupervisorParentIfConfigured() error = %v", err)
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

func TestRestartSupervisorParentIfConfiguredSkipsOtherModes(t *testing.T) {
	t.Setenv(supervisorParentRestartModeEnv, "")
	got, configured, err := RestartSupervisorParentIfConfigured()
	if err != nil {
		t.Fatalf("RestartSupervisorParentIfConfigured() error = %v", err)
	}
	if configured {
		t.Fatal("configured = true, want false")
	}
	if got != (RestartResult{}) {
		t.Fatalf("result = %#v, want zero value", got)
	}
}

func TestRestartSupervisorParentIfConfiguredRejectsInvalidParent(t *testing.T) {
	originalPID := supervisorParentPID
	originalSignal := signalSupervisorParent
	t.Cleanup(func() {
		supervisorParentPID = originalPID
		signalSupervisorParent = originalSignal
	})
	t.Setenv(supervisorParentRestartModeEnv, supervisorParentRestartMode)

	supervisorParentPID = func() int { return 1 }
	signalSupervisorParent = func(int) error {
		t.Fatal("signalSupervisorParent should not be called")
		return errors.New("unreachable")
	}

	_, configured, err := RestartSupervisorParentIfConfigured()
	if !configured {
		t.Fatal("configured = false, want true")
	}
	if err == nil {
		t.Fatal("RestartSupervisorParentIfConfigured() error = nil, want invalid parent error")
	}
}

func TestRestartSupervisorParentIfConfiguredReturnsSignalFailure(t *testing.T) {
	originalPID := supervisorParentPID
	originalSignal := signalSupervisorParent
	t.Cleanup(func() {
		supervisorParentPID = originalPID
		signalSupervisorParent = originalSignal
	})
	t.Setenv(supervisorParentRestartModeEnv, supervisorParentRestartMode)

	supervisorParentPID = func() int { return os.Getpid() }
	signalSupervisorParent = func(int) error { return errors.New("signal failed") }

	_, configured, err := RestartSupervisorParentIfConfigured()
	if !configured {
		t.Fatal("configured = false, want true")
	}
	if err == nil || err.Error() != "signal failed" {
		t.Fatalf("RestartSupervisorParentIfConfigured() error = %v, want signal failed", err)
	}
}
