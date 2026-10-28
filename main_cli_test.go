package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
)

func TestResolveCLITarget(t *testing.T) {
	ctxDir := t.TempDir()
	writeDockerContext(t, ctxDir, "lab", "ssh://ops@lab")
	store := func() *hosts.Store {
		return hosts.NewStore([]hosts.Host{
			{Name: "prod", Host: "ssh://root@prod", SSHKeyPath: "/k/id_prod"},
			{Name: "pw", Host: "ssh://u@pw", SSHAuth: hosts.SSHAuthPassword},
			{Name: "secure", Host: "tcp://secure:2376", TLSCACert: "ca", TLSCert: "cert", TLSKey: "key"},
		}, nil)
	}
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}

	tests := []struct {
		name      string
		cfg       config.Config
		getenv    func(string) string
		wantLabel string
		wantCode  agent.Code // "" = success
		check     func(t *testing.T, cfg config.Config)
	}{
		{"saved key host", config.Config{SavedHost: "prod", Host: config.DefaultHost, SSHPassword: "stale"}, env(), "prod", "",
			func(t *testing.T, c config.Config) {
				if c.Host != "ssh://root@prod" || c.SSHKeyFile != "/k/id_prod" || c.SSHPassword != "" {
					t.Errorf("cfg = %+v", c)
				}
			}},
		{"saved TLS host", config.Config{SavedHost: "secure", TLSCACert: "flag-ca"}, env(), "secure", "",
			func(t *testing.T, c config.Config) {
				if c.Host != "tcp://secure:2376" || c.TLSCACert != "ca" || c.TLSCert != "cert" || c.TLSKey != "key" {
					t.Errorf("cfg = %+v", c)
				}
			}},
		{"saved password host without env", config.Config{SavedHost: "pw"}, env(), "", agent.CodeAuthRequired, nil},
		{"saved password host with env", config.Config{SavedHost: "pw", SSHPassword: "from-flag"}, env(passwordEnv, "s3cret"), "pw", "",
			func(t *testing.T, c config.Config) {
				if c.Host != "ssh://u@pw" || c.SSHPassword != "s3cret" {
					t.Errorf("cfg = %+v", c)
				}
			}},
		{"unknown saved host", config.Config{SavedHost: "nope"}, env(), "", agent.CodeNotFound, nil},
		{"saved host with -H", config.Config{SavedHost: "prod", Host: "tcp://x", HostFlagSet: true}, env(), "", agent.CodeUsage, nil},
		{"saved host with -context", config.Config{SavedHost: "prod", Context: "lab"}, env(), "", agent.CodeUsage, nil},
		{"saved host ignores DOCKER_CONTEXT", config.Config{SavedHost: "prod"}, env("DOCKER_CONTEXT", "lab"), "prod", "",
			func(t *testing.T, c config.Config) {
				if c.Host != "ssh://root@prod" {
					t.Errorf("cfg = %+v", c)
				}
			}},
		{"-H matching a saved host", config.Config{Host: "tcp://secure:2376", HostFlagSet: true}, env(), "secure", "",
			func(t *testing.T, c config.Config) {
				if c.TLSCACert != "ca" {
					t.Errorf("saved TLS not reused: %+v", c)
				}
			}},
		{"-H unknown URL", config.Config{Host: "tcp://other:2375", HostFlagSet: true}, env(passwordEnv, "pw1"), "tcp://other:2375", "",
			func(t *testing.T, c config.Config) {
				if c.SSHPassword != "pw1" {
					t.Errorf("D9C_SSH_PASSWORD not applied: %+v", c)
				}
			}},
		{"-context", config.Config{Context: "lab", Host: config.DefaultHost}, env(), "lab", "",
			func(t *testing.T, c config.Config) {
				if c.Host != "ssh://ops@lab" {
					t.Errorf("cfg = %+v", c)
				}
			}},
		{"DOCKER_CONTEXT", config.Config{Host: config.DefaultHost}, env("DOCKER_CONTEXT", "lab"), "lab", "", nil},
		{"unknown context", config.Config{Context: "nope"}, env(), "", agent.CodeUsage, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			label, err := resolveCLITarget(&cfg, store(), tt.getenv, ctxDir)
			if tt.wantCode != "" {
				var ae *agent.Error
				if !errors.As(err, &ae) || ae.Code != tt.wantCode {
					t.Fatalf("err = %v, want code %s", err, tt.wantCode)
				}
				if ae.Hint == "" {
					t.Errorf("error %q has no hint", ae.Message)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if label != tt.wantLabel {
				t.Errorf("label = %q, want %q", label, tt.wantLabel)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

// writeCLIConfig writes a unified config with the given saved hosts YAML
// (the `hosts:` list body) and returns its path.
func writeCLIConfig(t *testing.T, hostsYAML string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "d9c-config.yaml")
	if err := os.WriteFile(path, []byte("hosts:\n"+hostsYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// cliRun runs runCLI on a fresh config and returns the exit status and output.
func cliRun(t *testing.T, cfg *config.Config, args ...string) (int, string, string) {
	t.Helper()
	t.Cleanup(func() { docker.SetStrictHostKeys(false) })
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv(passwordEnv, "")
	var stdout, stderr bytes.Buffer
	code := runCLI(cfg, agent.Builtin(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunCLI(t *testing.T) {
	cfgPath := writeCLIConfig(t, "  - name: prod\n    host: tcp://127.0.0.1:1\n    read_only: true\n  - name: pw\n    host: ssh://u@pw.invalid\n    ssh_auth: password\n")

	t.Run("demo info", func(t *testing.T) {
		code, out, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath, Host: config.DefaultHost}, "info", "-demo")
		if code != 0 || errOut != "" {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		var env struct {
			Schema  int
			Host    string
			Runtime string
			Data    agent.InfoData
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("stdout is not JSON: %v (%q)", err, out)
		}
		want, _ := docker.NewFakeBackend().Info()
		if env.Schema != agent.SchemaVersion || env.Host != "demo" || env.Runtime != "docker" || env.Data.Containers != want.Containers {
			t.Errorf("envelope = %+v", env)
		}
	})

	t.Run("lifecycle text output", func(t *testing.T) {
		code, out, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath}, "stop", "web", "ghost", "-o", "text", "-demo", "--allow-write")
		if code != agent.ExitPartial {
			t.Fatalf("exit %d, want %d (stderr %q)", code, agent.ExitPartial, errOut)
		}
		for _, want := range []string{"REF", "RESULT", "web", "ok", "ghost", "failed", "not_found"} {
			if !strings.Contains(out, want) {
				t.Errorf("text output lacks %q:\n%s", want, out)
			}
		}
		if !strings.Contains(errOut, "partial failure: 1 of 2 containers failed") {
			t.Errorf("stderr = %q, want the partial notice", errOut)
		}
	})

	t.Run("hosts ls needs no connection", func(t *testing.T) {
		// -H points nowhere: a local operation must not dial it.
		code, out, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath, Host: "tcp://127.0.0.1:1", HostFlagSet: true}, "hosts", "ls")
		if code != 0 || errOut != "" {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		want := `{"schema":1,"host":"","runtime":"","data":[{"name":"prod","host":"tcp://127.0.0.1:1","read_only":true,"agent_tier":"read"},{"name":"pw","host":"ssh://u@pw.invalid","read_only":false,"agent_tier":"read"}],"truncated":false}` + "\n"
		if out != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}
	})

	// Each read command runs end to end on the demo backend: exit 0, one JSON
	// envelope on stdout, nothing on stderr.
	t.Run("demo read commands", func(t *testing.T) {
		for _, args := range [][]string{
			{"ps", "-a"}, {"inspect", "web"}, {"inspect", "--type", "image", "a08c488a9779"},
			{"logs", "web", "-n", "2"}, {"logs", "web", "--grep", "ERROR"},
			{"images"}, {"networks"}, {"volumes"}, {"system", "df"}, {"events", "--since", "1h"},
			{"compose", "ls"}, {"compose", "ps", "webapp"}, {"compose", "config", "webapp"},
			{"compose", "logs", "webapp", "--tail", "1"},
		} {
			code, out, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath}, append(args, "-demo")...)
			var env agent.Envelope
			if code != 0 || errOut != "" || json.Unmarshal([]byte(out), &env) != nil || env.Data == nil {
				t.Errorf("%q: exit %d, stdout %q, stderr %q", args, code, out, errOut)
			}
		}
		code, _, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath}, "logs", "ghost", "-demo")
		if code != agent.CodeNotFound.ExitCode() || !strings.Contains(errOut, `"not_found"`) {
			t.Errorf("logs ghost: exit %d, stderr %q", code, errOut)
		}
	})

	t.Run("output options", func(t *testing.T) {
		code, out, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath}, "ps", "-a", "-demo", "-o", "text", "--fields", "name,state", "--limit", "1")
		if code != 0 || out != "NAME  STATE\nweb   running\n" || !strings.HasPrefix(errOut, "truncated: showing the first 1 of 3") {
			t.Errorf("text: exit %d, stdout %q, stderr %q", code, out, errOut)
		}
		code, out, errOut = cliRun(t, &config.Config{ConfigFile: cfgPath}, "logs", "web", "-n", "2", "-demo", "-o", "ndjson")
		if lines := strings.Split(strings.TrimSpace(out), "\n"); code != 0 || errOut != "" || len(lines) != 3 || !strings.Contains(lines[2], `"truncated":true`) {
			t.Errorf("ndjson: exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("help", func(t *testing.T) {
		code, out, _ := cliRun(t, &config.Config{ConfigFile: cfgPath}, "help")
		if code != 0 || !strings.Contains(out, "hosts ls") || !strings.Contains(out, "info") {
			t.Errorf("exit %d, stdout %q", code, out)
		}
	})

	errorCases := []struct {
		name     string
		cfg      config.Config
		args     []string
		wantCode agent.Code
	}{
		{"usage", config.Config{ConfigFile: cfgPath}, []string{"info", "--bogus"}, agent.CodeUsage},
		{"unknown field", config.Config{ConfigFile: cfgPath}, []string{"ps", "-demo", "--fields", "bogus"}, agent.CodeUsage},
		{"limit on object", config.Config{ConfigFile: cfgPath}, []string{"info", "-demo", "--limit", "1"}, agent.CodeUsage},
		{"unknown saved host", config.Config{ConfigFile: cfgPath}, []string{"info", "--host", "nope"}, agent.CodeNotFound},
		{"password host without env", config.Config{ConfigFile: cfgPath}, []string{"info", "--host", "pw"}, agent.CodeAuthRequired},
		{"unreachable saved host", config.Config{ConfigFile: cfgPath}, []string{"info", "--host", "prod"}, agent.CodeConnection},
		{"missing container", config.Config{ConfigFile: cfgPath}, []string{"inspect", "ghost", "-demo"}, agent.CodeNotFound},
		{"broken config", config.Config{ConfigFile: writeCLIConfig(t, "  - [")}, []string{"info", "-demo"}, agent.CodeInternal},
	}
	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			code, out, errOut := cliRun(t, &cfg, tt.args...)
			if out != "" {
				t.Errorf("stdout = %q on failure, want empty", out)
			}
			var doc agent.ErrorEnvelope
			if err := json.Unmarshal([]byte(errOut), &doc); err != nil || doc.Error == nil {
				t.Fatalf("stderr is not an error envelope: %v (%q)", err, errOut)
			}
			if doc.Error.Code != tt.wantCode || code != tt.wantCode.ExitCode() {
				t.Errorf("code = %s exit %d, want %s exit %d", doc.Error.Code, code, tt.wantCode, tt.wantCode.ExitCode())
			}
			if doc.Error.Message == "" || strings.Contains(doc.Error.Message, "Error response from daemon") {
				t.Errorf("message = %q, want a non-empty message without the daemon preamble", doc.Error.Message)
			}
		})
	}
}

