package export

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mirador/internal/db"
)

func openTestDB(t *testing.T, name string) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), name+".db"))
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

type seed struct {
	category      db.Category
	deletedCat    db.Category
	tagA          db.Tag
	tagB          db.Tag
	unusedTag     db.Tag
	account       db.Account
	otherAccount  db.Account
	deletedAcct   db.Account
	activeTask    db.Task
	completedTask db.Task
	deletedTask   db.Task
	archivedTask  db.Task
}

func seedDatabase(t *testing.T, d *db.DB) seed {
	t.Helper()
	var s seed
	var err error

	s.category, err = d.CreateCategory("Work", "#3b82f6", 0, true)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	s.deletedCat, err = d.CreateCategory("Old", "#111111", 1, false)
	if err != nil {
		t.Fatalf("create deleted category: %v", err)
	}
	if err := d.SoftDeleteCategory(s.deletedCat.ID); err != nil {
		t.Fatalf("soft delete category: %v", err)
	}
	s.tagA, err = d.CreateTag("Urgent", "#ff0000")
	if err != nil {
		t.Fatalf("create tag A: %v", err)
	}
	s.tagB, err = d.CreateTag("Waiting", "#00ff00")
	if err != nil {
		t.Fatalf("create tag B: %v", err)
	}
	s.unusedTag, err = d.CreateTag("Unused", "#0000ff")
	if err != nil {
		t.Fatalf("create unused tag: %v", err)
	}

	s.account, err = d.CreateAccount(s.category.ID, "Acme", 0,
		"https://acme.example", "phone", "", "", "email", "", "", "", nil, "2031-03-15", "Line one\nLine two", []int64{s.tagA.ID, s.tagB.ID})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	s.otherAccount, err = d.CreateAccount(s.category.ID, "Beta", 1,
		"", "", "", "", "", "", "", "", nil, "", "Line two", []int64{s.tagB.ID})
	if err != nil {
		t.Fatalf("create other account: %v", err)
	}
	s.deletedAcct, err = d.CreateAccount(s.category.ID, "Gone", 2,
		"", "", "", "", "", "", "", "", nil, "", "archived", []int64{s.tagA.ID})
	if err != nil {
		t.Fatalf("create deleted account: %v", err)
	}

	s.activeTask, err = d.CreateTask(s.account.ID, "Active task", "", "active notes", "2030-01-02")
	if err != nil {
		t.Fatalf("create active task: %v", err)
	}
	s.completedTask, err = d.CreateTask(s.account.ID, "Completed task", "", "done notes", "2030-01-03")
	if err != nil {
		t.Fatalf("create completed task: %v", err)
	}
	s.completedTask, err = d.UpdateTask(s.completedTask.ID, s.completedTask.Title, s.completedTask.Ticket, s.completedTask.Details, s.completedTask.DueDate, true)
	if err != nil {
		t.Fatalf("complete task: %v", err)
	}
	s.deletedTask, err = d.CreateTask(s.otherAccount.ID, "Deleted task", "", "delete me", "2030-01-04")
	if err != nil {
		t.Fatalf("create deleted task: %v", err)
	}
	if err := d.SoftDeleteTask(s.deletedTask.ID); err != nil {
		t.Fatalf("soft delete task: %v", err)
	}
	s.archivedTask, err = d.CreateTask(s.deletedAcct.ID, "Archived with account", "", "", "2030-01-05")
	if err != nil {
		t.Fatalf("create archived task: %v", err)
	}
	if err := d.SoftDeleteAccount(s.deletedAcct.ID); err != nil {
		t.Fatalf("soft delete account: %v", err)
	}
	return s
}

