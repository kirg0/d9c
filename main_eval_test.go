package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
)

// evalStep is one command a skill prescribes, the exit status it should end
// with on its scenario and the evidence its output must hold (stdout for
// success, stderr otherwise); none lists text that must not appear.
type evalStep struct {
	args []string
	exit int
	want []string
	none []string
}

// skillEvals replays, per demo scenario, the steps of the skill it was built
// for and checks that the planted problem shows up where the skill looks. This
// is the free, deterministic half of the skill evals; the model-graded half is
// the claude plugin eval suite in plugins/d9c/evals.
var skillEvals = map[string][]evalStep{
	"default": {
		{args: []string{"ps"}, want: []string{`"name":"web"`, `"name":"api"`}},
	},
	"oom": {
		{args: []string{"ps", "-a", "--fields", "name,state,status"}, want: []string{`"name":"worker"`, "Exited (137)"}},
		{args: []string{"inspect", "worker"}, want: []string{`"exitcode":137`, `"oomkilled":true`, `"restartcount":4`, `"memory":268435456`}},
		{args: []string{"logs", "worker", "--tail", "200"}, want: []string{"heap 251 MiB"}},
		{args: []string{"events", "--since", "1h"}, want: []string{"container oom worker"}},
	},
	"restart-loop": {
		{args: []string{"ps", "-a", "--fields", "name,state,status"}, want: []string{"Restarting (1)", `"name":"db"`, "Exited (0)"}},
		{args: []string{"logs", "api", "--since", "30m", "--tail", "0", "--grep", "FATAL|ERROR"},
			want: []string{"lookup db: no such host", "could not connect to the database"}, none: []string{"starting on"}},
		{args: []string{"events", "--since", "1h"}, want: []string{"container stop db", "container restart api"}},
		{args: []string{"inspect", "api"}, want: []string{`"restartcount":37`, "DB_PASSWORD=<redacted>"}, none: []string{"s3cr3t-demo"}},
	},
	"staging": {
		{args: []string{"stop", "--filter", "label=env=staging", "--dry-run"},
			want: []string{"web-staging", "api-staging", "worker-staging"}, none: []string{"web-prod", "api-prod"}},
		{args: []string{"stop", "--filter", "label=env=staging", "--filter", "status=running"}, want: []string{`"done":3`, `"failed":0`}},
		{args: []string{"rm", "--filter", "label=env=staging"}, exit: 7, want: []string{"confirmation_required", "migrate-staging"}},
		{args: []string{"wait", "web-prod", "--until", "running", "--timeout", "0"}, want: []string{`"state":"running"`}},
	},
	"unhealthy-compose": {
		{args: []string{"compose", "ps", "shop"}, want: []string{"shop-api-1", `"health":"unhealthy"`}, none: []string{`"name":"web"`}},
		{args: []string{"compose", "backup", "shop"}, want: []string{`"project":"shop"`, ".tar.gz"}},
		{args: []string{"compose", "pull", "shop"}, want: []string{`"action":"compose pull"`, "acme/shop-web:2.0"}, none: []string{"nginx"}},
		{args: []string{"compose", "up", "shop"}, want: []string{`"action":"compose up"`, "shop-api-1"}, none: []string{"shop-web-1  Created"}},
		{args: []string{"wait", "shop-api-1", "--until", "healthy", "--timeout", "0"}, exit: 9, want: []string{`"code":"timeout"`}},
		{args: []string{"inspect", "shop-api-1"}, want: []string{"expected 42: run migrations", `"failingstreak":9`}},
		{args: []string{"compose", "logs", "shop", "--since", "10m"}, want: []string{"shop-api-1 | ", "refusing to serve until migrations run"}},
		{args: []string{"compose", "config", "shop"}, want: []string{"acme/shop-api:2.0"}, none: []string{"sk_live_demo"}},
	},
	"stale-images": {
		{args: []string{"images"}, want: []string{"acme/web:3.0", "acme/report:1.3", "<none>:<none>"}},
		{args: []string{"ps", "-a", "--fields", "name,image,state"}, want: []string{"acme/web:3.2", "acme/report:1.4"}, none: []string{"acme/web:3.1"}},
		{args: []string{"image", "rm", "acme/web:3.0", "--dry-run"}, want: []string{"remove acme/web:3.0 (3a2b00000030, 151 MB)"}, none: []string{"used by"}},
		{args: []string{"image", "rm", "acme/web:3.2", "--dry-run"}, want: []string{"used by containers: web"}},
		{args: []string{"image", "history", "acme/report:1.4"}, want: []string{`"layers"`}},
	},
	"disk-full": {
		{args: []string{"system", "df"}, want: []string{"12.4GB (64%)", "9.3GB"}},
		{args: []string{"system", "prune", "--dry-run"}, want: []string{`"operation":"system prune"`, `"confirm":true`}},
		{args: []string{"system", "prune"}, exit: 7, want: []string{"confirmation_required"}},
		{args: []string{"volume", "prune", "--dry-run"}, want: []string{"candidates among the 2 volumes", "old_reports_cache"}},
	},
	"fleet": {
		{args: []string{"info"}, want: []string{`"ncpu":4`, `"mem_total":8589934592`}},
		{args: []string{"stats"}, want: []string{`"name":"search"`, `"cpu_percent":180`}},
		{args: []string{"ps", "-a", "--fields", "name,health"}, want: []string{`"health":"unhealthy"`}},
		{args: []string{"info", "--all-hosts"}, want: []string{`"host":"prod"`, `"host":"staging"`, `"ncpu":4`}},
	},
	"insecure": {
		{args: []string{"ps", "--fields", "name,image,ports"}, want: []string{"0.0.0.0:6379", "redis:latest", "portainer/portainer-ce:latest"}},
		{args: []string{"inspect", "admin-panel"}, want: []string{`"privileged":true`, "/var/run/docker.sock", "ADMIN_PASSWORD=<redacted>", `"user":""`},
			none: []string{"hunter2-demo"}},
		{args: []string{"inspect", "backup"}, want: []string{`"capadd":["SYS_ADMIN"]`, `"source":"/"`, `"user":"0"`}},
		{args: []string{"inspect", "app"}, want: []string{`"memory":536870912`, `"user":"10001"`, "127.0.0.1"}},
	},
	"isolated-network": {
		{args: []string{"ps", "--fields", "name,networks"}, want: []string{`"networks":["web-net"]`, `"networks":["api-net"]`}},
		{args: []string{"inspect", "api-net", "--type", "network"}, want: []string{`"internal":true`, `"name":"backend"`}, none: []string{"frontend"}},
		{args: []string{"logs", "frontend", "--grep", "ENOTFOUND"}, want: []string{"getaddrinfo ENOTFOUND backend"}},
		{args: []string{"inspect", "backend"}, want: []string{`"name":"uploads"`, `"destination":"/srv/uploads"`}},
		{args: []string{"network", "create", "shared-net", "--dry-run"}, want: []string{"create network shared-net"}},
	},
}

