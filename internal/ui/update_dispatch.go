package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"d9c/internal/alerts"
	"d9c/internal/docker"
	"d9c/internal/theme"
	"d9c/internal/ui/cmdline"
	"d9c/internal/ui/styles"

	tea "github.com/charmbracelet/bubbletea"
)

// This file holds the `:` command dispatch tables and their helpers, split out
// of update.go; handleCommand (update.go) is the entry point.

func (m *Model) dispatchCommand(cmd *cmdline.CommandMsg) (tea.Cmd, error) {
	// ── view switching (always valid) ──────────────────────────────────────────
	switch cmd.Name {
	case "containers", "c":
		return func() tea.Msg { return switchResourceMsg{ViewContainers} }, nil
	case "images", "img":
		return func() tea.Msg { return switchResourceMsg{ViewImages} }, nil
	case "networks", "net":
		return func() tea.Msg { return switchResourceMsg{ViewNetworks} }, nil
	case "volumes", "vol":
		return func() tea.Msg { return switchResourceMsg{ViewVolumes} }, nil
	case "hosts", "h", "dashboard", "dash":
		return func() tea.Msg { return switchResourceMsg{ViewHosts} }, nil
	case "compose", "co":
		return func() tea.Msg { return switchResourceMsg{ViewCompose} }, nil
	case "events":
		return openEvents(m.backend), nil
	case "system":
		// System-wide ops, available from any view: `system df` / `system prune`.
		if len(cmd.Args) == 0 {
			return nil, fmt.Errorf("usage: system df | system prune")
		}
		switch cmd.Args[0] {
		case "df":
			return systemDFCmd(m.backend), nil
		case "prune":
			prune := systemPruneCmd(m.backend)
			return func() tea.Msg {
				return openConfirmMsg{
					prompt: "Удалить остановленные контейнеры, неиспользуемые сети,\nвисячие образы и build-кэш? (тома не затрагиваются)",
					action: prune,
				}
			}, nil
		default:
			return nil, fmt.Errorf("unknown system command: %s (df | prune)", cmd.Args[0])
		}
	case "theme":
		// Switch the color scheme on the fly, no config file needed. styles.Apply
		// mutates package-level styles, which the View functions read lazily — safe
		// because dispatchCommand runs on the bubbletea event-loop goroutine, not
		// inside a tea.Cmd. Applies on top of any config-file color overrides.
		names := strings.Join(theme.Names(), ", ")
		if len(cmd.Args) == 0 {
			cur := theme.NameOf(styles.Active())
			if cur == "" {
				cur = "custom"
			}
			return nil, fmt.Errorf("usage: theme <name> (%s); current: %s", names, cur)
		}
		name := strings.ToLower(strings.TrimSpace(cmd.Args[0]))
		pal, ok := theme.ByName(name)
		if !ok {
			return nil, fmt.Errorf("unknown theme %q (available: %s)", name, names)
		}
		styles.Apply(pal)
		m.copyNotif = "тема: " + name
		return clearCopyNotifCmd(), nil
	case "interval":
		// Auto-refresh cadence, available from any view. No arg reports the
		// current value; `pause`/`resume` toggle the freeze.
		if len(cmd.Args) == 0 {
			state := m.refreshInterval.String()
			if m.paused {
				state += " (на паузе)"
			}
			return nil, fmt.Errorf("интервал автообновления: %s; задать: interval <dur> (напр. 5s), пауза: p", state)
		}
		switch strings.ToLower(cmd.Args[0]) {
		case "pause", "off":
			m.paused = true
			m.copyNotif = "автообновление на паузе"
			return clearCopyNotifCmd(), nil
		case "resume", "on":
			m.paused = false
			m.copyNotif = "автообновление возобновлено"
			return tea.Batch(m.fetchCurrentResource(), clearCopyNotifCmd()), nil
		}
		d, err := time.ParseDuration(cmd.Args[0])
		if err != nil {
			return nil, fmt.Errorf("неверный интервал %q (примеры: 1s, 5s, 2m)", cmd.Args[0])
		}
		m.refreshInterval = clampInterval(d)
		m.paused = false
		m.copyNotif = "интервал: " + m.refreshInterval.String()
		// The running tick loop reads m.refreshInterval when it reschedules, so the
		// new cadence applies after at most one old interval. Don't start a second
		// tickCmd here — that would run two tick chains in parallel. Refresh now so
		// the change is felt immediately.
		return tea.Batch(m.fetchCurrentResource(), clearCopyNotifCmd()), nil
	case "alert":
		// Resource-usage thresholds, available from any view. No arg reports the
		// current config; `cpu`/`mem <pct>` set a metric, `off` disables both.
		if len(cmd.Args) == 0 {
			return nil, fmt.Errorf("алерты: %s; задать: alert cpu <%%> | alert mem <%%> | alert off", alertSummary(m.alerts))
		}
		switch strings.ToLower(cmd.Args[0]) {
		case "off", "none", "clear":
			m.alerts = alerts.Thresholds{}
		case "cpu":
			if err := m.setAlertThreshold("cpu", cmd.Args); err != nil {
				return nil, err
			}
		case "mem", "memory":
			if err := m.setAlertThreshold("mem", cmd.Args); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("usage: alert cpu <%%> | alert mem <%%> | alert off")
		}
		m.applyColumns(m.width)
		m.refreshTableRows()
		m.copyNotif = "алерты: " + alertSummary(m.alerts)
		return clearCopyNotifCmd(), nil
	}

	// ── user plugins (custom commands) ────────────────────────────────────────
	// Checked before per-view dispatch but only for names that aren't built-ins,
	// so built-in commands always take precedence over a same-named plugin.
	if !m.cmdline.IsBuiltin(cmd.Name) {
		if p, ok := m.plugins.Lookup(m.pluginScope(), cmd.Name); ok {
			return m.pluginCmd(p), nil
		}
	}

	// ── hosts management (own command set) ────────────────────────────────────
	if m.resource == ViewHosts {
		return m.dispatchHostCommand(cmd)
	}

	// ── compose management (own command set) ──────────────────────────────────
	if m.resource == ViewCompose {
		return m.dispatchComposeCommand(cmd)
	}

	// ── image build/tag/push/history (own command set) ────────────────────────
	if m.resource == ViewImages {
		if ok, c, err := m.dispatchImageCommand(cmd); ok {
			return c, err
		}
	}

	// ── run wizard (containers; images view pre-fills via dispatchImageCommand) ─
	if cmd.Name == "run" {
		if m.resource != ViewContainers {
			return nil, fmt.Errorf("run is not available for %s", m.resource.String())
		}
		return func() tea.Msg { return openRunFormMsg{} }, nil
	}

	// ── create (networks / volumes) ───────────────────────────────────────────
	if cmd.Name == "create" {
		switch m.resource {
		case ViewNetworks:
			return func() tea.Msg { return openNetFormMsg{} }, nil
		case ViewVolumes:
			return func() tea.Msg { return openVolFormMsg{} }, nil
		default:
			return nil, fmt.Errorf("create is not available for %s", m.resource.String())
		}
	}

	// ── prune (images / volumes) ──────────────────────────────────────────────
	if cmd.Name == "prune" {
		switch m.resource {
		case ViewImages:
			return containerAction(func() error { _, err := m.backend.PruneImages(); return err }), nil
		case ViewVolumes:
			return containerAction(func() error { _, err := m.backend.PruneVolumes(); return err }), nil
		default:
			return nil, fmt.Errorf("prune is not available for %s", m.resource.String())
		}
	}

	// ── pull (images only) ────────────────────────────────────────────────────
	if cmd.Name == "pull" {
		if m.resource != ViewImages {
			return nil, fmt.Errorf("pull is only available for images")
		}
		// An explicit argument (pull nginx) wins; otherwise pull the selected
		// image directly. With neither, open a modal so the user can type the
		// image reference to pull.
		ref := strings.TrimSpace(strings.Join(cmd.Args, " "))
		if ref == "" {
			ref = m.selectedImageRef()
		}
		if ref == "" {
			return func() tea.Msg { return openPullFormMsg{} }, nil
		}
		return containerAction(func() error { return m.backend.PullImage(ref) }), nil
	}

	// ── resource remove (works for all views) ─────────────────────────────────
	if cmd.Name == "rm" {
		if m.selectedID() == "" {
			return nil, fmt.Errorf("nothing selected")
		}
		force := len(cmd.Args) > 0 && cmd.Args[0] == "-f"
		switch m.resource {
		case ViewImages:
			// Use tag reference so Docker can remove a single repo reference;
			// fall back to short ID for untagged images.
			ref := m.selectedImageRef()
			return containerAction(func() error { return m.backend.RemoveImage(ref, force) }), nil
		case ViewNetworks:
			id := m.selectedID()
			return containerAction(func() error { return m.backend.RemoveNetwork(id) }), nil
		case ViewVolumes:
			id := m.selectedID()
			return containerAction(func() error { return m.backend.RemoveVolume(id) }), nil
		default:
			ids := m.targetContainerIDs()
			if len(ids) == 0 {
				return nil, fmt.Errorf("no container selected")
			}
			return bulkAction(ids, func(id string) error { return m.backend.RemoveContainer(id, force) }), nil
		}
	}

	// ── container-only commands ───────────────────────────────────────────────
	if m.resource != ViewContainers {
		return nil, fmt.Errorf("command %q is only available for containers", cmd.Name)
	}

	ids := m.targetContainerIDs()
	if len(ids) == 0 {
		return nil, fmt.Errorf("no container selected")
	}
	id := ids[0]

	switch cmd.Name {
	case "start":
		return bulkAction(ids, m.backend.StartContainer), nil
	case "stop":
		return bulkAction(ids, m.backend.StopContainer), nil
	case "restart":
		return bulkAction(ids, m.backend.RestartContainer), nil
	case "kill":
		sig := ""
		if len(cmd.Args) > 0 {
			sig = cmd.Args[0]
		}
		return bulkAction(ids, func(id string) error { return m.backend.KillContainer(id, sig) }), nil
	case "logs":
		return openLogs(m.backend, id, parseLogOptions(cmd.Args)), nil
	case "exec":
		// exec targets a single container (the cursor), not a bulk selection.
		if st := m.containerState(id); st != "" && st != "running" {
			return nil, fmt.Errorf("exec: container is not running (state: %s)", st)
		}
		return execCmd(m.backend, id, m.containerName(id), cmd.Args), nil
	case "files", "browse":
		// Open the filesystem browser on the cursor container (optional path arg).
		dir := ""
		if len(cmd.Args) > 0 {
			dir = cmd.Args[0]
		}
		if st := m.containerState(id); st != "" && st != "running" {
			return nil, fmt.Errorf("files: container is not running (state: %s)", st)
		}
		return fsListCmd(m.backend, id, m.containerName(id), firstNonEmpty(dir, "/")), nil
	case "cp":
		// Upload a local file/dir into the cursor container:
		//   cp <local-path> <container-dir>
		if len(cmd.Args) < 2 {
			return nil, fmt.Errorf("usage: cp <local-path> <container-dir>")
		}
		local, dest := cmd.Args[0], cmd.Args[1]
		return containerAction(func() error { return m.backend.CopyToContainer(id, local, dest) }), nil
	default:
		return nil, fmt.Errorf("unknown command: %s", cmd.Name)
	}
}

