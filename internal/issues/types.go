package issues

import "time"

type State string

const (
	StateTodo       State = "todo"
	StateInProgress State = "in_progress"
	StateBlocked    State = "blocked"
	StateDone       State = "done"
	StateCanceled   State = "canceled"
)

type Category string

const (
	CategoryTask       Category = "task"
	CategoryWorkstream Category = "workstream"
	CategoryProject    Category = "project"
)

type Issue struct {
	ID            string     `json:"id"`
	ProjectPrefix string     `json:"project_prefix"`
	Category      Category   `json:"category"`
	Title         string     `json:"title"`
	Body          string     `json:"body"`
	State         State      `json:"state"`
	ParentID      *string    `json:"parent_id,omitempty"`
	Version       int64      `json:"version"`
	BlockedBy     []string   `json:"blocked_by"`
	CreatedAt     time.Time  `json:"created_at"`
	LastUpdatedAt time.Time  `json:"last_updated_at"`
	ClosedAt      *time.Time `json:"closed_at,omitempty"`
}

type TreeNode struct {
	Issue    Issue      `json:"issue"`
	Children []TreeNode `json:"children"`
}