func TestCSVRoundTrip(t *testing.T) {
	source := openTestDB(t, "source")
	s := seedDatabase(t, source)

	data, err := Export(source, true, nil)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("parse export: %v", err)
	}
	if len(records) != 1+2+3+3+4 { // header + categories + tags + accounts + tasks
		t.Fatalf("export row count = %d, want %d", len(records), 1+2+3+3+4)
	}
	for i, record := range records {
		if len(record) != len(header) {
			t.Fatalf("row %d has %d columns, want %d", i, len(record), len(header))
		}
		if i > 0 && record[0] != strconv.Itoa(FormatVersion) {
			t.Fatalf("row %d format_version = %q", i, record[0])
		}
	}

	target := openTestDB(t, "target")
	result, err := ImportCSV(target, bytes.NewReader(data), "duplicate", nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	want := ImportResult{Categories: 2, Tags: 3, Accounts: 3, Tasks: 4}
	if result != want {
		t.Fatalf("import result = %+v, want %+v", result, want)
	}

	categories, err := target.ListCategories(false)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(categories) != 1 || categories[0].Name != "Work" || !categories[0].Expanded {
		t.Fatalf("active categories = %+v, want one expanded Work", categories)
	}
	allCategories, err := target.ListCategories(true)
	if err != nil {
		t.Fatalf("list all categories: %v", err)
	}
	if len(allCategories) != 2 {
		t.Fatalf("all categories = %d, want 2", len(allCategories))
	}
	var deletedCategory db.Category
	for _, category := range allCategories {
		if category.DeletedAt != nil {
			deletedCategory = category
		}
	}
	if deletedCategory.Name != "Old" || deletedCategory.Expanded {
		t.Fatalf("deleted category = %+v, want Old collapsed", deletedCategory)
	}

	tags, err := target.ListTags(false)
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("tags = %d, want 3", len(tags))
	}
	usage := map[string]int{}
	colors := map[string]string{}
	for _, tag := range tags {
		usage[tag.Name] = tag.UsageCount
		colors[tag.Name] = tag.Color
	}
	if usage["Urgent"] != 1 || usage["Waiting"] != 2 || usage["Unused"] != 0 {
		t.Fatalf("tag usage = %+v, want Urgent=1 Waiting=2 Unused=0", usage)
	}
	if colors["Urgent"] != "#ff0000" || colors["Waiting"] != "#00ff00" || colors["Unused"] != "#0000ff" {
		t.Fatalf("tag colors = %+v", colors)
	}

	accounts, err := target.ListAccounts(db.AccountFilter{})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("active accounts = %d, want 2", len(accounts))
	}
	byName := map[string]db.Account{}
	for _, account := range accounts {
		byName[account.Name] = account
	}
	acme := byName["Acme"]
	if acme.SFDC != "https://acme.example" || acme.Subdomain != "phone" {
		t.Fatalf("acme info fields = %+v", acme)
	}
	if acme.Notes != "Line one\nLine two" {
		t.Fatalf("acme notes = %q", acme.Notes)
	}
	if acme.RenewalDate != "2031-03-15" {
		t.Fatalf("acme renewal date = %q, want 2031-03-15", acme.RenewalDate)
	}
	if len(acme.Tags) != 2 || acme.Tags[0].Name != "Urgent" || acme.Tags[1].Name != "Waiting" {
		t.Fatalf("acme tags = %+v", acme.Tags)
	}
	if acme.CategoryName != "Work" || acme.CategoryID == 0 {
		t.Fatalf("acme category = %+v", acme)
	}

	deletedAccounts, err := target.ListDeletedAccounts()
	if err != nil {
		t.Fatalf("list deleted accounts: %v", err)
	}
	if len(deletedAccounts) != 1 || deletedAccounts[0].Name != "Gone" || deletedAccounts[0].DeletedAt == nil {
		t.Fatalf("deleted accounts = %+v, want Gone", deletedAccounts)
	}
	if len(deletedAccounts[0].Tags) != 1 || deletedAccounts[0].Tags[0].Name != "Urgent" {
		t.Fatalf("deleted account tags = %+v, want Urgent", deletedAccounts[0].Tags)
	}

	tasks, err := target.ListTasks(db.TaskFilter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("active tasks = %d, want 2", len(tasks))
	}
	taskByName := map[string]db.Task{}
	for _, task := range tasks {
		taskByName[task.Title] = task
	}
	active := taskByName["Active task"]
	if active.Details != "active notes" || active.DueDate != "2030-01-02" || active.Completed {
		t.Fatalf("active task = %+v", active)
	}
	completed := taskByName["Completed task"]
	if !completed.Completed || completed.CompletedAt == nil {
		t.Fatalf("completed task = %+v", completed)
	}
	if completed.CompletedAt == nil || s.completedTask.CompletedAt == nil || *completed.CompletedAt != *s.completedTask.CompletedAt {
		t.Fatalf("completed_at = %v, want %v", completed.CompletedAt, s.completedTask.CompletedAt)
	}

	allTasks, err := target.ListTasks(db.TaskFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("list all tasks: %v", err)
	}
	if len(allTasks) != 4 {
		t.Fatalf("all tasks = %d, want 4", len(allTasks))
	}
	for _, task := range allTasks {
		switch task.Title {
		case "Deleted task":
			if task.DeletedAt == nil {
				t.Fatalf("Deleted task lost deleted_at: %+v", task)
			}
		case "Archived with account":
			if task.DeletedAt == nil {
				t.Fatalf("Archived task lost deleted_at: %+v", task)
			}
		}
	}
	if s.activeTask.SortOrder != active.SortOrder || s.activeTask.DueDate != active.DueDate {
		t.Fatalf("sort/due order changed: got %v/%s want %v/%s",
			active.SortOrder, active.DueDate, s.activeTask.SortOrder, s.activeTask.DueDate)
	}
}