// setAlertThreshold parses a percentage (or "off"/"0") from `alert <metric> <v>`
// and applies it to the named metric. The "%" suffix is tolerated.
func (m *Model) setAlertThreshold(metric string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: alert %s <percent> (or: alert %s off)", metric, metric)
	}
	raw := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(args[1]), "%"))
	var v float64
	if raw != "off" && raw != "none" {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f < 0 {
			return fmt.Errorf("неверный порог %q (пример: alert %s 80)", args[1], metric)
		}
		v = f
	}
	switch metric {
	case "cpu":
		m.alerts.CPU = v
	case "mem":
		m.alerts.Mem = v
	}
	return nil
}

// alertSummary renders the active thresholds for the footer/command help.
func alertSummary(t alerts.Thresholds) string {
	if !t.Active() {
		return "выключены"
	}
	parts := make([]string, 0, 2)
	if t.CPU > 0 {
		parts = append(parts, fmt.Sprintf("CPU≥%g%%", t.CPU))
	}
	if t.Mem > 0 {
		parts = append(parts, fmt.Sprintf("MEM≥%g%%", t.Mem))
	}
	return strings.Join(parts, " ")
}

// firstNonEmpty returns a if it is non-empty, otherwise b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// dispatchHostCommand handles the hosts view command set: connect/add/edit/rm.
func (m *Model) dispatchHostCommand(cmd *cmdline.CommandMsg) (tea.Cmd, error) {
	switch cmd.Name {
	case "connect":
		name := m.selectedID()
		if name == "" {
			return nil, fmt.Errorf("no host selected")
		}
		h, ok := m.hostStore.Find(name)
		if !ok {
			return nil, fmt.Errorf("host %q not found", name)
		}
		return connectCmd(m.cfg, h.Host), nil

	case "add":
		// Open the modal form pre-filled with base defaults.
		return func() tea.Msg { return openHostFormMsg{editing: false} }, nil

	case "edit":
		name := m.selectedID()
		if name == "" {
			return nil, fmt.Errorf("no host selected")
		}
		h, ok := m.hostStore.Find(name)
		if !ok {
			return nil, fmt.Errorf("host %q not found", name)
		}
		// Open the modal form pre-filled with the selected host.
		return func() tea.Msg { return openHostFormMsg{editing: true, name: h.Name, host: h.Host} }, nil

	case "rm":
		name := m.selectedID()
		if name == "" {
			return nil, fmt.Errorf("no host selected")
		}
		if err := m.hostStore.Remove(name); err != nil {
			return nil, err
		}
		return m.saveHostsThenRefresh()

	default:
		return nil, fmt.Errorf("unknown hosts command: %s", cmd.Name)
	}
}

