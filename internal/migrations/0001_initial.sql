-- Mirador baseline schema. A fresh database is created from this file in one
-- step; later schema changes are added as new numbered migrations.
-- The schema_version table itself is created by the migration runner before
-- this file is applied.

CREATE TABLE categories (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '#000000',
    sort_order INTEGER NOT NULL DEFAULT 0,
    expanded INTEGER NOT NULL DEFAULT 1 CHECK (expanded IN (0, 1)),
    deleted_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Partial unique indexes let a name be reused after a soft delete while still
-- giving import conflict handling a reliable active-name match.
CREATE UNIQUE INDEX idx_categories_name_active
    ON categories(name) WHERE deleted_at IS NULL;
CREATE INDEX idx_categories_sort_order ON categories(sort_order, name);
CREATE INDEX idx_categories_deleted_at ON categories(deleted_at);

CREATE TABLE tags (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '#000000',
    deleted_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_tags_name_active ON tags(name) WHERE deleted_at IS NULL;
CREATE INDEX idx_tags_deleted_at ON tags(deleted_at);

CREATE TABLE accounts (
    id INTEGER PRIMARY KEY,
    category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    sfdc TEXT,
    subdomain TEXT,
    csm_name TEXT,
    csm_link TEXT,
    ae_name TEXT,
    ae_link TEXT,
    chatapp_channel TEXT,
    arr TEXT,
    churn_risk BOOL NULL,
    renewal_date TEXT,
    notes TEXT,
    deleted_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_accounts_category_name_active
    ON accounts(category_id, name) WHERE deleted_at IS NULL;
CREATE INDEX idx_accounts_category_sort ON accounts(category_id, sort_order, name);
CREATE INDEX idx_accounts_deleted_at ON accounts(deleted_at);

CREATE TABLE account_tags (
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (account_id, tag_id)
);

CREATE INDEX idx_account_tags_tag ON account_tags(tag_id);

CREATE TABLE tasks (
    id INTEGER PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    ticket TEXT,
    notes TEXT,
    due_date TEXT NOT NULL CHECK (due_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    completed INTEGER NOT NULL DEFAULT 0 CHECK (completed IN (0, 1)),
    completed_at TEXT,
    sort_order REAL NOT NULL DEFAULT 0,
    deleted_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tasks_due_order ON tasks(due_date, sort_order, id);
CREATE INDEX idx_tasks_account ON tasks(account_id);
CREATE INDEX idx_tasks_deleted_at ON tasks(deleted_at);
CREATE INDEX idx_tasks_completed ON tasks(completed, completed_at);

CREATE TABLE task_notes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    note TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_task_notes_task_id ON task_notes(task_id);
CREATE INDEX idx_task_notes_created_at ON task_notes(created_at);

CREATE TABLE api_keys (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    key_prefix TEXT NOT NULL,
    key_hash TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TEXT,
    expires_at TEXT
);

CREATE INDEX idx_api_keys_prefix ON api_keys(key_prefix);
