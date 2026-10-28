package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
)

// invokeWrite runs a built-in op on b under the destructive ceiling.
func invokeWrite(b docker.Backend, name string, args Args) (Result, error) {
	return Builtin().Invoke(context.Background(), &Env{Backend: b}, name, args, Call{Ceiling: TierDestructive, Confirmed: true})
}

func TestResolveContainer(t *testing.T) {
	list := []docker.Container{
		{ID: "9ae942fd8fbc", Name: "web"},
		{ID: "9a0000000001", Name: "worker"},
		{ID: "3f1ab77c9012", Name: "db"},
	}
	tests := []struct {
		name, ref string
		wantName  string
		wantCode  Code
	}{
		{"exact name", "db", "db", ""},
		{"slash name", "/web", "web", ""},
		{"exact id", "3f1ab77c9012", "db", ""},
		{"unique prefix", "9ae", "web", ""},
		{"full id", "3f1ab77c9012aaaabbbbccccddddeeeeffff0000111122223333444455556666", "db", ""},
		{"ambiguous prefix", "9a", "", CodeUsage},
		{"unknown", "ghost", "", CodeNotFound},
		{"empty", "", "", CodeUsage},
		{"name beats prefix", "worker", "worker", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := resolveContainer(list, tt.ref)
			if tt.wantCode != "" {
				if err == nil || err.Code != tt.wantCode {
					t.Fatalf("err = %v, want code %s", err, tt.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.Name != tt.wantName {
				t.Errorf("resolved %q, want %q", c.Name, tt.wantName)
			}
		})
	}
}

func TestParseContainerFilter(t *testing.T) {
	list := docker.NewFakeBackend().Containers // web (running), api (running), db (exited)
	tests := []struct {
		name    string
		filters []string
		want    []string
		wantErr string
	}{
		{"none", nil, []string{"web", "api", "db"}, ""},
		{"status", []string{"status=running"}, []string{"web", "api"}, ""},
		{"status case", []string{"STATUS=Exited"}, []string{"db"}, ""},
		{"name exact", []string{"name=web"}, []string{"web"}, ""},
		{"name is not substring", []string{"name=we"}, nil, ""},
		{"name glob", []string{"name=*b"}, []string{"web", "db"}, ""},
		{"image glob", []string{"image=nginx:*"}, []string{"web"}, ""},
		{"label key", []string{"label=tier"}, []string{"web", "api", "db"}, ""},
		{"label value", []string{"label=env=prod"}, []string{"web", "api"}, ""},
		{"label missing", []string{"label=owner"}, nil, ""},
		{"network", []string{"network=backend"}, []string{"api", "db"}, ""},
		{"network exact", []string{"network=back"}, nil, ""},
		{"health", []string{"health=starting"}, []string{"api"}, ""},
		{"health none", []string{"health=none"}, []string{"db"}, ""},
		{"and", []string{"label=env=prod", "network=backend"}, []string{"api"}, ""},
		{"no equals", []string{"running"}, nil, "want key=value"},
		{"empty value", []string{"status="}, nil, "want key=value"},
		{"unknown key", []string{"color=red"}, nil, `unknown key "color"`},
		{"bad glob", []string{"name=["}, nil, "invalid glob"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, err := parseContainerFilter(tt.filters)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if Classify(err).Code != CodeUsage {
					t.Errorf("code = %s, want usage", Classify(err).Code)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got []string
			for _, c := range list {
				if match == nil || match(c) {
					got = append(got, c.Name)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("matched %v, want %v", got, tt.want)
			}
		})
	}
}

// Refs come first in the order given, filter matches follow, and a container
// named twice is acted on once.
func TestResolveTargets(t *testing.T) {
	list := docker.NewFakeBackend().Containers
	match, err := parseContainerFilter([]string{"status=running"})
	if err != nil {
		t.Fatal(err)
	}
	got := resolveTargets(list, []string{"db", "ghost", "web", "9ae942fd8fbc"}, match)
	var summary []string
	for _, tg := range got {
		summary = append(summary, tg.Ref+"|"+tg.Name+"|"+tg.Result+"|"+string(tg.Code))
	}
	want := []string{"db|db||", "ghost||failed|not_found", "web|web||", "|api||"}
	if !reflect.DeepEqual(summary, want) {
		t.Errorf("targets = %v, want %v", summary, want)
	}
}

// killSpy records the signal each kill was sent with.
type killSpy struct {
	*docker.FakeBackend
	signals []string
}

func (k *killSpy) KillContainer(id, signal string) error {
	k.signals = append(k.signals, signal)
	return k.FakeBackend.KillContainer(id, signal)
}

func TestLifecycleOps(t *testing.T) {
	tests := []struct {
		name      string
		op        string
		args      Args
		wantState map[string]string // container name → state after the call; "" = removed
		wantDone  int
		wantFail  int
		partial   bool
	}{
		{"stop two", "stop", Args{"targets": []string{"web", "api"}},
			map[string]string{"web": "exited", "api": "exited", "db": "exited"}, 2, 0, false},
		{"start", "start", Args{"targets": []string{"db"}},
			map[string]string{"db": "running"}, 1, 0, false},
		{"restart by prefix", "restart", Args{"targets": []string{"3f1"}},
			map[string]string{"db": "running"}, 1, 0, false},
		{"stop by filter", "stop", Args{"filter": []string{"label=env=prod"}},
			map[string]string{"web": "exited", "api": "exited"}, 2, 0, false},
		{"kill", "kill", Args{"targets": []string{"web"}},
			map[string]string{"web": "exited", "api": "running"}, 1, 0, false},
		{"rm stopped", "rm", Args{"targets": []string{"db"}},
			map[string]string{"db": ""}, 1, 0, false},
		{"rm force", "rm", Args{"targets": []string{"web"}, "force": true},
			map[string]string{"web": ""}, 1, 0, false},
		{"partial unknown", "stop", Args{"targets": []string{"web", "ghost"}},
			map[string]string{"web": "exited"}, 1, 1, true},
		{"partial running rm", "rm", Args{"filter": []string{"network=backend"}},
			map[string]string{"api": "running", "db": ""}, 1, 1, true},
		{"filter matches nothing", "stop", Args{"filter": []string{"name=nope"}},
			map[string]string{"web": "running"}, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fb := docker.NewFakeBackend()
			res, err := invokeWrite(fb, tt.op, tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			data := res.Data.(ActionData)
			if data.Action != tt.op || data.Done != tt.wantDone || data.Failed != tt.wantFail || res.Partial != tt.partial {
				t.Errorf("got action=%s done=%d failed=%d partial=%v, want %s/%d/%d/%v",
					data.Action, data.Done, data.Failed, res.Partial, tt.op, tt.wantDone, tt.wantFail, tt.partial)
			}
			if tt.partial && !strings.Contains(res.Hint, "failed") {
				t.Errorf("partial hint = %q", res.Hint)
			}
			if len(data.Targets) == 0 && !strings.Contains(res.Hint, "no containers matched") {
				t.Errorf("empty result hint = %q", res.Hint)
			}
			state := map[string]string{}
			for _, c := range fb.Containers {
				state[c.Name] = c.State
			}
			for name, want := range tt.wantState {
				if got := state[name]; got != want {
					t.Errorf("%s state = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestLifecycleErrors(t *testing.T) {
	tests := []struct {
		name     string
		op       string
		args     Args
		wantCode Code
		wantMsg  string
	}{
		{"no targets", "stop", nil, CodeUsage, "no containers given"},
		{"bad filter", "stop", Args{"filter": []string{"bogus"}}, CodeUsage, "want key=value"},
		{"single unknown", "start", Args{"targets": []string{"ghost"}}, CodeNotFound, "no such container: ghost"},
		{"all unknown", "rm", Args{"targets": []string{"ghost", "phantom"}}, CodeNotFound, "rm failed on all 2 containers: ghost: no such container: ghost; phantom:"},
		{"running without force", "rm", Args{"targets": []string{"web"}}, CodeInternal, "running"},
		{"empty ref", "stop", Args{"targets": []string{""}}, CodeUsage, "empty container reference"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := invokeWrite(docker.NewFakeBackend(), tt.op, tt.args)
			e := Classify(err)
			if e == nil || e.Code != tt.wantCode || !strings.Contains(e.Message, tt.wantMsg) {
				t.Fatalf("err = %v, want %s containing %q", err, tt.wantCode, tt.wantMsg)
			}
		})
	}
}

// The tier ceiling gates the write commands: safe ones need safe, rm and kill
// need destructive.
func TestLifecycleTiers(t *testing.T) {
	r := Builtin()
	args := Args{"targets": []string{"web"}}
	for _, tt := range []struct {
		op      string
		ceiling Tier
		allowed bool
	}{
		{"stop", TierRead, false},
		{"stop", TierSafe, true},
		{"rm", TierSafe, false},
		{"kill", TierSafe, false},
		{"kill", TierDestructive, true},
	} {
		_, err := r.Invoke(context.Background(), &Env{Backend: docker.NewFakeBackend()}, tt.op, args, Call{Ceiling: tt.ceiling})
		forbidden := Classify(err) != nil && Classify(err).Code == CodeForbidden
		if forbidden == tt.allowed {
			t.Errorf("%s under %s: err = %v, allowed want %v", tt.op, tt.ceiling, err, tt.allowed)
		}
	}
}

func TestKillSignal(t *testing.T) {
	spy := &killSpy{FakeBackend: docker.NewFakeBackend()}
	if _, err := invokeWrite(spy, "kill", Args{"targets": []string{"web"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeWrite(spy, "kill", Args{"targets": []string{"api"}, "signal": "HUP"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"KILL", "HUP"}; !reflect.DeepEqual(spy.signals, want) {
		t.Errorf("signals = %v, want %v", spy.signals, want)
	}
}

// A cancelled context leaves the remaining targets unattempted.
func TestLifecycleCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fb := docker.NewFakeBackend()
	_, err := Builtin().Invoke(ctx, &Env{Backend: fb}, "stop", Args{"targets": []string{"web"}}, Call{Ceiling: TierDestructive, Confirmed: true})
	if err == nil || !strings.Contains(err.Error(), "not attempted") {
		t.Fatalf("err = %v, want not attempted", err)
	}
	if fb.Containers[0].State != "running" {
		t.Errorf("web was stopped despite the cancelled context")
	}
}
