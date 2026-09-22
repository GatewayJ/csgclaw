package larkcli

import (
	"strings"
	"testing"
)

func TestMergeCommandEnvReplacesCaseVariants(t *testing.T) {
	got := mergeCommandEnv(
		[]string{"PATH=/usr/bin", "lark_channel=ambient"},
		map[string]string{"LARK_CHANNEL": "1"},
	)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "lark_channel=") || !strings.Contains(joined, "LARK_CHANNEL=1") {
		t.Fatalf("mergeCommandEnv() = %q", joined)
	}
}
