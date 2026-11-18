package db

import (
	"database/sql"
	"errors"
	"fmt"
)

func scanCategory(row interface{ Scan(dest ...any) error }) (Category, error) {
	var c Category
	var expanded int
	var deleted sql.NullString
	err := row.Scan(&c.ID, &c.Name, &c.Color, &c.SortOrder, &expanded, &deleted, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Category{}, err
	}
	c.Expanded = expanded != 0
	c.DeletedAt = nullString(deleted)
	return c, nil
}

// ListCategories returns categories ordered by sort_order then name.
func (d *DB) ListCategories(includeDeleted bool) ([]Category, error) {
	query := `SELECT id, name, color, sort_order, expanded, deleted_at, created_at, updated_at FROM categories`
	if !includeDeleted {
		query += ` WHERE deleted_at IS NULL`
	}
	query += ` ORDER BY sort_order, name COLLATE NOCASE`

	rows, err := d.sql.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	return categories, rows.Err()
}

// GetCategory returns one category.
func (d *DB) GetCategory(id int64, includeDeleted bool) (Category, error) {
	query := `SELECT id, name, color, sort_order, expanded, deleted_at, created_at, updated_at FROM categories WHERE id = ?`
	if !includeDeleted {
		query += ` AND deleted_at IS NULL`
	}
	c, err := scanCategory(d.sql.QueryRow(query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

// ReorderCategory moves a category to a new sort order.
func (d *DB) ReorderCategory(id int64, sortOrder float64) error {
	result, err := d.sql.Exec(`
		UPDATE categories
		SET sort_order = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NULL`, sortOrder, id)
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

// NextCategorySortOrder returns a sort order after the current last category.
func (d *DB) NextCategorySortOrder() (float64, error) {
	var max sql.NullFloat64
	if err := d.sql.QueryRow(`SELECT MAX(sort_order) FROM categories WHERE deleted_at IS NULL`).Scan(&max); err != nil {
		return 0, err
	}
	if !max.Valid {
		return 0, nil
	}
	return max.Float64 + 1, nil
}

// CreateCategory inserts a category.
func (d *DB) CreateCategory(name, color string, sortOrder float64, expanded bool) (Category, error) {
	result, err := d.sql.Exec(`
		INSERT INTO categories(name, color, sort_order, expanded)
		VALUES (?, ?, ?, ?)`, name, color, sortOrder, boolInt(expanded))
	if err != nil {
		return Category{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Category{}, err
	}
	return d.GetCategory(id, false)
}

// UpdateCategory updates a category's editable fields.
func (d *DB) UpdateCategory(id int64, name, color string, sortOrder float64, expanded bool) (Category, error) {
	result, err := d.sql.Exec(`
		UPDATE categories
		SET name = ?, color = ?, sort_order = ?, expanded = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NULL`, name, color, sortOrder, boolInt(expanded), id)
	if err != nil {
		return Category{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Category{}, err
	}
	if affected == 0 {
		return Category{}, ErrNotFound
	}
	return d.GetCategory(id, false)
}

// SoftDeleteCategory archives a category and cascades the archive to its
// accounts and tasks at the application level. SQLite's ON DELETE CASCADE does
// not fire for soft deletes, so the child updates are explicit.
func (d *DB) SoftDeleteCategory(id int64) error {
	stamp := deleteStamp()
	return d.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			UPDATE tasks
			SET deleted_at = ?, updated_at = ?
			WHERE deleted_at IS NULL
			  AND account_id IN (SELECT id FROM accounts WHERE category_id = ? AND deleted_at IS NULL)`, stamp, stamp, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE accounts
			SET deleted_at = ?, updated_at = ?
			WHERE category_id = ? AND deleted_at IS NULL`, stamp, stamp, id); err != nil {
			return err
		}
		result, err := tx.Exec(`
			UPDATE categories
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

// RestoreCategory clears the archive flag. When withChildren is true, children
// archived by the same cascade (matching deleted_at timestamp) are restored too.
func (d *DB) RestoreCategory(id int64, withChildren bool) error {
	category, err := d.GetCategory(id, true)
	if err != nil {
		return err
	}
	if category.DeletedAt == nil {
		return fmt.Errorf("%w: category is not in the trash", ErrConflict)
	}
	deletedAt := *category.DeletedAt
	return d.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			UPDATE categories SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
			return err
		}
		if withChildren {
			if _, err := tx.Exec(`
				UPDATE tasks
				SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP
				WHERE deleted_at = ?
				  AND account_id IN (SELECT id FROM accounts WHERE category_id = ?)`, deletedAt, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`
				UPDATE accounts
				SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP
				WHERE category_id = ? AND deleted_at = ?`, id, deletedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

// HardDeleteCategory permanently removes a category. Foreign keys cascade to
// accounts, tasks, and account_tags.
func (d *DB) HardDeleteCategory(id int64) error {
	result, err := d.sql.Exec(`DELETE FROM categories WHERE id = ?`, id)
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

// uncategorizedTx is satisfied by *sql.DB and *sql.Tx so the helper can be
// used both directly and inside a transaction.
type uncategorizedTx interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// EnsureUncategorizedCategory returns the active "Uncategorized" category,
// creating it if it does not exist yet. It is the safety-net bucket for
// accounts whose category has been archived or deleted.
func (d *DB) EnsureUncategorizedCategory() (Category, error) {
	return ensureUncategorizedCategory(d.sql)
}

func ensureUncategorizedCategory(q uncategorizedTx) (Category, error) {
	const name = "Uncategorized"
	cat, err := scanCategory(q.QueryRow(`
		SELECT id, name, color, sort_order, expanded, deleted_at, created_at, updated_at
		FROM categories
		WHERE deleted_at IS NULL AND LOWER(name) = LOWER(?)
		LIMIT 1`, name))
	if err == nil {
		return cat, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Category{}, err
	}
	result, err := q.Exec(`
		INSERT INTO categories(name, color, sort_order, expanded)
		VALUES (?, '#94a3b8', 0, 1)`, name)
	if err != nil {
		return Category{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Category{}, err
	}
	return scanCategory(q.QueryRow(`
		SELECT id, name, color, sort_order, expanded, deleted_at, created_at, updated_at
		FROM categories
		WHERE id = ?`, id))
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
