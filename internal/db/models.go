package db

import (
	"database/sql"
	"math"
	"strings"
	"time"
)

// Category is a top-level grouping of accounts.
type Category struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Color     string  `json:"color"`
	SortOrder float64 `json:"sort_order"`
	Expanded  bool    `json:"expanded"`
	DeletedAt *string `json:"deleted_at,omitempty"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// Tag is a reusable label with a color fixed at creation.
type Tag struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Color      string  `json:"color"`
	DeletedAt  *string `json:"deleted_at,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	UsageCount int     `json:"usage_count"`
}

// Account is a customer/account that belongs to exactly one category.
type Account struct {
	ID           int64   `json:"id"`
	CategoryID   int64   `json:"category_id"`
	Name         string  `json:"name"`
	SortOrder    float64 `json:"sort_order,omitempty"`
	SFDC         string  `json:"sfdc"`
	Subdomain    string  `json:"subdomain"`
	CSMName      string  `json:"csm_name"`
	CSMLink      string  `json:"csm_link"`
	AEName       string  `json:"ae_name"`
	AELink       string  `json:"ae_link"`
	SlackChannel string  `json:"chatapp_channel"`
	Arr          string  `json:"arr"`
	ChurnRisk    *bool   `json:"churn_risk"`
	RenewalDate  string  `json:"renewal_date"`
	Notes        string  `json:"details"`
	DeletedAt    *string `json:"deleted_at,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	CategoryName string  `json:"category_name"`
	Tags         []Tag   `json:"tags"`
	Tasks        []Task  `json:"tasks"`
}

// Task is a single to-do item belonging to an account.
type Task struct {
	ID           int64      `json:"id"`
	AccountID    int64      `json:"account_id"`
	Title        string     `json:"title"`
	Ticket       string     `json:"ticket"`
	Details      string     `json:"details"`
	NotesCount   int        `json:"notes_count"`
	Notes        []TaskNote `json:"notes"`
	DueDate      string     `json:"due_date"`
	Completed    bool       `json:"completed"`
	CompletedAt  *string    `json:"completed_at,omitempty"`
	SortOrder    float64    `json:"sort_order"`
	DeletedAt    *string    `json:"deleted_at,omitempty"`
	CreatedAt    string     `json:"created_at"`
	UpdatedAt    string     `json:"updated_at"`
	AccountName  string     `json:"account_name"`
	CategoryID   int64      `json:"category_id"`
	CategoryName string     `json:"category_name"`
	Overdue      bool       `json:"overdue"`

	// Inherited from the parent account, displayed on the tasks page.
}

// TaskNote is a timestamped note attached to a task.
type TaskNote struct {
	ID        int64  `json:"id"`
	TaskID    int64  `json:"task_id"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

// TaskFilter narrows a task list query. Slice fields match any of their
// values; slices are combined with AND.
type TaskFilter struct {
	AccountID      int64
	CategoryIDs    []int64
	TagIDs         []int64
	Search         string
	CSMNames       []string
	AENames        []string
	ChurnRisks     []bool
	HideDone       bool
	CompletedOnly  bool
	Overdue        bool
	IncludeDeleted bool
}

// AccountFilter narrows an account list query. Slice fields match any of their
// values; slices are combined with AND.
type AccountFilter struct {
	CategoryIDs    []int64
	TagIDs         []int64
	Name           string
	CSMNames       []string
	AENames        []string
	ChurnRisks     []bool
	IncludeDeleted bool
}

// placeholders returns a comma-separated list of n SQL bind markers.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// anySlice widens a typed slice for use as SQL arguments.
func anySlice[T any](values []T) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

// APIKey is a credential for script/automation access to the API.
// The full key value is only returned once at creation; afterwards only the
// prefix is stored and displayed.
type APIKey struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	KeyPrefix  string  `json:"key_prefix"`
	KeyHash    string  `json:"-"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
	ExpiresAt  *string `json:"expires_at,omitempty"`
}

// Today returns the local date in the date-only format used by due_date.
func Today() string { return time.Now().Format("2006-01-02") }

func nullString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	s := value.String
	return &s
}

func stringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
