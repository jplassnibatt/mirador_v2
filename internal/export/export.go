// Package export implements Mirador's versioned CSV export and import.
//
// The file is a single CSV with one row per record. The account_tags
// many-to-many relation is represented as a JSON array of tag names on each
// account row. Every row carries format_version=2. IDs in the file are local
// to the file; import builds a mapping to database IDs, which makes the format
// portable between machines.
package export

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mirador/internal/db"
)

// FormatVersion is the current CSV format version.
const FormatVersion = 2

// Sections selects which record types are exported or imported.
// A nil or empty map means all sections.
type Sections map[string]bool

var validSections = map[string]bool{
	"categories": true,
	"tags":       true,
	"accounts":   true,
	"tasks":      true,
}

// ParseSections parses a comma-separated section list like
// "categories,tags,accounts,tasks". Empty or omitted means all sections.
// Unknown section names return an error.
func ParseSections(s string) (Sections, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	sections := make(Sections)
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(strings.ToLower(part))
		if name == "" {
			continue
		}
		if !validSections[name] {
			return nil, fmt.Errorf("%w: unknown section %q", db.ErrInvalid, name)
		}
		sections[name] = true
	}
	return sections, nil
}

// has reports whether a section is selected. Nil means all sections.
func (s Sections) has(section string) bool {
	if s == nil {
		return true
	}
	return s[section]
}

const defaultColor = "#6b7280"