// dispatchComposeCommand handles the compose view command set. Lifecycle ops run
// over the Docker API on the project's containers; compose-engine operations
// (up/down/pull/edit/config) need SSH and arrive in a later iteration.
func (m *Model) dispatchComposeCommand(cmd *cmdline.CommandMsg) (tea.Cmd, error) {
	// Compose ops that exec `docker compose` or touch the project's files on the
	// host need SSH; reject them up front on a tcp:// connection (rather than
	// opening a modal that would fail on submit). They're also hidden from
	// autocomplete/help, so this only fires when typed manually.
	if !m.composeHostOps && cmdline.IsComposeHostOp(cmd.Name) {
		return nil, fmt.Errorf("%s requires an SSH connection (use -H ssh://...)", cmd.Name)
	}
	// create authors a brand-new project; it needs a directory, not a selection.
	if cmd.Name == "create" {
		if len(cmd.Args) == 0 {
			return nil, fmt.Errorf("create: directory required (usage: create <dir>)")
		}
		dir := cmd.Args[0]
		return func() tea.Msg { return openComposeCreateMsg{dir: dir} }, nil
	}

	project := m.selectedID()
	if project == "" {
		return nil, fmt.Errorf("no compose project selected")
	}
	label := m.composeNameFor(project) // short name for op titles
	switch cmd.Name {
	case "start":
		return containerAction(func() error { return m.backend.ComposeStart(project) }), nil
	case "stop":
		return containerAction(func() error { return m.backend.ComposeStop(project) }), nil
	case "restart":
		return containerAction(func() error { return m.backend.ComposeRestart(project) }), nil
	case "pause":
		return containerAction(func() error { return m.backend.ComposePause(project) }), nil
	case "unpause":
		return containerAction(func() error { return m.backend.ComposeUnpause(project) }), nil
	case "remove", "rm":
		return containerAction(func() error { return m.backend.ComposeRemove(project) }), nil
	case "up":
		return streamOpCmd(func() (<-chan string, func(), error) { return m.backend.ComposeUp(project) }, "compose up: "+label), nil
	case "pull":
		return streamOpCmd(func() (<-chan string, func(), error) { return m.backend.ComposePull(project) }, "compose pull: "+label), nil
	case "down":
		return streamOpCmd(func() (<-chan string, func(), error) { return m.backend.ComposeDown(project) }, "compose down: "+label), nil
	case "config":
		return composeConfigCmd(m.backend, project), nil
	case "edit":
		return composeEditCmd(m.backend, project), nil
	case "backup":
		return backupComposeCmd(m.backend, project), nil
	case "backups":
		// Open the catalog of existing backups for this deployment.
		return listBackupsCmd(project, label), nil
	case "restore":
		// With no file, browse the catalog; with one, restore it directly.
		if len(cmd.Args) == 0 {
			return listBackupsCmd(project, label), nil
		}
		return restoreComposeCmd(m.backend, project, label, cmd.Args[0]), nil
	default:
		return nil, fmt.Errorf("unknown compose command: %s", cmd.Name)
	}
}