func TestDuplicateImport(t *testing.T) {
	source := openTestDB(t, "source")
	seedDatabase(t, source)
	data, err := Export(source, true, nil)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	result, err := ImportCSV(source, bytes.NewReader(data), "duplicate", nil)
	if err != nil {
		t.Fatalf("duplicate import: %v", err)
	}
	if result != (ImportResult{Categories: 2, Tags: 3, Accounts: 3, Tasks: 4}) {
		t.Fatalf("duplicate import result = %+v", result)
	}
	categories, err := source.ListCategories(false)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	names := map[string]bool{}
	for _, category := range categories {
		names[category.Name] = true
	}
	if !names["Work"] || !names["Work (imported)"] {
		t.Fatalf("category names after duplicate import = %v", names)
	}
	tags, err := source.ListTags(false)
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	tagNames := map[string]bool{}
	for _, tag := range tags {
		tagNames[tag.Name] = true
	}
	if !tagNames["Urgent"] || !tagNames["Urgent (imported)"] {
		t.Fatalf("tag names after duplicate import = %v", tagNames)
	}
	accounts, err := source.ListAccounts(db.AccountFilter{})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	accountNames := map[string]int{}
	for _, account := range accounts {
		accountNames[account.Name]++
	}
	if len(accounts) != 4 || accountNames["Acme"] != 2 || accountNames["Beta"] != 2 {
		t.Fatalf("account names after duplicate import = %v, want two each of Acme and Beta", accountNames)
	}
	allTasks, err := source.ListTasks(db.TaskFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("list all tasks: %v", err)
	}
	if len(allTasks) != 8 {
		t.Fatalf("tasks after duplicate import = %d, want 8 (always appended)", len(allTasks))
	}
}

