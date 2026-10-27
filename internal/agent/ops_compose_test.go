package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
)

func TestResolveCompose(t *testing.T) {
	fb := docker.NewFakeBackend()
	fb.Composes = append(fb.Composes,
		docker.ComposeProject{Project: "shop", Name: "shop", WorkingDir: "/srv/shop-a"},
		docker.ComposeProject{Project: "shop", Name: "shop", WorkingDir: "/srv/shop-b"},
		docker.ComposeProject{Project: "bare", Name: "bare"}, // no working_dir label
	)
	tests := []struct {
		ref      string
		wantID   string
		wantCode Code
		wantHint string
	}{
		{"webapp", "/srv/webapp", "", ""},
		{"/srv/monitoring", "/srv/monitoring", "", ""},
		{"/srv/shop-b", "/srv/shop-b", "", ""},
		{"bare", "bare", "", ""},
		{"shop", "", CodeUsage, "/srv/shop-a, /srv/shop-b"},
		{"nope", "", CodeNotFound, "compose ls"},
	}
	for _, tt := range tests {
		p, err := resolveCompose(fb, tt.ref)
		if tt.wantCode != "" {
			e := Classify(err)
			if e.Code != tt.wantCode || !strings.Contains(e.Hint, tt.wantHint) {
				t.Errorf("resolve %q: err = %+v, want %s with hint %q", tt.ref, e, tt.wantCode, tt.wantHint)
			}
			continue
		}
		if err != nil || p.Identity() != tt.wantID {
			t.Errorf("resolve %q = %q (%v), want %q", tt.ref, p.Identity(), err, tt.wantID)
		}
	}
}

func TestComposeOps(t *testing.T) {
	fb := docker.NewFakeBackend()

	ls := invoke(t, fb, "compose ls", nil).Data.([]ComposeEntry)
	want := ComposeEntry{Project: "webapp", Name: "webapp", WorkingDir: "/srv/webapp",
		ConfigFiles: "/srv/webapp/docker-compose.yml", Status: "running", Running: 3, Total: 3}
	if len(ls) != 3 || ls[0] != want {
		t.Errorf("compose ls = %+v", ls)
	}

	ps := invoke(t, fb, "compose ps", Args{"project": "webapp"}).Data.([]ContainerEntry)
	if len(ps) != len(fb.Containers) {
		t.Errorf("compose ps = %d containers, want %d", len(ps), len(fb.Containers))
	}
	if e := invokeErr(t, fb, "compose ps", Args{"project": "ghost"}); e.Code != CodeNotFound {
		t.Errorf("compose ps ghost: code = %s", e.Code)
	}

	cfg := invoke(t, fb, "compose config", Args{"project": "webapp"}).Data.(ComposeConfigData)
	if cfg.Project != "webapp" || !strings.Contains(cfg.Config, "nginx:1.25") {
		t.Errorf("compose config = %+v", cfg)
	}
	fb.NoHostCompose = true
	if e := invokeErr(t, fb, "compose config", Args{"project": "webapp"}); e.Code != CodeUsage || !strings.Contains(e.Hint, "SSH") {
		t.Errorf("compose config over tcp: %+v, want usage with an SSH hint", e)
	}

	logs := invoke(t, docker.NewFakeBackend(), "compose logs", Args{"project": "webapp", "tail": 2}).Data.(LogsData)
	if logs.Target != "webapp" || len(logs.Lines) != 2 || !strings.HasPrefix(logs.Lines[0], "web | ") {
		t.Errorf("compose logs = %+v", logs)
	}
}

// Compose logs come back merged by time, whatever order the per-container
// streams delivered them in.
func TestComposeLogsMerged(t *testing.T) {
	b := &linesBackend{FakeBackend: docker.NewFakeBackend(), lines: []string{
		"db | 2026-10-27T10:00:02Z ready",
		"web | 2026-10-27T10:00:01Z start",
		"db | 2026-10-27T10:00:00Z boot",
		"web | 2026-10-27T10:00:03.5Z GET / 500",
	}}
	res := invoke(t, b, "compose logs", Args{"project": "webapp", "grep": "boot|ready|start"})
	want := []string{"db | 2026-10-27T10:00:00Z boot", "web | 2026-10-27T10:00:01Z start", "db | 2026-10-27T10:00:02Z ready"}
	if got := res.Data.(LogsData).Lines; !reflect.DeepEqual(got, want) {
		t.Errorf("merged = %q, want %q", got, want)
	}
	// --tail is per container in compose: a full tail is not flagged.
	if res.Truncated {
		t.Error("compose logs flagged truncated below the cap")
	}
	if len(b.opts) != 1 || !b.opts[0].NoFollow || b.opts[0].Tail != defaultLogTail {
		t.Errorf("compose log options = %+v", b.opts)
	}
}

func TestSortByTimestamp(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"by time", []string{"a | 2026-01-01T00:00:02Z x", "b | 2026-01-01T00:00:01Z y"},
			[]string{"b | 2026-01-01T00:00:01Z y", "a | 2026-01-01T00:00:02Z x"}},
		{"nanoseconds", []string{"a | 2026-01-01T00:00:01.9Z x", "b | 2026-01-01T00:00:01.10Z y"},
			[]string{"b | 2026-01-01T00:00:01.10Z y", "a | 2026-01-01T00:00:01.9Z x"}},
		{"untimed follows its predecessor", []string{"a | 2026-01-01T00:00:05Z x", "a | error: boom", "b | 2026-01-01T00:00:01Z y"},
			[]string{"b | 2026-01-01T00:00:01Z y", "a | 2026-01-01T00:00:05Z x", "a | error: boom"}},
		{"no prefix", []string{"2026-01-01T00:00:02Z x", "2026-01-01T00:00:01Z y"},
			[]string{"2026-01-01T00:00:01Z y", "2026-01-01T00:00:02Z x"}},
		{"stable on ties", []string{"b | 2026-01-01T00:00:01Z 1", "a | 2026-01-01T00:00:01Z 2"},
			[]string{"b | 2026-01-01T00:00:01Z 1", "a | 2026-01-01T00:00:01Z 2"}},
		{"empty", []string{}, []string{}},
	}
	for _, tt := range tests {
		sortByTimestamp(tt.in)
		if !reflect.DeepEqual(tt.in, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, tt.in, tt.want)
		}
	}
}
