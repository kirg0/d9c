# d9c — guidance for Claude

A Go TUI for monitoring/managing Docker on a remote host (bubbletea + Docker SDK, connecting over TCP/SSH).

## Roadmap

The current development plan lives in [PLANE.md](PLANE.md). Check it at the start of work, take the next item from it, and **after implementing a feature and passing the quality gate, mark the item as done (`[x]`)**.

## Knowledge base (memory) — `.claude/memory/`

The [.claude/memory/](.claude/memory/) directory holds the project's migrated knowledge base
(notes per feature, pitfalls, test patterns, invariants, tooling). The index is
[.claude/memory/MEMORY.md](.claude/memory/MEMORY.md). **At the start of work, read the index and
open the relevant notes** — it is a copy of the working memory that travels with the project
DIRECTORY (copied when the folder is moved to another PC), but it is **not tracked by git** (`.claude/`
is in `.gitignore`), so a `git clone` will not bring it along — move the whole directory.
Notes are snapshots from the time of writing: verify `file:line` references and behavior against
the current code, and update the corresponding memory file after significant changes.

## Quality gate — mandatory before considering a task done

Run the full set of checks (PowerShell, Go in `C:\Program Files\Go\bin`, tools in `%USERPROFILE%\go\bin`):

```
make check      # = fmtcheck + vet + golangci-lint + test
```

or manually:

```
gofmt -l .               # must be empty
go vet ./...
golangci-lint run ./...  # config in .golangci.yml; install: make tools
go test ./...
```

`golangci-lint` (v2) enables errcheck, errorlint, gocritic, revive, staticcheck, misspell, unparam, and more. `cmd/*` (diagnostic utilities) are excluded from the linter.

Do not report success until everything is green. For concurrent code, add `go test -race ./...`.

## Tests — for every new/changed function

- Extract pure logic into separate functions and cover it with table-driven tests (`internal/ui/update_test.go`, `internal/docker/resources_test.go` are the reference examples).
- Test the TUI headlessly via `teatest` (`internal/ui/app_test.go`). Running without Docker: `go run . -demo` (fake backend in `internal/docker/fake.go`).
- Two teatest pitfalls: (1) send the nudge key `r` first, otherwise the first frame is not flushed; (2) `WaitFor` drains the `Output()` buffer — check several substrings of one frame in a SINGLE condition.
- The UI language defaults to English (`i18n.EN`), so tests assert the English variant of `i18n.T(ru, en)` strings; a test that switches the language must restore `i18n.EN` in `t.Cleanup`.

## Go conventions in this project

- **Errors:** wrap with `fmt.Errorf("...: %w", err)`, preserving the operation context. Messages start lowercase, with no trailing period (ST1005). Translate raw Docker daemon errors into readable text with a recommendation (`friendlyImageRemoveErr` in `resources.go`).
- **Architecture:** all Docker operations go through the `docker.Backend` interface (`client.go`). A new backend = an implementation of the interface (see `FakeBackend`). Do not reach `*client.Client` around the interface from the UI layer.
- **bubbletea (Elm):** `Update` does not mutate external state and makes no blocking/IO calls — everything goes through `tea.Cmd`, with the result returned as a typed `Msg`. Long operations must not block the event loop.
- **UI layers:** each component (`table`, `detail`, `logs`, `cmdline`, `filter`) is self-contained and implements its own `Update/View`; the root model delegates. A new mode = a `Mode` constant + a branch in `handleKey` + a renderer in `view.go`. A new `:` command = a case in `dispatchCommand`. A new section (resource) = a `ResourceView` constant + branches in `relayout`/`fetchCurrentResource`/`refreshTableRows`/`buildCopyItems` + columns/rows in `table` + a command set in `cmdline`.
- **bubbles/table invariant:** the number of cells in every row MUST equal the number of columns — `renderRow` iterates over cells and indexes `cols[i]`, otherwise it panics with `index out of range`. Do not add a "hidden" ID cell beyond the columns. Get the row identifier via `Model.selectedID()` (NAME = first column for hosts/volumes, ID = last column for the rest).
- **Style:** `gofmt`/tabs only. Group imports (stdlib / external / `d9c/...`). Exported symbols get doc comments starting with the symbol name. Platform-specific code goes behind build tags (`*_windows.go` / `*_other.go`).
- **Styling:** all colors/styles live only in `internal/ui/styles/styles.go`; do not hardcode lipgloss styles in place.
- **Localization:** user-facing strings go through `i18n.T(ru, en)`; English is the default language, Russian is selected via `:lang ru` / `lang: ru` in the config.

## Running

```
go run . -demo                 # demo data, no Docker
go run . -H tcp://host:2375
go run . -H ssh://user@host
```
