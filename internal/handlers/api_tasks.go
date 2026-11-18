package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"mirador/internal/db"
)

type taskRequest struct {
	AccountID *int64  `json:"account_id"`
	Title     *string `json:"title"`
	Ticket    *string `json:"ticket"`
	Details   *string `json:"details"`
	DueDate   *string `json:"due_date"`
	Completed *bool   `json:"completed"`
}

type reorderRequest struct {
	DueDate   string   `json:"due_date"`
	SortOrder *float64 `json:"sort_order"`
	BeforeID  *int64   `json:"before_id"`
	AfterID   *int64   `json:"after_id"`
}

type taskNoteRequest struct {
	Note *string `json:"note"`
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	completedOnly := queryBool(r, "completed")
	// The task list hides completed work by default, matching the due-date
	// view. include_done=1 returns everything; completed=1 selects only done
	// tasks and therefore overrides the default.
	hideDone := !queryBool(r, "include_done") && !completedOnly
	tasks, err := s.store.ListTasks(db.TaskFilter{
		AccountID:      queryID(r, "account_id"),
		CategoryIDs:    queryIDs(r, "category_id"),
		TagIDs:         queryIDs(r, "tag"),
		Search:         strings.TrimSpace(r.URL.Query().Get("q")),
		CSMNames:       queryStrings(r, "csm"),
		AENames:        queryStrings(r, "ae"),
		ChurnRisks:     parseChurnFilters(queryStrings(r, "churn")),
		HideDone:       hideDone,
		CompletedOnly:  completedOnly,
		Overdue:        queryBool(r, "overdue"),
		IncludeDeleted: queryBool(r, "include_deleted"),
	})
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if tasks == nil {
		tasks = []db.Task{}
	}
	s.writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	task, err := s.store.GetTask(id)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, task)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var request taskRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleDBError(w, err)
		return
	}
	if request.AccountID == nil || *request.AccountID <= 0 {
		s.writeError(w, http.StatusBadRequest, "account_id is required")
		return
	}
	if request.Title == nil {
		s.writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	title, err := cleanTitle(*request.Title)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	ticket, err := cleanText(stringOrDefault(request.Ticket, ""), 200)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	details, err := cleanText(stringOrDefault(request.Details, ""), 10000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if request.DueDate == nil {
		s.writeError(w, http.StatusBadRequest, "due_date is required")
		return
	}
	dueDate, err := validateDueDate(*request.DueDate)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	task, err := s.store.CreateTask(*request.AccountID, title, ticket, details, dueDate)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, task)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	current, err := s.store.GetTask(id)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	var request taskRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleDBError(w, err)
		return
	}
	title := current.Title
	if request.Title != nil {
		title, err = cleanTitle(*request.Title)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
	}
	ticket, err := cleanText(stringOrDefault(request.Ticket, current.Ticket), 200)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	details, err := cleanText(stringOrDefault(request.Details, current.Details), 10000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	dueDate := current.DueDate
	if request.DueDate != nil {
		dueDate, err = validateDueDate(*request.DueDate)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
	}
	completed := boolOrDefault(request.Completed, current.Completed)

	task, err := s.store.UpdateTask(id, title, ticket, details, dueDate, completed)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, task)
}

func (s *Server) reorderTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	current, err := s.store.GetTask(id)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	var request reorderRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleDBError(w, err)
		return
	}
	dueDate := request.DueDate
	if dueDate == "" {
		dueDate = current.DueDate
	}
	dueDate, err = validateDueDate(dueDate)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if dueDate != current.DueDate {
		s.handleDBError(w, db.ErrCrossDate)
		return
	}

	sortOrder := current.SortOrder
	if request.SortOrder != nil {
		sortOrder = *request.SortOrder
	} else if request.BeforeID != nil || request.AfterID != nil {
		sortOrder, err = s.sortOrderBetween(request, current.DueDate)
		if err != nil {
			s.handleDBError(w, err)
			return
		}
	}

	task, err := s.store.ReorderTask(id, dueDate, sortOrder)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, task)
}

func (s *Server) sortOrderBetween(request reorderRequest, dueDate string) (float64, error) {
	var before, after *db.Task
	if request.BeforeID != nil {
		task, err := s.store.GetTask(*request.BeforeID)
		if err != nil {
			return 0, err
		}
		if task.DueDate != dueDate {
			return 0, fmt.Errorf("%w: before_id is in a different due-date section", db.ErrCrossDate)
		}
		before = &task
	}
	if request.AfterID != nil {
		task, err := s.store.GetTask(*request.AfterID)
		if err != nil {
			return 0, err
		}
		if task.DueDate != dueDate {
			return 0, fmt.Errorf("%w: after_id is in a different due-date section", db.ErrCrossDate)
		}
		after = &task
	}
	switch {
	case before != nil && after != nil:
		return (before.SortOrder + after.SortOrder) / 2, nil
	case before != nil:
		return before.SortOrder + 1, nil
	case after != nil:
		return after.SortOrder - 1, nil
	default:
		return 0, nil
	}
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if queryBool(r, "permanent") {
		err = s.store.HardDeleteTask(id)
	} else {
		err = s.store.SoftDeleteTask(id)
	}
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) restoreTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if err := s.store.RestoreTask(id); err != nil {
		s.handleDBError(w, err)
		return
	}
	// GetTaskAny is used instead of GetTask because the restored task's account
	// (and possibly its category) may still be archived; GetTask requires all
	// three to be active and would wrongly return "not found".
	task, err := s.store.GetTaskAny(id)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, task)
}

func (s *Server) listTaskNotes(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	notes, err := s.store.ListTaskNotes(id)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, notes)
}

func (s *Server) addTaskNote(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	var request taskNoteRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleDBError(w, err)
		return
	}
	if request.Note == nil {
		s.writeError(w, http.StatusBadRequest, "note is required")
		return
	}
	note, err := cleanText(*request.Note, 10000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	taskNote, err := s.store.AddTaskNote(id, note)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, taskNote)
}

func (s *Server) editTaskNote(w http.ResponseWriter, r *http.Request) {
	taskID, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	noteID, err := pathID(r, "noteId")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	var request taskNoteRequest
	if err := decodeJSON(w, r, &request); err != nil {
		s.handleDBError(w, err)
		return
	}
	if request.Note == nil {
		s.writeError(w, http.StatusBadRequest, "note is required")
		return
	}
	note, err := cleanText(*request.Note, 10000)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	taskNote, err := s.store.UpdateTaskNote(noteID, note)
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	_ = taskID
	s.writeJSON(w, http.StatusOK, taskNote)
}

func (s *Server) deleteTaskNote(w http.ResponseWriter, r *http.Request) {
	taskID, err := pathID(r, "id")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	noteID, err := pathID(r, "noteId")
	if err != nil {
		s.handleDBError(w, err)
		return
	}
	if err := s.store.DeleteTaskNote(noteID); err != nil {
		s.handleDBError(w, err)
		return
	}
	_ = taskID
	s.writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
