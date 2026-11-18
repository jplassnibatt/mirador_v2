package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func scanAccount(row interface{ Scan(dest ...any) error }) (Account, error) {
	var a Account
	var sfdc, subdomain, csmName, csmLink, aeName, aeLink, slackChannel, arr, renewalDate, notes, deleted sql.NullString
	var churnRisk sql.NullBool
	err := row.Scan(
		&a.ID, &a.CategoryID, &a.Name, &a.SortOrder,
		&sfdc, &subdomain, &csmName, &csmLink, &aeName, &aeLink, &slackChannel, &arr, &churnRisk, &renewalDate, &notes,
		&deleted, &a.CreatedAt, &a.UpdatedAt, &a.CategoryName,
	)
	if err != nil {
		return Account{}, err
	}
	a.SFDC = stringValue(sfdc)
	a.Subdomain = stringValue(subdomain)
	a.CSMName = stringValue(csmName)
	a.CSMLink = stringValue(csmLink)
	a.AEName = stringValue(aeName)
	a.AELink = stringValue(aeLink)
	a.SlackChannel = stringValue(slackChannel)
	a.Arr = stringValue(arr)
	a.ChurnRisk = boolPtr(churnRisk)
	a.RenewalDate = stringValue(renewalDate)
	a.Notes = stringValue(notes)
	a.DeletedAt = nullString(deleted)
	a.Tags = []Tag{}
	return a, nil
}

func accountSelect() string {
	return `
		SELECT a.id, a.category_id, a.name, a.sort_order,
		       a.sfdc, a.subdomain, a.csm_name, a.csm_link, a.ae_name, a.ae_link, a.chatapp_channel, a.arr, a.churn_risk, a.renewal_date, a.notes,
		       a.deleted_at, a.created_at, a.updated_at, c.name
		FROM accounts a
		JOIN categories c ON c.id = a.category_id`
}

