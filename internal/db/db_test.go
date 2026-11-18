package db

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "mirador.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return d
}

func mustCategory(t *testing.T, d *DB, name string) Category {
	t.Helper()
	category, err := d.CreateCategory(name, "#3b82f6", 0, true)
	if err != nil {
		t.Fatalf("create category %q: %v", name, err)
	}
	return category
}

func mustTag(t *testing.T, d *DB, name, color string) Tag {
	t.Helper()
	tag, err := d.CreateTag(name, color)
	if err != nil {
		t.Fatalf("create tag %q: %v", name, err)
	}
	return tag
}

func mustAccount(t *testing.T, d *DB, categoryID int64, name string, tagIDs ...int64) Account {
	t.Helper()
	sfdc := "https://example.com/" + name
	subdomain := ""
	csmName := ""
	csmLink := ""
	aeName := ""
	aeLink := ""
	slackChannel := ""
	notes := "notes for " + name
	account, err := d.CreateAccount(categoryID, name, float64(0), sfdc, subdomain, csmName, csmLink, aeName, aeLink, slackChannel, "", nil, "", notes, tagIDs)
	if err != nil {
		t.Fatalf("create account %q: %v", name, err)
	}
	return account
}

func mustTask(t *testing.T, d *DB, accountID int64, title, dueDate string) Task {
	t.Helper()
	task, err := d.CreateTask(accountID, title, "TICKET-"+title, "notes for "+title, dueDate)
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return task
}

func countRows(t *testing.T, d *DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := d.SQL().QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return count
}

func TestOpenMigratesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "mirador.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open fresh database: %v", err)
	}

	var version int
	if err := d.SQL().QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 1 {
		t.Fatalf("schema version = %d, want 1", version)
	}
	var userVersion int
	if err := d.SQL().QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if userVersion != version {
		t.Fatalf("user_version = %d, want %d", userVersion, version)
	}
	var journalMode string
	if err := d.SQL().QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	for _, table := range []string{"categories", "accounts", "tasks", "tags", "account_tags", "task_notes", "api_keys"} {
		if got := countRows(t, d, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table); got != 1 {
			t.Errorf("table %q not created", table)
		}
	}

	category := mustCategory(t, d, "Persisted")
	if category.ID == 0 {
		t.Fatal("created category has zero id")
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()
	categories, err := reopened.ListCategories(false)
	if err != nil {
		t.Fatalf("list categories after reopen: %v", err)
	}
	if len(categories) != 1 || categories[0].Name != "Persisted" {
		t.Fatalf("categories after reopen = %+v, want one Persisted row", categories)
	}
	if version := countRows(t, reopened, `SELECT COUNT(*) FROM schema_version`); version != 1 {
		t.Fatalf("schema_version rows = %d, want 1 (migrations must not rerun)", version)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "backups")); !os.IsNotExist(err) {
		t.Fatalf("fresh database should not create a backup directory, stat error = %v", err)
	}
}

func TestBackupCopiesDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mirador.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer d.Close()
	mustCategory(t, d, "Before backup")

	if err := d.backup(); err != nil {
		t.Fatalf("backup: %v", err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "mirador-*.db"))
	if err != nil {
		t.Fatalf("find backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("backup count = %d, want 1", len(backups))
	}
	copied, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(backups[0])+"?mode=ro")
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer copied.Close()
	var name string
	if err := copied.QueryRow(`SELECT name FROM categories`).Scan(&name); err != nil {
		t.Fatalf("read backup category: %v", err)
	}
	if name != "Before backup" {
		t.Fatalf("backup category = %q, want Before backup", name)
	}
}

