package upgrade

import (
	"fmt"
	"os"
	"strings"
)

const supervisorParentRestartModeEnv = "CSGCLAW_UPGRADE_RESTART_MODE"

const supervisorParentRestartMode = "supervisor-parent"

var (
	supervisorParentPID    = os.Getppid
	signalSupervisorParent = func(pid int) error {
		proc, err := findProcessByPID(pid)
		if err != nil {
			return fmt.Errorf("find supervisor-managed server process %d: %w", pid, err)
		}
		if err := proc.Signal(os.Interrupt); err != nil {
			return fmt.Errorf("signal supervisor-managed server process %d: %w", pid, err)
		}
		return nil
	}
)

// RestartSupervisorParentIfConfigured asks Supervisor to restart the current
// foreground server by stopping the helper's parent process. It is enabled
// only for helpers spawned by a Supervisor-managed server.
func RestartSupervisorParentIfConfigured() (RestartResult, bool, error) {
	if strings.TrimSpace(os.Getenv(supervisorParentRestartModeEnv)) != supervisorParentRestartMode {
		return RestartResult{}, false, nil
	}

	pid := supervisorParentPID()
	if pid <= 1 {
		return RestartResult{}, true, fmt.Errorf("invalid supervisor-managed server parent pid %d", pid)
	}
	if err := signalSupervisorParent(pid); err != nil {
		return RestartResult{}, true, err
	}
	return RestartResult{
		DaemonWasRunning: true,
		Restarted:        true,
	}, true, nil
}