func TestOverrideImport(t *testing.T) {
	source := openTestDB(t, "source")
	seedDatabase(t, source)
	data, err := Export(source, true, nil)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	target := openTestDB(t, "target")
	if _, err := ImportCSV(target, bytes.NewReader(data), "duplicate", nil); err != nil {
		t.Fatalf("seed target import: %v", err)
	}

	tags, err := target.ListTags(false)
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	var urgent db.Tag
	for _, tag := range tags {
		if tag.Name == "Urgent" {
			urgent = tag
		}
	}
	if urgent.ID == 0 {
		t.Fatal("Urgent tag missing from target")
	}
	if _, err := target.SQL().Exec(`UPDATE tags SET color = '#abcdef' WHERE id = ?`, urgent.ID); err != nil {
		t.Fatalf("change tag color: %v", err)
	}
	accounts, err := target.ListAccounts(db.AccountFilter{})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	var acme db.Account
	for _, account := range accounts {
		if account.Name == "Acme" {
			acme = account
		}
	}
	if acme.ID == 0 {
		t.Fatal("Acme account missing from target")
	}
	churnTrue := true
	if _, err := target.UpdateAccount(acme.ID, acme.Name, acme.SortOrder,
		"changed", "changed", "changed", "changed", "changed", "changed", "changed", "changed", &churnTrue, "changed", "changed", nil); err != nil {
		t.Fatalf("change account: %v", err)
	}

	result, err := ImportCSV(target, bytes.NewReader(data), "override", nil)
	if err != nil {
		t.Fatalf("override import: %v", err)
	}
	if result != (ImportResult{Categories: 2, Tags: 3, Accounts: 3, Tasks: 4}) {
		t.Fatalf("override import result = %+v", result)
	}
	categories, err := target.ListCategories(false)
	if err != nil {
		t.Fatalf("list categories after override: %v", err)
	}
	if len(categories) != 1 {
		t.Fatalf("active categories after override = %d, want 1", len(categories))
	}
	account, err := target.GetAccount(acme.ID, false)
	if err != nil {
		t.Fatalf("get overridden account: %v", err)
	}
	if account.SFDC != "https://acme.example" || account.Notes != "Line one\nLine two" {
		t.Fatalf("override did not restore account fields: %+v", account)
	}
	tag, err := target.GetTag(urgent.ID)
	if err != nil {
		t.Fatalf("get overridden tag: %v", err)
	}
	if tag.Color != "#ff0000" {
		t.Fatalf("override tag color = %q, want #ff0000", tag.Color)
	}
	allTasks, err := target.ListTasks(db.TaskFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("list all tasks after override: %v", err)
	}
	if len(allTasks) != 8 {
		t.Fatalf("tasks after override = %d, want 8 (tasks always append)", len(allTasks))
	}
}

func TestActiveOnlyExport(t *testing.T) {
	source := openTestDB(t, "source")
	seedDatabase(t, source)

	data, err := Export(source, false, nil)
	if err != nil {
		t.Fatalf("active-only export: %v", err)
	}
	target := openTestDB(t, "target")
	result, err := ImportCSV(target, bytes.NewReader(data), "duplicate", nil)
	if err != nil {
		t.Fatalf("import active-only export: %v", err)
	}
	// One active category, three tags, two active accounts, two active tasks.
	if result != (ImportResult{Categories: 1, Tags: 3, Accounts: 2, Tasks: 2}) {
		t.Fatalf("active-only import result = %+v", result)
	}
	deletedCategories, _ := target.ListDeletedCategories()
	deletedAccounts, _ := target.ListDeletedAccounts()
	deletedTasks, _ := target.ListDeletedTasks()
	if len(deletedCategories) != 0 || len(deletedAccounts) != 0 || len(deletedTasks) != 0 {
		t.Fatalf("active-only import produced deleted rows: %d/%d/%d",
			len(deletedCategories), len(deletedAccounts), len(deletedTasks))
	}
}

