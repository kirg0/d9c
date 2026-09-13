package ui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
	"github.com/kirg0/d9c/internal/ui/cpform"
)

// formStepper builds a sized model and returns a step function (feeding one
// message through Update and returning its command) plus an accessor.
func formStepper(t *testing.T, store *hosts.Store) (step func(tea.Msg) tea.Cmd, cur func() Model) {
	t.Helper()
	var tm tea.Model = NewModel(&config.Config{}, docker.NewFakeBackend(), store, nil, false)
	step = func(msg tea.Msg) tea.Cmd {
		var c tea.Cmd
		tm, c = tm.Update(msg)
		return c
	}
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	return step, func() Model { return tm.(Model) }
}

var (
	formEnter = tea.KeyMsg{Type: tea.KeyEnter}
	formNav   = []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyDown}, {Type: tea.KeyShiftTab}, {Type: tea.KeyUp}}
)

func formRunes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// Every modal form keeps focus inside itself: Enter with the required field
// blank shows an error instead of acting, field navigation and typing never
// close the modal.
func TestFormKeysKeepModalOpen(t *testing.T) {
	tests := []struct {
		name          string
		open          tea.Msg
		mode          Mode
		enterRequired bool // Enter on a blank form must be rejected
	}{
		{"push", openPushFormMsg{ref: "myreg:5000/app:1"}, ModePushForm, false},
		{"network", openNetFormMsg{}, ModeNetForm, true},
		{"volume", openVolFormMsg{}, ModeVolForm, true},
		{"build", openBuildFormMsg{}, ModeBuildForm, true},
		{"run", openRunFormMsg{}, ModeRunForm, true},
		{"exec", openExecFormMsg{}, ModeExecForm, true},
		{"host", openHostFormMsg{}, ModeHostForm, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, cur := formStepper(t, &hosts.Store{})
			step(tt.open)
			if cur().mode != tt.mode {
				t.Fatalf("mode = %v, want %v", cur().mode, tt.mode)
			}
			if tt.enterRequired {
				if cmd := step(formEnter); cmd != nil {
					t.Errorf("Enter on a blank form returned a command")
				}
				if cur().mode != tt.mode {
					t.Fatalf("blank submit closed the form: mode = %v", cur().mode)
				}
			}
			for _, k := range formNav {
				step(k)
				if cur().mode != tt.mode {
					t.Fatalf("%q closed the form: mode = %v", k.String(), cur().mode)
				}
			}
			step(formRunes("x"))
			if cur().mode != tt.mode {
				t.Errorf("typing closed the form: mode = %v", cur().mode)
			}
		})
	}
}

// The SSH auth selector toggles with space/arrows, and saving an edited host
// closes the form.
func TestHostFormAuthToggleAndSave(t *testing.T) {
	lab := hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthKey}
	step, cur := formStepper(t, &hosts.Store{Hosts: []hosts.Host{lab}})
	step(openHostFormMsg{editing: true, host: lab})
	if cur().mode != ModeHostForm {
		t.Fatalf("mode = %v, want ModeHostForm", cur().mode)
	}
	for i := 0; i < 10 && !cur().hostForm.OnAuthField(); i++ {
		step(tea.KeyMsg{Type: tea.KeyTab})
	}
	if !cur().hostForm.OnAuthField() {
		t.Fatal("the SSH auth selector is not reachable with Tab")
	}
	before := cur().hostForm.Result().SSHAuth
	step(tea.KeyMsg{Type: tea.KeySpace})
	if cur().hostForm.Result().SSHAuth == before {
		t.Error("space should toggle the SSH auth method")
	}
	step(tea.KeyMsg{Type: tea.KeyLeft})
	if cur().hostForm.Result().SSHAuth != before {
		t.Error("left should toggle the SSH auth method back")
	}

	if cmd := step(formEnter); cmd == nil {
		t.Error("saving a host should refresh the hosts list")
	}
	if cur().mode != ModeNormal {
		t.Errorf("mode after save = %v, want ModeNormal", cur().mode)
	}
}

// While a connect is in flight the credential prompt and the progress window
// swallow keys; after a failure Enter (and only Enter) retries.
func TestConnectModalsKeyHandling(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	m.mode = ModeConnectAuth
	m.connForm.Open("prod", "ssh://deploy@prod", "deploy")
	for _, k := range formNav {
		model, _ := m.handleConnectAuth(k)
		m = model.(Model)
		if m.mode != ModeConnectAuth {
			t.Fatalf("%q closed the prompt", k.String())
		}
	}
	_ = m.connForm.Connecting()
	if _, cmd := m.handleConnectAuth(formEnter); cmd != nil {
		t.Error("a busy prompt must swallow Enter (no duplicate dial)")
	}

	model, _ := m.beginConnect(hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthKey, SSHKeyPath: "/keys/id"})
	m = model.(Model)
	if _, cmd := m.handleConnecting(formEnter); cmd != nil {
		t.Error("the progress window must swallow Enter while dialing")
	}
	model, _ = m.Update(connectResultMsg{err: errors.New("dial tcp: connection refused"), host: "ssh://me@lab"})
	m = model.(Model)
	if _, cmd := m.handleConnecting(formRunes("x")); cmd != nil {
		t.Error("only Enter retries a failed connect")
	}
	if _, cmd := m.handleConnecting(formEnter); cmd == nil {
		t.Error("Enter should retry after a failure")
	}
}

// The upload wizard: l/Backspace browse the local tree, Tab moves to the
// destination, and a blank destination is rejected without uploading.
func TestCpFormBrowserAndDestination(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	step, cur := formStepper(t, nil)
	step(openCpFormMsg{containerID: "abc123", name: "web"})
	step(cpListedMsg{dir: tmp, entries: []cpform.Entry{{Name: "sub", IsDir: true}, {Name: "a.txt"}}})

	if cmd := step(formRunes("l")); cmd == nil {
		t.Error("l on a directory should list it")
	}
	if cmd := step(tea.KeyMsg{Type: tea.KeyBackspace}); cmd == nil {
		t.Error("backspace should list the parent directory")
	}
	step(tea.KeyMsg{Type: tea.KeyDown}) // cursor onto a.txt

	step(tea.KeyMsg{Type: tea.KeyShiftTab}) // focus the destination field
	for range len(cur().cpForm.Dest()) {
		step(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if cmd := step(formEnter); cmd != nil {
		t.Error("a blank destination must not start an upload")
	}
	if cur().mode != ModeCpForm {
		t.Fatalf("mode = %v, want ModeCpForm (stays open)", cur().mode)
	}
	step(formRunes("/"))
	if got := cur().cpForm.Dest(); got != "/" {
		t.Errorf("destination = %q, want typed /", got)
	}
}
