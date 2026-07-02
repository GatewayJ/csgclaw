package upgrade

import (
	"errors"
	"os"
	"strings"
)

const (
	UpgradeErrorArchiveInvalid  = "archive_invalid"
	UpgradeErrorDiskSpace       = "disk_space"
	UpgradeErrorHTTPAsset       = "http_asset"
	UpgradeErrorHTTPMetadata    = "http_metadata"
	UpgradeErrorMissingPath     = "missing_path"
	UpgradeErrorNetworkCheck    = "network_check"
	UpgradeErrorNetworkDownload = "network_download"
	UpgradeErrorPermission      = "permission"
)

// ClassifyFailure returns internal diagnostic kinds for upgrade helper failures.
// The web UI maps these finer-grained kinds into broader user-facing messages.
func ClassifyFailure(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case isLikelyArchiveFailure(msg):
		return UpgradeErrorArchiveInvalid
	case isLikelyNetworkFailure(msg):
		if isDownloadContext(msg) {
			return UpgradeErrorNetworkDownload
		}
		return UpgradeErrorNetworkCheck
	case isLikelyHTTPStatusFailure(msg):
		if strings.Contains(msg, "download release asset") {
			return UpgradeErrorHTTPAsset
		}
		return UpgradeErrorHTTPMetadata
	case isLikelyDiskSpaceFailure(msg):
		return UpgradeErrorDiskSpace
	case isLikelyPermissionFailure(err, msg):
		return UpgradeErrorPermission
	case isLikelyMissingPathFailure(err, msg):
		return UpgradeErrorMissingPath
	default:
		return ""
	}
}

func isLikelyArchiveFailure(msg string) bool {
	return containsAny(msg,
		"read release archive",
		"release archive contains",
		"open release archive entry",
		"close release archive entry",
	)
}

func isLikelyNetworkFailure(msg string) bool {
	return containsAny(msg,
		"stream error",
		"received from peer",
		"connection reset",
		"connection refused",
		"connection aborted",
		"unexpected eof",
		"i/o timeout",
		"timeout awaiting response headers",
		"tls handshake timeout",
		"context deadline exceeded",
		"no such host",
		"server misbehaving",
		"network is unreachable",
		"temporary failure in name resolution",
		"proxyconnect",
		"http2",
		"server closed idle connection",
		"use of closed network connection",
		"broken pipe",
	)
}

func isDownloadContext(msg string) bool {
	return containsAny(msg, "download", "csgclaw-upgrade", ".tar.gz", ".zip")
}

func isLikelyHTTPStatusFailure(msg string) bool {
	return strings.Contains(msg, "unexpected status") &&
		(strings.Contains(msg, "fetch latest release metadata") || strings.Contains(msg, "download release asset"))
}

func isLikelyDiskSpaceFailure(msg string) bool {
	return containsAny(msg,
		"no space left on device",
		"disk quota exceeded",
		"not enough space",
		"there is not enough space",
	)
}

func isLikelyPermissionFailure(err error, msg string) bool {
	return errors.Is(err, os.ErrPermission) ||
		containsAny(msg, "permission denied", "operation not permitted", "access is denied")
}

func isLikelyMissingPathFailure(err error, msg string) bool {
	return errors.Is(err, os.ErrNotExist) ||
		containsAny(msg, "no such file or directory", "cannot find the file specified")
}

func containsAny(s string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(s, pattern) {
			return true
		}
	}
	return false
}
