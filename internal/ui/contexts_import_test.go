package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/dockerctx"
	"github.com/kirg0/d9c/internal/hosts"
	"github.com/kirg0/d9c/internal/ui/cmdline"
)

// contextStore writes a Docker CLI context store with a TLS tcp context
// ("prod") and a plain ssh one ("lab", the current context) into a temp dir.
// lab's URL matches the hostsModel fixture (already saved); prod's does not,
// and its name clashes with the saved tcp://prod:2375.
func contextStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, meta string, tls bool) {
		metaDir := filepath.Join(dir, "contexts", "meta", dockerctx.ID(name))
		if err := os.MkdirAll(metaDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
		if !tls {
			return
		}
		tlsDir := filepath.Join(dir, "contexts", "tls", dockerctx.ID(name), "docker")
		if err := os.MkdirAll(tlsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"ca.pem", "cert.pem", "key.pem"} {
			if err := os.WriteFile(filepath.Join(tlsDir, f), []byte("pem"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("prod", `{"Name":"prod","Endpoints":{"docker":{"Host":"tcp://prod:2376","SkipTLSVerify":true}}}`, true)
	write("lab", `{"Name":"lab","Endpoints":{"docker":{"Host":"ssh://me@lab"}}}`, false)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"lab"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// contextsModel is hostsModel pointed at a fixture context store.
func contextsModel(t *testing.T, persistErr error) (*modelHarness, *hosts.Store, string) {
	t.Helper()
	h, store := hostsModel(t, persistErr)
	dir := contextStore(t)
	m := h.cur()
	m.dockerCfgDir = dir
	h.set(m)
	return h, store, dir
}

// runImport dispatches `:import <args>` and feeds the loaded contexts back.
func runImport(t *testing.T, h *modelHarness, args ...string) {
	t.Helper()
	m := h.cur()
	cmd, err := m.dispatchHostCommand(&cmdline.CommandMsg{Name: "import", Args: args})
	if err != nil || cmd == nil {
		t.Fatalf("import %v: cmd=%v err=%v", args, cmd, err)
	}
	h.step(cmd())
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestHostTLSAndBaseline(t *testing.T) {
	base := tlsFiles{"fca", "fcert", "fkey"}
	withTLS := hosts.Host{Host: "tcp://p:2376", TLSCACert: "ca"}
	if got := hostTLS(withTLS, base); got != (tlsFiles{ca: "ca"}) {
		t.Errorf("hostTLS(with TLS) = %+v", got)
	}
	if got := hostTLS(hosts.Host{Host: "tcp://p:2375"}, base); got != base {
		t.Errorf("hostTLS(plain) = %+v, want baseline", got)
	}

	store := hosts.NewStore([]hosts.Host{{Name: "p", Host: "tcp://p:2376", TLSCACert: "ca", TLSCert: "c", TLSKey: "k"}}, nil)
	tests := []struct {
		name string
		cfg  config.Config
		want tlsFiles
	}{
		{"flags", config.Config{Host: "tcp://x", TLSCACert: "fca", TLSCert: "fc", TLSKey: "fk"}, tlsFiles{"fca", "fc", "fk"}},
		{"none", config.Config{Host: "tcp://x"}, tlsFiles{}},
		{"from context", config.Config{Host: "tcp://x", Context: "x", TLSCACert: "ca"}, tlsFiles{}},
		{"from saved host", config.Config{Host: "tcp://p:2376", TLSCACert: "ca", TLSCert: "c", TLSKey: "k"}, tlsFiles{}},
		{"flags differ from saved host", config.Config{Host: "tcp://p:2376", TLSCACert: "other"}, tlsFiles{ca: "other"}},
	}
	for _, tt := range tests {
		cfg := tt.cfg
		if got := baselineTLS(&cfg, store); got != tt.want {
			t.Errorf("%s: baselineTLS = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

// Connecting to a host with its own TLS uses it; a later plain host falls
// back to the session baseline rather than inheriting the previous host's TLS.
func TestBeginConnectAppliesHostTLS(t *testing.T) {
	cfg := &config.Config{TLSCACert: "flag-ca"}
	m := NewModel(cfg, docker.NewFakeBackend(), &hosts.Store{}, nil, false)
	nm, _ := m.beginConnect(hosts.Host{Name: "p", Host: "tcp://p:2376", TLSCACert: "ca", TLSCert: "c", TLSKey: "k"})
	if cfg.TLSCACert != "ca" || cfg.TLSCert != "c" || cfg.TLSKey != "k" {
		t.Errorf("host TLS not applied: %+v", cfg)
	}
	_, _ = nm.(Model).beginConnect(hosts.Host{Name: "q", Host: "tcp://q:2375"})
	if cfg.TLSCACert != "flag-ca" || cfg.TLSCert != "" || cfg.TLSKey != "" {
		t.Errorf("baseline not restored: %+v", cfg)
	}
}

func TestImportSummary(t *testing.T) {
	tests := []struct {
		imported, skipped, insecure int
		want                        []string
		notWant                     []string
	}{
		{2, 0, 0, []string{"imported contexts: 2"}, []string{"already", "SkipTLSVerify"}},
		{1, 3, 0, []string{"imported contexts: 1", "already saved: 3"}, []string{"SkipTLSVerify"}},
		{1, 0, 1, []string{"SkipTLSVerify unsupported", ": 1)"}, []string{"already"}},
	}
	for _, tt := range tests {
		got := importSummary(tt.imported, tt.skipped, tt.insecure)
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("importSummary%v = %q, missing %q", []int{tt.imported, tt.skipped, tt.insecure}, got, w)
			}
		}
		for _, w := range tt.notWant {
			if strings.Contains(got, w) {
				t.Errorf("importSummary%v = %q, must not contain %q", []int{tt.imported, tt.skipped, tt.insecure}, got, w)
			}
		}
	}
}

func TestDispatchImportUsage(t *testing.T) {
	h, _ := hostsModel(t, nil)
	m := h.cur()
	for _, args := range [][]string{nil, {"hosts"}} {
		if _, err := m.dispatchHostCommand(&cmdline.CommandMsg{Name: "import", Args: args}); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("import %v err = %v, want usage", args, err)
		}
	}
}

func TestImportContextsPicker(t *testing.T) {
	h, store, _ := contextsModel(t, nil)
	runImport(t, h, "contexts")

	m := h.cur()
	if m.mode != ModeContextPicker || len(m.ctxItems) != 2 {
		t.Fatalf("mode=%v items=%+v, want picker over 2 contexts", m.mode, m.ctxItems)
	}
	// Sorted by name: lab, prod — the cursor starts on the current one (lab).
	if m.ctxItems[m.ctxCursor].Name != "lab" {
		t.Errorf("cursor on %q, want current context lab", m.ctxItems[m.ctxCursor].Name)
	}
	view := m.viewContextOverlay()
	for _, want := range []string{"Import Docker contexts", "[ ] lab", "ssh://me@lab", "current, saved", "prod", "tcp://prod:2376  (TLS)"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker view missing %q:\n%s", want, view)
		}
	}

	// Movement is clamped; space toggles; `a` flips all on, then all off.
	h.step(key("up"))
	h.step(key("down"))
	h.step(key("down"))
	h.step(key("down"))
	if h.cur().ctxCursor != 1 {
		t.Fatalf("cursor = %d, want clamped to 1", h.cur().ctxCursor)
	}
	h.step(key(" "))
	if c := h.cur().ctxChecked; c[0] || !c[1] {
		t.Fatalf("checked = %v after space", c)
	}
	h.step(key("a"))
	if c := h.cur().ctxChecked; !c[0] || !c[1] {
		t.Fatalf("checked = %v after a", c)
	}
	h.step(key("a"))
	if c := h.cur().ctxChecked; c[0] || c[1] {
		t.Fatalf("checked = %v after second a", c)
	}
	h.step(key(" ")) // check prod only
	h.step(key("enter"))

	m = h.cur()
	if m.mode != ModeNormal || m.ctxItems != nil {
		t.Errorf("picker not closed: mode=%v", m.mode)
	}
	got, ok := store.Find("prod-2") // "prod" is taken by tcp://prod:2375
	if !ok || got.Host != "tcp://prod:2376" || !got.HasTLS() {
		t.Errorf("prod not imported with TLS: %+v ok=%v", got, ok)
	}
	if len(store.Hosts) != 3 {
		t.Errorf("hosts = %+v, want lab/prod pre-existing + imported prod", store.Hosts)
	}
	if !strings.Contains(m.copyNotif, "imported contexts: 1") || !strings.Contains(m.copyNotif, "SkipTLSVerify") {
		t.Errorf("notice = %q", m.copyNotif)
	}
}

// Enter with nothing checked imports the highlighted context; lab's URL is
// already saved, so it is reported as skipped and nothing is persisted.
func TestImportContextsPickerHighlighted(t *testing.T) {
	saves := 0
	h, store, _ := contextsModel(t, nil)
	m := h.cur()
	counting := hosts.NewStore(store.List(), func([]hosts.Host) error { saves++; return nil })
	m.hostStore = counting
	h.set(m)

	runImport(t, h, "contexts")
	h.step(key("enter"))
	if saves != 0 || len(counting.Hosts) != 2 {
		t.Errorf("saves=%d hosts=%+v, want no change", saves, counting.Hosts)
	}
	if n := h.cur().copyNotif; !strings.Contains(n, "imported contexts: 0") || !strings.Contains(n, "already saved: 1") {
		t.Errorf("notice = %q", n)
	}

	runImport(t, h, "contexts")
	h.step(key("q"))
	if h.cur().mode != ModeNormal {
		t.Error("q must close the picker")
	}
}

func TestImportContextsByName(t *testing.T) {
	h, store, _ := contextsModel(t, nil)
	runImport(t, h, "contexts", "prod")
	if h.cur().mode != ModeNormal {
		t.Error("named import must not open the picker")
	}
	if got, ok := store.Find("prod-2"); !ok || !got.HasTLS() || got.Host != "tcp://prod:2376" {
		t.Errorf("prod not imported under a suffixed name: %+v ok=%v", got, ok)
	}

	runImport(t, h, "CONTEXTS", "ghost", "lab", "nope")
	if e := h.cur().err; !strings.Contains(e, "ghost, nope") {
		t.Errorf("err = %q, want missing names", e)
	}
}

func TestImportContextsSaveError(t *testing.T) {
	h, _, _ := contextsModel(t, errors.New("disk full"))
	runImport(t, h, "contexts", "prod")
	if e := h.cur().err; e != "disk full" {
		t.Errorf("err = %q, want the save error", e)
	}
}

func TestImportContextsEmptyAndError(t *testing.T) {
	h, _ := hostsModel(t, nil)
	m := h.cur()
	m.dockerCfgDir = t.TempDir()
	h.set(m)
	runImport(t, h, "contexts")
	if n := h.cur().copyNotif; !strings.Contains(n, "no docker contexts found") || h.cur().mode == ModeContextPicker {
		t.Errorf("empty store: notice=%q mode=%v", n, h.cur().mode)
	}

	h.step(contextsLoadedMsg{err: errors.New("boom")})
	if h.cur().err != "boom" {
		t.Errorf("err = %q", h.cur().err)
	}
}

// TestDemo_ImportContexts drives the picker end to end: `:import contexts`
// opens it over the hosts view, space checks prod, Enter imports it.
func TestDemo_ImportContexts(t *testing.T) {
	store := &hosts.Store{}
	m := NewModel(&config.Config{Demo: true}, docker.NewFakeBackend(), store, nil, false)
	m.dockerCfgDir = contextStore(t)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	waitFor(t, tm, "web")

	tm.Type(":hosts")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Hosts")

	tm.Type(":import contexts")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Import Docker contexts", "lab", "prod")

	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(key(" "))
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "imported contexts: 1", "tcp://prod:2376")

	tm.Quit()
	if h, ok := store.Find("prod"); !ok || !h.HasTLS() {
		t.Errorf("prod not persisted with TLS: %+v ok=%v", h, ok)
	}
}