// accountWhere builds the WHERE conditions (without the leading "WHERE ")
// and query arguments for an account query. ListAccounts and CountAccounts
// share it so a count always matches the displayed list under the same filter.
func accountWhere(filter AccountFilter) (string, []any) {
	var conditions []string
	var args []any

	if !filter.IncludeDeleted {
		conditions = append(conditions, "a.deleted_at IS NULL", "c.deleted_at IS NULL")
	}
	if len(filter.CategoryIDs) > 0 {
		conditions = append(conditions, "a.category_id IN ("+placeholders(len(filter.CategoryIDs))+")")
		args = append(args, anySlice(filter.CategoryIDs)...)
	}
	if filter.Name != "" {
		conditions = append(conditions, "a.name LIKE ?")
		args = append(args, "%"+filter.Name+"%")
	}
	if len(filter.TagIDs) > 0 {
		conditions = append(conditions, `EXISTS (
			SELECT 1 FROM account_tags at WHERE at.account_id = a.id AND at.tag_id IN (`+placeholders(len(filter.TagIDs))+`)
		)`)
		args = append(args, anySlice(filter.TagIDs)...)
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
	if len(conditions) == 0 {
		return "", nil
	}
	return strings.Join(conditions, " AND "), args
}

// ListAccounts returns accounts ordered by sort_order then name.
func (d *DB) ListAccounts(filter AccountFilter) ([]Account, error) {
	query := accountSelect()
	where, args := accountWhere(filter)
	if where != "" {
		query += " WHERE " + where
	}
	query += " ORDER BY a.name COLLATE NOCASE"

	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := d.attachTagsToAccounts(accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

// AccountFilterInfo holds the account fields used to decide which facet
// filters match a given account.
type AccountFilterInfo struct {
	CSMName   string
	AEName    string
	ChurnRisk *bool
}

// AccountFilterInfos returns the filter-relevant fields of every active
// account, keyed by account id. Used to render per-account "matching filters"
// on the tasks page.
func (d *DB) AccountFilterInfos() (map[int64]AccountFilterInfo, error) {
	rows, err := d.sql.Query(`SELECT id, csm_name, ae_name, churn_risk FROM accounts WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]AccountFilterInfo)
	for rows.Next() {
		var id int64
		var csm, ae sql.NullString
		var churn sql.NullBool
		if err := rows.Scan(&id, &csm, &ae, &churn); err != nil {
			return nil, err
		}
		result[id] = AccountFilterInfo{
			CSMName:   stringValue(csm),
			AEName:    stringValue(ae),
			ChurnRisk: boolPtr(churn),
		}
	}
	return result, rows.Err()
}

// ListCSMNames returns the distinct CSM names assigned to active accounts.
func (d *DB) ListCSMNames() ([]string, error) {
	return d.listDistinctAccountText("csm_name")
}

// ListAENames returns the distinct AE names assigned to active accounts.
func (d *DB) ListAENames() ([]string, error) {
	return d.listDistinctAccountText("ae_name")
}

// listDistinctAccountText reads distinct non-empty values from an internal
// account text column. The column whitelist lives in the exported wrappers.
func (d *DB) listDistinctAccountText(column string) ([]string, error) {
	rows, err := d.sql.Query(`
		SELECT DISTINCT ` + column + ` AS value
		FROM accounts
		WHERE deleted_at IS NULL
		  AND ` + column + ` IS NOT NULL
		  AND TRIM(` + column + `) <> ''
		ORDER BY value COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// GetAccount returns one account with its tags.
func (d *DB) GetAccount(id int64, includeDeleted bool) (Account, error) {
	query := accountSelect() + " WHERE a.id = ?"
	if !includeDeleted {
		query += " AND a.deleted_at IS NULL AND c.deleted_at IS NULL"
	}
	a, err := scanAccount(d.sql.QueryRow(query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	accounts := []Account{a}
	if err := d.attachTagsToAccounts(accounts); err != nil {
		return Account{}, err
	}
	tasks, err := d.ListTasks(TaskFilter{AccountID: id})
	if err != nil {
		return Account{}, err
	}
	accounts[0].Tasks = tasks
	return accounts[0], nil
}

// CreateAccount inserts an account and assigns its tags atomically.
func (d *DB) CreateAccount(categoryID int64, name string, sortOrder float64, sfdc, subdomain, csmName, csmLink, aeName, aeLink, slackChannel, arr string, churnRisk *bool, renewalDate, notes string, tagIDs []int64) (Account, error) {
	var id int64
	err := d.WithTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM categories WHERE id = ? AND deleted_at IS NULL`, categoryID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("%w: category not found", ErrNotFound)
		}
		result, err := tx.Exec(`
			INSERT INTO accounts(category_id, name, sort_order, sfdc, subdomain, csm_name, csm_link, ae_name, ae_link, chatapp_channel, arr, churn_risk, renewal_date, notes)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			categoryID, name, sortOrder, nullable(sfdc), nullable(subdomain), nullable(csmName), nullable(csmLink), nullable(aeName), nullable(aeLink), nullable(slackChannel), nullable(arr), nullableBool(churnRisk), nullable(renewalDate), nullable(notes))
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return err
		}
		return setAccountTagsTx(tx, id, tagIDs)
	})
	if err != nil {
		return Account{}, err
	}
	return d.GetAccount(id, false)
}

// UpdateAccount updates an account and optionally replaces its tags. A nil
// tagIDs slice leaves the existing assignments untouched.
func (d *DB) UpdateAccount(id int64, name string, sortOrder float64, sfdc, subdomain, csmName, csmLink, aeName, aeLink, slackChannel, arr string, churnRisk *bool, renewalDate, notes string, tagIDs *[]int64) (Account, error) {
	err := d.WithTx(func(tx *sql.Tx) error {
		result, err := tx.Exec(`
			UPDATE accounts
			SET name = ?, sort_order = ?, sfdc = ?, subdomain = ?, csm_name = ?, csm_link = ?, ae_name = ?, ae_link = ?, chatapp_channel = ?, arr = ?, churn_risk = ?, renewal_date = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND deleted_at IS NULL`,
			name, sortOrder, nullable(sfdc), nullable(subdomain), nullable(csmName), nullable(csmLink), nullable(aeName), nullable(aeLink), nullable(slackChannel), nullable(arr), nullableBool(churnRisk), nullable(renewalDate), nullable(notes), id)
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
		if tagIDs != nil {
			return setAccountTagsTx(tx, id, *tagIDs)
		}
		return nil
	})
	if err != nil {
		return Account{}, err
	}
	return d.GetAccount(id, false)
}

// SoftDeleteAccount archives an account and cascades the archive to its tasks.
func (d *DB) SoftDeleteAccount(id int64) error {
	stamp := deleteStamp()
	return d.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			UPDATE tasks
			SET deleted_at = ?, updated_at = ?
			WHERE account_id = ? AND deleted_at IS NULL`, stamp, stamp, id); err != nil {
			return err
		}
		result, err := tx.Exec(`
			UPDATE accounts
			SET deleted_at = ?, updated_at = ?
			WHERE id = ? AND deleted_at IS NULL`, stamp, stamp, id)
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
	})
}

// MoveAccount moves an account to a different category.
func (d *DB) MoveAccount(id int64, newCategoryID int64, sortOrder float64) error {
	var exists int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM categories WHERE id = ? AND deleted_at IS NULL`, newCategoryID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("%w: category not found", ErrNotFound)
	}
	result, err := d.sql.Exec(`
		UPDATE accounts
		SET category_id = ?, sort_order = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NULL`, newCategoryID, sortOrder, id)
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

// RestoreAccount clears the archive flag. When withChildren is true, tasks
// archived by the same cascade are restored too. If the account's category is
// missing or still archived, the account is moved to the uncategorized
// category so it never references a deleted category.
func (d *DB) RestoreAccount(id int64, withChildren bool) error {
	account, err := d.GetAccount(id, true)
	if err != nil {
		return err
	}
	if account.DeletedAt == nil {
		return fmt.Errorf("%w: account is not in the trash", ErrConflict)
	}
	deletedAt := *account.DeletedAt
	return d.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			UPDATE accounts SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
			return err
		}
		// If the account's category is gone or still archived, move it to
		// uncategorized instead of leaving a dangling category reference.
		var categoryDeletedAt sql.NullString
		catErr := tx.QueryRow(`SELECT deleted_at FROM categories WHERE id = ?`, account.CategoryID).Scan(&categoryDeletedAt)
		categoryMissing := errors.Is(catErr, sql.ErrNoRows)
		categoryDeleted := catErr == nil && categoryDeletedAt.Valid
		if catErr != nil && !categoryMissing {
			return catErr
		}
		if categoryMissing || categoryDeleted {
			uncategorized, err := ensureUncategorizedCategory(tx)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`
				UPDATE accounts SET category_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
				uncategorized.ID, id); err != nil {
				return err
			}
		}
		if withChildren {
			if _, err := tx.Exec(`
				UPDATE tasks SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP
				WHERE account_id = ? AND deleted_at = ?`, id, deletedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

// HardDeleteAccount permanently removes an account and its tasks.
func (d *DB) HardDeleteAccount(id int64) error {
	result, err := d.sql.Exec(`DELETE FROM accounts WHERE id = ?`, id)
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

func setAccountTagsTx(tx *sql.Tx, accountID int64, tagIDs []int64) error {
	if _, err := tx.Exec(`DELETE FROM account_tags WHERE account_id = ?`, accountID); err != nil {
		return err
	}
	seen := make(map[int64]bool, len(tagIDs))
	for _, tagID := range tagIDs {
		if tagID <= 0 || seen[tagID] {
			continue
		}
		seen[tagID] = true
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM tags WHERE id = ? AND deleted_at IS NULL`, tagID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("%w: tag %d not found", ErrNotFound, tagID)
		}
		if _, err := tx.Exec(`INSERT INTO account_tags(account_id, tag_id) VALUES (?, ?)`, accountID, tagID); err != nil {
			return err
		}
	}
	return nil
}

// SetAccountTags replaces an account's tag assignments.
func (d *DB) SetAccountTags(accountID int64, tagIDs []int64) error {
	return d.WithTx(func(tx *sql.Tx) error {
		return setAccountTagsTx(tx, accountID, tagIDs)
	})
}

func (d *DB) attachTagsToAccounts(accounts []Account) error {
	if len(accounts) == 0 {
		return nil
	}
	ids := make([]any, 0, len(accounts))
	placeholders := make([]string, 0, len(accounts))
	index := make(map[int64]int, len(accounts))
	for i, account := range accounts {
		ids = append(ids, account.ID)
		placeholders = append(placeholders, "?")
		index[account.ID] = i
	}
	query := `
		SELECT at.account_id, t.id, t.name, t.color, t.deleted_at, t.created_at, t.updated_at
		FROM account_tags at
		JOIN tags t ON t.id = at.tag_id
		WHERE t.deleted_at IS NULL AND at.account_id IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY t.name COLLATE NOCASE`
	rows, err := d.sql.Query(query, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var accountID int64
		var tag Tag
		var deleted sql.NullString
		if err := rows.Scan(&accountID, &tag.ID, &tag.Name, &tag.Color, &deleted, &tag.CreatedAt, &tag.UpdatedAt); err != nil {
			return err
		}
		tag.DeletedAt = nullString(deleted)
		if i, ok := index[accountID]; ok {
			accounts[i].Tags = append(accounts[i].Tags, tag)
		}
	}
	return rows.Err()
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func boolPtr(nb sql.NullBool) *bool {
	if !nb.Valid {
		return nil
	}
	b := nb.Bool
	return &b
}

func nullableBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

// CountAccounts returns the number of accounts matching filter. It shares the
// WHERE conditions with ListAccounts so the count stays in sync with the
// displayed list when category, tag, CSM, AE, churn, or search filters are
// applied.
func (d *DB) CountAccounts(filter AccountFilter) (int, error) {
	var count int
	where, args := accountWhere(filter)
	query := `SELECT COUNT(*) FROM accounts a JOIN categories c ON c.id = a.category_id`
	if where != "" {
		query += " WHERE " + where
	}
	err := d.sql.QueryRow(query, args...).Scan(&count)
	return count, err
}
