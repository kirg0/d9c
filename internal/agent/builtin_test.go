package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
)

// Each built-in operation has its declared tier, only `hosts ls` may run
// without a connection, and the ops returning container-authored content
// carry the prompt-injection note.
func TestBuiltinDeclarations(t *testing.T) {
	r := Builtin()
	tests := []struct {
		name      string
		tier      Tier
		local     bool
		untrusted bool
	}{
		{"hosts ls", TierRead, true, false},
		{"info", TierRead, false, false},
		{"ps", TierRead, false, false},
		{"inspect", TierRead, false, true},
		{"logs", TierRead, false, true},
		{"stats", TierRead, false, false},
		{"images", TierRead, false, false},
		{"networks", TierRead, false, false},
		{"volumes", TierRead, false, false},
		{"system df", TierRead, false, false},
		{"events", TierRead, false, false},
		{"compose ls", TierRead, false, false},
		{"compose ps", TierRead, false, false},
		{"compose config", TierRead, false, true},
		{"compose logs", TierRead, false, true},
		{"start", TierSafe, false, false},
		{"stop", TierSafe, false, false},
		{"restart", TierSafe, false, false},
		{"kill", TierDestructive, false, false},
		{"rm", TierDestructive, false, false},
		{"pull", TierSafe, false, false},
		{"compose up", TierSafe, false, false},
		{"compose down", TierDestructive, false, false},
		{"compose pull", TierSafe, false, false},
		{"compose restart", TierSafe, false, false},
		{"compose backup", TierSafe, false, false},
		{"compose restore", TierDestructive, false, false},
		{"network create", TierSafe, false, false},
		{"volume create", TierSafe, false, false},
		{"image prune", TierDestructive, false, false},
		{"volume prune", TierDestructive, false, false},
		{"system prune", TierDestructive, false, false},
	}
	if got := len(r.Ops()); got != len(tests) {
		t.Errorf("Builtin has %d ops, want %d", got, len(tests))
	}
	for _, tt := range tests {
		op, ok := r.Lookup(tt.name)
		if !ok {
			t.Errorf("%q not registered", tt.name)
			continue
		}
		if op.Tier != tt.tier {
			t.Errorf("%q tier = %s, want %s", tt.name, op.Tier, tt.tier)
		}
		if op.Local != tt.local {
			t.Errorf("%q Local = %v, want %v", tt.name, op.Local, tt.local)
		}
		if op.Untrusted != tt.untrusted {
			t.Errorf("%q Untrusted = %v, want %v", tt.name, op.Untrusted, tt.untrusted)
		}
	}
}

func TestHostsLs(t *testing.T) {
	r := Builtin()
	ctx := context.Background()

	// No store: an empty list, never null.
	res, err := r.Invoke(ctx, &Env{}, "hosts ls", nil, Call{Ceiling: TierRead})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := res.Data.([]HostEntry); !ok || got == nil || len(got) != 0 {
		t.Errorf("no store: Data = %#v, want empty []HostEntry", res.Data)
	}

	store := hosts.NewStore([]hosts.Host{
		{Name: "prod", Host: "ssh://root@prod", SSHKeyPath: "/k", ReadOnly: true},
		{Name: "lab", Host: "tcp://10.0.0.2:2375"},
	}, nil)
	res, err = r.Invoke(ctx, &Env{Hosts: store}, "hosts ls", nil, Call{Ceiling: TierRead})
	if err != nil {
		t.Fatal(err)
	}
	want := []HostEntry{
		{Name: "prod", Host: "ssh://root@prod", ReadOnly: true},
		{Name: "lab", Host: "tcp://10.0.0.2:2375"},
	}
	if !reflect.DeepEqual(res.Data, want) {
		t.Errorf("Data = %#v, want %#v", res.Data, want)
	}
}

// errInfoBackend fails Info with a canned error.
type errInfoBackend struct {
	docker.Backend
	err error
}

func (b errInfoBackend) Info() (docker.HostSummary, error) { return docker.HostSummary{}, b.err }

func TestInfo(t *testing.T) {
	r := Builtin()
	ctx := context.Background()

	fb := docker.NewFakeBackend()
	res, err := r.Invoke(ctx, &Env{Backend: fb}, "info", nil, Call{Ceiling: TierRead})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := fb.Info()
	got, ok := res.Data.(InfoData)
	if !ok {
		t.Fatalf("Data = %T, want InfoData", res.Data)
	}
	if got.Containers != s.Containers || got.Running != s.Running || got.Images != s.Images || got.Version != s.Version {
		t.Errorf("Data = %+v, want counts of %+v", got, s)
	}

	// No backend is a frontend bug, reported as internal rather than a panic.
	if _, err := r.Invoke(ctx, &Env{}, "info", nil, Call{Ceiling: TierRead}); Classify(err).Code != CodeInternal {
		t.Errorf("nil backend: err = %v, want internal", err)
	}

	// Backend errors are classified.
	refused := errors.New("dial tcp 10.0.0.1:2375: connect: connection refused")
	_, err = r.Invoke(ctx, &Env{Backend: errInfoBackend{err: refused}}, "info", nil, Call{Ceiling: TierRead})
	if Classify(err).Code != CodeConnection {
		t.Errorf("refused: err = %v, want connection", err)
	}
}