func TestPruneBackups(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 7; i++ {
		name := fmt.Sprintf("mirador-20260101-00000%d.000000000.db", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("backup"), 0o600); err != nil {
			t.Fatalf("write backup: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "other.db"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("write unrelated file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mirador-notes.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("write unrelated note: %v", err)
	}

	if err := pruneBackups(dir, "mirador", 5); err != nil {
		t.Fatalf("prune backups: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read backup directory: %v", err)
	}
	var backups []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "mirador-") && strings.HasSuffix(name, ".db") {
			backups = append(backups, name)
		}
	}
	if len(backups) != 5 {
		t.Fatalf("backups after prune = %d, want 5", len(backups))
	}
	if _, err := os.Stat(filepath.Join(dir, "mirador-20260101-000000.000000000.db")); !os.IsNotExist(err) {
		t.Fatalf("oldest backup was not pruned, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mirador-20260101-000006.000000000.db")); err != nil {
		t.Fatalf("newest backup was pruned: %v", err)
	}
	for _, name := range []string{"other.db", "mirador-notes.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("unrelated file %q was removed: %v", name, err)
		}
	}
}

func TestCategoryAccountTaskFlow(t *testing.T) {
	d := openTestDB(t)
	category := mustCategory(t, d, "Work")
	otherCategory := mustCategory(t, d, "Home")

	tagA := mustTag(t, d, "Urgent", "#ff0000")
	tagB := mustTag(t, d, "Waiting", "#00ff00")
	account := mustAccount(t, d, category.ID, "Acme", tagA.ID, tagB.ID)
	otherAccount := mustAccount(t, d, otherCategory.ID, "Beta")

	if len(account.Tags) != 2 {
		t.Fatalf("account tags = %+v, want 2", account.Tags)
	}
	if account.Tags[0].Name != "Urgent" || account.Tags[1].Name != "Waiting" {
		t.Fatalf("account tags not sorted by name: %+v", account.Tags)
	}
	if account.SFDC != "https://example.com/Acme" {
		t.Fatalf("sfdc = %q", account.SFDC)
	}

	if _, err := d.CreateAccount(category.ID, "Acme", 0, "sfdc", "subdomain", "csmName", "csmLink", "aeName", "aeLink", "slackChannel", "", nil, "", "notes", []int64{}); !IsConflict(err) {
		t.Fatalf("duplicate account name error = %v, want unique conflict", err)
	}
	if _, err := d.CreateCategory("Work", "#000000", 0, true); !IsConflict(err) {
		t.Fatalf("duplicate category name error = %v, want unique conflict", err)
	}
	if _, err := d.CreateTag("Urgent", "#000000"); !IsConflict(err) {
		t.Fatalf("duplicate tag name error = %v, want unique conflict", err)
	}

	today := Today()
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	taskA := mustTask(t, d, account.ID, "First", today)
	taskB := mustTask(t, d, account.ID, "Second", today)
	taskC := mustTask(t, d, account.ID, "Overdue", yesterday)
	otherTask := mustTask(t, d, otherAccount.ID, "Other", today)

	if taskB.SortOrder <= taskA.SortOrder {
		t.Fatalf("second task sort order = %v, want after %v", taskB.SortOrder, taskA.SortOrder)
	}
	if taskA.Ticket != "TICKET-First" {
		t.Fatalf("task ticket = %q, want TICKET-First", taskA.Ticket)
	}
	if !taskC.Overdue {
		t.Fatalf("task due %s should be overdue", yesterday)
	}
	if taskA.Overdue {
		t.Fatal("task due today should not be overdue")
	}

	tasks, err := d.ListTasks(TaskFilter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("list tasks returned %d, want 4", len(tasks))
	}
	if tasks[0].Title != "Overdue" || tasks[0].DueDate != yesterday {
		t.Fatalf("tasks not sorted by due date: %+v", tasks)
	}

	completed, err := d.UpdateTask(taskA.ID, taskA.Title, taskA.Ticket, taskA.Details, taskA.DueDate, true)
	if err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if !completed.Completed || completed.CompletedAt == nil {
		t.Fatalf("completed task = %+v, want completed_at set", completed)
	}
	firstCompletedAt := *completed.CompletedAt

	again, err := d.UpdateTask(taskA.ID, taskA.Title, taskA.Ticket, taskA.Details, taskA.DueDate, true)
	if err != nil {
		t.Fatalf("re-save completed task: %v", err)
	}
	if again.CompletedAt == nil || *again.CompletedAt != firstCompletedAt {
		t.Fatalf("completed_at changed on re-save: %v -> %v", firstCompletedAt, again.CompletedAt)
	}

	reopened, err := d.UpdateTask(taskA.ID, taskA.Title, taskA.Ticket, taskA.Details, taskA.DueDate, false)
	if err != nil {
		t.Fatalf("uncomplete task: %v", err)
	}
	if reopened.Completed || reopened.CompletedAt != nil {
		t.Fatalf("uncompleted task = %+v, want completed_at cleared", reopened)
	}

	hidden, err := d.ListTasks(TaskFilter{HideDone: true})
	if err != nil {
		t.Fatalf("list tasks hiding done: %v", err)
	}
	if len(hidden) != 4 {
		t.Fatalf("hide-done returned %d, want 4", len(hidden))
	}
	completedOnly, err := d.ListTasks(TaskFilter{CompletedOnly: true})
	if err != nil {
		t.Fatalf("list completed tasks: %v", err)
	}
	if len(completedOnly) != 0 {
		t.Fatalf("completed filter returned %d, want 0", len(completedOnly))
	}
	overdue, err := d.ListTasks(TaskFilter{Overdue: true})
	if err != nil {
		t.Fatalf("list overdue tasks: %v", err)
	}
	if len(overdue) != 1 || overdue[0].ID != taskC.ID {
		t.Fatalf("overdue filter = %+v, want only task %d", overdue, taskC.ID)
	}
	byTag, err := d.ListTasks(TaskFilter{TagIDs: []int64{tagA.ID}})
	if err != nil {
		t.Fatalf("list tasks by tag: %v", err)
	}
	if len(byTag) != 3 {
		t.Fatalf("tag filter returned %d tasks, want 3", len(byTag))
	}
	byAccount, err := d.ListTasks(TaskFilter{AccountID: otherAccount.ID})
	if err != nil {
		t.Fatalf("list tasks by account: %v", err)
	}
	if len(byAccount) != 1 || byAccount[0].ID != otherTask.ID {
		t.Fatalf("account filter = %+v, want only task %d", byAccount, otherTask.ID)
	}

	// Moving a task to another due date appends it to that date's section.
	moved, err := d.UpdateTask(taskB.ID, taskB.Title, "TICKET-B-moved", taskB.Details, yesterday, false)
	if err != nil {
		t.Fatalf("move task to another date: %v", err)
	}
	if moved.DueDate != yesterday || moved.SortOrder <= taskC.SortOrder {
		t.Fatalf("moved task = %+v, want appended after task with sort %v", moved, taskC.SortOrder)
	}
	if moved.Ticket != "TICKET-B-moved" {
		t.Fatalf("moved task ticket = %q, want TICKET-B-moved", moved.Ticket)
	}

	// Reorder only within the same due-date section.
	reordered, err := d.ReorderTask(taskB.ID, yesterday, taskC.SortOrder-1)
	if err != nil {
		t.Fatalf("reorder task: %v", err)
	}
	if reordered.SortOrder >= taskC.SortOrder {
		t.Fatalf("reordered sort order = %v, want before %v", reordered.SortOrder, taskC.SortOrder)
	}
	if _, err := d.ReorderTask(taskB.ID, today, 0); !errors.Is(err, ErrCrossDate) {
		t.Fatalf("cross-date reorder error = %v, want ErrCrossDate", err)
	}
	if _, err := d.ReorderTask(taskB.ID, yesterday, math.NaN()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NaN sort order error = %v, want ErrInvalid", err)
	}

	// Deleting a tag removes it from accounts and tasks.
	if err := d.DeleteTag(tagB.ID); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
	account, err = d.GetAccount(account.ID, false)
	if err != nil {
		t.Fatalf("get account after tag delete: %v", err)
	}
	if len(account.Tags) != 1 || account.Tags[0].ID != tagA.ID {
		t.Fatalf("account tags after delete = %+v, want only tag %d", account.Tags, tagA.ID)
	}
	if _, err := d.GetTask(taskC.ID); err != nil {
		t.Fatalf("get task after tag delete: %v", err)
	}
}