// With no host at all the CLI tries the default socket (like the docker CLI)
// and, failing that, says how to pick a host.
func TestRunCLIDefaultHostHint(t *testing.T) {
	if _, err := os.Stat(strings.TrimPrefix(config.DefaultHost, "unix://")); err == nil {
		t.Skip("a local Docker socket exists; the default host would answer")
	}
	cfg := &config.Config{ConfigFile: writeCLIConfig(t, ""), Host: config.DefaultHost}
	code, _, errOut := cliRun(t, cfg, "info")
	if code != agent.CodeConnection.ExitCode() || !strings.Contains(errOut, "no host was given") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

// TestRunCLIPolicy drives the agent write policy end to end. The saved hosts
// point at closed ports, so a command the policy admits fails with
// connection and a refused one with forbidden — before any dial.
func TestRunCLIPolicy(t *testing.T) {
	cfgPath := writeCLIConfig(t, `  - name: prod
    host: tcp://127.0.0.1:1
    read_only: true
  - name: dev
    host: tcp://127.0.0.1:2
    agent:
      allowWrite: true
  - name: locked
    host: tcp://127.0.0.1:3
    agent:
      allowWrite: false
  - name: wide
    host: tcp://127.0.0.1:4
    agent:
      maxTier: destructive
agent:
  maxTier: safe
`)
	tests := []struct {
		name     string
		args     []string
		wantCode agent.Code // "" = success
		wantErr  string
	}{
		{"default refuses writes", []string{"stop", "web", "-demo"}, agent.CodeForbidden, "off by default"},
		{"flag allows safe", []string{"stop", "web", "-demo", "--allow-write"}, "", ""},
		{"flag stops at maxTier", []string{"rm", "web", "-demo", "--allow-write"}, agent.CodeForbidden, "above agent.maxTier safe (set globally)"},
		{"read-only flag wins", []string{"stop", "web", "-demo", "--allow-write", "-read-only"}, agent.CodeForbidden, "-read-only flag"},
		{"host read_only wins", []string{"stop", "web", "--host", "prod", "--allow-write"}, agent.CodeForbidden, `saved host \"prod\" has read_only: true`},
		{"host allowWrite admits safe", []string{"stop", "web", "--host", "dev"}, agent.CodeConnection, ""},
		{"host keeps global maxTier", []string{"rm", "web", "--host", "dev"}, agent.CodeForbidden, "above agent.maxTier safe"},
		{"explicit false beats flag", []string{"stop", "web", "--host", "locked", "--allow-write"}, agent.CodeForbidden, `allowWrite is false for saved host \"locked\"`},
		{"-H matches saved host", []string{"stop", "web", "-H", "tcp://127.0.0.1:3", "--allow-write"}, agent.CodeForbidden, "allowWrite is false"},
		{"host maxTier with flag", []string{"rm", "web", "--host", "wide", "--allow-write"}, agent.CodeConnection, ""},
		{"maxTier alone keeps writes off", []string{"rm", "web", "--host", "wide"}, agent.CodeForbidden, "off by default"},
		{"reads need no permission", []string{"ps", "--host", "locked"}, agent.CodeConnection, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath}, tt.args...)
			want := 0
			if tt.wantCode != "" {
				want = tt.wantCode.ExitCode()
			}
			if code != want || (tt.wantCode != "" && !strings.Contains(errOut, `"`+string(tt.wantCode)+`"`)) {
				t.Fatalf("exit %d, want %d (%s); stderr %q", code, want, tt.wantCode, errOut)
			}
			if !strings.Contains(errOut, tt.wantErr) {
				t.Errorf("stderr %q lacks %q", errOut, tt.wantErr)
			}
		})
	}

	t.Run("invalid maxTier", func(t *testing.T) {
		bad := writeCLIConfig(t, "  - name: x\n    host: tcp://127.0.0.1:1\nagent:\n  maxTier: all\n")
		code, _, errOut := cliRun(t, &config.Config{ConfigFile: bad}, "ps", "-demo")
		if code != agent.CodeUsage.ExitCode() || !strings.Contains(errOut, `unknown tier \"all\"`) {
			t.Errorf("exit %d, stderr %q", code, errOut)
		}
	})

	t.Run("help follows the policy", func(t *testing.T) {
		_, out, _ := cliRun(t, &config.Config{ConfigFile: cfgPath}, "help")
		if strings.Contains(out, "  stop ") || !strings.Contains(out, "commands that change state are hidden (allowed tier: read)") {
			t.Errorf("default help:\n%s", out)
		}
		_, out, _ = cliRun(t, &config.Config{ConfigFile: cfgPath, SavedHost: "dev"}, "help")
		if !strings.Contains(out, "  stop ") || strings.Contains(out, "  rm ") || !strings.Contains(out, "allowed tier: safe") {
			t.Errorf("help for dev:\n%s", out)
		}
	})

	t.Run("hosts ls reports each ceiling", func(t *testing.T) {
		_, out, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath}, "hosts", "ls", "--fields", "name,agent_tier", "-o", "text")
		want := "NAME    AGENT_TIER\nprod    read\ndev     safe\nlocked  read\nwide    read\n"
		if out != want {
			t.Errorf("stdout %q, want %q (stderr %q)", out, want, errOut)
		}
	})
}

