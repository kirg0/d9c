package agent

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kirg0/d9c/internal/docker"
)

// Long operations pass their steps to Env.Progress while they run.
func TestProgressReported(t *testing.T) {
	waitPoll = time.Millisecond
	t.Cleanup(func() { waitPoll = 500 * time.Millisecond })

	tests := []struct {
		name string
		op   string
		args Args
		want string // a substring of some reported line
	}{
		{"compose stream", "compose up", Args{"project": "webapp"}, "webapp"},
		{"pull", "pull", Args{"image": "nginx:1.27"}, "pulling nginx:1.27"},
		{"backup", "compose backup", Args{"project": "webapp"}, "archiving project webapp"},
		{"wait", "wait", Args{"container": "api", "until": "healthy", "timeout": 1}, "api is running, health starting, waiting for healthy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir()) // compose backup writes its archive here
			var got []string
			env := &Env{Backend: docker.NewFakeBackend(), Progress: func(m string) { got = append(got, m) }}
			res, err := Builtin().Invoke(context.Background(), env, tt.op, tt.args, Call{Ceiling: TierDestructive, Confirmed: true})
			if tt.op != "wait" && err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(got, func(l string) bool { return strings.Contains(l, tt.want) }) {
				t.Errorf("reported %q, want a line with %q", got, tt.want)
			}
			if d, ok := res.Data.(ComposeActionData); ok && !slices.Equal(d.Output, got) {
				t.Errorf("reported %q, but the output is %q", got, d.Output)
			}
		})
	}
}

// A nil Progress (the CLI) is fine.
func TestReportWithoutProgress(t *testing.T) {
	var env *Env
	env.report("ignored")
	(&Env{}).report("ignored")
}
