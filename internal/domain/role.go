package domain

// Role is an instance of a Kind, declared in configuration with its own prompt.
// Adding a role of an existing kind needs no code (04-roles.md).
type Role struct {
	Name        string `json:"name"`
	Kind        Kind   `json:"kind"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
}