// run() hands a known command to the CLI and turns its status into exitCode;
// an unknown first word keeps the TUI path (here failing on a broken config
// before any terminal is touched).
func TestRunDispatch(t *testing.T) {
	cfgPath := writeCLIConfig(t, "  - name: prod\n    host: tcp://127.0.0.1:1\n")
	t.Cleanup(func() { docker.SetStrictHostKeys(false) })

	// Silence the CLI's stdout/stderr.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	t.Cleanup(func() { os.Stdout, os.Stderr = oldOut, oldErr; _ = devnull.Close() })

	if err := runWithArgs(t, "-config", cfgPath, "hosts", "ls"); err != nil {
		t.Errorf("hosts ls: err = %v, want nil", err)
	}
	if err := runWithArgs(t, "-demo", "-config", cfgPath, "info"); err != nil {
		t.Errorf("-demo info: err = %v, want nil", err)
	}
	var code exitCode
	if err := runWithArgs(t, "-config", cfgPath, "info", "--bogus"); !errors.As(err, &code) || int(code) != 2 {
		t.Errorf("usage error: err = %v, want exitCode(2)", err)
	}
	if err := runWithArgs(t, "-config", cfgPath, "info", "--host", "nope"); !errors.As(err, &code) || int(code) != 3 {
		t.Errorf("unknown host: err = %v, want exitCode(3)", err)
	}
	if got := exitCode(4).Error(); got != "exit status 4" {
		t.Errorf("exitCode.Error() = %q", got)
	}

	// Not a command: the TUI path runs and reports the broken config itself.
	broken := writeCLIConfig(t, "  - [")
	if err := runWithArgs(t, "-config", broken, "not-a-command"); err == nil || errors.As(err, &code) || !strings.Contains(err.Error(), "loading config") {
		t.Errorf("TUI path: err = %v, want a plain loading-config error", err)
	}
}