var (
	header = []string{
		"format_version", "record_type", "id", "name", "category_id", "account_id",
		"title", "ticket", "color", "sort_order", "expanded", "sfdc", "subdomain", "csm_name", "csm_link",
		"ae_name", "ae_link", "chatapp_channel", "arr", "churn_risk", "renewal_date", "details", "due_date", "completed", "completed_at",
		"deleted_at", "created_at", "updated_at", "tags",
	}
	hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	datePattern     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// Bundle is the in-memory form of an export.
type Bundle struct {
	Categories  []db.Category
	Tags        []db.Tag
	Accounts    []db.Account
	Tasks       []db.Task
	AccountTags map[int64][]string
}

// Load reads all requested rows from the database.
func Load(d *db.DB, includeDeleted bool, sections Sections) (Bundle, error) {
	var categories []db.Category
	var err error

	if sections.has("categories") {
		categories, err = d.ListCategories(includeDeleted)
		if err != nil {
			return Bundle{}, err
		}
	}
	var tags []db.Tag
	if sections.has("tags") {
		tags, err = d.ListTags(includeDeleted)
		if err != nil {
			return Bundle{}, err
		}
	}
	var accounts []db.Account
	if sections.has("accounts") {
		accounts, err = d.ListAccounts(db.AccountFilter{IncludeDeleted: includeDeleted})
		if err != nil {
			return Bundle{}, err
		}
	}
	var tasks []db.Task
	if sections.has("tasks") {
		tasks, err = d.ListTasks(db.TaskFilter{IncludeDeleted: includeDeleted})
		if err != nil {
			return Bundle{}, err
		}
	}
	accountTags, err := d.AllAccountTags()
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{
		Categories:  categories,
		Tags:        tags,
		Accounts:    accounts,
		Tasks:       tasks,
		AccountTags: accountTags,
	}, nil
}

// CSV renders a bundle as the versioned CSV format.
func CSV(bundle Bundle, sections Sections) ([]byte, error) {
	var builder strings.Builder
	writer := csv.NewWriter(&builder)
	if err := writer.Write(header); err != nil {
		return nil, err
	}

	version := strconv.Itoa(FormatVersion)
	write := func(row []string) error {
		full := make([]string, len(header))
		full[0] = version
		copy(full[1:], row)
		return writer.Write(full)
	}

	for _, c := range bundle.Categories {
		if !sections.has("categories") {
			break
		}
		if err := write([]string{
			"category", strconv.FormatInt(c.ID, 10), c.Name, "", "",
			"", "", c.Color, strconv.FormatFloat(c.SortOrder, 'f', -1, 64), boolString(c.Expanded),
			"", "", "", "", "", "", "", "", "", "", "", "", "", "", ptrString(c.DeletedAt), c.CreatedAt, c.UpdatedAt, "",
		}); err != nil {
			return nil, err
		}
	}
	for _, t := range bundle.Tags {
		if !sections.has("tags") {
			break
		}
		if err := write([]string{
			"tag", strconv.FormatInt(t.ID, 10), t.Name, "", "",
			"", "", t.Color, "0", "",
			"", "", "", "", "", "", "", "", "", "", "", "", "", "", ptrString(t.DeletedAt), t.CreatedAt, t.UpdatedAt, "",
		}); err != nil {
			return nil, err
		}
	}
	for _, a := range bundle.Accounts {
		if !sections.has("accounts") {
			break
		}
		tagNames := bundle.AccountTags[a.ID]
		if tagNames == nil {
			tagNames = []string{}
		}
		tagsJSON, err := json.Marshal(tagNames)
		if err != nil {
			return nil, err
		}
		if err := write([]string{
			"account", strconv.FormatInt(a.ID, 10), a.Name, strconv.FormatInt(a.CategoryID, 10), "",
			"", "", "", strconv.FormatFloat(a.SortOrder, 'f', -1, 64),
			"", a.SFDC, a.Subdomain, a.CSMName, a.CSMLink, a.AEName, a.AELink, a.SlackChannel, a.Arr, churnRiskCsv(a.ChurnRisk), a.RenewalDate, a.Notes, "", "", "", ptrString(a.DeletedAt), a.CreatedAt, a.UpdatedAt, string(tagsJSON),
		}); err != nil {
			return nil, err
		}
	}
	for _, t := range bundle.Tasks {
		if !sections.has("tasks") {
			break
		}
		if err := write([]string{
			"task", strconv.FormatInt(t.ID, 10), "", "", strconv.FormatInt(t.AccountID, 10),
			t.Title, t.Ticket, "", strconv.FormatFloat(t.SortOrder, 'f', -1, 64), "",
			"", "", "", "", "", "", "", "", "", "", t.Details, t.DueDate, boolString(t.Completed), ptrString(t.CompletedAt),
			ptrString(t.DeletedAt), t.CreatedAt, t.UpdatedAt, "",
		}); err != nil {
			return nil, err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return []byte(builder.String()), nil
}

// Export reads and renders a complete CSV export.
func Export(d *db.DB, includeDeleted bool, sections Sections) ([]byte, error) {
	bundle, err := Load(d, includeDeleted, sections)
	if err != nil {
		return nil, err
	}
	return CSV(bundle, sections)
}

// ExportJSON reads and renders a complete JSON export.
func ExportJSON(d *db.DB, includeDeleted bool, sections Sections) ([]byte, error) {
	bundle, err := Load(d, includeDeleted, sections)
	if err != nil {
		return nil, err
	}
	return JSON(bundle, sections)
}

// JSON renders a bundle as JSON.
func JSON(bundle Bundle, sections Sections) ([]byte, error) {
	type exportBundle struct {
		FormatVersion int            `json:"format_version"`
		Categories    *[]db.Category `json:"categories,omitempty"`
		Tags          *[]db.Tag      `json:"tags,omitempty"`
		Accounts      *[]db.Account  `json:"accounts,omitempty"`
		Tasks         *[]db.Task     `json:"tasks,omitempty"`
	}
	b := exportBundle{
		FormatVersion: FormatVersion,
	}
	if sections.has("categories") {
		b.Categories = &bundle.Categories
	}
	if sections.has("tags") {
		b.Tags = &bundle.Tags
	}
	if sections.has("accounts") {
		b.Accounts = &bundle.Accounts
	}
	if sections.has("tasks") {
		b.Tasks = &bundle.Tasks
	}
	return json.MarshalIndent(b, "", "  ")
}

// ImportJSON imports all data from a JSON reader.
func ImportJSON(d *db.DB, r io.Reader, conflict string, sections Sections) (ImportResult, error) {
	var imp jsonImport
	if err := json.NewDecoder(r).Decode(&imp); err != nil {
		return ImportResult{}, fmt.Errorf("%w: invalid JSON: %v", db.ErrInvalid, err)
	}
	if imp.FormatVersion != 0 && imp.FormatVersion != FormatVersion {
		return ImportResult{}, fmt.Errorf("%w: unsupported format version %d, want %d", db.ErrInvalid, imp.FormatVersion, FormatVersion)
	}

	result := ImportResult{}

	err := d.WithTx(func(tx *sql.Tx) error {
		catIDMap := make(map[int64]int64)
		tagIDMap := make(map[int64]int64)
		acctIDMap := make(map[int64]int64)

		// 1. Categories
		for _, c := range imp.Categories {
			if !sections.has("categories") {
				break
			}
			newID, err := importCategoryJSON(tx, c, conflict)
			if err != nil {
				return err
			}
			catIDMap[c.ID] = newID
			result.Categories++
		}

		// 2. Tags
		for _, t := range imp.Tags {
			if !sections.has("tags") {
				break
			}
			newID, err := importTagJSON(tx, t, conflict)
			if err != nil {
				return err
			}
			tagIDMap[t.ID] = newID
			result.Tags++
		}

		// 3. Accounts (with tags matched by name)
		for _, a := range imp.Accounts {
			if !sections.has("accounts") {
				break
			}
			newCatID := catIDMap[a.CategoryID]
			newID, err := importAccountJSON(tx, a, newCatID, conflict, tagIDMap)
			if err != nil {
				return err
			}
			acctIDMap[a.ID] = newID
			result.Accounts++
		}

		// 4. Tasks (tags inherited from account)
		for _, t := range imp.Tasks {
			if !sections.has("tasks") {
				break
			}
			newAcctID := acctIDMap[t.AccountID]
			_, err := importTaskJSON(tx, t, newAcctID, conflict)
			if err != nil {
				return err
			}
			result.Tasks++
		}

		return nil
	})

	return result, err
}

// jsonImport is the input structure for JSON import.
type jsonImport struct {
	FormatVersion int           `json:"format_version"`
	Categories    []db.Category `json:"categories"`
	Tags          []db.Tag      `json:"tags"`
	Accounts      []db.Account  `json:"accounts"`
	Tasks         []db.Task     `json:"tasks"`
}

func importCategoryJSON(tx *sql.Tx, c db.Category, conflict string) (int64, error) {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		return 0, fmt.Errorf("%w: category name is required", db.ErrInvalid)
	}
	color, err := validColor(c.Color)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", db.ErrInvalid, err)
	}

	var id int64
	var deleted sql.NullString
	err = tx.QueryRow(`SELECT id, deleted_at FROM categories WHERE name = ?`, name).Scan(&id, &deleted)
	if err == sql.ErrNoRows {
		result, err := tx.Exec(`
			INSERT INTO categories(name, color, sort_order, expanded, deleted_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			name, color, c.SortOrder, c.Expanded, nullString(c.DeletedAt), c.CreatedAt, c.UpdatedAt)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	}
	if err != nil {
		return 0, err
	}

	// Existing category with same name
	switch conflict {
	case "override":
		if deleted.Valid {
			if _, err := tx.Exec(`UPDATE categories SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
				return 0, err
			}
		}
		if _, err := tx.Exec(`UPDATE categories SET name = ?, color = ?, sort_order = ?, expanded = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, name, color, c.SortOrder, c.Expanded, id); err != nil {
			return 0, err
		}
		return id, nil
	case "duplicate":
		result, err := tx.Exec(`
			INSERT INTO categories(name, color, sort_order, expanded, deleted_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			name+" (imported)", color, c.SortOrder, c.Expanded, nullString(c.DeletedAt), c.CreatedAt, c.UpdatedAt)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	default:
		return 0, fmt.Errorf("%w: conflict must be override or duplicate", db.ErrInvalid)
	}
}

func importTagJSON(tx *sql.Tx, t db.Tag, conflict string) (int64, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return 0, fmt.Errorf("%w: tag name is required", db.ErrInvalid)
	}
	color, err := validColor(t.Color)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", db.ErrInvalid, err)
	}

	var id int64
	var deleted sql.NullString
	err = tx.QueryRow(`SELECT id, deleted_at FROM tags WHERE name = ?`, name).Scan(&id, &deleted)
	if err == sql.ErrNoRows {
		result, err := tx.Exec(`
			INSERT INTO tags(name, color, deleted_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`,
			name, color, nullString(t.DeletedAt), t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	}
	if err != nil {
		return 0, err
	}

	switch conflict {
	case "override":
		if deleted.Valid {
			if _, err := tx.Exec(`UPDATE tags SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
				return 0, err
			}
		}
		if _, err := tx.Exec(`UPDATE tags SET color = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, color, id); err != nil {
			return 0, err
		}
		return id, nil
	case "duplicate":
		result, err := tx.Exec(`
			INSERT INTO tags(name, color, deleted_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`,
			name+" (imported)", color, nullString(t.DeletedAt), t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	default:
		return 0, fmt.Errorf("%w: conflict must be override or duplicate", db.ErrInvalid)
	}
}

func importAccountJSON(tx *sql.Tx, a db.Account, categoryID int64, conflict string, tagIDMap map[int64]int64) (int64, error) {
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return 0, fmt.Errorf("%w: account name is required", db.ErrInvalid)
	}

	var id int64
	var deleted sql.NullString
	var err error
	err = tx.QueryRow(`SELECT id, deleted_at FROM accounts WHERE category_id = ? AND name = ?`, categoryID, name).Scan(&id, &deleted)
	if err == sql.ErrNoRows {
		result, err := tx.Exec(`
			INSERT INTO accounts(category_id, name, sort_order, sfdc, subdomain, csm_name, csm_link, ae_name, ae_link, chatapp_channel, arr, churn_risk, renewal_date, notes, deleted_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			categoryID, name, a.SortOrder, nullString(&a.SFDC), nullString(&a.Subdomain), nullString(&a.CSMName), nullString(&a.CSMLink), nullString(&a.AEName), nullString(&a.AELink), nullString(&a.SlackChannel), nullString(&a.Arr), nullBool(a.ChurnRisk), nullString(&a.RenewalDate), nullString(&a.Notes), nullString(a.DeletedAt), a.CreatedAt, a.UpdatedAt)
		if err != nil {
			return 0, err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	} else {
		// Existing account with same name
		switch conflict {
		case "override":
			if deleted.Valid {
				if _, err := tx.Exec(`UPDATE accounts SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
					return 0, err
				}
			}
			if _, err := tx.Exec(`UPDATE accounts SET sort_order = ?, sfdc = ?, subdomain = ?, csm_name = ?, csm_link = ?, ae_name = ?, ae_link = ?, chatapp_channel = ?, arr = ?, churn_risk = ?, renewal_date = ?, notes = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
				a.SortOrder, nullString(&a.SFDC), nullString(&a.Subdomain), nullString(&a.CSMName), nullString(&a.CSMLink), nullString(&a.AEName), nullString(&a.AELink), nullString(&a.SlackChannel), nullString(&a.Arr), nullBool(a.ChurnRisk), nullString(&a.RenewalDate), nullString(&a.Notes), id); err != nil {
				return 0, err
			}
		case "duplicate":
			result, err := tx.Exec(`
				INSERT INTO accounts(category_id, name, sort_order, sfdc, subdomain, csm_name, csm_link, ae_name, ae_link, chatapp_channel, arr, churn_risk, renewal_date, notes, deleted_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				categoryID, name+" (imported)", a.SortOrder, nullString(&a.SFDC), nullString(&a.Subdomain), nullString(&a.CSMName), nullString(&a.CSMLink), nullString(&a.AEName), nullString(&a.AELink), nullString(&a.SlackChannel), nullString(&a.Arr), nullBool(a.ChurnRisk), nullString(&a.RenewalDate), nullString(&a.Notes), nullString(a.DeletedAt), a.CreatedAt, a.UpdatedAt)
			if err != nil {
				return 0, err
			}
			id, err = result.LastInsertId()
			if err != nil {
				return 0, err
			}
		default:
			return 0, fmt.Errorf("%w: conflict must be override or duplicate", db.ErrInvalid)
		}
	}

	// Recreate account_tags (for both new and existing accounts)
	_, err = tx.Exec(`DELETE FROM account_tags WHERE account_id = ?`, id)
	if err != nil {
		return 0, err
	}
	for _, tag := range a.Tags {
		newTagID, ok := tagIDMap[tag.ID]
		if !ok {
			// Tag not imported; try to find by name
			var foundID int64
			err := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, tag.Name).Scan(&foundID)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return 0, err
			}
			newTagID = foundID
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO account_tags(account_id, tag_id) VALUES (?, ?)`, id, newTagID); err != nil {
			return 0, err
		}
	}

	return id, nil
}

func toInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func importTaskJSON(tx *sql.Tx, t db.Task, accountID int64, conflict string) (int64, error) {
	title := strings.TrimSpace(t.Title)
	if title == "" {
		return 0, fmt.Errorf("%w: task title is required", db.ErrInvalid)
	}
	ticket := t.Ticket
	notes := t.Details

	var id int64
	var deleted sql.NullString
	var err error
	err = tx.QueryRow(`SELECT id, deleted_at FROM tasks WHERE account_id = ? AND title = ?`, accountID, title).Scan(&id, &deleted)
	if err == sql.ErrNoRows {
		result, err := tx.Exec(`
			INSERT INTO tasks(account_id, title, ticket, notes, due_date, completed, completed_at, sort_order, deleted_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			accountID, title, nullString(&ticket), nullString(&notes), t.DueDate, toInt(t.Completed), nullString(t.CompletedAt), t.SortOrder, nullString(t.DeletedAt), t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	}
	if err != nil {
		return 0, err
	}

	switch conflict {
	case "override":
		if deleted.Valid {
			if _, err := tx.Exec(`UPDATE tasks SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
				return 0, err
			}
		}
		if _, err := tx.Exec(`UPDATE tasks SET title = ?, ticket = ?, notes = ?, due_date = ?, completed = ?, completed_at = ?, sort_order = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			title, nullString(&ticket), nullString(&notes), t.DueDate, toInt(t.Completed), nullString(t.CompletedAt), t.SortOrder, id); err != nil {
			return 0, err
		}
		return id, nil
	case "duplicate":
		result, err := tx.Exec(`
			INSERT INTO tasks(account_id, title, ticket, notes, due_date, completed, completed_at, sort_order, deleted_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			accountID, title+" (imported)", nullString(&ticket), nullString(&notes), t.DueDate, toInt(t.Completed), nullString(t.CompletedAt), t.SortOrder, nullString(t.DeletedAt), t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	default:
		return 0, fmt.Errorf("%w: conflict must be override or duplicate", db.ErrInvalid)
	}
}

// ImportResult reports how many records were written.
type ImportResult struct {
	Categories int `json:"categories"`
	Tags       int `json:"tags"`
	Accounts   int `json:"accounts"`
	Tasks      int `json:"tasks"`
}

type categoryRow struct {
	fileID    int64
	name      string
	color     string
	sortOrder int
	expanded  bool
	deletedAt *string
	createdAt string
	updatedAt string
}

type tagRow struct {
	fileID    int64
	name      string
	color     string
	deletedAt *string
	createdAt string
	updatedAt string
}

type accountRow struct {
	fileID       int64
	categoryID   int64
	name         string
	sortOrder    int
	sfdc         string
	subdomain    string
	csmName      string
	csmLink      string
	aeName       string
	aeLink       string
	slackChannel string
	arr          string
	churnRisk    *bool
	renewalDate  string
	notes        string
	deletedAt    *string
	createdAt    string
	updatedAt    string
	tags         []string
}

type taskRow struct {
	fileID      int64
	accountID   int64
	title       string
	ticket      string
	notes       string
	dueDate     string
	completed   bool
	completedAt *string
	sortOrder   float64
	deletedAt   *string
	createdAt   string
	updatedAt   string
}

// ImportCSV parses and applies a CSV export inside one transaction.
// conflict is "override" (update matching named rows) or "duplicate" (insert
// renamed copies). Tasks are always appended because they have no unique name.
func ImportCSV(d *db.DB, r io.Reader, conflict string, sections Sections) (ImportResult, error) {
	conflict = strings.ToLower(strings.TrimSpace(conflict))
	if conflict == "" {
		conflict = "duplicate"
	}
	if conflict != "override" && conflict != "duplicate" {
		return ImportResult{}, fmt.Errorf("%w: conflict must be override or duplicate", db.ErrInvalid)
	}

	categories, tags, accounts, tasks, err := parseCSV(r)
	if err != nil {
		return ImportResult{}, err
	}

	result := ImportResult{}
	err = d.WithTx(func(tx *sql.Tx) error {
		categoryMap := make(map[int64]int64, len(categories))
		accountMap := make(map[int64]int64, len(accounts))

		for _, row := range categories {
			if !sections.has("categories") {
				break
			}
			id, err := importCategory(tx, row, conflict)
			if err != nil {
				return err
			}
			categoryMap[row.fileID] = id
			result.Categories++
		}
		for _, row := range tags {
			if !sections.has("tags") {
				break
			}
			if _, err := importTag(tx, row, conflict); err != nil {
				return err
			}
			result.Tags++
		}
		for _, row := range accounts {
			if !sections.has("accounts") {
				break
			}
			categoryID, ok := categoryMap[row.categoryID]
			if !ok {
				return fmt.Errorf("%w: account %q references unknown category id %d", db.ErrInvalid, row.name, row.categoryID)
			}
			id, err := importAccount(tx, row, categoryID, conflict)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM account_tags WHERE account_id = ?`, id); err != nil {
				return err
			}
			for _, name := range row.tags {
				name = strings.TrimSpace(name)
				if name == "" {
					return fmt.Errorf("%w: account %q has an empty tag name", db.ErrInvalid, row.name)
				}
				tagID, err := findOrCreateTagByName(tx, name)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT OR IGNORE INTO account_tags(account_id, tag_id) VALUES (?, ?)`, id, tagID); err != nil {
					return err
				}
			}
			accountMap[row.fileID] = id
			result.Accounts++
		}
		for _, row := range tasks {
			if !sections.has("tasks") {
				break
			}
			accountID, ok := accountMap[row.accountID]
			if !ok {
				return fmt.Errorf("%w: task %q references unknown account id %d", db.ErrInvalid, row.title, row.accountID)
			}
			if err := importTask(tx, row, accountID); err != nil {
				return err
			}
			result.Tasks++
		}
		// Account tags are resolved by name on account rows; tag file IDs are
		// only needed to validate the tag records themselves.
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func importCategory(tx *sql.Tx, row categoryRow, conflict string) (int64, error) {
	existingID, _, found, err := findCategoryByName(tx, row.name)
	if err != nil {
		return 0, err
	}
	if found {
		if conflict == "override" {
			_, err := tx.Exec(`
				UPDATE categories
				SET name = ?, color = ?, sort_order = ?, expanded = ?, deleted_at = ?,
				    created_at = COALESCE(NULLIF(?, ''), created_at),
				    updated_at = ?
				WHERE id = ?`,
				row.name, row.color, row.sortOrder, boolInt(row.expanded), nullString(row.deletedAt),
				row.createdAt, timestampOrNow(row.updatedAt), existingID)
			return existingID, err
		}
		row.name, err = uniqueCategoryName(tx, row.name)
		if err != nil {
			return 0, err
		}
	}
	return insertCategory(tx, row)
}

func insertCategory(tx *sql.Tx, row categoryRow) (int64, error) {
	result, err := tx.Exec(`
		INSERT INTO categories(name, color, sort_order, expanded, deleted_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		row.name, row.color, row.sortOrder, boolInt(row.expanded), nullString(row.deletedAt),
		timestampOrNow(row.createdAt), timestampOrNow(row.updatedAt))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func importTag(tx *sql.Tx, row tagRow, conflict string) (int64, error) {
	existingID, found, err := findTagByName(tx, row.name)
	if err != nil {
		return 0, err
	}
	if found {
		if conflict == "override" {
			_, err := tx.Exec(`
				UPDATE tags SET color = ?, deleted_at = ?, updated_at = ? WHERE id = ?`,
				row.color, nullString(row.deletedAt), timestampOrNow(row.updatedAt), existingID)
			return existingID, err
		}
		row.name, err = uniqueTagName(tx, row.name)
		if err != nil {
			return 0, err
		}
	}
	result, err := tx.Exec(`
		INSERT INTO tags(name, color, deleted_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		row.name, row.color, nullString(row.deletedAt),
		timestampOrNow(row.createdAt), timestampOrNow(row.updatedAt))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func importAccount(tx *sql.Tx, row accountRow, categoryID int64, conflict string) (int64, error) {
	existingID, found, err := findAccountByName(tx, categoryID, row.name)
	if err != nil {
		return 0, err
	}
	if found {
		if conflict == "override" {
			_, err := tx.Exec(`
				UPDATE accounts
				SET name = ?, sort_order = ?, sfdc = ?, subdomain = ?, csm_name = ?, csm_link = ?, ae_name = ?, ae_link = ?, chatapp_channel = ?, arr = ?, churn_risk = ?, renewal_date = ?,
				    notes = ?, deleted_at = ?,
				    created_at = COALESCE(NULLIF(?, ''), created_at),
				    updated_at = ?
				WHERE id = ?`,
				row.name, row.sortOrder, nullString(&row.sfdc), nullString(&row.subdomain), nullString(&row.csmName), nullString(&row.csmLink), nullString(&row.aeName), nullString(&row.aeLink), nullString(&row.slackChannel), nullString(&row.arr), nullBool(row.churnRisk), nullString(&row.renewalDate),
				nullString(&row.notes), nullString(row.deletedAt), row.createdAt,
				timestampOrNow(row.updatedAt), existingID)
			return existingID, err
		}
		row.name, err = uniqueAccountName(tx, categoryID, row.name)
		if err != nil {
			return 0, err
		}
	}
	result, err := tx.Exec(`
		INSERT INTO accounts(category_id, name, sort_order, sfdc, subdomain, csm_name, csm_link, ae_name, ae_link, chatapp_channel, arr, churn_risk, renewal_date, notes, deleted_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		categoryID, row.name, row.sortOrder, nullString(&row.sfdc), nullString(&row.subdomain), nullString(&row.csmName), nullString(&row.csmLink), nullString(&row.aeName), nullString(&row.aeLink), nullString(&row.slackChannel), nullString(&row.arr), nullBool(row.churnRisk), nullString(&row.renewalDate),
		nullString(&row.notes), nullString(row.deletedAt), timestampOrNow(row.createdAt), timestampOrNow(row.updatedAt))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func importTask(tx *sql.Tx, row taskRow, accountID int64) error {
	_, err := tx.Exec(`
		INSERT INTO tasks(account_id, title, ticket, notes, due_date, completed, completed_at, sort_order, deleted_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, row.title, nullString(&row.ticket), nullString(&row.notes), row.dueDate, boolInt(row.completed),
		nullString(row.completedAt), row.sortOrder, nullString(row.deletedAt),
		timestampOrNow(row.createdAt), timestampOrNow(row.updatedAt))
	return err
}

func findCategoryByName(tx *sql.Tx, name string) (int64, *string, bool, error) {
	var id int64
	var deleted sql.NullString
	err := tx.QueryRow(`
		SELECT id, deleted_at FROM categories
		WHERE name = ?
		ORDER BY CASE WHEN deleted_at IS NULL THEN 0 ELSE 1 END, id
		LIMIT 1`, name).Scan(&id, &deleted)
	if err == sql.ErrNoRows {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, err
	}
	return id, ptrOrNil(deleted), true, nil
}

func findTagByName(tx *sql.Tx, name string) (int64, bool, error) {
	var id int64
	err := tx.QueryRow(`
		SELECT id FROM tags
		WHERE name = ?
		ORDER BY CASE WHEN deleted_at IS NULL THEN 0 ELSE 1 END, id
		LIMIT 1`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func findAccountByName(tx *sql.Tx, categoryID int64, name string) (int64, bool, error) {
	var id int64
	err := tx.QueryRow(`
		SELECT id FROM accounts
		WHERE category_id = ? AND name = ?
		ORDER BY CASE WHEN deleted_at IS NULL THEN 0 ELSE 1 END, id
		LIMIT 1`, categoryID, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func findOrCreateTagByName(tx *sql.Tx, name string) (int64, error) {
	id, found, err := findTagByName(tx, name)
	if err != nil {
		return 0, err
	}
	if found {
		return id, nil
	}
	result, err := tx.Exec(`INSERT INTO tags(name, color) VALUES (?, ?)`, name, defaultColor)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func uniqueCategoryName(tx *sql.Tx, base string) (string, error) {
	for i := 1; ; i++ {
		candidate := importedName(base, i)
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM categories WHERE name = ?`, candidate).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
}

func uniqueTagName(tx *sql.Tx, base string) (string, error) {
	for i := 1; ; i++ {
		candidate := importedName(base, i)
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM tags WHERE name = ?`, candidate).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
}

func uniqueAccountName(tx *sql.Tx, categoryID int64, base string) (string, error) {
	for i := 1; ; i++ {
		candidate := importedName(base, i)
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM accounts WHERE category_id = ? AND name = ?`, categoryID, candidate).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
}

func importedName(base string, attempt int) string {
	if attempt == 1 {
		return base + " (imported)"
	}
	return fmt.Sprintf("%s (imported %d)", base, attempt)
}

func parseCSV(r io.Reader) ([]categoryRow, []tagRow, []accountRow, []taskRow, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = len(header)
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: read CSV: %v", db.ErrInvalid, err)
	}
	if len(records) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("%w: empty CSV", db.ErrInvalid)
	}
	if !equalRow(records[0], header) {
		return nil, nil, nil, nil, fmt.Errorf("%w: unexpected CSV header", db.ErrInvalid)
	}

	var categories []categoryRow
	var tags []tagRow
	var accounts []accountRow
	var tasks []taskRow
	categoryIDs := map[int64]bool{}
	tagIDs := map[int64]bool{}
	accountIDs := map[int64]bool{}

	for line, row := range records[1:] {
		if len(row) != len(header) {
			return nil, nil, nil, nil, fmt.Errorf("%w: line %d has %d columns", db.ErrInvalid, line+2, len(row))
		}
		field := func(name string) string {
			for i, column := range header {
				if column == name {
					return strings.TrimSpace(row[i])
				}
			}
			return ""
		}
		if field("format_version") != strconv.Itoa(FormatVersion) {
			return nil, nil, nil, nil, fmt.Errorf("%w: line %d has unsupported format_version %q", db.ErrInvalid, line+2, field("format_version"))
		}
		id, err := parseID(field("id"))
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("%w: line %d: %v", db.ErrInvalid, line+2, err)
		}

		switch field("record_type") {
		case "category":
			if categoryIDs[id] {
				return nil, nil, nil, nil, fmt.Errorf("%w: duplicate category id %d", db.ErrInvalid, id)
			}
			categoryIDs[id] = true
			name := field("name")
			if name == "" {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: category name is empty", db.ErrInvalid, line+2)
			}
			color, err := validColor(field("color"))
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: %v", db.ErrInvalid, line+2, err)
			}
			sortOrder, err := parseInt(field("sort_order"))
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: %v", db.ErrInvalid, line+2, err)
			}
			categories = append(categories, categoryRow{
				fileID: id, name: name, color: color, sortOrder: sortOrder,
				expanded:  parseBool(field("expanded")),
				deletedAt: optional(field("deleted_at")),
				createdAt: field("created_at"), updatedAt: field("updated_at"),
			})
		case "tag":
			if tagIDs[id] {
				return nil, nil, nil, nil, fmt.Errorf("%w: duplicate tag id %d", db.ErrInvalid, id)
			}
			tagIDs[id] = true
			name := field("name")
			if name == "" {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: tag name is empty", db.ErrInvalid, line+2)
			}
			color, err := validColor(field("color"))
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: %v", db.ErrInvalid, line+2, err)
			}
			tags = append(tags, tagRow{
				fileID: id, name: name, color: color,
				deletedAt: optional(field("deleted_at")),
				createdAt: field("created_at"), updatedAt: field("updated_at"),
			})
		case "account":
			if accountIDs[id] {
				return nil, nil, nil, nil, fmt.Errorf("%w: duplicate account id %d", db.ErrInvalid, id)
			}
			accountIDs[id] = true
			name := field("name")
			if name == "" {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: account name is empty", db.ErrInvalid, line+2)
			}
			categoryID, err := parseID(field("category_id"))
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: category_id: %v", db.ErrInvalid, line+2, err)
			}
			sortOrder, err := parseInt(field("sort_order"))
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: %v", db.ErrInvalid, line+2, err)
			}
			var tagNames []string
			if raw := field("tags"); raw != "" {
				if err := json.Unmarshal([]byte(raw), &tagNames); err != nil {
					return nil, nil, nil, nil, fmt.Errorf("%w: line %d: tags must be a JSON array of names", db.ErrInvalid, line+2)
				}
			}
			renewalDate := field("renewal_date")
			if renewalDate != "" && !validDate(renewalDate) {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: invalid renewal_date %q", db.ErrInvalid, line+2, renewalDate)
			}
			churnRisk, err := parseChurn(field("churn_risk"))
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: %v", db.ErrInvalid, line+2, err)
			}
			accounts = append(accounts, accountRow{
				fileID: id, categoryID: categoryID, name: name, sortOrder: sortOrder,
				sfdc: field("sfdc"), subdomain: field("subdomain"), csmName: field("csm_name"), csmLink: field("csm_link"), aeName: field("ae_name"), aeLink: field("ae_link"), slackChannel: field("chatapp_channel"),
				arr: field("arr"), churnRisk: churnRisk, renewalDate: renewalDate,
				notes: field("details"), deletedAt: optional(field("deleted_at")),
				createdAt: field("created_at"), updatedAt: field("updated_at"), tags: tagNames,
			})
		case "task":
			accountID, err := parseID(field("account_id"))
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: account_id: %v", db.ErrInvalid, line+2, err)
			}
			title := field("title")
			if title == "" {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: task title is empty", db.ErrInvalid, line+2)
			}
			dueDate := field("due_date")
			if !validDate(dueDate) {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: invalid due_date %q", db.ErrInvalid, line+2, dueDate)
			}
			sortOrder, err := parseFloat(field("sort_order"))
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%w: line %d: %v", db.ErrInvalid, line+2, err)
			}
			tasks = append(tasks, taskRow{
				fileID: id, accountID: accountID, title: title, ticket: field("ticket"), notes: field("details"),
				dueDate: dueDate, completed: parseBool(field("completed")),
				completedAt: optional(field("completed_at")), sortOrder: sortOrder,
				deletedAt: optional(field("deleted_at")),
				createdAt: field("created_at"), updatedAt: field("updated_at"),
			})
		default:
			return nil, nil, nil, nil, fmt.Errorf("%w: line %d: unknown record_type %q", db.ErrInvalid, line+2, field("record_type"))
		}
	}
	return categories, tags, accounts, tasks, nil
}

