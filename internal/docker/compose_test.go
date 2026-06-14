package docker

import (
	"strings"
	"testing"
)

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