// -host also works in TUI mode: the saved host is applied before connecting,
// and conflicts or unknown names stop startup with the same messages.
func TestRunSavedHostTUIErrors(t *testing.T) {
	cfgPath := writeCLIConfig(t, "  - name: prod\n    host: tcp://127.0.0.1:1\n")
	if err := runWithArgs(t, "-config", cfgPath, "-host", "nope"); err == nil || !strings.Contains(err.Error(), `no saved host named "nope"`) {
		t.Errorf("unknown host: err = %v", err)
	}
	if err := runWithArgs(t, "-config", cfgPath, "-host", "prod", "-H", "tcp://x:1"); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("conflict: err = %v", err)
	}
}

func TestApplySavedHost(t *testing.T) {
	store := hosts.NewStore([]hosts.Host{
		{Name: "pw", Host: "ssh://u@pw", SSHAuth: hosts.SSHAuthPassword},
		{Name: "plain", Host: "ssh://u@plain"},
	}, nil)

	// Password hosts keep the password the caller supplied (-ssh-password).
	cfg := &config.Config{SavedHost: "pw", SSHPassword: "flag", SSHKeyFile: "/flag/key"}
	if _, err := applySavedHost(cfg, store); err != nil || cfg.SSHPassword != "flag" || cfg.Host != "ssh://u@pw" {
		t.Errorf("password host: err=%v cfg=%+v", err, cfg)
	}
	// A host without a stored key keeps -ssh-key; key auth drops the password.
	cfg = &config.Config{SavedHost: "plain", SSHPassword: "flag", SSHKeyFile: "/flag/key"}
	if _, err := applySavedHost(cfg, store); err != nil || cfg.SSHPassword != "" || cfg.SSHKeyFile != "/flag/key" {
		t.Errorf("key host: err=%v cfg=%+v", err, cfg)
	}
}

