# Mirador

Mirador is a single-user, local task tracker built with Go and SQLite. It
organizes work as **categories → accounts → tasks**, with reusable tags, a flat
due-date view, drag reordering, a trash with restore, and versioned CSV
export/import. The UI is server-rendered HTML with a small amount of vanilla
JavaScript; the same data is available through a JSON API under `/api/v1`.

The app has no login and is intended to run on the loopback interface. Keep the
default `127.0.0.1` bind unless you understand the exposure.

## Status

v1.0.0 — first stable release. The JSON API under `/api/v1` is committed; any
breaking change will be released as v2.

## Requirements to build

- Go 1.22 or newer
- CGO enabled and a C compiler (the SQLite driver is
  `github.com/mattn/go-sqlite3`, downloaded at build time)

On Debian/Ubuntu, `build-essential` is enough for the C compiler.
On macOS, install the Xcode Command Line Tools (`xcode-select --install`).

## Build and run

### Linux

```sh
go build -o mirador .
./mirador
```

### macOS

```sh
go build -o mirador .
./mirador
```

Then open <http://127.0.0.1:8080> or <http://localhost:8080>.

Or run directly from source:

```sh
go run .
```

Then open <http://127.0.0.1:8080>.

### Configuration

| Flag | Environment variable | Default |
| --- | --- | --- |
| `-addr` | `MIRADOR_ADDR` | `127.0.0.1:8080` |
| `-db` | `MIRADOR_DB` | `<user config dir>/mirador/mirador.db` |

On Linux the default database path is usually
`~/.config/mirador/mirador.db`. On macOS it is usually
`~/Library/Application Support/Mirador/mirador.db`. The parent directory is
created with mode `0700`.

```sh
mirador -addr 127.0.0.1:9090 -db /path/to/mirador.db
```

The process handles `SIGINT`/`SIGTERM` with a graceful shutdown.

## Download prebuilt binaries

Prebuilt binaries for Linux and macOS are attached to each GitHub Release on
this repository. No Go installation or compilation is needed — just download
the binary for your platform and run it.

| Platform | Binary |
| --- | --- |
| Linux (amd64) | `mirador-linux-amd64` |
| macOS Apple Silicon (arm64) | `mirador-darwin-arm64` |
| macOS Intel (amd64) | `mirador-darwin-amd64` |

```sh
# Make it executable and run
chmod +x mirador-*
./mirador-*
```

The binaries are unsigned. On macOS, Gatekeeper may block the first launch;
right-click the binary → **Open** → **Open**, or run:

```sh
xattr -d com.apple.quarantine mirador-darwin-arm64
```

This is a one-time step per machine.

## Features
- Categories with color, custom sort order, and persisted expand/collapse.
- Accounts with hyperlink-capable info fields (SFDC, subdomain, CSM name +
  ChatApp CSM link, AE name + ChatApp AE link, ChatApp channel), notes,
  multiple tags, and alphabetical/custom ordering.
- Compact mode for dense lists on the accounts and tasks pages. Toggle it from
  the page header; the preference is remembered in a cookie.
- Tasks with a date-only due date, an optional ticket reference (plain text
  or a link), notes, completion flag, overdue marker, and due-date sorting.
- Drag reordering inside a due-date section. Cross-date drops are rejected by
  both the browser and the API; edit the task to reschedule it.
- A separate flat due-date view that hides completed tasks by default.
- Tags with usage counts and an editable color (defaults to `#6b7280`). Tags
  filter accounts and tasks; deleting a tag is permanent.
- The filter bar applies live as you type or toggle chips. Every facet except
  churn accepts multiple values: values inside one facet match any, and
  facets combine with AND. It narrows accounts and tasks by account name,
  category, CSM, AE, churn risk, and tag. Task views also search task title,
  ticket, and details.
- Soft-delete trash for categories, accounts, and tasks. Categories and
  accounts can be restored alone or with the children archived by the same
  delete operation. Permanent delete is available from the trash.
- Versioned CSV export/import, including trashed rows by default.
- JSON API, request IDs, security headers, and structured
  request logging.

## REST API

All endpoints are under `/api/v1`. JSON bodies use these shapes:

- Category: `name`, `color` (optional, defaults to `#6b7280`), `sort_order`, `expanded`
- Account: `category_id`, `name`, `sort_order`, `sfdc`, `subdomain`,
  `csm_name`, `csm_link`, `ae_name`, `ae_link`, `chatapp_channel`,
  `arr`, `churn_risk`, `renewal_date` (`YYYY-MM-DD`, optional), `details`, `tags`
- Task: `account_id`, `title`, `ticket`, `notes`, `due_date` (`YYYY-MM-DD`), `completed`
- Tag: `name`, `color` (optional, defaults to `#6b7280`)

### Authentication

API requests carry a key in the `Mirador-Api-Key` header (or
`Authorization: Bearer`). API-key requests bypass CSRF. Create a key on the
`/api` page; the full key is shown once at creation.

```sh
curl -H "Mirador-Api-Key: mir_abcd1234..." \
  http://127.0.0.1:8080/api/v1/accounts
```

Without a key, browser-style requests need a CSRF cookie plus matching
`X-CSRF-Token` (fetch it from `GET /api/v1/csrf`); `GET /api/v1/export` is the
only other CSRF exemption.

