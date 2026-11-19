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

## Download prebuilt binaries

Prebuilt binaries for Linux and macOS are attached to each GitHub Release. No
compilation is needed — just download the binary for your platform and run it.

| Platform | Binary |
| --- | --- |
| Linux (64-bit) | `mirador-linux-amd64` |
| macOS Apple Silicon (M1/M2/M3) | `mirador-darwin-arm64` |
| macOS Intel | `mirador-darwin-amd64` |

```sh
# Make it executable and run
chmod +x mirador-darwin-arm64   # or mirador-linux-amd64, etc.
./mirador-darwin-arm64
```

Then open <http://127.0.0.1:8080> or <http://localhost:8080> in your browser.

### macOS: first launch warning

Because the binaries are not signed by Apple, macOS may block the first launch.
You'll see a warning about verification. Here's how to get around it:

**Option 1:** Open **System Settings** → **Privacy & Security** → scroll to
**Security** → click **Open Anyway** next to the blocked binary.

**Option 2:** Remove the quarantine flag:

```sh
xattr -d com.apple.quarantine mirador-darwin-arm64
```

This is a one-time step per machine.

## Create a macOS app bundle (.app)

If you'd rather double-click a normal Mac app icon instead of running from
Terminal, you can package the binary into a `.app` bundle:

```sh
./make-mac-app.sh          # Apple Silicon (default), version 1.0.0
./make-mac-app.sh amd64    # Intel Mac
./make-mac-app.sh arm64 1.1.0  # Custom architecture and version
```

This creates `Mirador.app`. Double-click it, then open
<http://127.0.0.1:8080> in your browser.

The app runs as a background server (no Dock icon). To see it in the Dock,
remove the `LSUIElement` key from `Mirador.app/Contents/Info.plist`.

## Build from source

If you want to build Mirador yourself, you need:

- **Go 1.22 or newer** — `brew install go` on macOS, or install from
  [go.dev](https://go.dev)
- **A C compiler** — on macOS run `xcode-select --install`; on Debian/Ubuntu
  `build-essential` is enough

### Linux

```sh
go build -o mirador .
./mirador
```

### macOS

```sh
GOOS=darwin GOARCH=arm64 go build -o mirador .   # Apple Silicon
GOOS=darwin GOARCH=amd64 go build -o mirador .   # Intel
./mirador
```

Then open <http://127.0.0.1:8080>.

Or run directly without building:

```sh
go run .
```

### Configuration

| Flag | Environment variable | Default |
| --- | --- | --- |
| `-addr` | `MIRADOR_ADDR` | `127.0.0.1:8080` |
| `-db` | `MIRADOR_DB` | user config dir |

Example:

```sh
./mirador -addr 127.0.0.1:9090 -db /path/to/mirador.db
```

The process handles `SIGINT`/`SIGTERM` with a graceful shutdown.

## Check for updates

The app includes an update button in the footer. Clicking it checks GitHub for
a newer release and, if one exists, opens the releases page in your browser so
you can download it.

## Features

- Categories with color, custom sort order, and persisted expand/collapse.
- Accounts with hyperlink-capable info fields, notes, multiple tags, and ordering.
- Tasks with due dates, completion flag, overdue markers, and drag reordering.
- Tags with usage counts and editable colors.
- Soft-delete trash with restore for categories, accounts, and tasks.
- Versioned CSV export/import.
- JSON API under `/api/v1`.

## Constraints and notes

- Single-user and no authentication. Do not expose the listener publicly.
- Due dates are date-only (`YYYY-MM-DD`); times and time zones are not stored.
- Tag deletion is permanent; category, account, and task deletion is soft.
- Drag reordering changes order only within the same due date.
- CSV import appends tasks every time; importing twice creates duplicate tasks.