func TestSoftDeleteCascadeAndRestore(t *testing.T) {
	d := openTestDB(t)
	category := mustCategory(t, d, "Work")
	tag := mustTag(t, d, "Client", "#123456")
	account := mustAccount(t, d, category.ID, "Acme", tag.ID)
	task := mustTask(t, d, account.ID, "Call", "2030-01-02")

	if err := d.SoftDeleteCategory(category.ID); err != nil {
		t.Fatalf("soft delete category: %v", err)
	}
	if _, err := d.GetCategory(category.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active category lookup error = %v, want ErrNotFound", err)
	}
	if _, err := d.GetAccount(account.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active account lookup error = %v, want ErrNotFound", err)
	}
	if _, err := d.GetTask(task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active task lookup error = %v, want ErrNotFound", err)
	}

	deletedCategories, err := d.ListDeletedCategories()
	if err != nil {
		t.Fatalf("list deleted categories: %v", err)
	}
	deletedAccounts, err := d.ListDeletedAccounts()
	if err != nil {
		t.Fatalf("list deleted accounts: %v", err)
	}
	deletedTasks, err := d.ListDeletedTasks()
	if err != nil {
		t.Fatalf("list deleted tasks: %v", err)
	}
	if len(deletedCategories) != 1 || len(deletedAccounts) != 1 || len(deletedTasks) != 1 {
		t.Fatalf("trash counts = %d/%d/%d, want 1/1/1", len(deletedCategories), len(deletedAccounts), len(deletedTasks))
	}
	if deletedCategories[0].DeletedAt == nil || deletedAccounts[0].DeletedAt == nil || deletedTasks[0].DeletedAt == nil {
		t.Fatal("cascaded rows must carry deleted_at")
	}
	if *deletedCategories[0].DeletedAt != *deletedAccounts[0].DeletedAt ||
		*deletedCategories[0].DeletedAt != *deletedTasks[0].DeletedAt {
		t.Fatalf("cascade timestamps differ: category %q account %q task %q",
			*deletedCategories[0].DeletedAt, *deletedAccounts[0].DeletedAt, *deletedTasks[0].DeletedAt)
	}

	if err := d.RestoreCategory(category.ID, true); err != nil {
		t.Fatalf("restore category with children: %v", err)
	}
	if _, err := d.GetCategory(category.ID, false); err != nil {
		t.Fatalf("category not restored: %v", err)
	}
	if _, err := d.GetAccount(account.ID, false); err != nil {
		t.Fatalf("account not restored with category: %v", err)
	}
	if _, err := d.GetTask(task.ID); err != nil {
		t.Fatalf("task not restored with category: %v", err)
	}

	// Restore without children leaves descendants in the trash.
	if err := d.SoftDeleteCategory(category.ID); err != nil {
		t.Fatalf("soft delete category again: %v", err)
	}
	if err := d.RestoreCategory(category.ID, false); err != nil {
		t.Fatalf("restore category without children: %v", err)
	}
	if _, err := d.GetAccount(account.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("account should remain deleted, got %v", err)
	}
	if _, err := d.GetTask(task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("task should remain deleted, got %v", err)
	}
	if err := d.RestoreAccount(account.ID, true); err != nil {
		t.Fatalf("restore account with children: %v", err)
	}
	if _, err := d.GetTask(task.ID); err != nil {
		t.Fatalf("task not restored with account: %v", err)
	}
	if err := d.RestoreCategory(category.ID, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("restoring an active category error = %v, want ErrConflict", err)
	}

	// Account-only cascade and restore.
	other := mustAccount(t, d, category.ID, "Beta")
	otherTask := mustTask(t, d, other.ID, "Send invoice", "2030-02-03")
	if err := d.SoftDeleteAccount(other.ID); err != nil {
		t.Fatalf("soft delete account: %v", err)
	}
	if _, err := d.GetTask(otherTask.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("task should be archived with account, got %v", err)
	}
	if err := d.RestoreAccount(other.ID, false); err != nil {
		t.Fatalf("restore account without children: %v", err)
	}
	if _, err := d.GetTask(otherTask.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("task should remain archived, got %v", err)
	}
	if err := d.RestoreTask(otherTask.ID); err != nil {
		t.Fatalf("restore task: %v", err)
	}
	if _, err := d.GetTask(otherTask.ID); err != nil {
		t.Fatalf("task not restored: %v", err)
	}
}