| Method and path | Purpose |
| --- | --- |
| `GET /api/v1/categories` | List categories (`include_deleted=1` to include trash) |
| `POST /api/v1/categories` | Create a category |
| `GET /api/v1/categories/{id}` | Get a category |
| `PUT /api/v1/categories/{id}` | Update a category |
| `DELETE /api/v1/categories/{id}` | Soft delete (`?permanent=1` to purge) |
| `POST /api/v1/categories/{id}/restore` | Restore (`?with_children=1` for descendants) |
| `GET /api/v1/accounts` | List accounts with their tasks (`category_id`, `tag`, `include_deleted`) |
| `POST /api/v1/accounts` | Create an account |
| `GET /api/v1/accounts/{id}` | Get an account with tags |
| `PUT /api/v1/accounts/{id}` | Update an account |
| `DELETE /api/v1/accounts/{id}` | Soft delete (`?permanent=1` to purge) |
| `POST /api/v1/accounts/{id}/restore` | Restore (`?with_children=1` for tasks) |
| `GET /api/v1/tasks` | List tasks (`account_id`, `category_id`, `tag`, `include_done`, `completed`, `overdue`, `include_deleted`) |
| `POST /api/v1/tasks` | Create a task |
| `GET /api/v1/tasks/{id}` | Get a task |
| `PUT /api/v1/tasks/{id}` | Update a task |
| `PATCH /api/v1/tasks/{id}/reorder` | Reorder within the same due date |
| `DELETE /api/v1/tasks/{id}` | Soft delete (`?permanent=1` to purge) |
| `POST /api/v1/tasks/{id}/restore` | Restore a task |
| `GET /api/v1/tags` | List tags with usage counts |
| `POST /api/v1/tags` | Create a tag |
| `GET /api/v1/tags/{id}` | Get a tag |
| `PUT /api/v1/tags/{id}` | Update a tag's name and/or color |
| `DELETE /api/v1/tags/{id}` | Permanently delete a tag |
| `GET /api/v1/trash` | List deleted categories, accounts, and tasks |
| `GET /api/v1/export` | Download CSV (`?active_only=1` excludes trash) |
| `POST /api/v1/export` | Same as `GET` |
| `POST /api/v1/import` | Import CSV (`?conflict=duplicate` or `override`) |

Task lists hide completed tasks by default, matching the due-date view. Add
`include_done=1` to include them, or `completed=1` to select only completed
tasks.

Reorder bodies accept either `sort_order` or `before_id`/`after_id`:

```json
{"due_date":"2030-06-01","sort_order":1.5}
```

A different `due_date` returns `409 Conflict`.

## CSV format

The export is one CSV file with a header and one row per record. Every row
starts with `format_version=2`; the current format is version 2.

Columns, in order:

```text
format_version, record_type, id, name, category_id, account_id, title, ticket,
color, sort_order, expanded, sfdc, subdomain, csm_name, csm_link, ae_name,
ae_link, chatapp_channel, arr, churn_risk, renewal_date, details, due_date,
completed, completed_at, deleted_at, created_at, updated_at, tags
```

- `record_type` is `category`, `tag`, `account`, or `task`.
- IDs are local to the file. Import maps them to database IDs.
- `account.tags` is a JSON array of tag names, for example
  `["Urgent","Waiting"]`. Empty arrays are written as `[]`.
- Rows include `deleted_at` when exporting trash. Use `active_only=1` to omit
  archived rows.
- Tasks are always appended on import; they have no unique name to match.
- For categories, tags, and accounts, `conflict=override` updates a row with
  the same name and `conflict=duplicate` inserts a renamed copy such as
  `Work (imported)`. Tag colors are mutable; an override also restores the
  tag color from the CSV.
- Import accepts a raw CSV request body or a multipart form with a `file`
  field. The maximum upload is 20 MiB.

A failed import is rolled back as one transaction.

## Database, migrations, and backups

Migrations are embedded SQL files in `internal/migrations` and are applied in
order at startup. `0001_initial.sql` is the complete current schema, so a fresh
database is created in one step; later schema changes are added as new numbered
files. The runner creates `schema_version`, applies each pending migration in a
transaction, and records its version.

Before migrating an existing database, Mirador checkpoints WAL and writes a
copy to:

```text
<database directory>/backups/<database-name>-<UTC timestamp>.db
```

The five most recent backups are kept. A fresh database does not create a
backup.

To restore a backup:

1. Stop Mirador.
2. Copy the chosen backup over the database path.
3. Remove stale `-wal` and `-shm` files next to the database, if present.
4. Start Mirador and check `/health`.

Migrations are up-only. To roll back a schema change, restore a pre-migration
backup.

The connection uses WAL mode, a busy timeout, `PRAGMA foreign_keys=ON`, and a
startup integrity check. Foreign keys enforce hard-delete cascades; soft-delete
cascades are applied explicitly so the same delete timestamp can be used for
restore-with-children.

## Constraints and notes

- Single-user and no authentication. Do not expose the listener publicly.
- Due dates are date-only (`YYYY-MM-DD`); times and time zones are not stored.
- Tag deletion is permanent; category, account, and task deletion is soft.
- Drag reordering changes order only within the same due date. Use the task
  edit form to change a due date.
- CSV import appends tasks every time; importing the same file twice creates
  duplicate task rows by design.
- `churn_risk` is a nullable boolean: `true` = at risk, `false` = not at risk,
  `null` = unknown. The UI exposes all three states ("—", "At risk", "Not at
  risk"). On the API, omit the field on PUT to keep the current value, or send
  `null` to clear it back to unknown.
