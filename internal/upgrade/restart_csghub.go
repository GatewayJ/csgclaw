package upgrade

import (
	"fmt"
	"os"
	"strings"
)

const csghubServerRestartModeEnv = "CSGCLAW_UPGRADE_RESTART_MODE"

const csghubServerRestartMode = "supervisor-parent"

var (
	csghubServerParentPID    = os.Getppid
	signalCSGHubServerParent = func(pid int) error {
		proc, err := findProcessByPID(pid)
		if err != nil {
			return fmt.Errorf("find CSGHub-managed server process %d: %w", pid, err)
		}
		if err := proc.Signal(os.Interrupt); err != nil {
			return fmt.Errorf("signal CSGHub-managed server process %d: %w", pid, err)
		}
		return nil
	}
)

// RestartCSGHubServerIfConfigured asks CSGHub to restart the current
// foreground server by stopping the upgrade helper's parent process. It is
// enabled only for helpers launched by a CSGHub-managed server.
func RestartCSGHubServerIfConfigured() (RestartResult, bool, error) {
	if strings.TrimSpace(os.Getenv(csghubServerRestartModeEnv)) != csghubServerRestartMode {
		return RestartResult{}, false, nil
	}

	pid := csghubServerParentPID()
	if pid <= 1 {
		return RestartResult{}, true, fmt.Errorf("invalid CSGHub-managed server parent pid %d", pid)
	}
	if err := signalCSGHubServerParent(pid); err != nil {
		return RestartResult{}, true, err
	}
	return RestartResult{
		DaemonWasRunning: true,
		Restarted:        true,
	}, true, nil
}