func equalRow(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id %q", value)
	}
	return id, nil
}

func parseInt(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", value)
	}
	return n, nil
}

func parseFloat(value string) (float64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", value)
	}
	return n, nil
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func validColor(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultColor, nil
	}
	if !hexColorPattern.MatchString(value) {
		return "", fmt.Errorf("invalid color %q", value)
	}
	return strings.ToLower(value), nil
}

func validDate(value string) bool {
	if !datePattern.MatchString(value) {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func optional(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	v := value
	return &v
}

func ptrOrNil(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}

func nullString(value *string) any {
	if value == nil {
		return nil
	}
	if strings.TrimSpace(*value) == "" {
		return nil
	}
	return *value
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// churnRiskCsv renders a nullable bool for CSV export: "1", "0", or "".
func churnRiskCsv(b *bool) string {
	if b == nil {
		return ""
	}
	if *b {
		return "1"
	}
	return "0"
}

// nullBool widens a *bool for use as a SQL argument: nil → NULL, true → 1,
// false → 0.
func nullBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

// parseChurn parses a CSV churn_risk cell: "true"/"1"/"yes" → true,
// "false"/"0"/"no" → false, "" → nil, anything else is an error.
func parseChurn(value string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "null", "~":
		return nil, nil
	case "true", "1", "yes":
		b := true
		return &b, nil
	case "false", "0", "no":
		b := false
		return &b, nil
	default:
		return nil, fmt.Errorf("invalid churn_risk %q, want true/false", value)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func timestampOrNow(value string) string {
	if strings.TrimSpace(value) == "" {
		return time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	return value
}