// Write commands leave one audit line each, whatever their outcome; read
// commands leave none; and a write command does not run without a log.
func TestRunCLIAudit(t *testing.T) {
	cfgPath := writeCLIConfig(t, "  - name: prod\n    host: tcp://127.0.0.1:1\n")
	logPath := filepath.Join(filepath.Dir(cfgPath), agent.AuditFileName)
	runs := []struct {
		args       []string
		wantExit   int
		wantResult string
		wantCode   agent.Code
	}{
		{[]string{"ps", "-demo"}, 0, "", ""},
		{[]string{"stop", "web", "-demo"}, 5, agent.AuditError, agent.CodeForbidden},
		{[]string{"stop", "web", "db", "-demo", "--allow-write"}, 0, agent.AuditOK, ""},
		{[]string{"rm", "--filter", "status=exited", "-demo", "--dry-run"}, 0, agent.AuditPlanned, ""},
		{[]string{"stop", "web", "--host", "prod", "--allow-write"}, 4, agent.AuditError, agent.CodeConnection},
		{[]string{"stop", "web", "--host", "nope", "--allow-write"}, 3, agent.AuditError, agent.CodeNotFound},
	}
	var want []agent.AuditEntry
	for _, r := range runs {
		if code, _, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath}, r.args...); code != r.wantExit {
			t.Fatalf("%v: exit %d, want %d (%s)", r.args, code, r.wantExit, errOut)
		}
		if r.wantResult != "" {
			want = append(want, agent.AuditEntry{Result: r.wantResult, Code: r.wantCode})
		}
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("audit log has %d lines, want %d:\n%s", len(lines), len(want), raw)
	}
	for i, line := range lines {
		var e agent.AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Result != want[i].Result || e.Code != want[i].Code || e.Time.IsZero() || e.Op == "" {
			t.Errorf("line %d = %s, want result %s code %q", i, line, want[i].Result, want[i].Code)
		}
	}
	var stop agent.AuditEntry
	_ = json.Unmarshal([]byte(lines[1]), &stop)
	if stop.Host != "demo" || !reflect.DeepEqual(stop.Targets, []string{"web (9ae942fd8fbc)", "db (3f1ab77c9012)"}) {
		t.Errorf("stop entry = %+v", stop)
	}
	var unreachable agent.AuditEntry
	_ = json.Unmarshal([]byte(lines[3]), &unreachable)
	var missing agent.AuditEntry
	_ = json.Unmarshal([]byte(lines[4]), &missing)
	if unreachable.Host != "prod" || missing.Host != "nope" {
		t.Errorf("hosts = %q, %q, want prod, nope", unreachable.Host, missing.Host)
	}

	// No log, no write: the command fails before dialing the host.
	blockedCfg := writeCLIConfig(t, "")
	if err := os.Mkdir(filepath.Join(filepath.Dir(blockedCfg), agent.AuditFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := cliRun(t, &config.Config{ConfigFile: blockedCfg}, "stop", "web", "-H", "tcp://127.0.0.1:1", "--allow-write")
	if code != 1 || !strings.Contains(errOut, "audit log") {
		t.Errorf("blocked log: exit %d, stderr %q", code, errOut)
	}
	if code, _, _ := cliRun(t, &config.Config{ConfigFile: blockedCfg}, "ps", "-demo"); code != 0 {
		t.Errorf("read command with a blocked log: exit %d", code)
	}
}
