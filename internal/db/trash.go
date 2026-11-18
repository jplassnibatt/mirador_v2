package db

// ListDeletedCategories returns archived categories, newest first.
func (d *DB) ListDeletedCategories() ([]Category, error) {
	rows, err := d.sql.Query(`
		SELECT id, name, color, sort_order, expanded, deleted_at, created_at, updated_at
		FROM categories
		WHERE deleted_at IS NOT NULL
		ORDER BY deleted_at DESC, name COLLATE NOCASE`)
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

// ListDeletedAccounts returns archived accounts, newest first.
func (d *DB) ListDeletedAccounts() ([]Account, error) {
	rows, err := d.sql.Query(accountSelect() + `
		WHERE a.deleted_at IS NOT NULL
		ORDER BY a.deleted_at DESC, a.name COLLATE NOCASE`)
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

// ListDeletedTasks returns archived tasks, newest first.
func (d *DB) ListDeletedTasks() ([]Task, error) {
	rows, err := d.sql.Query(taskSelect() + `
		WHERE t.deleted_at IS NOT NULL
		ORDER BY t.deleted_at DESC, t.due_date, t.id`)
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
	return tasks, nil
}
