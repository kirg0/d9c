# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **Public test coverage report.** On every push to `main`, CI publishes the
  HTML coverage report to <https://kirg0.github.io/d9c/coverage/> (plus
  `functions.txt` and a shields.io `badge.json` behind the new README coverage
  badge). `make cover` builds the same report locally (`coverage.html`).

## [1.23.1] - 2026-09-13

### Tests

- **Test coverage 73.8% → 80.3%** (`go test -cover ./...`; 89.1% excluding
  the diagnostic `cmd/*` utilities). `internal/docker` 71.3% → 87.4%: an
  in-process SSH server (`golang.org/x/crypto/ssh`) exercises compose over SSH
  (up/pull/down/config, creating/reading/writing the compose file,
  backup/restore with sudo fallbacks), `sshStream`/`sshPipe`/`sshInteractive`,
  `sshRunner` and the full `ssh://` backend via `docker system dial-stdio`
  proxied into a mock daemon; plus the CRI backend methods and
  `ListPath`/`docker cp` against the mock daemon with exec hijacking.
  `internal/ui` 83.6% → 85.9% (modal form keys), `internal/ui/shell`
  79.0% → 85.0%, root package 1.4% → 67.6% (startup error paths, legacy host
  migration).

## [1.23.0] - 2026-09-13

### Changed

- **English is now the default UI language.** Without a `lang:` key in
  `d9c-config.yaml` the interface starts in English (previously Russian).
  Russian remains fully supported: `:lang ru` or `lang: ru` in the config.
  An existing `lang:` setting is honored as before. The `:lang` picker now
  lists `English` first.
- **`CLAUDE.md` and `CHANGELOG.md` translated to English.**

## [1.22.4] - 2026-09-13

### Changed

- **Go module path `d9c` → `github.com/kirg0/d9c`.** `go install
  github.com/kirg0/d9c@latest` now works; the version ldflags are
  `-X github.com/kirg0/d9c/internal/version.Version=...`.
- **The main README is now English** (`README.md`), the Russian one is
  `README-RU.md`; dynamic badges (release, CI, Go Report Card), animated demo
  `docs/demo.gif`.

### Fixed

- **The top line (header) disappeared in the Containers section.** At startup
  the table height was computed from zero-width placeholder columns, so the
  body came out one line taller than the window — the terminal scrolled and the
  `d9c vX.Y.Z › Containers` header went off-screen (including in stats mode `s`).
  Changing columns now recalculates the viewport height.

## [1.22.0] - 2026-07-19

### Added