func TestImportValidation(t *testing.T) {
	source := openTestDB(t, "source")
	seedDatabase(t, source)
	data, err := Export(source, true, nil)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	target := openTestDB(t, "target")

	if _, err := ImportCSV(target, strings.NewReader(""), "duplicate", nil); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("empty import error = %v, want ErrInvalid", err)
	}
	if _, err := ImportCSV(target, strings.NewReader("a,b\n"), "duplicate", nil); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("bad header error = %v, want ErrInvalid", err)
	}
	if _, err := ImportCSV(target, bytes.NewReader(data), "merge", nil); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("bad conflict mode error = %v, want ErrInvalid", err)
	}

	unsupported := strings.Replace(string(data), "\n"+strconv.Itoa(FormatVersion)+",category,", "\n"+strconv.Itoa(FormatVersion+1)+",category,", 1)
	if _, err := ImportCSV(target, strings.NewReader(unsupported), "duplicate", nil); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("unsupported version error = %v, want ErrInvalid", err)
	}

	duplicateID := string(data)
	lines := strings.SplitAfter(duplicateID, "\n")
	duplicateID = strings.Join(lines[:2], "") + lines[1] + strings.Join(lines[2:], "")
	if _, err := ImportCSV(target, strings.NewReader(duplicateID), "duplicate", nil); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("duplicate id error = %v, want ErrInvalid", err)
	}

	unknownCategory := mutateRecords(t, data, func(record []string) []string {
		if record[1] == "account" {
			record[4] = "9999"
		}
		return record
	})
	if _, err := ImportCSV(target, strings.NewReader(unknownCategory), "duplicate", nil); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("unknown category error = %v, want ErrInvalid", err)
	}

	badDueDate := mutateRecords(t, data, func(record []string) []string {
		if record[1] == "task" {
			record[22] = "not-a-date"
		}
		return record
	})
	if _, err := ImportCSV(target, strings.NewReader(badDueDate), "duplicate", nil); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("bad due date error = %v, want ErrInvalid", err)
	}

	badRenewalDate := mutateRecords(t, data, func(record []string) []string {
		if record[1] == "account" {
			record[20] = "03/15/2031"
		}
		return record
	})
	if _, err := ImportCSV(target, strings.NewReader(badRenewalDate), "duplicate", nil); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("bad renewal date error = %v, want ErrInvalid", err)
	}

	emptyTag := mutateRecords(t, data, func(record []string) []string {
		if record[1] == "account" {
			record[28] = `[""]`
		}
		return record
	})
	if _, err := ImportCSV(target, strings.NewReader(emptyTag), "duplicate", nil); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("empty tag error = %v, want ErrInvalid", err)
	}

	// A failed import must not leave partial data behind.
	if accounts, err := target.ListAccounts(db.AccountFilter{IncludeDeleted: true}); err != nil || len(accounts) != 0 {
		t.Fatalf("target after failed imports has %d accounts (err %v), want 0", len(accounts), err)
	}
}