func TestSkillEvalScenarios(t *testing.T) {
	cfgPath := writeCLIConfig(t, "  - name: prod\n    host: tcp://127.0.0.1:1\n  - name: staging\n    host: tcp://127.0.0.1:2\n")
	for _, sc := range docker.FakeScenarios() {
		steps, ok := skillEvals[sc.Name]
		if !ok {
			t.Errorf("scenario %s has no steps in skillEvals", sc.Name)
			continue
		}
		t.Run(sc.Name, func(t *testing.T) {
			t.Chdir(t.TempDir()) // compose backup writes its archive here
			for _, st := range steps {
				cfg := &config.Config{ConfigFile: cfgPath, Host: config.DefaultHost, Demo: true, DemoScenario: sc.Name}
				code, out, errOut := cliRun(t, cfg, st.args...)
				got := out
				if code != 0 && code != agent.ExitPartial {
					got = errOut
				}
				if code != st.exit {
					t.Errorf("%v: exit %d, want %d (stdout %.300s, stderr %.300s)", st.args, code, st.exit, out, errOut)
					continue
				}
				for _, w := range st.want {
					if !strings.Contains(got, w) {
						t.Errorf("%v: output lacks %q: %.600s", st.args, w, got)
					}
				}
				for _, n := range st.none {
					if strings.Contains(got, n) {
						t.Errorf("%v: output holds %q", st.args, n)
					}
				}
			}
		})
	}
	for name := range skillEvals {
		if _, ok := docker.LookupFakeScenario(name); !ok {
			t.Errorf("skillEvals has steps for the unknown scenario %s", name)
		}
	}
}

