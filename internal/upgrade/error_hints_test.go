package upgrade

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestClassifyFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "network download",
			err:  errors.New("write /tmp/csgclaw-upgrade/archive.tar.gz: stream error: stream ID 3"),
			want: UpgradeErrorNetworkDownload,
		},
		{
			name: "archive unexpected eof",
			err:  errors.New("read release archive entry: unexpected EOF"),
			want: UpgradeErrorArchiveInvalid,
		},
		{
			name: "permission",
			err:  fmt.Errorf("open /Users/me/.csgclaw/logs/upgrade-helper.log: %w", os.ErrPermission),
			want: UpgradeErrorPermission,
		},
		{
			name: "disk space",
			err:  errors.New("write /tmp/csgclaw-upgrade/archive.tar.gz: no space left on device"),
			want: UpgradeErrorDiskSpace,
		},
		{
			name: "http asset",
			err:  errors.New("download release asset csgclaw_v0.3.15_darwin_arm64.tar.gz: unexpected status 404 Not Found"),
			want: UpgradeErrorHTTPAsset,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyFailure(tt.err); got != tt.want {
				t.Fatalf("ClassifyFailure() = %q, want %q", got, tt.want)
			}
		})
	}
}
