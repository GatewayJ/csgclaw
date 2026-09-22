package larkcli

import "strings"

// AllowsUnattendedConfigCommand identifies the small set of lark-cli commands
// that may update the managed worker configuration during a channel turn. The
// caller must confirm that the requested executable resolves to the executable
// recorded by the active managed extension.
func AllowsUnattendedConfigCommand(command string, executableAllowed func(string) bool) bool {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "\r\n;&|<>`$") {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) < 4 || executableAllowed == nil {
		return false
	}
	fields[0] = strings.Trim(fields[0], `"'`)
	if fields[0] == "" || !executableAllowed(fields[0]) {
		return false
	}
	for index := 1; index < len(fields); index++ {
		fields[index] = strings.Trim(fields[index], `"'`)
	}
	switch {
	case len(fields) == 6 && fields[1] == "auth" && fields[2] == "login" && fields[3] == "--no-wait" && fields[4] == "--json" && fields[5] == "--recommend":
		return true
	case len(fields) == 5 && fields[1] == "auth" && fields[2] == "login" && fields[3] == "--device-code" && fields[4] != "":
		return true
	case len(fields) == 4 && fields[1] == "config" && fields[2] == "strict-mode" && fields[3] == "off":
		return true
	case len(fields) == 4 && fields[1] == "config" && fields[2] == "default-as" && fields[3] == "auto":
		return true
	default:
		return false
	}
}
