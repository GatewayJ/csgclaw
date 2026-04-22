package csghub

// AgentDefaults are csghub-specific agent-layer defaults carried by
// the provider so callers can keep a provider-only injection shape.
type AgentDefaults struct {
	ManagerImage string
	NameScope    string
	Downstream   map[string]string
}