- **CRI-O / generic CRI backend on top of `crictl`.** New host schemes:
  `crio://` / `cri://` (crictl on the local machine, optionally with a socket
  path — `crio:///var/run/crio/crio.sock`) and `crio+ssh://user@host`
  / `cri+ssh://user@host[/path/to.sock]` (crictl on a remote host over SSH,
  with the same key/password authentication as ssh://). Works with CRI-O, the
  containerd CRI plugin and any runtime with a CRI socket; the header shows a
  `cri-o` label (or `cri` for other runtimes) based on `RuntimeName` from
  `crictl version`. Covered: containers (name shown as `pod/container`, state
  from the CRI status), inspect, start/stop/rm, kill (= CRI stop with a zero
  timeout; CRI supports no other signals), logs `-f/--tail/--since`, CPU/MEM
  metrics (CPU% from the delta of the cumulative counter between ticks),
  interactive exec (over SSH), filesystem browsing, images
  (list/inspect/rmi/pull/prune), events (cri-tools ≥ 1.26), `system df`
  emulation, counters for the Hosts dashboard. Outside the CRI model — graceful
  degradation: Networks/Volumes/Compose show empty lists, build/tag/push/run/cp
  return a clear error "CRI manages only pods, containers and images". The
  implementation follows the nerdctl backend (local/ssh runner, pure parsers
  under table tests); pure CRI gRPC was rejected: it covers neither logs
  (files on the host) nor exec (SPDY streaming), so SSH is needed anyway.

## [1.21.0] - 2026-07-04

### Added

- **Connection status window for all host types** (#22). Previously the modal
  with a "connecting to …" spinner was shown only for SSH hosts with password
  authentication — connecting with a key or over TCP (Enter in Hosts,
  `:connect`) happened "silently", and an error landed as a line in the footer.
  Now every connection opens a status window: a spinner while dialing; on error
  the window stays open with the message (Enter — retry, Esc — close); on
  success it closes by itself. Special errors (changed SSH host key, host not
  found, unreachable unix socket) are still shown in separate informational
  windows with instructions.

## [1.20.7] - 2026-07-04

### Fixed

- **Micro-optimizations of the filter, sorting and tables** (#21). `filter.Match`
  lowercases the string once rather than per term; `filter.Compile` is
  memoized; name sorting precomputes keys; a guard against a panic in
  `truncate` at zero width. (Entry restored retroactively — the v1.20.7
  release shipped without a CHANGELOG note.)

## [1.20.6] - 2026-07-04

### Fixed

- **Compose operations over SSH no longer run an extra `docker version` probe
  before every run.** `sshNeedsSudo()` made an SSH round-trip on every
  up/down/pull/create/restore. The "is sudo needed" verdict is now cached for
  the lifetime of the connection (following `Runtime()`; a reconnect creates a
  new backend and resets the cache). An ambiguous probe — both attempts failed,
  likely a dropped connection — is not cached and is retried on the next call.

## [1.20.5] - 2026-07-04

### Fixed

- **Themes now apply to the log viewer, inspect, events and the command-line
  hint.** Log level coloring (ERROR/WARN/INFO/DEBUG and the timestamp), YAML
  highlighting in inspect (keys/strings/numbers/bool/null), the `docker events`
  feed (type/action/scope), search match highlighting, the scrollbar line and
  the autocomplete ghost text were hardcoded to the Tokyo Night palette (~35 hex
  values outside `styles.go`) and ignored `:theme` / `theme:` from the config.
  All styles were moved into `styles.Palette`/`styles.Apply` and switch together
  with the theme, including the live preview in the picker. The convention is
  enforced by a test: a hex literal outside the `styles` package fails `go test`.

## [1.20.4] - 2026-07-03

### Fixed

- **Logs and events no longer hang the interface on chatty streams.**
  Previously every log/event line went through the event loop as a separate
  message, and on every line the buffer was re-joined in full (and, with an
  active search, fully rescanned) — on a live `docker logs -f` of a busy
  container this caused quadratic CPU growth and noticeable stalls. Lines are
  now delivered in batches (the first one blocking, the rest drained from the
  channel without waiting, up to 256 at a time), with one redraw per batch, and
  search is updated incrementally: only the appended lines are checked.
- **The log/event buffer no longer grows without bound.** A cap of 10,000
  lines was introduced (like scrollback): old lines are evicted by new ones, and
  the indices of found matches are shifted correctly after trimming (the
  current match cursor stays on its line). Previously `:logs` without `--tail`
  on an active container gradually ate memory and slowed down every redraw.

## [1.20.3] - 2026-07-02

### Fixed

Following a full live run of every backend operation on a containerd host
(Debian 13, containerd v2.3.2, rootless nerdctl 2.3.4):

- **containerd: `:system df` no longer fails.** nerdctl (2.x) has no
  `system df` command, and the user saw a raw `level=fatal msg="unknown
  subcommand \"df\""`. The report is now assembled from object lists: the
  number of images with their total size, containers (total/running) and
  volumes.
- **containerd: the hosts dashboard is no longer empty.** `Info()` did not fill
  in the host name, CPU count and memory size — they are now taken from
  `nerdctl info --format json` (Name/NCPU/MemTotal, plus ServerVersion without
  parsing Components).
- **containerd: the `network:` filter now works.** The JSON output of
  `nerdctl ps` has no Networks field, so a container's network list was always
  empty. Networks are now extracted from the internal label
  `nerdctl/networks=["…"]` (carefully, bypassing commas inside the JSON value
  that break regular label parsing).
- **File browser: a nonexistent directory no longer reports "the container has
  no `ls`".** The message `ls: /path: No such file or directory` was wrongly
  caught by the check for a missing `ls` itself (the `ls:` prefix matched). The
  error is now attributed to the directory if stderr mentions the requested
  path; the false message is fixed for both backends (docker and nerdctl).
- **containerd: friendly errors instead of raw fatals.** The logrus wrapper
  (`time="…" level=fatal msg="1 errors:
no such image: …"`) is stripped from all
  errors of single-step nerdctl commands — the interface shows only the gist
  ("no such image: …").

## [1.20.2] - 2026-07-02

### Fixed

- **Events: an empty viewer no longer looks broken.** The event feed on a
  containerd host without activity (no healthchecks or background operations,
  unlike a typical docker daemon) stayed empty and was indistinguishable from a
  broken stream. Now: (1) until the first event the viewer shows a "waiting for
  events…" hint; (2) if the stream ended on its own (the `nerdctl events`
  process died, SSH dropped) — the feed shows a prominent line
  `[error] event stream ended — press r to reconnect` instead of eternal
  silence. Lines/closures of a stale (replaced) stream are not mixed into the
  live feed (messages carry a channel identifier).

## [1.20.1] - 2026-07-02

### Fixed

- **containerd/nerdctl: `Enter`/`i`/`l`/`e` did not work in the Compose section.**
  nerdctl does not set the `working_dir` label, so the identifier cell (the PATH
  column, `ComposeIDColumn`) was empty and `selectedID()` returned `""` — the keys
  silently did nothing. The identifier cell is now taken from
  `ComposeProject.Identity()` (working_dir, falling back to the project name);
  the same fallback is applied in `composeNameFor`, the copy menu and plugin
  variables. Filtering by the project label in the backends already supported
  such an identifier.

## [1.20.0] - 2026-07-01

### Added

- **containerd/nerdctl: working `up`/`pull`/`down` for Compose without a compose
  file.** nerdctl (unlike docker compose) does not set the `working_dir`/
  `config_files` labels, and `nerdctl compose ls` is missing in 2.x — there is
  nowhere to recover the compose file path of a discovered project from, so the
  engine commands used to fail. They are now reconstructed from the
  `com.docker.compose.project` labels (present on both containers and networks):
  **up** starts the project's containers, **pull** pulls the service images
  (with streamed progress), **down** stops and removes the project's containers
  and its networks (named volumes are left alone, like `docker compose down` by
  default). Verified on a real test stand. `config`/`edit`/`backup` remain
  unavailable for containerd (they need a compose file) (#19).

## [1.19.3] - 2026-07-01

### Fixed

- **containerd/nerdctl: starting a container with a port (`-p`) failed with
  `iptables not found`.** A non-interactive SSH session has
  PATH = `/usr/local/bin:/usr/bin:/bin` (without `/usr/sbin`, where `iptables`
  lives); nerdctl calls it client-side when publishing a port and could not find
  it → `failed to load networking flags` (visible as a "hang" while pulling the
  image, then an error). The SSH runner now adds
  `/usr/local/sbin:/usr/sbin:/sbin` to PATH for all nerdctl commands. Verified on
  a real host: `run -d -p 8080:80 nginx` starts normally (#18).

## [1.19.2] - 2026-07-01

### Fixed

- **`nerdctl+ssh://` hosts were not recognized as SSH in the UI.** All SSH
  authentication (host form, storage, connect modal, login parsing) was tied to
  `HasPrefix(host, "ssh://")`, so containerd hosts with the `nerdctl+ssh://`
  scheme did not offer the key/password choice, lost their saved authentication
  and **did not prompt for a password on connect** → the connection failed. A
  single helper `hosts.IsSSH` (ssh:// | nerdctl+ssh://) was added;
  `SSHUser`/`WithSSHUser` understand and preserve the `nerdctl+` prefix. The
  login/password modal now opens for containerd hosts over SSH too (#17). The
  direct passwordless form is unchanged: `-H nerdctl+ssh://user@host` (key/agent)
  or `-ssh-password` on the CLI.

## [1.19.1] - 2026-07-01

### Fixed

- **containerd/nerdctl: empty server version in the Hosts dashboard.**
  `Info().Version` was built with the wrong template
  `nerdctl version --format '{{.Server.Version}}'` — nerdctl keeps the server
  version in `Server.Components[]` (containerd), not in a flat
  `.Server.Version` field. It now parses `nerdctl version --format json` → the
  containerd component version. Found during a live check on a real host
  (containerd v2.3.2, rootless mode; #16).

## [1.19.0] - 2026-07-01

### Added

- **containerd support via nerdctl.** containerd has no Docker-compatible API,
  so d9c drives it through `nerdctl` (a Docker-compatible CLI frontend) — a new
  `nerdctlBackend` behind the same `docker.Backend` interface. Connect using the
  new host schemes: `nerdctl://` (nerdctl on the local machine) and
  `nerdctl+ssh://user@host` (nerdctl on a remote host over SSH, on top of the
  existing SSH plumbing). The full set of sections is implemented: containers
  (list/start/stop/restart/kill/rm/inspect/logs/stats/run), exec (over the SSH
  transport), images (list/pull/rmi/tag/push/build/history/prune), networks,
  volumes, Compose (discovery via the same `com.docker.compose.*` labels +
  `nerdctl compose up/down/pull`), events, `system df`/`prune`. The header shows
  a **containerd** label.
- **containerd namespaces.** A new optional `docker.NamespacedBackend`
  interface; the `:namespace <name>` command switches the namespace,
  `:namespace` without an argument opens a picker (list from
  `nerdctl namespace ls`). The active namespace is shown in the header as
  `containerd:<ns>`. All nerdctl commands are automatically scoped with
  `--namespace`.
- SSH transport helpers were moved to `internal/docker/ssh_exec.go`
  (client-parameterized `sshOutput`/`sshStream`/`sshPipe`/`sshInteractive`) and
  are shared between the docker and nerdctl backends; the docker backend methods
  became thin wrappers (behavior unchanged).

### Limitations

- `docker cp` and editing/backing up compose files are available only when
  nerdctl runs locally (over SSH the files would live on the remote host). Local
  interactive exec requires the ssh transport.

## [1.18.0] - 2026-07-01

### Added

- **Podman support over the Docker-compatible API.** d9c connects to Podman
  (`podman system service`) with the same backend as Docker — over `tcp://`,
  `unix://` or `ssh://`, without a separate flag. The engine is detected from
  the `/version` response (`docker.Runtime`: the "Podman Engine"
  component/platform); the result is cached and rechecked on host
  change/reconnect. When Podman is on the other side, a **podman** label appears
  in the header next to the host. Compose operations over SSH
  (`up`/`pull`/`down`/`config`/`create`) and the `version` probe automatically
  use `podman compose` / `podman` instead of `docker compose` / `docker`. The
  README has a section on rootless sockets and connecting. `Backend.Runtime()`
  was added to all implementations (real/fake/disconnected);
  `FakeBackend.RuntimeKind` allows exercising Podman paths in demo/tests.

## [1.17.0] - 2026-06-30

### Added

- **Connection status in the credentials modal.** When connecting to a
  password-authenticated `ssh://` host, the "Connect to …" modal no longer closes
  immediately: while the SSH dial is in progress, it shows a spinner with the
  status **"connecting to …"** (input is locked, a repeated Enter is ignored). On
  success the modal closes and the Containers section opens; on an
  authentication error it stays open with a clear message inside — you can fix
  the login/password and retry without reopening the form (the exception is a
  changed host key: a separate dialog is shown). The spinner follows
  `pullform`/`runform`.

## [1.16.0] - 2026-06-30

### Added

- **SSH authentication by key or password, with credentials prompted on
  connect.** The host add/edit form (`:add`/`:edit`, keys `a`/`e`) now offers a
  connection method for `ssh://` hosts: **Key** (with an optional "Key path"
  field — you can specify a custom private key path; empty = ssh-agent / default
  keys) or **Password** (`←/→/space` toggle the method). With password
  authentication **only the login is saved to the config** — the password is
  never written to disk. Connecting to such a host (`Enter` or `:connect`) opens
  a **"Connect to …"** modal with login and password fields: the saved login is
  prefilled but can be changed before connecting (the URL is rewritten with the
  new login). The password lives only in memory for the session (for
  auto-reconnect). Closes [#13](https://github.com/kirg0/d9c/issues/13). New
  fields `hosts.Host.SSHAuth`/`SSHKeyPath`, component `internal/ui/connform`,
  mode `ModeConnectAuth`.

## [1.15.0] - 2026-06-29

### Added

- **UI localization (Russian / English).** The language is switched with the
  `:lang` command (without an argument — a `Русский`/`English` picker modal with
  live preview, like `:theme`; `:lang en` / `:lang ru` — directly) and **is saved
  to the config** (the `lang:` key in `d9c-config.yaml`) — it survives a restart.
  The interface was Russian by default. New package `internal/i18n` (global
  current language + a `T(ru, en)` helper); translated: help, notifications,
  startup dialogs, modal titles, command hints and friendly Docker errors. The
  footer/header were already in English, so English mode is fully English.

## [1.13.0] - 2026-06-26

### Added

- **Unified configuration file.** All application settings — theme, individual
  color overrides, hotkeys, alert thresholds and **the list of saved hosts** —
  are now stored in a single `d9c-config.yaml` (new `hosts:` section).
  Previously hosts lived in a separate `d9c-hosts.json`. Plugins remain in their
  own `d9c-plugins.yaml`. New package `internal/settings` — the only
  reader/writer of the file: writing any section (editing a host, choosing a
  theme) rewrites the whole file without clobbering other sections, and
  validation is delegated to the `theme`/`keymap`/`alerts` packages (their pure
  `Resolve`).
- **The theme from the picker is saved to disk.** Confirming a theme in the
  picker modal (`Enter`) writes the choice to `theme:` in the config — it
  survives a restart. The `:theme <name>` command still changes the theme for
  the current session only.
- **Automatic host migration.** An existing `d9c-hosts.json` is migrated once on
  first launch into `hosts:` of the new config and renamed to
  `d9c-hosts.json.migrated`. The `-hosts-file` flag points to the migration
  source.

## [1.12.1] - 2026-06-25

### Fixed

- **Row selection disappeared when switching themes on the fly.** The bubbles
  table fixes the selection style once at creation, so after `:theme` (or a
  preview in the picker) the cursor row no longer matched the new ANSI prefix
  and the highlight vanished in colored sections (Hosts/Containers/Compose) —
  it was visible only in the startup theme. The table now re-syncs its styles on
  every theme change (`table.RefreshStyles`, called from the shared
  `Model.applyPalette`).
- **`k9s` theme: selection on a black background.** Optional `SelectBg`/
  `SelectFg` were added to the palette; for k9s the selected row is drawn as a
  bright inverted bar (aqua background + black text) instead of a barely visible
  `BgAlt` shift. Empty fields = previous behavior, other themes are unaffected.

## [1.12.0] - 2026-06-25

### Added

- **Built-in `k9s` theme.** A bright palette in the spirit of the stock k9s skin:
  black background, aqua accents, orange headers, saturated green/yellow/red
  statuses and dodgerblue borders. Available in the `:theme` modal and as
  `:theme k9s`.

## [1.11.0] - 2026-06-25

### Added

- **Theme picker modal with live preview.** `:theme` without an argument opens
  a list of built-in themes (tokyonight/dracula/nord/gruvbox/solarized/catppuccin)
  with color swatches; as the cursor moves, the theme is applied to the whole
  interface live (`styles.Apply`), `Enter` confirms the choice, `q`/`Esc` rolls
  back to the original palette. `:theme <name>` still applies a theme directly.
  New mode `ModeThemePicker` (`internal/ui/theme_picker.go`), helper
  `styles.Swatch`.

## [1.10.0] - 2026-06-25

### Added

- **Driver selection when creating a network/volume.** In the `:create` modals
  (Networks/Volumes) the Driver field became a selector: known drivers are
  cycled with `←`/`→` (networks — `bridge`/`host`/`overlay`/`macvlan`/
  `ipvlan`/`none`, volumes — `local`), and the `custom…` entry opens input for
  an arbitrary plugin driver. Reusable component `internal/ui/driverfield`.

## [1.1.2] - 2026-06-18

### Changed

- **Publication prep: personal data removed.** The diagnostic utilities
  `cmd/setup`, `cmd/ping`, `cmd/addgroup` no longer contain a hardcoded home
  host address and user names — the host (and, for `addgroup`, the user name)
  are now passed as command-line arguments. The test fixture in
  `internal/hosts` was switched to a neutral `deploy@10.0.0.5`.

## [1.1.1] - 2026-06-18

### Fixed

- **Logs: auto-scroll (follow) can now be toggled.** The `f` key turns
  following the log tail on/off (in the Containers and Compose sections). When
  enabled, the view jumps to the end and keeps the last line in sight; a
  `FOLLOW` indicator in the scrollbar and a `Follow: on/off` hint in the footer.
- **Console: exit with `Ctrl-D`.** Previously `Ctrl-D` was forwarded to the
  session as a `0x04` byte and relied on the remote shell — unreliable. It is
  now handled at the application level and closes the panel (like `Ctrl+\`), as
  the hint promises.
- **Files: fixed overlapping characters in the hint.** The wide glyph `⌫` was
  replaced with ASCII `bksp/h` in the footer and the help screen.

## [1.1.0] - 2026-06-17

### Changed

- **Compose: SSH-only commands hidden on `tcp://`.** Compose operations that
  need access to the host shell and filesystem — `create`, `up`, `down`,
  `pull`, `config`, `edit`, `backup`, `restore` — now work only over SSH. On a
  `tcp://` connection they disappear entirely from all surfaces (autocomplete
  and the command-line placeholder, the `?` help screen, the `:` footer, the
  `e Edit` hint, key handlers and `dispatchComposeCommand`), instead of being
  offered and failing at launch time.

### Added

- `Backend.SupportsHostCompose()` (`ssh://` → `true`, `tcp://` → `false`),
  plumbed into `ui.Model.composeHostOps` and `cmdline`; updated on
  `connect`/`reconnect`.

### Preserved over `tcp://`

- The backup catalog remains browsable (view/delete are local operations);
  only `restore` is hidden.
- Project discovery, `inspect`, `logs` and the container lifecycle
  (start/stop/restart/pause/unpause/remove) keep working.

## Earlier versions

The history of versions `1.0.0`–`1.0.12` is available in git tags and commit
history: `git log --oneline` / `git tag -l`.
