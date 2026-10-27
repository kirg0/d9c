package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
)

func nopHandler(context.Context, *Env, Args) (Result, error) { return Result{}, nil }

func testOp(name string, tier Tier) Op {
	return Op{Name: name, Summary: "test op", Args: NoArgs(), Tier: tier, Handler: nopHandler}
}

func TestRegisterValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Op)
		wantErr string
	}{
		{"valid", func(*Op) {}, ""},
		{"two words", func(o *Op) { o.Name = "compose ls" }, ""},
		{"dash", func(o *Op) { o.Name = "system df-x" }, ""},
		{"empty name", func(o *Op) { o.Name = "" }, "name must be"},
		{"upper case", func(o *Op) { o.Name = "PS" }, "name must be"},
		{"double space", func(o *Op) { o.Name = "compose  ls" }, "name must be"},
		{"trailing space", func(o *Op) { o.Name = "ps " }, "name must be"},
		{"bad tier", func(o *Op) { o.Tier = Tier(3) }, "invalid tier"},
		{"nil handler", func(o *Op) { o.Handler = nil }, "nil handler"},
		{"no summary", func(o *Op) { o.Summary = "" }, "missing summary"},
		{"nil schema", func(o *Op) { o.Args = nil }, "must be an object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := testOp("ps", TierRead)
			tt.mutate(&op)
			err := NewRegistry().Register(op)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestRegisterDuplicateAndMustRegister(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(testOp("ps", TierRead))
	if err := r.Register(testOp("ps", TierSafe)); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate err = %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("MustRegister did not panic on a bad op")
		}
	}()
	r.MustRegister(testOp("BAD", TierRead))
}

func TestRegistryListing(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(
		testOp("rm", TierDestructive),
		testOp("ps", TierRead),
		testOp("start", TierSafe),
		testOp("compose ls", TierRead),
	)
	names := func(ops []Op) string {
		s := make([]string, len(ops))
		for i, op := range ops {
			s[i] = op.Name
		}
		return strings.Join(s, ",")
	}
	tests := []struct {
		ceiling Tier
		want    string
	}{
		{TierRead, "compose ls,ps"},
		{TierSafe, "compose ls,ps,start"},
		{TierDestructive, "compose ls,ps,rm,start"},
	}
	for _, tt := range tests {
		if got := names(r.Allowed(tt.ceiling)); got != tt.want {
			t.Errorf("Allowed(%v) = %s, want %s", tt.ceiling, got, tt.want)
		}
	}
	if got := names(r.Ops()); got != "compose ls,ps,rm,start" {
		t.Errorf("Ops() = %s", got)
	}
	if op, ok := r.Lookup("start"); !ok || op.Tier != TierSafe {
		t.Errorf("Lookup(start) = %+v, %v", op, ok)
	}
	if _, ok := r.Lookup("stop"); ok {
		t.Error("Lookup(stop) found a missing op")
	}
}

func TestOpDescription(t *testing.T) {
	op := testOp("logs", TierRead)
	if op.Description() != "test op" {
		t.Errorf("trusted description = %q", op.Description())
	}
	op.Untrusted = true
	if got := op.Description(); got != "test op "+UntrustedNote {
		t.Errorf("untrusted description = %q", got)
	}
}

func TestInvoke(t *testing.T) {
	var gotArgs Args
	r := NewRegistry()
	r.MustRegister(
		Op{
			Name:    "echo",
			Summary: "returns its args",
			Args:    Object(map[string]*Schema{"n": Integer("count").WithDefault(2)}),
			Tier:    TierRead,
			Handler: func(_ context.Context, _ *Env, a Args) (Result, error) {
				gotArgs = a
				return Result{Data: a.Int("n"), Truncated: true, Hint: "raise n"}, nil
			},
		},
		Op{
			Name:    "fail",
			Summary: "fails like the daemon",
			Args:    NoArgs(),
			Tier:    TierRead,
			Handler: func(context.Context, *Env, Args) (Result, error) {
				return Result{}, errors.New("Error response from daemon: No such container: web")
			},
		},
		Op{
			Name:    "boom",
			Summary: "panics",
			Args:    NoArgs(),
			Tier:    TierRead,
			Handler: func(context.Context, *Env, Args) (Result, error) {
				panic("nil map")
			},
		},
		testOp("rm", TierDestructive),
	)
	ctx := context.Background()

	res, err := r.Invoke(ctx, &Env{}, "echo", nil, TierRead)
	if err != nil || res.Data != 2 || !res.Truncated || res.Hint != "raise n" || gotArgs.Int("n") != 2 {
		t.Fatalf("echo = %+v, %v (args %v)", res, err, gotArgs)
	}

	tests := []struct {
		name    string
		op      string
		args    Args
		ceiling Tier
		want    Code
	}{
		{"unknown op", "nope", nil, TierDestructive, CodeUsage},
		{"bad args", "echo", Args{"n": "x"}, TierRead, CodeUsage},
		{"over ceiling", "rm", nil, TierSafe, CodeForbidden},
		{"handler error classified", "fail", nil, TierRead, CodeNotFound},
		{"handler panic", "boom", nil, TierRead, CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Invoke(ctx, &Env{}, tt.op, tt.args, tt.ceiling)
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != tt.want {
				t.Fatalf("err = %v, want code %q", err, tt.want)
			}
			if ae.Hint == "" {
				t.Errorf("%s: expected a hint", tt.name)
			}
		})
	}
}

// TestInvokeWithFakeBackend runs a realistic read op end to end on the demo
// backend and checks the envelope it would be emitted in.
func TestInvokeWithFakeBackend(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(Op{
		Name:    "ps",
		Summary: "list containers",
		Args:    Object(map[string]*Schema{"all": Boolean("include stopped containers")}),
		Tier:    TierRead,
		Handler: func(_ context.Context, env *Env, a Args) (Result, error) {
			cs, err := env.Backend.ListContainers(a.Bool("all"))
			if err != nil {
				return Result{}, err
			}
			return Result{Data: cs}, nil
		},
	})
	fb := docker.NewFakeBackend()
	res, err := r.Invoke(context.Background(), &Env{Backend: fb}, "ps", Args{"all": true}, TierRead)
	if err != nil {
		t.Fatal(err)
	}
	cs, ok := res.Data.([]docker.Container)
	if !ok || len(cs) == 0 {
		t.Fatalf("data = %#v", res.Data)
	}
	b, err := json.Marshal(NewEnvelope("demo", fb.Runtime(), res))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `{"schema":1,"host":"demo","runtime":"docker","data":[`) ||
		!strings.HasSuffix(string(b), `],"truncated":false}`) {
		t.Errorf("envelope = %.200s…", b)
	}
}

func TestNewEnvelope(t *testing.T) {
	env := NewEnvelope("prod", docker.RuntimePodman, Result{Data: []int{1}, Truncated: true, Hint: "use --limit"})
	b, _ := json.Marshal(env)
	want := `{"schema":1,"host":"prod","runtime":"podman","data":[1],"truncated":true,"hint":"use --limit"}`
	if string(b) != want {
		t.Errorf("envelope = %s\nwant       %s", b, want)
	}
	b, _ = json.Marshal(NewEnvelope("h", docker.RuntimeUnknown, Result{}))
	if string(b) != `{"schema":1,"host":"h","runtime":"docker","data":null,"truncated":false}` {
		t.Errorf("empty envelope = %s", b)
	}
}
