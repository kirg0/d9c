package docker

import (
	"strings"
	"testing"
)

// SupportsHostCompose distinguishes ssh:// (host shell available) from tcp://.
func TestSupportsHostCompose(t *testing.T) {
	if (&dockerBackend{}).SupportsHostCompose() {
		t.Error("tcp backend (no sshClient) must report SupportsHostCompose=false")
	}
	if NewDisconnected(nil).SupportsHostCompose() {
		t.Error("disconnected backend must report SupportsHostCompose=false")
	}
	if !NewFakeBackend().SupportsHostCompose() {
		t.Error("fake backend defaults to host compose supported (ssh-like)")
	}
	if (&FakeBackend{NoHostCompose: true}).SupportsHostCompose() {
		t.Error("fake backend with NoHostCompose must report false (tcp-like)")
	}
}

// groupComposeProjects must split independent deployments that share a project
// name but live in different working_dirs (the real-world bug), while still
// collapsing a normal single-dir project's services into one entry.
func TestGroupComposeProjects(t *testing.T) {
	members := []composeMember{
		// Two deployments sharing project "mcmc" but different working_dirs.
		{project: "mcmc", workdir: "/d/core.licensing", config: "/d/core.licensing/docker-compose.yaml", state: "running"},
		{project: "mcmc", workdir: "/d/platform_triggers", config: "/d/platform_triggers/docker-compose.yaml", state: "running"},
		// A normal project: two services, one working_dir → one group.
		{project: "shop", workdir: "/srv/shop", config: "/srv/shop/compose.yaml", state: "running"},
		{project: "shop", workdir: "/srv/shop", config: "/srv/shop/compose.yaml", state: "exited"},
		// No project label → ignored.
		{project: "", workdir: "/x", state: "running"},
	}

	got := groupComposeProjects(members)
	if len(got) != 3 {
		t.Fatalf("got %d deployments, want 3: %+v", len(got), got)
	}

	byName := map[string]ComposeProject{}
	for _, p := range got {
		byName[p.Name] = p
	}
	cl, ok := byName["core.licensing"]
	if !ok {
		t.Fatalf("missing core.licensing deployment; got %+v", got)
	}
	if cl.Project != "mcmc" || cl.WorkingDir != "/d/core.licensing" || cl.Total != 1 {
		t.Errorf("core.licensing = %+v, want project mcmc, its own dir, 1 container", cl)
	}
	if _, ok := byName["platform_triggers"]; !ok {
		t.Errorf("the other mcmc deployment was merged away: %+v", got)
	}
	shop := byName["shop"]
	if shop.Total != 2 || shop.Running != 1 || shop.Status != "partial" {
		t.Errorf("shop = %+v, want 2 total / 1 running / partial", shop)
	}
}

func TestComposeIdentityAndDisplayName(t *testing.T) {
	if id := composeIdentity("mcmc", "/d/a"); id != "/d/a" {
		t.Errorf("identity with workdir = %q, want /d/a", id)
	}
	if id := composeIdentity("solo", ""); id != "solo" {
		t.Errorf("identity without workdir = %q, want project fallback solo", id)
	}
	if n := composeDisplayName("mcmc", "/d/core.licensing"); n != "core.licensing" {
		t.Errorf("display name = %q, want basename core.licensing", n)
	}
	if n := composeDisplayName("solo", ""); n != "solo" {
		t.Errorf("display name without workdir = %q, want project solo", n)
	}
}

// composeFilter must scope by working_dir for a path identity and by project for
// a bare name — otherwise deployments sharing a project name leak into each
// other's lifecycle operations.
func TestComposeFilter(t *testing.T) {
	pathArgs := composeFilter("/d/core.licensing").Get("label")
	if len(pathArgs) != 1 || pathArgs[0] != composeWorkdirLabel+"=/d/core.licensing" {
		t.Errorf("path identity → %v, want working_dir label", pathArgs)
	}
	nameArgs := composeFilter("legacy").Get("label")
	if len(nameArgs) != 1 || nameArgs[0] != composeProjectLabel+"=legacy" {
		t.Errorf("name identity → %v, want project label", nameArgs)
	}
}

func TestBackupFileName(t *testing.T) {
	got := backupFileName("web app/x")
	if !strings.HasPrefix(got, "web-app-x-") {
		t.Errorf("name = %q, want prefix web-app-x-", got)
	}
	if !strings.HasSuffix(got, ".tar.gz") {
		t.Errorf("name = %q, want .tar.gz suffix", got)
	}
	if got := backupFileName("---"); !strings.HasPrefix(got, "compose-") {
		t.Errorf("empty-after-sanitize name = %q, want compose- prefix", got)
	}
}

