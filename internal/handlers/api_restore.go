package handlers

import (
	"errors"
	"net/http"

	"mirador/internal/db"
)

// restoreRequest is the body for the bulk restore endpoint.
type restoreRequest struct {
	Categories   []int64 `json:"categories"`
	Accounts     []int64 `json:"accounts"`
	Tasks        []int64 `json:"tasks"`
	WithChildren bool    `json:"with_children"`
}

// RestoreResult describes the outcome of restoring a single record.
type RestoreResult struct {
	ID     int64  `json:"id"`
	Status string `json:"status"` // "restored", "skipped", "error"
	Reason string `json:"reason,omitempty"`
}

// RestoreResponse aggregates per-type restore results.
type RestoreResponse struct {
	Categories []RestoreResult `json:"categories"`
	Accounts   []RestoreResult `json:"accounts"`
	Tasks      []RestoreResult `json:"tasks"`
}

// archivesResponse is the GET /api/v1/archives response.
type archivesResponse struct {
	Categories []db.Category `json:"categories"`
	Accounts   []db.Account  `json:"accounts"`
	Tasks      []db.Task     `json:"tasks"`
}

// archives handles GET /api/v1/archives — lists all archived (soft-deleted)
// categories, accounts, and tasks.
func (s *Server) archives(w http.ResponseWriter, r *http.Request) {
	categories, err := s.store.ListDeletedCategories()
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	accounts, err := s.store.ListDeletedAccounts()
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	tasks, err := s.store.ListDeletedTasks()
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if categories == nil {
		categories = []db.Category{}
	}
	if accounts == nil {
		accounts = []db.Account{}
	}
	if tasks == nil {
		tasks = []db.Task{}
	}
	s.writeJSON(w, http.StatusOK, archivesResponse{
		Categories: categories,
		Accounts:   accounts,
		Tasks:      tasks,
	})
}

// archiveItem is a single archived record with its type.
type archiveItem struct {
	Type string      `json:"type"` // "category", "account", or "task"
	Data interface{} `json:"data"`
}

// getArchive handles GET /api/v1/archives/{id} — returns a single archived
// category, account, or task by ID. Since IDs can overlap across tables, it
// searches category first, then account, then task. Only soft-deleted
// records are returned.
func (s *Server) getArchive(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}

	// Try category first.
	if cat, err := s.store.GetCategory(id, true); err == nil && cat.DeletedAt != nil {
		s.writeJSON(w, http.StatusOK, archiveItem{Type: "category", Data: cat})
		return
	}

	// Try account next.
	if acc, err := s.store.GetAccount(id, true); err == nil && acc.DeletedAt != nil {
		s.writeJSON(w, http.StatusOK, archiveItem{Type: "account", Data: acc})
		return
	}

	// Try task last.
	if task, err := s.store.GetTaskAny(id); err == nil && task.DeletedAt != nil {
		s.writeJSON(w, http.StatusOK, archiveItem{Type: "task", Data: task})
		return
	}

	s.writeError(w, http.StatusNotFound, "archived record not found")
}

// restoreAll bulk-restores archived categories, accounts, and tasks by ID.
// Each record is restored independently; a failure on one does not prevent
// the others from being restored. Records already active are reported as
// skipped rather than failing the whole request.
func (s *Server) restoreAll(w http.ResponseWriter, r *http.Request) {
	var req restoreRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.handleDBError(w, err)
		return
	}

	resp := RestoreResponse{
		Categories: []RestoreResult{},
		Accounts:   []RestoreResult{},
		Tasks:      []RestoreResult{},
	}

	for _, id := range req.Categories {
		resp.Categories = append(resp.Categories, restoreOne(func() error {
			return s.store.RestoreCategory(id, req.WithChildren)
		}, id))
	}
	for _, id := range req.Accounts {
		resp.Accounts = append(resp.Accounts, restoreOne(func() error {
			return s.store.RestoreAccount(id, req.WithChildren)
		}, id))
	}
	for _, id := range req.Tasks {
		resp.Tasks = append(resp.Tasks, restoreOne(func() error {
			return s.store.RestoreTask(id)
		}, id))
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// restoreOne runs a restore operation and classifies the outcome.
func restoreOne(fn func() error, id int64) RestoreResult {
	if err := fn(); err == nil {
		return RestoreResult{ID: id, Status: "restored"}
	} else if errors.Is(err, db.ErrNotFound) {
		return RestoreResult{ID: id, Status: "skipped", Reason: "not found"}
	} else if errors.Is(err, db.ErrConflict) {
		return RestoreResult{ID: id, Status: "skipped", Reason: "not in trash"}
	} else if db.IsConflict(err) {
		return RestoreResult{ID: id, Status: "skipped", Reason: "name already exists"}
	} else {
		return RestoreResult{ID: id, Status: "error", Reason: err.Error()}
	}
}
