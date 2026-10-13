package apps

import (
	"csgclaw/internal/connectorcli"
	"errors"
	"os"
	"path/filepath"
)

// Restore configuration snapshots recorded by earlier connector versions.
func (s *Service) restoreCLIConfig(id string) error {
	r, err := s.cliSnapshot(id)
	if err != nil {
		return err
	}
	return s.restoreCLIFile(id, r, r.CLI.Operation)
}
func (s *Service) restoreCLIFile(id string, r record, op *cliOperation) error {
	var err error
	if !op.BackupTaken || op.Committed {
		return nil
	}
	if err = connectorcli.RecoverProcess(op.Process); err != nil {
		return err
	}
	target := filepath.Join(r.CLI.ConfigDir, "config.json")
	if !op.ConfigExisted {
		err := os.Remove(target)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := os.ReadFile(filepath.Join(s.dataRoot, id, "operations", op.SessionID, "config.json"))
	if err != nil {
		return err
	}
	return connectorcli.AtomicWrite(target, data)
}