func mutateRecords(t *testing.T, data []byte, mutate func([]string) []string) string {
	t.Helper()
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("parse export: %v", err)
	}
	var builder strings.Builder
	writer := csv.NewWriter(&builder)
	for _, record := range records {
		if err := writer.Write(mutate(record)); err != nil {
			t.Fatalf("write record: %v", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatalf("write CSV: %v", err)
	}
	return builder.String()
}

func TestJSONRoundTrip(t *testing.T) {
	source := openTestDB(t, "source")
	seedDatabase(t, source)

	data, err := ExportJSON(source, true, nil)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	var exported struct {
		FormatVersion int           `json:"format_version"`
		Categories    []db.Category `json:"categories"`
		Tags          []db.Tag      `json:"tags"`
		Accounts      []db.Account  `json:"accounts"`
		Tasks         []db.Task     `json:"tasks"`
	}
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("parse export: %v", err)
	}
	if exported.FormatVersion != FormatVersion {
		t.Fatalf("format_version = %d, want %d", exported.FormatVersion, FormatVersion)
	}
	// headerless JSON: count records directly
	if len(exported.Categories) != 2 || len(exported.Tags) != 3 || len(exported.Accounts) != 3 || len(exported.Tasks) != 4 {
		t.Fatalf("export counts = %d/%d/%d/%d, want 2/3/3/4", len(exported.Categories), len(exported.Tags), len(exported.Accounts), len(exported.Tasks))
	}
	// Check tags are embedded on accounts
	acme := findAccountInSlice(exported.Accounts, "Acme")
	if acme == nil {
		t.Fatal("acme account not found in export")
	}
	if len(acme.Tags) != 2 || acme.Tags[0].Name != "Urgent" || acme.Tags[1].Name != "Waiting" {
		t.Fatalf("acme tags = %+v", acme.Tags)
	}
	if acme.RenewalDate != "2031-03-15" {
		t.Fatalf("acme renewal date = %q, want 2031-03-15", acme.RenewalDate)
	}

	target := openTestDB(t, "target")
	result, err := ImportJSON(target, bytes.NewReader(data), "duplicate", nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	want := ImportResult{Categories: 2, Tags: 3, Accounts: 3, Tasks: 4}
	if result != want {
		t.Fatalf("import result = %+v, want %+v", result, want)
	}

	// Verify round-trip data integrity
	categories, err := target.ListCategories(false)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(categories) != 1 || categories[0].Name != "Work" || !categories[0].Expanded {
		t.Fatalf("active categories = %+v, want one expanded Work", categories)
	}

	allCategories, err := target.ListCategories(true)
	if err != nil {
		t.Fatalf("list all categories: %v", err)
	}
	if len(allCategories) != 2 {
		t.Fatalf("all categories = %d, want 2", len(allCategories))
	}

	tags, err := target.ListTags(false)
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("tags = %d, want 3", len(tags))
	}

	accounts, err := target.ListAccounts(db.AccountFilter{})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("active accounts = %d, want 2", len(accounts))
	}
	byName := map[string]db.Account{}
	for _, account := range accounts {
		byName[account.Name] = account
	}
	acme2 := byName["Acme"]
	if acme2.SFDC != "https://acme.example" || acme2.Subdomain != "phone" {
		t.Fatalf("acme info fields = %+v", acme2)
	}
	if acme2.Notes != "Line one\nLine two" {
		t.Fatalf("acme notes = %q", acme2.Notes)
	}
	if acme2.RenewalDate != "2031-03-15" {
		t.Fatalf("acme renewal date = %q, want 2031-03-15", acme2.RenewalDate)
	}
	if len(acme2.Tags) != 2 || acme2.Tags[0].Name != "Urgent" || acme2.Tags[1].Name != "Waiting" {
		t.Fatalf("acme tags = %+v", acme2.Tags)
	}

	tasks, err := target.ListTasks(db.TaskFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("tasks = %d, want 4", len(tasks))
	}
}

func findAccountInSlice(accounts []db.Account, name string) *db.Account {
	for i := range accounts {
		if accounts[i].Name == name {
			return &accounts[i]
		}
	}
	return nil
}

func TestJSONImportInvalidFormat(t *testing.T) {
	target := openTestDB(t, "target")
	data := []byte(`{"format_version":99,"categories":[]}`)
	_, err := ImportJSON(target, bytes.NewReader(data), "duplicate", nil)
	if !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("invalid format error = %v, want ErrInvalid", err)
	}
}

func TestJSONImportInvalidJSON(t *testing.T) {
	target := openTestDB(t, "target")
	_, err := ImportJSON(target, strings.NewReader("not json"), "duplicate", nil)
	if !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("invalid json error = %v, want ErrInvalid", err)
	}
}

func TestJSONImportDefaultsColor(t *testing.T) {
	target := openTestDB(t, "target")
	data := []byte(`{"format_version":2,"categories":[{"name":"No color"}],"tags":[{"name":"No color tag"}]}`)
	if _, err := ImportJSON(target, bytes.NewReader(data), "duplicate", nil); err != nil {
		t.Fatalf("import json: %v", err)
	}
	categories, err := target.ListCategories(false)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(categories) != 1 || categories[0].Color != "#6b7280" {
		t.Fatalf("category colors = %+v, want #6b7280", categories)
	}
	tags, err := target.ListTags(false)
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	if len(tags) != 1 || tags[0].Color != "#6b7280" {
		t.Fatalf("tag colors = %+v, want #6b7280", tags)
	}
}

func TestValidColorDefaults(t *testing.T) {
	color, err := validColor("")
	if err != nil || color != defaultColor {
		t.Fatalf("blank color = %q, %v; want %q", color, err, defaultColor)
	}
	if _, err := validColor("blue"); err == nil {
		t.Fatal("invalid color should be rejected")
	}
}
