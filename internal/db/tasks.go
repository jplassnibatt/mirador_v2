package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func scanTask(row interface{ Scan(dest ...any) error }) (Task, error) {
	var t Task
	var ticket, notes, completedAt, deleted sql.NullString
	var completed int
	err := row.Scan(
		&t.ID, &t.AccountID, &t.Title, &ticket, &notes, &t.DueDate,
		&completed, &completedAt, &t.SortOrder, &deleted,
		&t.CreatedAt, &t.UpdatedAt, &t.AccountName, &t.CategoryID, &t.CategoryName,
	)
	if err != nil {
		return Task{}, err
	}
	t.Ticket = stringValue(ticket)
	t.Details = stringValue(notes)
	t.Completed = completed != 0
	t.CompletedAt = nullString(completedAt)
	t.DeletedAt = nullString(deleted)
	t.Overdue = !t.Completed && t.DueDate < Today()
	return t, nil
}

func taskSelect() string {
	return `
		SELECT t.id, t.account_id, t.title, t.ticket, t.notes, t.due_date,
		       t.completed, t.completed_at, t.sort_order, t.deleted_at,
		       t.created_at, t.updated_at, a.name, c.id, c.name
		FROM tasks t
		JOIN accounts a ON a.id = t.account_id
		JOIN categories c ON c.id = a.category_id`
}

// ListTasks returns tasks ordered by due date then sort_order.
func (d *DB) ListTasks(filter TaskFilter) ([]Task, error) {
	query := taskSelect()
	var conditions []string
	var args []any

	if !filter.IncludeDeleted {
		conditions = append(conditions, "t.deleted_at IS NULL", "a.deleted_at IS NULL", "c.deleted_at IS NULL")
	}
	if filter.AccountID > 0 {
		conditions = append(conditions, "t.account_id = ?")
		args = append(args, filter.AccountID)
	}
	if len(filter.CategoryIDs) > 0 {
		conditions = append(conditions, "a.category_id IN ("+placeholders(len(filter.CategoryIDs))+")")
		args = append(args, anySlice(filter.CategoryIDs)...)
	}
	if len(filter.TagIDs) > 0 {
		conditions = append(conditions, `EXISTS (
			SELECT 1 FROM account_tags at WHERE at.account_id = a.id AND at.tag_id IN (`+placeholders(len(filter.TagIDs))+`)
		)`)
		args = append(args, anySlice(filter.TagIDs)...)
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		conditions = append(conditions, `(a.name LIKE ? OR t.title LIKE ? OR t.ticket LIKE ? OR t.notes LIKE ?)`)
		args = append(args, like, like, like, like)
	}
	if len(filter.CSMNames) > 0 {
		conditions = append(conditions, "a.csm_name COLLATE NOCASE IN ("+placeholders(len(filter.CSMNames))+")")
		args = append(args, anySlice(filter.CSMNames)...)
	}
	if len(filter.AENames) > 0 {
		conditions = append(conditions, "a.ae_name COLLATE NOCASE IN ("+placeholders(len(filter.AENames))+")")
		args = append(args, anySlice(filter.AENames)...)
	}
	if len(filter.ChurnRisks) > 0 {
		conditions = append(conditions, "a.churn_risk IN ("+placeholders(len(filter.ChurnRisks))+")")
		args = append(args, anySlice(filter.ChurnRisks)...)
	}
	if filter.HideDone {
		conditions = append(conditions, "t.completed = 0")
	}
	if filter.CompletedOnly {
		conditions = append(conditions, "t.completed = 1")
	}
	if filter.Overdue {
		conditions = append(conditions, "t.completed = 0", "t.due_date < ?")
		args = append(args, Today())
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY t.due_date, t.sort_order, t.id"

	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if tasks == nil {
		tasks = []Task{}
	}
	taskIDs := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}
	notesByTask, err := d.ListTaskNotesForTasks(taskIDs)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		tn := notesByTask[tasks[i].ID]
		tasks[i].Notes = tn
		tasks[i].NotesCount = len(tn)
	}
	return tasks, nil
}

