package files

import (
	"fmt"
	"path/filepath"
	"strings"

	channeltypes "csgclaw/internal/channel"
)

const (
	defaultMaxCount               = 8
	defaultMaxFileBytes           = int64(20 << 20)
	defaultMaxTotal               = int64(50 << 20)
	defaultMaxStagingBytes        = int64(200 << 20)
	defaultMaxStagedFiles         = 32
	defaultMaxConcurrentDownloads = 4
)

type Policy struct {
	MaxCount               int
	MaxFileBytes           int64
	MaxTotal               int64
	MaxStagingBytes        int64
	MaxStagedFiles         int
	MaxConcurrentDownloads int
}

func (p Policy) normalized() Policy {
	if p.MaxCount <= 0 {
		p.MaxCount = defaultMaxCount
	}
	if p.MaxFileBytes <= 0 {
		p.MaxFileBytes = defaultMaxFileBytes
	}
	if p.MaxTotal <= 0 {
		p.MaxTotal = defaultMaxTotal
	}
	if p.MaxStagingBytes <= 0 {
		p.MaxStagingBytes = defaultMaxStagingBytes
	}
	if p.MaxStagedFiles <= 0 {
		p.MaxStagedFiles = defaultMaxStagedFiles
	}
	if p.MaxConcurrentDownloads <= 0 {
		p.MaxConcurrentDownloads = defaultMaxConcurrentDownloads
	}
	return p
}

func authorize(resources []channeltypes.InboundFile, policy Policy) error {
	policy = policy.normalized()
	if len(resources) > policy.MaxCount {
		return fmt.Errorf("too many Feishu attachments: got %d, maximum is %d", len(resources), policy.MaxCount)
	}
	var declaredTotal int64
	for _, resource := range resources {
		if strings.TrimSpace(resource.ID) == "" {
			return fmt.Errorf("Feishu attachment file key is required")
		}
		if resource.SizeBytes < 0 {
			return fmt.Errorf("Feishu attachment %q has an invalid size", safeName(resource.Name))
		}
		if resource.SizeBytes > policy.MaxFileBytes {
			return fmt.Errorf("Feishu attachment %q exceeds the %d byte limit", safeName(resource.Name), policy.MaxFileBytes)
		}
		if resource.SizeBytes > policy.MaxTotal-declaredTotal {
			return fmt.Errorf("Feishu attachments exceed the %d byte total limit", policy.MaxTotal)
		}
		declaredTotal += resource.SizeBytes
	}
	return nil
}

func safeName(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "attachment"
	}
	return name
}