func TestBackupFilePrefix(t *testing.T) {
	if got := BackupFilePrefix("web app/x"); got != "web-app-x" {
		t.Errorf("prefix = %q, want web-app-x", got)
	}
	if got := BackupFilePrefix("---"); got != "compose" {
		t.Errorf("prefix = %q, want compose", got)
	}
	// The prefix must match what backupFileName actually produces.
	name := backupFileName("webapp")
	if !strings.HasPrefix(name, BackupFilePrefix("webapp")+"-") {
		t.Errorf("backupFileName %q not under prefix %q", name, BackupFilePrefix("webapp"))
	}
}

func TestComposeStatus(t *testing.T) {
	tests := []struct {
		name   string
		states []string
		want   string
	}{
		{"all running", []string{"running", "running"}, "running"},
		{"all stopped", []string{"exited", "exited"}, "stopped"},
		{"all paused", []string{"paused", "paused"}, "paused"},
		{"partial", []string{"running", "exited"}, "partial"},
		{"dead is error", []string{"running", "dead"}, "error"},
		{"restarting is error", []string{"restarting", "running"}, "error"},
		{"empty", nil, "unknown"},
	}
	for _, tt := range tests {
		if got := composeStatus(tt.states); got != tt.want {
			t.Errorf("composeStatus(%v) = %q, want %q", tt.states, got, tt.want)
		}
	}
}

func TestComposeCommand(t *testing.T) {
	tests := []struct {
		config string
		want   string
	}{
		{"/srv/app/docker-compose.yml", "docker compose up -d"},
		{"/srv/app/compose.yaml", "docker compose up -d"},
		{"/srv/app/prod.yml", "docker compose -f prod.yml up -d"},
		{"", "docker compose up -d"},
		{`C:\srv\app\stack.yml`, "docker compose -f stack.yml up -d"},
		{"/a/docker-compose.yml,/a/override.yml", "docker compose up -d"},
	}
	for _, tt := range tests {
		if got := composeCommand(tt.config); got != tt.want {
			t.Errorf("composeCommand(%q) = %q, want %q", tt.config, got, tt.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"webapp", "'webapp'"},
		{"/srv/my app", "'/srv/my app'"},
		{"it's", `'it'\''s'`},
	}
	for _, tt := range tests {
		if got := shellQuote(tt.in); got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveComposePath(t *testing.T) {
	tests := []struct {
		name            string
		workdir, config string
		want            string
	}{
		{"relative filename", "/srv/webapp", "docker-compose.yml", "/srv/webapp/docker-compose.yml"},
		{"absolute config", "/srv/webapp", "/etc/compose/app.yml", "/etc/compose/app.yml"},
		{"no workdir", "", "docker-compose.yml", "docker-compose.yml"},
		{"relative subdir", "/srv/app", "deploy/compose.yaml", "/srv/app/deploy/compose.yaml"},
		{"windows workdir slashes", `C:\srv\app`, "compose.yml", "C:/srv/app/compose.yml"},
		{"dot-relative", "/srv/app", "./docker-compose.yml", "/srv/app/docker-compose.yml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveComposePath(tt.workdir, tt.config); got != tt.want {
				t.Errorf("resolveComposePath(%q, %q) = %q, want %q", tt.workdir, tt.config, got, tt.want)
			}
		})
	}
}

func TestBuildComposeCmd(t *testing.T) {
	tests := []struct {
		name                     string
		project, workdir, config string
		action                   string
		want                     string
	}{
		{
			name: "single config", project: "webapp", workdir: "/srv/webapp",
			config: "/srv/webapp/docker-compose.yml", action: "up -d",
			want: "docker compose --project-name 'webapp' --project-directory '/srv/webapp' -f '/srv/webapp/docker-compose.yml' up -d",
		},
		{
			name: "multiple configs", project: "app", workdir: "/srv/app",
			config: "/srv/app/compose.yaml,/srv/app/override.yaml", action: "pull",
			want: "docker compose --project-name 'app' --project-directory '/srv/app' -f '/srv/app/compose.yaml' -f '/srv/app/override.yaml' pull",
		},
		{
			name: "no workdir", project: "x", workdir: "", config: "", action: "pull",
			want: "docker compose --project-name 'x' pull",
		},
		{
			// Relative config from the label must be resolved against workdir so
			// `-f` does not fall back to the SSH session's home directory.
			name: "relative config resolved to workdir", project: "webapp", workdir: "/srv/webapp",
			config: "docker-compose.yml", action: "pull",
			want: "docker compose --project-name 'webapp' --project-directory '/srv/webapp' -f '/srv/webapp/docker-compose.yml' pull",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildComposeCmd(tt.project, tt.workdir, tt.config, tt.action); got != tt.want {
				t.Errorf("buildComposeCmd = %q, want %q", got, tt.want)
			}
		})
	}
}
