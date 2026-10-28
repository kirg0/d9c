package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
)

func TestRegisterNeedsPlanner(t *testing.T) {
	for _, tier := range []Tier{TierSafe, TierDestructive} {
		op := testOp("x", tier)
		op.Plan = nil
		if err := NewRegistry().Register(op); err == nil || !strings.Contains(err.Error(), "planner") {
			t.Errorf("%s op without planner: err = %v", tier, err)
		}
	}
	if err := NewRegistry().Register(testOp("ps", TierRead)); err != nil {
		t.Errorf("read op without planner: %v", err)
	}
}

// TestInvokeCall covers how Call steers Invoke: dry runs plan regardless of
// the ceiling, destructive ops wait for confirmation, and the handler runs
// only when it should.
func TestInvokeCall(t *testing.T) {
	ran := 0
	handler := func(context.Context, *Env, Args) (Result, error) {
		ran++
		return Result{Data: "done"}, nil
	}
	plan := func(_ context.Context, _ *Env, a Args) ([]string, error) {
		if a.String("name") == "missing" {
			return nil, Errorf(CodeNotFound, "no such thing")
		}
		return []string{"touch " + a.String("name")}, nil
	}
	args := Object(map[string]*Schema{"name": String("Target.")})
	r := NewRegistry()
	r.MustRegister(
		Op{Name: "rm", Summary: "remove", Args: args, Tier: TierDestructive, Handler: handler, Plan: plan},
		Op{Name: "start", Summary: "start", Args: args, Tier: TierSafe, Handler: handler, Plan: plan},
		Op{Name: "ps", Summary: "list", Args: NoArgs(), Tier: TierRead, Handler: handler},
		Op{Name: "boom", Summary: "panics", Args: NoArgs(), Tier: TierSafe, Handler: handler,
			Plan: func(context.Context, *Env, Args) ([]string, error) { panic("bug") }},
		Op{Name: "noop", Summary: "nothing", Args: NoArgs(), Tier: TierSafe, Handler: handler,
			Plan: func(context.Context, *Env, Args) ([]string, error) { return nil, nil }},
	)
	ctx := context.Background()
	x := Args{"name": "x"}

	tests := []struct {
		name     string
		op       string
		args     Args
		call     Call
		wantCode Code   // "" = success
		wantRan  bool   // the handler ran
		wantPlan bool   // the result (or error) carries a plan
		wantHint string // substring of the hint
	}{
		{"destructive confirmed runs", "rm", x, Call{Ceiling: TierDestructive, Confirmed: true}, "", true, false, ""},
		{"destructive unconfirmed asks", "rm", x, Call{Ceiling: TierDestructive}, CodeConfirmationRequired, false, true, "--yes"},
		{"safe runs without confirmation", "start", x, Call{Ceiling: TierSafe}, "", true, false, ""},
		{"forbidden before confirmation", "rm", x, Call{Ceiling: TierSafe}, CodeForbidden, false, false, ""},
		{"dry run plans", "rm", x, Call{DryRun: true}, "", false, true, ""},
		{"dry run ignores confirmation", "rm", x, Call{DryRun: true, Confirmed: true, Ceiling: TierDestructive}, "", false, true, ""},
		{"dry run of read op", "ps", nil, Call{DryRun: true}, CodeUsage, false, false, "without --dry-run"},
		{"dry run bad args", "start", Args{"name": 1}, Call{DryRun: true}, CodeUsage, false, false, ""},
		{"planner error classified", "rm", Args{"name": "missing"}, Call{Ceiling: TierDestructive}, CodeNotFound, false, false, ""},
		{"planner panic", "boom", nil, Call{DryRun: true}, CodeInternal, false, false, "bug"},
		{"empty plan", "noop", nil, Call{DryRun: true}, "", false, true, "nothing to do"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran = 0
			res, err := r.Invoke(ctx, &Env{}, tt.op, tt.args, tt.call)
			var plan *PlanData
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if p, ok := res.Data.(PlanData); ok {
					plan = &p
				}
				if tt.wantHint != "" && !strings.Contains(res.Hint, tt.wantHint) {
					t.Errorf("hint = %q, want %q", res.Hint, tt.wantHint)
				}
			} else {
				var ae *Error
				if !errors.As(err, &ae) || ae.Code != tt.wantCode {
					t.Fatalf("err = %v, want code %s", err, tt.wantCode)
				}
				plan = ae.Plan
				if tt.wantHint != "" && !strings.Contains(ae.Hint, tt.wantHint) {
					t.Errorf("hint = %q, want %q", ae.Hint, tt.wantHint)
				}
			}
			if (ran > 0) != tt.wantRan {
				t.Errorf("handler ran %d times, want ran=%v", ran, tt.wantRan)
			}
			if (plan != nil) != tt.wantPlan {
				t.Fatalf("plan = %+v, want plan=%v", plan, tt.wantPlan)
			}
			if plan != nil {
				op, _ := r.Lookup(tt.op)
				if plan.Operation != tt.op || plan.Tier != op.Tier || plan.Confirm != (op.Tier == TierDestructive) || plan.Steps == nil {
					t.Errorf("plan = %+v", plan)
				}
			}
		})
	}
}