func TestRestoreAccountMovesToUncategorizedWhenCategoryDeleted(t *testing.T) {
	d := openTestDB(t)
	category := mustCategory(t, d, "Work")
	account := mustAccount(t, d, category.ID, "a11")
	mustTask(t, d, account.ID, "Call", "2030-01-02")

	// Delete the account, then its category.
	if err := d.SoftDeleteAccount(account.ID); err != nil {
		t.Fatalf("soft delete account: %v", err)
	}
	if err := d.SoftDeleteCategory(category.ID); err != nil {
		t.Fatalf("soft delete category: %v", err)
	}

	if err := d.RestoreAccount(account.ID, false); err != nil {
		t.Fatalf("restore account: %v", err)
	}

	restored, err := d.GetAccount(account.ID, false)
	if err != nil {
		t.Fatalf("get restored account: %v", err)
	}
	if restored.CategoryID == category.ID {
		t.Fatalf("account still references deleted category %d", category.ID)
	}
	uncategorized, err := d.EnsureUncategorizedCategory()
	if err != nil {
		t.Fatalf("ensure uncategorized: %v", err)
	}
	if restored.CategoryID != uncategorized.ID {
		t.Fatalf("account category_id = %d, want uncategorized %d", restored.CategoryID, uncategorized.ID)
	}
	if restored.CategoryName != "Uncategorized" {
		t.Fatalf("account category_name = %q, want Uncategorized", restored.CategoryName)
	}

	// The uncategorized category can be archived like any other category.
	// Archiving it archives its accounts too (cascade).
	if err := d.SoftDeleteCategory(uncategorized.ID); err != nil {
		t.Fatalf("soft delete uncategorized: %v", err)
	}
	// The account in uncategorized should also be archived.
	if _, err := d.GetAccount(account.ID, false); err == nil {
		t.Fatal("account in uncategorized should be archived after category archive")
	}
	// The category itself is archived.
	if _, err := d.GetCategory(uncategorized.ID, false); err == nil {
		t.Fatal("uncategorized category should be archived")
	}

	// Hard delete also works.
	if err := d.HardDeleteCategory(uncategorized.ID); err != nil {
		t.Fatalf("hard delete uncategorized: %v", err)
	}
	if _, err := d.GetCategory(uncategorized.ID, true); err == nil {
		t.Fatal("uncategorized category should be hard deleted")
	}
}

