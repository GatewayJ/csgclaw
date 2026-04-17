package apitypes

import "time"

type Agent struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	Image           string    `json:"image,omitempty"`
	BoxID           string    `json:"box_id,omitempty"`
	RuntimeMode     string    `json:"runtime_mode,omitempty"`
	Role            string    `json:"role"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	Profile         string    `json:"profile,omitempty"`
	Provider        string    `json:"provider,omitempty"`
	ModelID         string    `json:"model_id,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
}

type CreateAgentRequest struct {
	ID          string    `json:"id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Image       string    `json:"image,omitempty"`
	RuntimeMode string    `json:"runtime_mode,omitempty"`
	Role        string    `json:"role,omitempty"`
	Status      string    `json:"status,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	Profile     string    `json:"profile,omitempty"`
	ModelID     string    `json:"model_id,omitempty"`
}
