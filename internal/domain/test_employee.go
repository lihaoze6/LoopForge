package domain

// TestEmployee configures automation for an existing, ordinary project member.
type TestEmployee struct {
	ProjectID    string  `json:"projectId"`
	UserID       int64   `json:"userId"`
	RoleID       string  `json:"roleId,omitempty"`
	Duty         string  `json:"duty,omitempty"`
	Prompt       *string `json:"prompt,omitempty"`
	ConnectionID string  `json:"connectionId,omitempty"`
	Model        string  `json:"model,omitempty"`
	Paused       bool    `json:"paused"`
	Revision     int64   `json:"revision"`
}

type EmployeeModelConnection struct {
	ID         string   `json:"id"`
	ProjectID  string   `json:"projectId"`
	OwnerID    int64    `json:"ownerId"`
	EmployeeID int64    `json:"employeeId,omitempty"`
	Name       string   `json:"name"`
	Protocol   string   `json:"protocol"`
	BaseURL    string   `json:"baseUrl,omitempty"`
	Source     string   `json:"source,omitempty"`
	Models     []string `json:"models"`
	// AllowPrivate is derived at use time for a trusted owner/admin-managed
	// connection. It is never persisted or returned to clients.
	AllowPrivate bool `json:"-"`
}