// dispatchImageCommand handles the image-specific command set (build/tag/push/
// history). The first return value reports whether the command was recognised
// here; when false the caller falls through to the shared pull/prune/rm logic.
func (m *Model) dispatchImageCommand(cmd *cmdline.CommandMsg) (bool, tea.Cmd, error) {
	switch cmd.Name {
	case "build":
		// build authors a new image from a local context dir; no selection needed.
		// With nothing supplied, open a modal so the user can type the context
		// directory and tag; the build itself still runs in the progress console.
		dir := ""
		tag := ""
		if len(cmd.Args) > 0 {
			dir = cmd.Args[0]
		}
		if len(cmd.Args) > 1 {
			tag = cmd.Args[1]
		}
		if dir == "" {
			return true, func() tea.Msg { return openBuildFormMsg{tag: tag} }, nil
		}
		return true, streamOpCmd(func() (<-chan string, func(), error) { return m.backend.BuildImage(dir, tag) }, "build: "+dir), nil

	case "tag":
		if len(cmd.Args) == 0 {
			return true, nil, fmt.Errorf("tag: new reference required (usage: tag <new-ref>)")
		}
		ref := m.selectedImageRef()
		if ref == "" {
			return true, nil, fmt.Errorf("no image selected")
		}
		target := cmd.Args[0]
		return true, containerAction(func() error { return m.backend.TagImage(ref, target) }), nil

	case "push":
		ref := m.selectedImageRef()
		if ref == "" {
			return true, nil, fmt.Errorf("no image selected")
		}
		// Open the credentials modal; the actual push runs on submit.
		return true, func() tea.Msg { return openPushFormMsg{ref: ref} }, nil

	case "history":
		ref := m.selectedImageRef()
		if ref == "" {
			return true, nil, fmt.Errorf("no image selected")
		}
		return true, imageHistoryCmd(m.backend, ref), nil

	case "run":
		// Open the run wizard pre-filled with the selected image (if any).
		ref := m.selectedImageRef()
		return true, func() tea.Msg { return openRunFormMsg{image: ref} }, nil

	case "exec":
		// Open the one-off run wizard pre-filled with the selected image.
		ref := m.selectedImageRef()
		return true, func() tea.Msg { return openExecFormMsg{image: ref} }, nil
	}
	return false, nil, nil
}

