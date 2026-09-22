package larkcli

import "testing"

func TestAllowsUnattendedConfigCommand(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: "lark-cli auth login --no-wait --json --recommend", want: true},
		{command: "lark-cli auth login --device-code code-123", want: true},
		{command: "/usr/local/bin/lark-cli config strict-mode off", want: false},
		{command: `C:\\tools\\lark-cli.cmd config default-as auto`, want: false},
		{command: "lark-cli docs +fetch --doc token", want: false},
		{command: "lark-cli auth logout", want: false},
		{command: "lark-cli config strict-mode off && touch /tmp/unexpected", want: false},
		{command: "lark-cli auth login --device-code $(read-secret)", want: false},
		{command: "bash -lc 'lark-cli config strict-mode off'", want: false},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			if got := AllowsUnattendedConfigCommand(test.command, func(executable string) bool {
				return executable == "lark-cli"
			}); got != test.want {
				t.Fatalf("AllowsUnattendedConfigCommand(%q) = %v, want %v", test.command, got, test.want)
			}
		})
	}
}

func TestAllowsUnattendedConfigCommandRequiresExecutableIdentity(t *testing.T) {
	if AllowsUnattendedConfigCommand("lark-cli config strict-mode off", nil) {
		t.Fatal("AllowsUnattendedConfigCommand() accepted a command without executable identity validation")
	}
	if AllowsUnattendedConfigCommand("./lark-cli config strict-mode off", func(string) bool { return false }) {
		t.Fatal("AllowsUnattendedConfigCommand() accepted an untrusted executable")
	}
}