// Every skill has a demo scenario, and every scenario names a shipped skill.
func TestEverySkillHasAScenario(t *testing.T) {
	bodies := skillBodies(t)
	covered := map[string]bool{}
	for _, sc := range docker.FakeScenarios() {
		if _, ok := bodies[sc.Skill]; !ok {
			t.Errorf("scenario %s is for the unknown skill %s", sc.Name, sc.Skill)
		}
		covered[sc.Skill] = true
	}
	for name := range bodies {
		if !covered[name] {
			t.Errorf("skill %s has no demo scenario", name)
		}
	}
}

// A scenario's write ceiling applies only to its own fake engine: the same
// write is forbidden with -demo alone.
func TestDemoScenarioPolicy(t *testing.T) {
	cfgPath := writeCLIConfig(t, "")
	code, _, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath, Host: config.DefaultHost, Demo: true}, "stop", "web")
	if code != 5 || !strings.Contains(errOut, "forbidden") {
		t.Errorf("-demo stop: exit %d, %s", code, errOut)
	}
	code, _, errOut = cliRun(t, &config.Config{ConfigFile: cfgPath, Host: config.DefaultHost, Demo: true, DemoScenario: "oom"}, "stop", "mq")
	if code != 5 {
		t.Errorf("read-only scenario stop: exit %d, %s", code, errOut)
	}
	code, _, errOut = cliRun(t, &config.Config{ConfigFile: cfgPath, Host: config.DefaultHost, Demo: true, DemoScenario: "staging", ReadOnly: true}, "stop", "web-prod")
	if code != 5 || !strings.Contains(errOut, "-read-only") {
		t.Errorf("-read-only still wins: exit %d, %s", code, errOut)
	}
	code, out, _ := cliRun(t, &config.Config{ConfigFile: cfgPath, Host: config.DefaultHost, Demo: true, DemoScenario: "staging"}, "stop", "web-prod")
	if code != 0 || !strings.Contains(out, `"done":1`) {
		t.Errorf("scenario stop: exit %d, %s", code, out)
	}
	code, _, errOut = cliRun(t, &config.Config{ConfigFile: cfgPath, Host: config.DefaultHost, Demo: true, DemoScenario: "nope"}, "ps")
	if code != 2 || !strings.Contains(errOut, "unknown demo scenario") {
		t.Errorf("unknown scenario: exit %d, %s", code, errOut)
	}
}

// The env variable selects a scenario like the flag (the plugin's MCP server
// gets it from EVAL_D9C_SCENARIO during evals).
func TestDemoScenarioEnv(t *testing.T) {
	t.Setenv("D9C_DEMO_SCENARIO", "oom")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := runWithArgs(t, "ps", "-a", "--fields", "name")
	os.Stdout = old
	_ = w.Close()
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if runErr != nil || !strings.Contains(string(buf[:n]), `"name":"worker"`) {
		t.Errorf("run = %v, stdout %s", runErr, buf[:n])
	}
}

// The TUI refuses a misspelt scenario before it starts.
func TestRunTUIUnknownScenario(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	err := runWithArgs(t, "-config", filepath.Join(t.TempDir(), "c.yaml"), "-demo-scenario", "nope")
	if err == nil || !strings.Contains(err.Error(), "unknown demo scenario") {
		t.Errorf("run = %v", err)
	}
}
