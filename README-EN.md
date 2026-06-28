<p align="center">
  <img src="docs/logo.png" width="200" alt="d9c">
</p>

# d9c

<p align="center"><a href="README.md">Русский</a> · <b>English</b></p>

**A terminal (TUI) Docker manager for remote hosts** — in the spirit of [`k9s`](https://k9scli.io/)
and [`lazydocker`](https://github.com/jesseduffield/lazydocker), but focused on managing
Docker **over TCP or SSH**. A single binary, no agents on the remote side: connect to the daemon,
see containers, images, networks, volumes and Compose projects and manage them without leaving the terminal.

Built on [Bubble Tea](https://github.com/charmbracelet/bubbletea) and the official
[Docker SDK](https://pkg.go.dev/github.com/docker/docker).

![license](https://img.shields.io/badge/license-MIT-blue)
![go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![version](https://img.shields.io/badge/version-1.7.1-informational)
![platform](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![donate](https://img.shields.io/badge/donate-dalink.to-ff5e5b)](https://dalink.to/kirg08)

<p align="center">
  <img src="docs/demo.png" alt="d9c — Containers section: STATUS/HEALTH/PORTS/CPU%/MEM columns, connection indicator and key hints" width="900">
</p>

> Want to take a look without setting up Docker? Run the demo on fake data:
> `go run . -demo`.

---

## Table of contents

- [Features](#features)
- [Installation](#installation)
- [Quick start](#quick-start)
- [Connecting to Docker](#connecting-to-docker)
- [Sections and navigation](#sections-and-navigation)
- [Filter `/`](#filter-)
- [Config, themes and keys](#config-themes-and-keys)
- [Container filesystem](#container-filesystem-f--files)
- [Auto-refresh](#auto-refresh)
- [Resource threshold alerts](#resource-threshold-alerts)
- [Plugins](#plugins)
- [Development](#development)
- [Support the project](#support-the-project)
- [License](#license)

---

## Features

- **Remote Docker over TCP and SSH** — a single connection path for both transports, live
  `:connect`, saved hosts with CRUD, auto-reconnect on disconnect (backoff + banner).
- **All core resources** — Containers / Images / Networks / Volumes / Compose / Hosts.
- **Management, not just viewing** — start/stop/restart/kill/rm, bulk operations
  (multi-select with `Space` — bulk operations on containers and image removal),
  a `run` wizard, network/volume creation, `build`/`tag`/`push`
  (including to a private registry), `docker system df` and `prune` with confirmation.
- **Compose** — discovery by labels, `up`/`pull`/`down` with streaming, `config`, `edit`,
  `create`, backup/restore, project logs, drill-down into containers (operations on project
  files and running `docker compose` — over SSH only; see [Connecting to Docker](#connecting-to-docker)).
- **Logs and metrics** — `--tail/--since/--until`, search, save to file; live CPU/MEM/Net/Disk
  via the Stats API.
- **Built-in terminal** — interactive `exec` into a container (vt10x emulator), a single path for
  TCP and SSH.
- **Container filesystem browser** — navigation and `docker cp` in both directions.
- **Live daemon event log** (`docker events`) in a dedicated console.
- **Multi-host dashboard** — status and aggregates (`docker info`) across all saved hosts.
- **CPU/MEM alerts**, configurable **themes** and **hotkeys**, **plugins** (your own commands
  and keys from YAML — like in k9s).

---

## Installation

### Prebuilt binaries (recommended)

No Go and no compiler required — nothing needs to be installed on the remote host either.
Download the archive for your OS from the
[**Releases**](https://github.com/kirg0/d9c/releases/latest) page:

| OS | File |
|----|------|
| Linux (x86-64) | `d9c_vX.Y.Z_linux_amd64.tar.gz` |
| Linux (ARM64) | `d9c_vX.Y.Z_linux_arm64.tar.gz` |
| macOS (Intel) | `d9c_vX.Y.Z_darwin_amd64.tar.gz` |
| macOS (Apple Silicon) | `d9c_vX.Y.Z_darwin_arm64.tar.gz` |
| Windows (x86-64) | `d9c_vX.Y.Z_windows_amd64.zip` |

Inside the archive there is a single executable (`d9c` or `d9c.exe`) plus `README.md` and `LICENSE`.

**Linux / macOS:**

```sh
# unpack and put it on PATH (example for Linux amd64)
tar -xzf d9c_vX.Y.Z_linux_amd64.tar.gz
sudo install d9c_vX.Y.Z_linux_amd64/d9c /usr/local/bin/d9c

d9c -version          # check
```

> macOS may block an unsigned binary on first launch
> ("cannot be opened because the developer cannot be verified"). Remove the quarantine:
> `xattr -d com.apple.quarantine ./d9c`.

**Windows (PowerShell):**

```powershell
# unpack the zip and run d9c.exe from that folder,
# or put it in a directory that is on %PATH%
Expand-Archive d9c_vX.Y.Z_windows_amd64.zip
.\d9c_vX.Y.Z_windows_amd64\d9c.exe -version
```

**Checksum verification (optional).** Each release ships a
`checksums.txt` (SHA-256):

```sh
sha256sum -c checksums.txt 2>/dev/null | grep d9c_vX.Y.Z_linux_amd64.tar.gz
```

```powershell
# Windows
(Get-FileHash .\d9c_vX.Y.Z_windows_amd64.zip -Algorithm SHA256).Hash
```

### Building from source

Requires [Go 1.25+](https://go.dev/dl/):

```sh
git clone https://github.com/kirg0/d9c.git
cd d9c
make build          # binary ./d9c (or d9c.exe on Windows)
```

or directly via `go`:

```sh
go build -o d9c .
```

The version can be baked into the binary at build time (release builds do this automatically):

```sh
go build -ldflags "-X d9c/internal/version.Version=1.2.3" -o d9c .
```

The application follows [SemVer](https://semver.org); the current version is shown in the header (`d9c vX.Y.Z`)
and printed by the `-version` flag.

---

## Quick start

```sh
go run . -demo                 # demo data, no Docker
go run . -H tcp://host:2375    # remote daemon over TCP
go run . -H ssh://user@host    # remote daemon over an SSH tunnel
go run . -version              # print the version and exit
```

> The examples above are for running from source. If you installed a prebuilt binary,
> use `d9c` instead of `go run .` (e.g. `d9c -demo`, `d9c -H ssh://user@host`).

If no host is specified, d9c opens on the **Hosts** section, where you can pick a saved host
or add a new one — the connection happens via `Enter` / `:connect`.

---

## Connecting to Docker

| Transport | Example | Note |
| --- | --- | --- |
| TCP | `-H tcp://host:2375` | the daemon must listen on TCP (`-H tcp://0.0.0.0:2375` on the server side) |
| SSH | `-H ssh://user@host` | an SSH tunnel to the local daemon socket; keys from the agent/`~/.ssh` |

> **TCP vs SSH — what's available.** Almost everything (containers, images, networks, volumes, exec,
> container FS browser, events, dashboard) works over both transports via the Docker Engine API.
> But Compose operations that need access to the **host filesystem** or run `docker compose`
> itself as a process go around the API — SSH only. So **when connected over TCP the following
> Compose commands are unavailable** (they don't appear in the hints or in `?`):
> `create`, `up`, `down`, `pull`, `config`, `edit`, `backup`, `restore` (and the `e` key —
> edit the file). Over TCP you still get project discovery, view/inspect/logs, the local `backups`
> directory (view and delete archives only — restore requires SSH) and project container
> management: `start` / `stop` / `restart` / `pause` / `unpause` / `remove`. Need the
> full Compose set — connect via `-H ssh://...`.

The **Hosts** section is both the list of saved hosts and a multi-host dashboard: each host gets a row
with status (● up/down) and an aggregate from `docker info` (containers/running/images/daemon version).
Data is collected over a single connection per host, refreshed roughly every 10 seconds. `Enter` — connect
to the selected host. Management right from the section: `a` — add, `e` — edit, `d` — delete
(with confirmation); the same actions are available via the `:add` / `:edit` / `:rm` commands. The
`:dashboard` / `:dash` commands are aliases for `:hosts`. The host list is stored in the shared
`d9c-config.yaml` (the `hosts:` section, see [Config, themes and keys](#config-themes-and-keys)).

---

## Sections and navigation

Sections: **Containers / Images / Networks / Volumes / Compose / Hosts**.

- Navigation — arrow keys / `j` / `k`, `PgUp/PgDn`, `g`/`G`.
- Filter — `/`, command line — `:`, quit — `q`.
- Key hints are in the bottom line; full help for the current section is on the `?` key.

---

## Filter `/`

Plain text is a case-insensitive substring (multiple words are logical AND).
Structured terms are also available (for Containers they are the richest):

| Term | What it does |
| --- | --- |
| `nginx` | substring in name/image/status |
| `re:^web-\d+` | regular expression (case-insensitive) |
| `status:running` | by status/state (`running`, `exited`, `healthy`…) |
| `label:env` / `label:env=prod` | by container label (key or key=value) |
| `network:frontend` (`net:`) | by attached network |

Terms combine with a space (AND): `status:running label:env=prod net:bridge`.
A regex error is highlighted right in the filter line.

---

## Config, themes and keys

**All application settings** — theme, color overrides, hotkeys, alert thresholds
and the **list of saved hosts** — live in a single YAML file. By default d9c looks for
**`d9c-config.yaml` next to the executable**; a different path can be set with a flag:

```sh
d9c -config /path/to/d9c-config.yaml
```

Plugins are the only exception: they live in a separate `d9c-plugins.yaml`. A missing config
is not an error — the built-in `tokyonight` theme and an empty host list are used. The file is read
at startup, and changes made from the interface (editing hosts, picking a theme in the picker)
are written back immediately — the other sections are preserved. An old standalone
`d9c-hosts.json` is **automatically migrated** into the new config's `hosts:` on first run
(the file is renamed to `d9c-hosts.json.migrated`).

```yaml
lang: ru                  # UI language: ru (default) or en
theme: dracula            # built-in palette (tokyonight by default)
colors:                   # optional pointwise color overrides
  primary: "#ff79c6"
  danger: "#ff5555"
hosts:                    # saved hosts (Hosts section; usually edited from the UI)
  - name: prod
    host: ssh://user@prod.example.com
  - name: local
    host: tcp://localhost:2375
```

Built-in themes: `tokyonight`, `dracula`, `nord`, `gruvbox`, `solarized`,
`catppuccin`, `k9s` (bright, in the spirit of the k9s skin). The theme can also be switched
**on the fly, without a config** — via the `:theme <name>` command (e.g. `:theme nord`); `:theme`
without an argument opens a picker modal with a list of themes and live preview (arrows —
preview, Enter — apply, q/Esc — cancel). Picking a theme through the picker (Enter)
is **saved to the config** (`theme:`) — it survives a restart; `:theme <name>` changes the theme
for the current session only. In `colors` you can override any of the base colors on top of the
selected theme:

**The UI language** is switched the same way: the `:lang` command with no argument opens a
picker modal (`Русский` / `English`, arrows — preview, Enter — apply, q/Esc — cancel), while
`:lang en` / `:lang ru` change the language directly. The choice is **saved to the config**
(`lang:`) and survives a restart. The interface is Russian by default.

| Key | Purpose |
| --- | --- |
| `primary` | accents, active keys, indicators |
| `secondary` | table headers, labels |
| `success` | running / healthy / "● up" |
| `warning` | transitional states (paused, reconnect) |
| `danger` | errors, stopped, unhealthy |
| `muted` | dimmed text, separators |
| `bg` / `bgalt` | background and raised surfaces (selection, bars, modals) |
| `fg` | primary text |
| `border` | frames and lines |

A color value is hex (`#rgb` or `#rrggbb`) or an ANSI palette index `0`–`255`.
An unknown theme, an unknown color key or an invalid value is an error at
startup (`loading config: …`).

### Keys

Normal-mode actions can be remapped in the `keys:` section of the same
`d9c-config.yaml`. Only the actions you want to change need to be listed —
the rest stay at their defaults:

```yaml
keys:
  filter: f        # filter instead of "/"
  logs: g          # logs instead of "l"
  select: space    # mark for a bulk operation (the alias "space" = the spacebar)
```

| Action | Default | What it does |
| --- | --- | --- |
| `inspect` | `i` | details of the selected resource |
| `logs` | `l` | container / compose-project logs |
| `edit` | `e` | edit the compose file |
| `exec` | `x` | shell in a container (built-in terminal) |
| `filter` | `/` | filter by rows |
| `command` | `:` | command line |
| `toggle-all` | `a` | all / running only |
| `stats` | `s` | CPU/MEM metrics |
| `select` | `space` | mark for a bulk operation |
| `copy` | `y` | copy menu |
| `refresh` | `r` | refresh manually |
| `pause` | `p` | pause/resume auto-refresh |
| `help` | `?` | help |

A value is a key name in Bubble Tea notation (`f`, `ctrl+d`, `f5`, `space`, etc.).
Navigation (`↑/↓`, `j/k`, `PgUp/PgDn`), `Enter` and the quit keys (`q`, `esc`,
`Ctrl+C`) are fixed and cannot be remapped. An unknown action, an empty key, a
reserved key or one key bound to two actions is an error at startup
(`loading keybindings: …`). The `?` help shows the actual (remapped) keys.

---

## Container filesystem (`f` / `:files`)

In the **Containers** section the `f` key (or the `:files [path]` command) opens a browser of
the selected running container's filesystem. The listing is built via `ls`
inside the container, so in minimal images without `ls` (scratch/distroless) the
browser is unavailable (this is reported with a clear error).

| Key | Action |
| --- | --- |
| `enter` / `l` | enter a directory |
| `⌫` / `h` / `-` | go up one level |
| `d` | download the selected file/directory into d9c's working directory (`docker cp` out of the container) |
| `↑/↓` `j/k`, `g`/`G`, `PgUp/PgDn` | navigate the list |
| `q` / `esc` | close the browser |

Uploading INTO a container is done with the `:cp <local-path> <container-dir>` command (the target path
must be an existing directory inside the container). Calling `:cp` **without
arguments** opens a modal wizard: a built-in picker for the local filesystem
(navigating the machine where d9c runs) plus a destination directory field in the
container — `Tab` switches focus, `enter`/`l` enters a directory, `⌫`/`h`
goes up, `enter` in the destination field starts the upload. Downloading unpacks
the daemon's tar stream to disk with protection against escaping the destination
directory; symlinks and special files are skipped.

---

## Auto-refresh

Lists are refreshed on a timer. The initial interval is set by the `-interval` flag
(e.g. `-interval 5s`, `3s` by default); the `:interval <dur>` command changes it on the fly
(`:interval 10s`, range `1s`–`1h`), and `:interval` without an
argument shows the current value. The `p` key (or `:interval pause` /
`:interval resume`) pauses and resumes auto-refresh — the server status
indicator keeps working meanwhile, and manual refresh via `r` is always available.
The state is shown in the header: `↻3s` — the active interval, `⏸ paused` — paused.

---

## Resource threshold alerts

Containers whose load exceeds a given threshold are highlighted with a `⚠` marker
next to the name (in both Containers table modes), and a `⚠ N` counter appears in the
header — the number of "hot" containers. The thresholds rely on the live Stats API metrics
(the same CPU%/MEM% as in `s` mode); stopped and not-yet-polled containers
are not counted.

The initial thresholds are set by the `alerts:` section in `d9c-config.yaml` (optional;
`0` or absence = the metric is off):

```yaml
alerts:
  cpu: 80     # highlight a container at CPU% ≥ 80 (may exceed 100 on multi-core)
  mem: 90     # highlight at MEM% ≥ 90
```

The thresholds are changed on the fly with the `:alert` command:

| Command | Action |
| --- | --- |
| `:alert cpu <%>` | CPU% threshold (e.g. `:alert cpu 80`) |
| `:alert mem <%>` | MEM% threshold |
| `:alert cpu off` / `:alert mem off` | turn off an individual metric |
| `:alert off` | turn alerts off entirely |
| `:alert` | show the current thresholds |

---

## Plugins

Plugins are **custom commands and hotkeys** described in a YAML file
(like in k9s). Each plugin runs a **local** command (on the machine where
d9c runs) with substitution of the selected row's data. This lets you wire in `dive`, `lazydocker`,
`ctop`, your own scripts, `docker` commands and so on — without changing the application code.

### Where the file lives

By default d9c looks for the file **`d9c-plugins.yaml` next to the executable**.
A different path can be set with a flag:

```sh
d9c -plugins-file /path/to/plugins.yaml
```

A missing file is not an error, there will just be no plugins. The file is read **once at
startup**: after editing it, restart d9c.

### File format

The root is the `plugins` key with a list of objects:

```yaml
plugins:
  - name: dive                 # required — the command name (invoked as :dive)
    key: ctrl+d                # optional — a hotkey
    scope: images              # in which section it's available (default "*")
    description: Image layers  # optional — for documentation
    command: dive              # required — the executable (without arguments)
    args: ["${ID}"]            # optional — arguments (each on its own line)
    background: false          # optional — launch mode (default false)
```

#### Fields

| Field         | Req. | Description |
|---------------|:----:|----------|
| `name`        | yes  | The command name. Invoked as `:name`. |
| `command`     | yes  | The executable name/path. **Run directly, without a shell.** |
| `args`        | no   | The argument list. Each one is a separate list item (not a single string). |
| `scope`       | no   | The section where the plugin is active. Default `*` (everywhere). |
| `key`         | no   | A hotkey (Bubble Tea format: `ctrl+d`, `f5`, `alt+x`…). |
| `description` | no   | A short description (documentation). |
| `background`  | no   | `false` — interactive (takes over the terminal); `true` — in the background with output to a console. |

#### Allowed `scope` values

`containers`, `images`, `networks`, `volumes`, `compose`, `hosts`, or `*` (any section).
Case-insensitive. A plugin with `scope: containers` is only available in the containers section;
`scope: "*"` — in all of them.

### `${VARIABLE}` substitution

Before launch, the values from the **selected row** are substituted into `command` and into each
`args` item. Unknown placeholders are left as is (so a typo is visible).

Always available:

| Variable | Value |
|------------|----------|
| `${HOST}`  | The address of the current Docker host (`tcp://…` or `ssh://…`). |
| `${ID}`    | The identifier of the selected row. For containers/images/networks — the ID; for volumes/projects/hosts — the name (which is also the row key). |

Depending on the section, the following are added:

| Section (`scope`) | Additionally |
|------------------|---------------|
| `containers`     | `${NAME}` `${IMAGE}` `${STATUS}` `${STATE}` `${PORTS}` |
| `images`         | `${NAME}` `${IMAGE}` `${TAGS}` (all three = the image tags) |
| `networks`       | `${NAME}` `${DRIVER}` |
| `volumes`        | `${NAME}` `${DRIVER}` |
| `compose`        | `${NAME}` `${PATH}` (the working directory) `${STATUS}` |
| `hosts`          | `${NAME}` `${HOST}` (the URL of the selected host) |

> The remote daemon is `${HOST}`. Since the command runs locally, for actions
> against the remote daemon call the local client with this address, e.g.
> `docker -H ${HOST} …` or `docker -H ${HOST} exec -it ${ID} sh`.

### How to invoke a plugin

- **By command:** `:` → type `name` → Enter. The plugin names for the current section appear
  in autocompletion.
- **By key:** if `key` is set — press it in the appropriate section. The binding is shown
  in the hints at the bottom of the screen.

**Built-in commands and keys always take priority.** If you name a plugin like a built-in
command (`stop`, `rm`, `logs`…) or bind it to a taken key (`i`, `l`, `x`, `s`,
`a`, `/`, `:`…), the built-in action fires. So for keys prefer
`ctrl+<letter>` or function keys (`f2`…`f12`), and pick names different from the
built-in ones.

### Launch modes

**Interactive (`background: false`, default).** d9c **hands over the terminal**
to the launched program (like `exec`/shell), and returns the interface after it exits.
Suitable for interactive programs: a shell in a container, `dive`, `lazydocker`, `vim`,
`htop`. A non-zero exit code is shown as an error in the bottom line.

**Background (`background: true`).** The command runs without taking over the terminal, and its
stdout/stderr are **streamed line by line into the operation console** (like `compose up` progress).
Suitable for one-off commands that print text (`docker system df`, reports, scripts).
Close the console with `q`/`esc`.

### Important limitations

- **No shell.** `command` runs directly, so pipelines (`|`), redirections
  (`>`), substitutions (`$(…)`), wildcards (`*`) and environment variables are **not** expanded.
  To use them, call a shell explicitly:
  - Linux/macOS: `command: sh`, `args: ["-c", "docker -H ${HOST} logs ${ID} | tail -n 100"]`
  - Windows: `command: cmd`, `args: ["/c", "…"]`
- **The command runs locally**, on the machine with d9c. The needed binaries (`docker`, `dive`,
  `lazydocker`…) must be installed and available on `PATH`.
- **Cross-platform.** The paths to the shell and utilities differ on Windows and Linux —
  keep in mind where d9c runs.
- The file is read at startup; after changes a restart is needed.

### Full `d9c-plugins.yaml` example

```yaml
plugins:
  # An interactive shell in the selected container (via the remote daemon).
  - name: sh
    key: ctrl+s
    scope: containers
    description: Shell inside the container
    command: docker
    args: ["-H", "${HOST}", "exec", "-it", "${ID}", "sh"]

  # Explore image layers with dive.
  - name: dive
    key: ctrl+d
    scope: images
    description: Image layer analysis
    command: dive
    args: ["${TAGS}"]

  # Full lazydocker, connected to the same host.
  - name: lazy
    scope: "*"
    command: lazydocker

  # The daemon's disk usage — output to the operation console.
  - name: df
    scope: "*"
    background: true
    description: docker system df
    command: docker
    args: ["-H", "${HOST}", "system", "df"]

  # The last 200 log lines through a shell pipeline (in the background).
  - name: tail
    scope: containers
    background: true
    command: sh
    args: ["-c", "docker -H ${HOST} logs --tail 200 ${ID}"]
```

### Troubleshooting

- **The plugin isn't invoked by `:name`** — check the `scope` (does it match the current
  section or `*`) and that the name doesn't collide with a built-in command.
- **The key doesn't fire** — it's probably taken by a built-in action; change it to
  `ctrl+<…>`/`fN`.
- **`executable file not found`** — the needed binary isn't on `PATH` on the machine with d9c.
- **A pipeline/`>`/`*` "doesn't work"** — that's expected: wrap the command in `sh -c "…"` /
  `cmd /c "…"`.
- **A `loading plugins: …` error at startup** — invalid YAML, or a plugin without `name`/
  `command`, or with an unknown `scope`. Fix the file and restart.

---

## Development

The full set of checks before a commit (quality gate):

```sh
make check      # = fmtcheck + vet + golangci-lint + test
```

or manually:

```sh
gofmt -l .               # should be empty
go vet ./...
golangci-lint run ./...  # config in .golangci.yml; install: make tools
go test ./...
go test -race ./...      # for concurrent code
```

Useful Makefile targets: `make build`, `make run ARGS="-H tcp://host:2375"`, `make demo`,
`make test`, `make race`, `make lint`, `make tools` (installs `golangci-lint`/`staticcheck`).

Architecturally all Docker operations are hidden behind the `docker.Backend` interface, so the demo mode
(`-demo`) and headless tests use `FakeBackend` and don't require a real daemon. The UI is built
on the Elm model (Bubble Tea): `Update` doesn't block the event loop, long operations go through `tea.Cmd`.

---

## Support the project

d9c is developed in spare time. If the tool turned out useful, you can support
its development with a donation — it helps to find time for new features:

➡️ **[dalink.to/kirg08](https://dalink.to/kirg08)**

A repository star ⭐ is motivating too. Thank you!

---

## License

[MIT](LICENSE) © kirg0
