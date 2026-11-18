package db

import (
	"database/sql"
	"errors"
)

func scanTag(row interface{ Scan(dest ...any) error }) (Tag, error) {
	var t Tag
	var deleted sql.NullString
	err := row.Scan(&t.ID, &t.Name, &t.Color, &deleted, &t.CreatedAt, &t.UpdatedAt, &t.UsageCount)
	if err != nil {
		return Tag{}, err
	}
	t.DeletedAt = nullString(deleted)
	return t, nil
}

// ListTags returns tags with the number of active accounts using each tag.
func (d *DB) ListTags(includeDeleted bool) ([]Tag, error) {
	query := `
		SELECT t.id, t.name, t.color, t.deleted_at, t.created_at, t.updated_at,
		       (SELECT COUNT(*)
		          FROM account_tags at
		          JOIN accounts a ON a.id = at.account_id
		         WHERE at.tag_id = t.id AND a.deleted_at IS NULL) AS usage_count
		FROM tags t`
	if !includeDeleted {
		query += ` WHERE t.deleted_at IS NULL`
	}
	query += ` ORDER BY t.name COLLATE NOCASE`

	rows, err := d.sql.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []Tag
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// GetTag returns one tag.
func (d *DB) GetTag(id int64) (Tag, error) {
	t, err := scanTag(d.sql.QueryRow(`
		SELECT t.id, t.name, t.color, t.deleted_at, t.created_at, t.updated_at,
		       (SELECT COUNT(*)
		          FROM account_tags at
		          JOIN accounts a ON a.id = at.account_id
		         WHERE at.tag_id = t.id AND a.deleted_at IS NULL) AS usage_count
		FROM tags t WHERE t.id = ? AND t.deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, ErrNotFound
	}
	return t, err
}

// CreateTag inserts a tag.
func (d *DB) CreateTag(name, color string) (Tag, error) {
	result, err := d.sql.Exec(`INSERT INTO tags(name, color) VALUES (?, ?)`, name, color)
	if err != nil {
		return Tag{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Tag{}, err
	}
	return d.GetTag(id)
}

// UpdateTag changes a tag's name and color.
func (d *DB) UpdateTag(id int64, name, color string) (Tag, error) {
	result, err := d.sql.Exec(`
		UPDATE tags SET name = ?, color = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NULL`, name, color, id)
	if err != nil {
		return Tag{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Tag{}, err
	}
	if affected == 0 {
		return Tag{}, ErrNotFound
	}
	return d.GetTag(id)
}

// DeleteTag permanently removes a tag. account_tags rows cascade.
func (d *DB) DeleteTag(id int64) error {
	result, err := d.sql.Exec(`DELETE FROM tags WHERE id = ?`, id)
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

// CountTags returns the number of active (non-archived) tags.
func (d *DB) CountTags() (int, error) {
	var count int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM tags WHERE deleted_at IS NULL`).Scan(&count)
	return count, err
}