// GetTask returns one active task with its account tags.
func (d *DB) GetTask(id int64) (Task, error) {
	t, err := scanTask(d.sql.QueryRow(taskSelect()+`
		WHERE t.id = ? AND t.deleted_at IS NULL AND a.deleted_at IS NULL AND c.deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	tasks := []Task{t}
	notesByTask, err := d.ListTaskNotesForTasks([]int64{t.ID})
	if err != nil {
		return Task{}, err
	}
	tasks[0].Notes = notesByTask[t.ID]
	tasks[0].NotesCount = len(tasks[0].Notes)
	return tasks[0], nil
}

// GetTaskAny returns one task regardless of archive state (used by trash and
// import verification).
func (d *DB) GetTaskAny(id int64) (Task, error) {
	t, err := scanTask(d.sql.QueryRow(taskSelect()+` WHERE t.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return t, err
}

// CreateTask inserts a task at the end of its due-date section.
func (d *DB) CreateTask(accountID int64, title, ticket, notes, dueDate string) (Task, error) {
	var id int64
	err := d.WithTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`
			SELECT COUNT(*) FROM accounts a
			JOIN categories c ON c.id = a.category_id
			WHERE a.id = ? AND a.deleted_at IS NULL AND c.deleted_at IS NULL`, accountID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("%w: account not found", ErrNotFound)
		}
		sortOrder, err := nextTaskSortOrder(tx, dueDate)
		if err != nil {
			return err
		}
		result, err := tx.Exec(`
			INSERT INTO tasks(account_id, title, ticket, notes, due_date, sort_order)
			VALUES (?, ?, ?, ?, ?, ?)`, accountID, title, nullable(ticket), nullable(notes), dueDate, sortOrder)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return d.GetTask(id)
}

// UpdateTask updates task fields. Changing due_date moves the task to the end
// of the new date section; completion timestamps are maintained here.
func (d *DB) UpdateTask(id int64, title, ticket, notes, dueDate string, completed bool) (Task, error) {
	current, err := d.GetTask(id)
	if err != nil {
		return Task{}, err
	}
	err = d.WithTx(func(tx *sql.Tx) error {
		sortOrder := current.SortOrder
		if dueDate != current.DueDate {
			sortOrder, err = nextTaskSortOrder(tx, dueDate)
			if err != nil {
				return err
			}
		}
		var completedAt any
		switch {
		case completed && !current.Completed:
			completedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
		case !completed:
			completedAt = nil
		default:
			if current.CompletedAt != nil {
				completedAt = *current.CompletedAt
			}
		}
		_, err = tx.Exec(`
			UPDATE tasks
			SET title = ?, ticket = ?, notes = ?, due_date = ?, completed = ?, completed_at = ?, sort_order = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND deleted_at IS NULL`,
			title, nullable(ticket), nullable(notes), dueDate, boolInt(completed), completedAt, sortOrder, id)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return d.GetTask(id)
}

// ReorderTask persists a drag reorder within the same due-date section. A
// different due date is rejected; the user must edit the task to reschedule.
func (d *DB) ReorderTask(id int64, dueDate string, sortOrder float64) (Task, error) {
	if !finite(sortOrder) {
		return Task{}, fmt.Errorf("%w: sort_order must be a finite number", ErrInvalid)
	}
	current, err := d.GetTask(id)
	if err != nil {
		return Task{}, err
	}
	if dueDate != current.DueDate {
		return Task{}, ErrCrossDate
	}
	err = d.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			UPDATE tasks SET sort_order = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND deleted_at IS NULL`, sortOrder, id); err != nil {
			return err
		}
		return normalizeTaskOrder(tx, dueDate)
	})
	if err != nil {
		return Task{}, err
	}
	return d.GetTask(id)
}

func nextTaskSortOrder(tx *sql.Tx, dueDate string) (float64, error) {
	var max sql.NullFloat64
	if err := tx.QueryRow(`
		SELECT MAX(sort_order) FROM tasks
		WHERE due_date = ? AND deleted_at IS NULL`, dueDate).Scan(&max); err != nil {
		return 0, err
	}
	if !max.Valid {
		return 0, nil
	}
	return max.Float64 + 1, nil
}

// normalizeTaskOrder renumbers a date section when two tasks share the same
// sort_order. Gaps of one are fine because the client sends midpoints; this is
// only a repair for equal values.
func normalizeTaskOrder(tx *sql.Tx, dueDate string) error {
	rows, err := tx.Query(`
		SELECT id, sort_order FROM tasks
		WHERE due_date = ? AND deleted_at IS NULL
		ORDER BY sort_order, id`, dueDate)
	if err != nil {
		return err
	}
	var ids []int64
	var values []float64
	for rows.Next() {
		var id int64
		var value float64
		if err := rows.Scan(&id, &value); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	needsRepair := false
	for i := 1; i < len(values); i++ {
		if values[i] == values[i-1] {
			needsRepair = true
			break
		}
	}
	if !needsRepair {
		return nil
	}
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE tasks SET sort_order = ? WHERE id = ?`, float64(i), id); err != nil {
			return err
		}
	}
	return nil
}

// TaskNote is a timestamped note attached to a task.
func scanTaskNote(row interface{ Scan(dest ...any) error }) (TaskNote, error) {
	var tn TaskNote
	var createdAt sql.NullString
	err := row.Scan(&tn.ID, &tn.TaskID, &tn.Note, &createdAt)
	if err != nil {
		return TaskNote{}, err
	}
	tn.CreatedAt = stringValue(createdAt)
	return tn, nil
}

// AddTaskNote appends a timestamped note to a task.
func (d *DB) AddTaskNote(taskID int64, note string) (TaskNote, error) {
	var id int64
	err := d.WithTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = ? AND deleted_at IS NULL`, taskID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("%w: task not found", ErrNotFound)
		}
		result, err := tx.Exec(`
			INSERT INTO task_notes(task_id, note)
			VALUES (?, ?)`, taskID, note)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		return err
	})
	if err != nil {
		return TaskNote{}, err
	}
	return d.GetTaskNote(id)
}

// GetTaskNote returns one task note by ID.
func (d *DB) GetTaskNote(id int64) (TaskNote, error) {
	tn, err := scanTaskNote(d.sql.QueryRow(`SELECT id, task_id, note, created_at FROM task_notes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return TaskNote{}, ErrNotFound
	}
	return tn, err
}

