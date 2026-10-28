package agent

import (
	"context"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
)

func TestQuoteArgv(t *testing.T) {
	tests := []struct {
		cmd  []string
		want string
	}{
		{[]string{"ls", "-la", "/"}, "ls -la /"},
		{[]string{"sh", "-c", "echo hi"}, `sh -c "echo hi"`},
		{[]string{"printf", ""}, `printf ""`},
		{[]string{"echo", `it's`}, `echo "it's"`},
	}
	for _, tt := range tests {
		if got := quoteArgv(tt.cmd); got != tt.want {
			t.Errorf("quoteArgv(%q) = %s, want %s", tt.cmd, got, tt.want)
		}
	}
}

func TestExecResult(t *testing.T) {
	c := docker.Container{ID: "9ae942fd8fbc", Name: "web"}
	r := execResult(c, []string{"cat"}, docker.ExecResult{Stdout: []byte("ok\xff"), StdoutBytes: 3, ExitCode: 2}, 10)
	d := r.Data.(ExecData)
	if d.Stdout != "ok\uFFFD" || d.ExitCode != 2 || d.Stderr != "" || r.Truncated {
		t.Errorf("result = %+v (truncated %v)", d, r.Truncated)
	}
	r = execResult(c, []string{"cat"}, docker.ExecResult{Stderr: []byte("ab"), StderrBytes: 9}, 2)
	if !r.Truncated || r.Hint == "" {
		t.Errorf("cut stderr not reported: %+v", r)
	}
}

func TestExecTimeout(t *testing.T) {
	c := docker.Container{Name: "web"}
	e := execTimeout(c, 5, docker.ExecResult{Stdout: []byte("1\n2\n3\n4\n5\n6\n7\n")})
	if e.Code != CodeTimeout || e.Message != "exec in web timed out after 5s; last output: 3 | 4 | 5 | 6 | 7" {
		t.Errorf("err = %+v", e)
	}
	if e := execTimeout(c, 5, docker.ExecResult{}); e.Message != "exec in web timed out after 5s" {
		t.Errorf("silent command: %q", e.Message)
	}
}

func TestExecErrors(t *testing.T) {
	tests := []struct {
		args Args
		want Code
	}{
		{Args{"container": "ghost", "cmd": []string{"echo"}}, CodeNotFound},
		{Args{"container": "db", "cmd": []string{"echo"}}, CodeUsage},
		{Args{"container": "web", "cmd": []string{""}}, CodeUsage},
		{Args{"container": "web"}, CodeUsage},
		{Args{"container": "web", "cmd": []string{"echo"}, "timeout": 0}, CodeUsage},
	}
	for _, tt := range tests {
		_, err := Builtin().Invoke(context.Background(), &Env{Backend: docker.NewFakeBackend()}, "exec", tt.args,
			Call{Ceiling: TierDestructive, Confirmed: true})
		if e := Classify(err); e == nil || e.Code != tt.want {
			t.Errorf("%v: err = %v, want %s", tt.args, err, tt.want)
		}
	}
}