// fakeState snapshots everything a write op on the demo backend can change.
func fakeState(t *testing.T, b *docker.FakeBackend) []any {
	t.Helper()
	cs, _ := b.ListContainers(true)
	imgs, _ := b.ListImages()
	nets, _ := b.ListNetworks()
	vols, _ := b.ListVolumes()
	projs, _ := b.ListComposeProjects()
	return []any{cs, imgs, nets, vols, projs}
}

// A dry run of every write op plans at least one step and leaves the demo
// engine exactly as it was.
func TestDryRunNeverMutates(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("webapp.tar.gz", []byte("demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := map[string]Args{
		"start": {"targets": []string{"db"}}, "stop": {"targets": []string{"web"}},
		"restart": {"targets": []string{"web"}}, "kill": {"targets": []string{"web"}},
		"rm":   {"filter": []string{"status=exited"}},
		"pull": {"image": "nginx:1.27"}, "compose up": {"project": "legacy"},
		"compose down": {"project": "webapp"}, "compose pull": {"project": "webapp"},
		"compose restart": {"project": "webapp"}, "compose backup": {"project": "webapp"},
		"compose restore": {"project": "webapp", "file": "webapp.tar.gz"},
		"network create":  {"name": "edge"}, "volume create": {"name": "uploads"},
		"exec": {"container": "web", "cmd": []string{"rm", "-rf", "/data"}},
	}
	r := Builtin()
	for _, op := range r.Ops() {
		if op.Tier == TierRead {
			continue
		}
		t.Run(op.Name, func(t *testing.T) {
			fb := docker.NewFakeBackend()
			before := fakeState(t, fb)
			res, err := r.Invoke(context.Background(), &Env{Backend: fb}, op.Name, args[op.Name], Call{DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			plan, ok := res.Data.(PlanData)
			if !ok || len(plan.Steps) == 0 {
				t.Fatalf("data = %#v, want a plan with steps", res.Data)
			}
			if after := fakeState(t, fb); !reflect.DeepEqual(before, after) {
				t.Errorf("dry run changed the engine")
			}
			if entries, _ := os.ReadDir("."); len(entries) != 1 {
				t.Errorf("dry run wrote files: %v", entries)
			}
		})
	}
}

func TestPlanSteps(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("site.tar.gz", []byte("demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs("site.tar.gz")
	tests := []struct {
		op   string
		args Args
		want []string
	}{
		{"start", Args{"targets": []string{"web", "db"}}, []string{
			"start container web (9ae942fd8fbc) — already running",
			"start container db (3f1ab77c9012)",
		}},
		{"kill", Args{"targets": []string{"db"}, "signal": "HUP"}, []string{
			"send HUP to container db (3f1ab77c9012) — not running, will fail",
		}},
		{"rm", Args{"targets": []string{"web", "nope"}, "force": true}, []string{
			"remove container web (9ae942fd8fbc) — running, killed first",
			"no such container: nope — will be reported as failed",
		}},
		{"network create", Args{"name": "edge", "driver": "overlay", "subnet": "10.9.0.0/16", "gateway": "10.9.0.1"}, []string{
			"create network edge (driver overlay), subnet 10.9.0.0/16, gateway 10.9.0.1",
		}},
		{"volume create", Args{"name": "uploads"}, []string{"create volume uploads (driver local by default)"}},
		{"compose restore", Args{"project": "webapp", "file": "site.tar.gz"}, []string{
			"upload " + abs + " and overwrite the files of project webapp (/srv/webapp) with it",
			"bring the project up again",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			res, err := Builtin().Invoke(context.Background(), &Env{Backend: docker.NewFakeBackend()}, tt.op, tt.args, Call{DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			if got := res.Data.(PlanData).Steps; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("steps = %q\nwant    %q", got, tt.want)
			}
		})
	}
}

// Planning fails the way running would: a precondition the handler checks
// is checked by the planner too.
func TestPlanErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	tests := []struct {
		op   string
		args Args
		want Code
	}{
		{"stop", nil, CodeUsage},
		{"stop", Args{"filter": []string{"bogus=1"}}, CodeUsage},
		{"pull", Args{"image": " "}, CodeUsage},
		{"compose down", Args{"project": "ghost"}, CodeNotFound},
		{"compose restore", Args{"project": "webapp", "file": "nope.tar.gz"}, CodeNotFound},
		{"network create", Args{"name": "app-net"}, CodeUsage},
		{"network create", Args{"name": "edge", "gateway": "10.0.0.1"}, CodeUsage},
		{"volume create", Args{"name": "pgdata"}, CodeUsage},
	}
	for _, tt := range tests {
		_, err := Builtin().Invoke(context.Background(), &Env{Backend: docker.NewFakeBackend()}, tt.op, tt.args, Call{DryRun: true})
		if e := Classify(err); e == nil || e.Code != tt.want {
			t.Errorf("%s %v: err = %v, want %s", tt.op, tt.args, err, tt.want)
		}
	}
}
