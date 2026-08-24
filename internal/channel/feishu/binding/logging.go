package binding

func resolvedLogAttrs(resolved Resolved, extra ...any) []any {
	attrs := []any{
		"binding_id", resolved.Binding.ID,
		"agent_id", resolved.Binding.AgentID,
		"participant_id", resolved.Binding.ParticipantID,
		"app_id", resolved.App.AppID,
	}
	return append(attrs, extra...)
}
