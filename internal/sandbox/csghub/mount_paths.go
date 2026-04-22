package csghub

import (
	"os"
	"path/filepath"
	"strings"

	"csgclaw/internal/config"
)

const defaultPVCMountPath = "/opt/csgclaw"

func fillMountPathEnv(params Params) Params {
	if strings.TrimSpace(params.pvcMountPath) == "" {
		params.pvcMountPath = strings.TrimSpace(os.Getenv(config.EnvPVCMountPath))
	}
	if strings.TrimSpace(params.pvcMountPath) == "" {
		params.pvcMountPath = defaultPVCMountPath
	}
	if strings.TrimSpace(params.subpathRoot) == "" {
		params.subpathRoot = strings.Trim(strings.TrimSpace(os.Getenv(config.EnvSandboxSubpathRoot)), "/")
	}
	if strings.TrimSpace(params.subpathRoot) == "" {
		params.subpathRoot = strings.Trim(strings.TrimSpace(os.Getenv(config.EnvTenantID)), "/")
	}
	return params
}

func (p Params) resolveMountHostPath(hostPath string) string {
	raw := strings.TrimSpace(hostPath)
	if raw == "" {
		return raw
	}
	root := strings.TrimSpace(p.pvcMountPath)
	prefix := strings.TrimSpace(p.subpathRoot)
	if root == "" || prefix == "" || !filepath.IsAbs(raw) {
		return raw
	}

	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(raw))
	if err != nil {
		return raw
	}
	if rel == "." {
		return filepath.ToSlash(prefix)
	}
	if strings.HasPrefix(rel, "..") {
		return raw
	}
	return filepath.ToSlash(filepath.Join(prefix, rel))
}