// ListTaskNotes returns all notes for a task, chronological (oldest first).
func (d *DB) ListTaskNotes(taskID int64) ([]TaskNote, error) {
	rows, err := d.sql.Query(`
		SELECT id, task_id, note, created_at FROM task_notes
		WHERE task_id = ?
		ORDER BY created_at ASC, id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []TaskNote
	for rows.Next() {
		tn, err := scanTaskNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, tn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if notes == nil {
		notes = []TaskNote{}
	}
	return notes, nil
}

// ListTaskNotesForTasks returns all notes for the given task IDs, grouped by task ID.
func (d *DB) ListTaskNotesForTasks(taskIDs []int64) (map[int64][]TaskNote, error) {
	if len(taskIDs) == 0 {
		return map[int64][]TaskNote{}, nil
	}
	placeholders := make([]string, 0, len(taskIDs))
	args := make([]any, 0, len(taskIDs))
	for _, id := range taskIDs {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	rows, err := d.sql.Query(`
		SELECT id, task_id, note, created_at FROM task_notes
		WHERE task_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY task_id ASC, created_at ASC, id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64][]TaskNote, len(taskIDs))
	for rows.Next() {
		tn, err := scanTaskNote(rows)
		if err != nil {
			return nil, err
		}
		result[tn.TaskID] = append(result[tn.TaskID], tn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Ensure every task ID has an entry, even if empty
	for _, id := range taskIDs {
		if _, ok := result[id]; !ok {
			result[id] = []TaskNote{}
		}
	}
	return result, nil
}

// CountTaskNotes returns the number of notes for a task.
func (d *DB) CountTaskNotes(taskID int64) (int, error) {
	var count int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM task_notes WHERE task_id = ?`, taskID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// UpdateTaskNote updates the text of an existing task note.
func (d *DB) UpdateTaskNote(id int64, note string) (TaskNote, error) {
	_, err := d.sql.Exec(`UPDATE task_notes SET note = ? WHERE id = ?`, note, id)
	if err != nil {
		return TaskNote{}, err
	}
	return d.GetTaskNote(id)
}

// DeleteTaskNote removes a task note.
func (d *DB) DeleteTaskNote(id int64) error {
	result, err := d.sql.Exec(`DELETE FROM task_notes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDeleteTask archives a task.
func (d *DB) SoftDeleteTask(id int64) error {
	result, err := d.sql.Exec(`
		UPDATE tasks SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// RestoreTask clears the archive flag. If the parent account is also archived,
// it is restored too (along with its category if needed) so the task becomes
// visible again. A task restored without its parent account would be invisible
// because task queries join against accounts and filter out archived ones.
func (d *DB) RestoreTask(id int64) error {
	task, err := d.GetTaskAny(id)
	if err != nil {
		return err
	}
	if task.DeletedAt == nil {
		return fmt.Errorf("%w: task is not in the trash", ErrConflict)
	}

	// Restore the parent account if it's archived, so the task becomes visible.
	// withChildren=false avoids re-restoring this task through the account.
	account, err := d.GetAccount(task.AccountID, true)
	if err == nil && account.DeletedAt != nil {
		if err := d.RestoreAccount(task.AccountID, false); err != nil {
			return err
		}
	}

	result, err := d.sql.Exec(`
		UPDATE tasks SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NOT NULL`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// HardDeleteTask permanently removes a task.
func (d *DB) HardDeleteTask(id int64) error {
	result, err := d.sql.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// AllAccountTags returns tag names grouped by account for export.
func (d *DB) AllAccountTags() (map[int64][]string, error) {
	rows, err := d.sql.Query(`
		SELECT at.account_id, t.name
		FROM account_tags at
		JOIN tags t ON t.id = at.tag_id
		ORDER BY t.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64][]string)
	for rows.Next() {
		var accountID int64
		var name string
		if err := rows.Scan(&accountID, &name); err != nil {
			return nil, err
		}
		result[accountID] = append(result[accountID], name)
	}
	return result, rows.Err()
}

// CountTasks returns the number of active (non-archived) tasks.
func (d *DB) CountTasks() (int, error) {
	var count int
	err := d.sql.QueryRow(`
		SELECT COUNT(*)
		FROM tasks t
		JOIN accounts a ON a.id = t.account_id
		JOIN categories c ON c.id = a.category_id
		WHERE t.deleted_at IS NULL
		  AND a.deleted_at IS NULL
		  AND c.deleted_at IS NULL`).Scan(&count)
	return count, err
}

// CountTasksByCompletion returns the number of active tasks that are open
// (completed = 0) and completed (completed = 1).
func (d *DB) CountTasksByCompletion() (open int, completed int, err error) {
	err = d.sql.QueryRow(`
		SELECT
			SUM(CASE WHEN t.completed = 0 THEN 1 ELSE 0 END),
			SUM(CASE WHEN t.completed = 1 THEN 1 ELSE 0 END)
		FROM tasks t
		JOIN accounts a ON a.id = t.account_id
		JOIN categories c ON c.id = a.category_id
		WHERE t.deleted_at IS NULL
		  AND a.deleted_at IS NULL
		  AND c.deleted_at IS NULL`).Scan(&open, &completed)
	return open, completed, err
}
