package feishu

import (
	"errors"
	"strings"
)

type AppConfig struct {
	AppID       string
	AppSecret   string
	AdminOpenID string
}

type Snapshot struct {
	AdminOpenID string
	Bots        map[string]AppConfig
}

type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

func IsValidationError(err error) bool {
	var validationErr ValidationError
	return errors.As(err, &validationErr)
}

func AppsFromSnapshot(snapshot Snapshot) map[string]AppConfig {
	return cloneAppConfigs(snapshot.Bots)
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot = normalizeSnapshot(snapshot)
	if len(snapshot.Bots) == 0 {
		return Snapshot{AdminOpenID: snapshot.AdminOpenID}
	}
	cloned := Snapshot{
		AdminOpenID: snapshot.AdminOpenID,
		Bots:        make(map[string]AppConfig, len(snapshot.Bots)),
	}
	for botID, app := range snapshot.Bots {
		cloned.Bots[botID] = app
	}
	return cloned
}

func normalizeSnapshot(snapshot Snapshot) Snapshot {
	snapshot.AdminOpenID = strings.TrimSpace(snapshot.AdminOpenID)
	if len(snapshot.Bots) == 0 {
		snapshot.Bots = nil
		return snapshot
	}
	bots := make(map[string]AppConfig, len(snapshot.Bots))
	for botID, app := range snapshot.Bots {
		botID = strings.TrimSpace(botID)
		if botID == "" {
			continue
		}
		bots[botID] = AppConfig{
			AppID:       strings.TrimSpace(app.AppID),
			AppSecret:   strings.TrimSpace(app.AppSecret),
			AdminOpenID: snapshot.AdminOpenID,
		}
	}
	if len(bots) == 0 {
		snapshot.Bots = nil
		return snapshot
	}
	snapshot.Bots = bots
	return snapshot
}