func TestHardDeleteCascades(t *testing.T) {
	d := openTestDB(t)
	category := mustCategory(t, d, "Work")
	tag := mustTag(t, d, "Client", "#123456")
	account := mustAccount(t, d, category.ID, "Acme", tag.ID)
	mustTask(t, d, account.ID, "Call", "2030-01-02")

	if err := d.HardDeleteCategory(category.ID); err != nil {
		t.Fatalf("hard delete category: %v", err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM categories`,
		`SELECT COUNT(*) FROM accounts`,
		`SELECT COUNT(*) FROM tasks`,
		`SELECT COUNT(*) FROM account_tags`,
	} {
		if count := countRows(t, d, query); count != 0 {
			t.Fatalf("%s = %d after hard delete, want 0", query, count)
		}
	}
	if count := countRows(t, d, `SELECT COUNT(*) FROM tags`); count != 1 {
		t.Fatalf("tags = %d after category delete, want 1", count)
	}
}

func TestTagUsageCounts(t *testing.T) {
	d := openTestDB(t)
	category := mustCategory(t, d, "Work")
	tag := mustTag(t, d, "Client", "#123456")
	first := mustAccount(t, d, category.ID, "Acme", tag.ID)
	second := mustAccount(t, d, category.ID, "Beta", tag.ID)

	tags, err := d.ListTags(false)
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	if len(tags) != 1 || tags[0].UsageCount != 2 {
		t.Fatalf("tag usage = %+v, want 2", tags)
	}
	if err := d.SoftDeleteAccount(first.ID); err != nil {
		t.Fatalf("soft delete account: %v", err)
	}
	tags, err = d.ListTags(false)
	if err != nil {
		t.Fatalf("list tags after delete: %v", err)
	}
	if tags[0].UsageCount != 1 {
		t.Fatalf("tag usage after soft delete = %d, want 1", tags[0].UsageCount)
	}
	if err := d.DeleteTag(tag.ID); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
	if _, err := d.GetTag(tag.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted tag lookup error = %v, want ErrNotFound", err)
	}
	second, err = d.GetAccount(second.ID, false)
	if err != nil {
		t.Fatalf("get account after tag delete: %v", err)
	}
	if len(second.Tags) != 0 {
		t.Fatalf("account tags after tag delete = %+v, want none", second.Tags)
	}
}

func TestAccountAndTaskFieldFilters(t *testing.T) {
	d := openTestDB(t)
	churnYes := true
	churnNo := false
	work := mustCategory(t, d, "Work")
	personal := mustCategory(t, d, "Personal")
	tagHot := mustTag(t, d, "Hot", "#ff0000")
	tagCold := mustTag(t, d, "Cold", "#00ff00")

	acme, err := d.CreateAccount(work.ID, "Acme Corp", 0, "", "", "Alice", "", "Bob", "", "", "1200", &churnYes, "", "", []int64{tagHot.ID})
	if err != nil {
		t.Fatalf("create Acme: %v", err)
	}
	beta, err := d.CreateAccount(work.ID, "Beta LLC", 0, "", "", "Carol", "", "Bob", "", "", "2400", &churnNo, "", "", nil)
	if err != nil {
		t.Fatalf("create Beta: %v", err)
	}
	gamma, err := d.CreateAccount(personal.ID, "Gamma Inc", 0, "", "", "Alice", "", "Dan", "", "", "3600", nil, "", "", []int64{tagCold.ID})
	if err != nil {
		t.Fatalf("create Gamma: %v", err)
	}
	archived, err := d.CreateAccount(work.ID, "Old Co", 0, "", "", "Zed", "", "Zed", "", "", "", &churnYes, "", "", nil)
	if err != nil {
		t.Fatalf("create Old Co: %v", err)
	}
	if err := d.SoftDeleteAccount(archived.ID); err != nil {
		t.Fatalf("archive Old Co: %v", err)
	}

	renewal := mustTask(t, d, acme.ID, "Renewal call", "2030-01-02")
	invoice := mustTask(t, d, acme.ID, "Invoice follow-up", "2030-01-02")
	onboarding := mustTask(t, d, beta.ID, "Onboarding", "2030-01-02")

	accountCases := []struct {
		name   string
		filter AccountFilter
		want   []int64
	}{
		{"name substring", AccountFilter{Name: "acme"}, []int64{acme.ID}},
		{"name case-insensitive", AccountFilter{Name: "BETA"}, []int64{beta.ID}},
		{"category", AccountFilter{CategoryIDs: []int64{personal.ID}}, []int64{gamma.ID}},
		{"categories any", AccountFilter{CategoryIDs: []int64{work.ID, personal.ID}}, []int64{acme.ID, beta.ID, gamma.ID}},
		{"tag", AccountFilter{TagIDs: []int64{tagCold.ID}}, []int64{gamma.ID}},
		{"tags any", AccountFilter{TagIDs: []int64{tagHot.ID, tagCold.ID}}, []int64{acme.ID, gamma.ID}},
		{"csm", AccountFilter{CSMNames: []string{"alice"}}, []int64{acme.ID, gamma.ID}},
		{"csms any", AccountFilter{CSMNames: []string{"alice", "carol"}}, []int64{acme.ID, beta.ID, gamma.ID}},
		{"ae", AccountFilter{AENames: []string{"Bob"}}, []int64{acme.ID, beta.ID}},
		{"aes any", AccountFilter{AENames: []string{"bob", "dan"}}, []int64{acme.ID, beta.ID, gamma.ID}},
		{"churn", AccountFilter{ChurnRisks: []bool{true}}, []int64{acme.ID}},
		{"churn any", AccountFilter{ChurnRisks: []bool{true, false}}, []int64{acme.ID, beta.ID}},
		{"combined", AccountFilter{CategoryIDs: []int64{work.ID}, CSMNames: []string{"Alice"}}, []int64{acme.ID}},
	}
	for _, tc := range accountCases {
		accounts, err := d.ListAccounts(tc.filter)
		if err != nil {
			t.Fatalf("%s: list accounts: %v", tc.name, err)
		}
		if len(accounts) != len(tc.want) {
			t.Fatalf("%s: got %d accounts, want %d (%+v)", tc.name, len(accounts), len(tc.want), accounts)
		}
		for i, account := range accounts {
			if account.ID != tc.want[i] {
				t.Fatalf("%s: account %d = %d, want %d", tc.name, i, account.ID, tc.want[i])
			}
		}
	}

	taskCases := []struct {
		name   string
		filter TaskFilter
		want   []int64
	}{
		{"search account", TaskFilter{Search: "acme"}, []int64{renewal.ID, invoice.ID}},
		{"search title", TaskFilter{Search: "invoice"}, []int64{invoice.ID}},
		{"search ticket", TaskFilter{Search: "TICKET-Onboarding"}, []int64{onboarding.ID}},
		{"search notes", TaskFilter{Search: "notes for Onboarding"}, []int64{onboarding.ID}},
		{"csm", TaskFilter{CSMNames: []string{"Alice"}}, []int64{renewal.ID, invoice.ID}},
		{"csms any", TaskFilter{CSMNames: []string{"alice", "carol"}}, []int64{renewal.ID, invoice.ID, onboarding.ID}},
		{"ae", TaskFilter{AENames: []string{"Bob"}}, []int64{renewal.ID, invoice.ID, onboarding.ID}},
		{"churn", TaskFilter{ChurnRisks: []bool{true}}, []int64{renewal.ID, invoice.ID}},
		{"categories any", TaskFilter{CategoryIDs: []int64{work.ID, personal.ID}}, []int64{renewal.ID, invoice.ID, onboarding.ID}},
		{"tags any", TaskFilter{TagIDs: []int64{tagHot.ID, tagCold.ID}}, []int64{renewal.ID, invoice.ID}},
		{"combined", TaskFilter{CategoryIDs: []int64{work.ID}, Search: "invoice"}, []int64{invoice.ID}},
	}
	for _, tc := range taskCases {
		tasks, err := d.ListTasks(tc.filter)
		if err != nil {
			t.Fatalf("%s: list tasks: %v", tc.name, err)
		}
		if len(tasks) != len(tc.want) {
			t.Fatalf("%s: got %d tasks, want %d (%+v)", tc.name, len(tasks), len(tc.want), tasks)
		}
		for i, task := range tasks {
			if task.ID != tc.want[i] {
				t.Fatalf("%s: task %d = %d, want %d", tc.name, i, task.ID, tc.want[i])
			}
		}
	}

	csmNames, err := d.ListCSMNames()
	if err != nil {
		t.Fatalf("list CSM names: %v", err)
	}
	if len(csmNames) != 2 || csmNames[0] != "Alice" || csmNames[1] != "Carol" {
		t.Fatalf("CSM names = %v, want Alice and Carol", csmNames)
	}
	aeNames, err := d.ListAENames()
	if err != nil {
		t.Fatalf("list AE names: %v", err)
	}
	if len(aeNames) != 2 || aeNames[0] != "Bob" || aeNames[1] != "Dan" {
		t.Fatalf("AE names = %v, want Bob and Dan", aeNames)
	}
}

func TestReorderNormalizesDuplicateSortOrders(t *testing.T) {
	d := openTestDB(t)
	category := mustCategory(t, d, "Work")
	account := mustAccount(t, d, category.ID, "Acme")
	first := mustTask(t, d, account.ID, "First", "2030-01-02")
	second := mustTask(t, d, account.ID, "Second", "2030-01-02")

	if _, err := d.SQL().Exec(`UPDATE tasks SET sort_order = 0 WHERE id IN (?, ?)`, first.ID, second.ID); err != nil {
		t.Fatalf("force duplicate sort orders: %v", err)
	}
	if _, err := d.ReorderTask(first.ID, first.DueDate, 0); err != nil {
		t.Fatalf("reorder task: %v", err)
	}
	tasks, err := d.ListTasks(TaskFilter{AccountID: account.ID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(tasks))
	}
	if tasks[0].SortOrder == tasks[1].SortOrder {
		t.Fatalf("duplicate sort orders remain: %v and %v", tasks[0].SortOrder, tasks[1].SortOrder)
	}
	if tasks[0].ID != first.ID || tasks[1].ID != second.ID {
		t.Fatalf("normalized order = %d,%d, want %d,%d", tasks[0].ID, tasks[1].ID, first.ID, second.ID)
	}
}

func TestActiveNamesCanBeReusedAfterSoftDelete(t *testing.T) {
	d := openTestDB(t)
	category := mustCategory(t, d, "Work")
	account := mustAccount(t, d, category.ID, "Acme")

	if err := d.SoftDeleteAccount(account.ID); err != nil {
		t.Fatalf("soft delete account: %v", err)
	}
	replacement := mustAccount(t, d, category.ID, "Acme")
	if replacement.ID == account.ID {
		t.Fatal("replacement account reused the deleted row")
	}
	if err := d.SoftDeleteCategory(category.ID); err != nil {
		t.Fatalf("soft delete category: %v", err)
	}
	replacementCategory := mustCategory(t, d, "Work")
	if replacementCategory.ID == category.ID {
		t.Fatal("replacement category reused the deleted row")
	}
}

func TestAccountCreateRollsBackOnInvalidTag(t *testing.T) {
	d := openTestDB(t)
	category := mustCategory(t, d, "Work")
	if _, err := d.CreateAccount(category.ID, "Acme", 0, "sfdc", "subdomain", "csmName", "csmLink", "aeName", "aeLink", "slackChannel", "", nil, "", "notes", []int64{9999}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid tag error = %v, want ErrNotFound", err)
	}
	accounts, err := d.ListAccounts(AccountFilter{})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("accounts after failed create = %+v, want none", accounts)
	}

	account := mustAccount(t, d, category.ID, "Acme")
	task := mustTask(t, d, account.ID, "Call", "2030-01-02")
	if err := d.SoftDeleteAccount(account.ID); err != nil {
		t.Fatalf("soft delete account: %v", err)
	}
	if _, err := d.CreateTask(account.ID, "Blocked", "", "", "2030-01-02"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("create task on deleted account error = %v, want ErrNotFound", err)
	}
	if _, err := d.GetTask(task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived task lookup error = %v, want ErrNotFound", err)
	}
}