// saveHostsThenRefresh persists the host store and returns a refresh command.
func (m *Model) saveHostsThenRefresh() (tea.Cmd, error) {
	if err := m.hostStore.Save(); err != nil {
		return nil, err
	}
	return fetchHosts(m.hostStore), nil
}

// imageRefFromTags picks the first real (non-<none>) tag from a comma-separated
// tag list, falling back to the image ID. Dangling images report their tags as
// "<none>:<none>", which Docker rejects as a reference ("invalid reference
// format"), so those must be skipped in favour of the ID.
func imageRefFromTags(tags, id string) string {
	for t := range strings.SplitSeq(tags, ", ") {
		t = strings.TrimSpace(t)
		if t != "" && !strings.Contains(t, "<none>") {
			return t
		}
	}
	return id
}

// parseLogOptions reads the flags of a `:logs` command: --tail N, --since X,
// --until X. Defaults to the last 100 lines and no time bounds.
func parseLogOptions(args []string) docker.LogOptions {
	opts := docker.LogOptions{Tail: 100}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tail":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					opts.Tail = n
				}
				i++
			}
		case "--since":
			if i+1 < len(args) {
				opts.Since = args[i+1]
				i++
			}
		case "--until":
			if i+1 < len(args) {
				opts.Until = args[i+1]
				i++
			}
		}
	}
	return opts
}

// targetContainerIDs returns the IDs a container command applies to: the bulk
// selection if any, otherwise the single container under the cursor.
func (m Model) targetContainerIDs() []string {
	if len(m.selected) > 0 {
		ids := make([]string, 0, len(m.selected))
		for id := range m.selected {
			ids = append(ids, id)
		}
		return ids
	}
	if id := m.selectedID(); id != "" {
		return []string{id}
	}
	return nil
}

// bulkAction applies fn to every id, aggregating the outcome into a single
// actionResultMsg. On any failure it reports how many of how many failed,
// wrapping the first error.
func bulkAction(ids []string, fn func(id string) error) tea.Cmd {
	return func() tea.Msg {
		var failed int
		var firstErr error
		for _, id := range ids {
			if err := fn(id); err != nil {
				failed++
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		if failed > 0 {
			return actionResultMsg{fmt.Errorf("%d of %d failed: %w", failed, len(ids), firstErr)}
		}
		return actionResultMsg{}
	}
}

// selectedImageRef returns the pull reference (tag or ID) for the selected image.
func (m Model) selectedImageRef() string {
	id := m.selectedID()
	if id == "" {
		return ""
	}
	for _, img := range m.images {
		if img.ID == id {
			return imageRefFromTags(img.Tags, img.ID)
		}
	}
	return id
}
