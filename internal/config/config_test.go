package config

import (
	"flag"
	"os"
	"testing"
	"time"
)

func TestGetenv_ReturnsEnvVar(t *testing.T) {
	os.Setenv("TEST_VAR", "hello")
	defer os.Unsetenv("TEST_VAR")

	got := getenv("TEST_VAR", "fallback")
	if got != "hello" {
		t.Errorf("getenv() = %q, want %q", got, "hello")
	}
}

func TestGetenv_ReturnsFallback(t *testing.T) {
	os.Unsetenv("TEST_VAR_MISSING")
	got := getenv("TEST_VAR_MISSING", "fallback")
	if got != "fallback" {
		t.Errorf("getenv() = %q, want %q", got, "fallback")
	}
}

// loadWithArgs runs Load against a fresh global FlagSet and fake os.Args,
// restoring both afterwards (the flag package registers into globals).
func loadWithArgs(t *testing.T, args ...string) *Config {
	t.Helper()
	oldCmdLine, oldArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = oldCmdLine, oldArgs })
	flag.CommandLine = flag.NewFlagSet("d9c-test", flag.ContinueOnError)
	os.Args = append([]string{"d9c"}, args...)
	return Load()
}

func TestLoad_Defaults(t *testing.T) {
	for _, v := range []string{"DOCKER_HOST", "DOCKER_TLS_CACERT", "DOCKER_TLS_CERT", "DOCKER_TLS_KEY", "DOCKER_SSH_KEY", "DOCKER_SSH_PASSWORD"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	cfg := loadWithArgs(t)
	if cfg.Host != DefaultHost {
		t.Errorf("Host = %q, want %q", cfg.Host, DefaultHost)
	}
	if cfg.RefreshInterval != DefaultRefreshInterval {
		t.Errorf("RefreshInterval = %v, want %v", cfg.RefreshInterval, DefaultRefreshInterval)
	}
	if cfg.ShowAll || cfg.Demo || cfg.ShowVersion {
		t.Error("bool flags should default to false")
	}
	if cfg.TLSCACert != "" || cfg.SSHKeyFile != "" || cfg.SSHPassword != "" {
		t.Error("path/credential flags should default to empty")
	}
}

func TestLoad_Flags(t *testing.T) {
	cfg := loadWithArgs(t,
		"-H", "tcp://box:2375",
		"-tlscacert", "ca.pem", "-tlscert", "cert.pem", "-tlskey", "key.pem",
		"-ssh-key", "id_ed25519", "-ssh-password", "pw",
		"-a", "-demo", "-version",
		"-hosts-file", "hosts.json", "-plugins-file", "plugins.yml", "-config", "d9c.yml",
		"-interval", "5s",
	)
	if cfg.Host != "tcp://box:2375" {
		t.Errorf("Host = %q", cfg.Host)
	}
	if cfg.TLSCACert != "ca.pem" || cfg.TLSCert != "cert.pem" || cfg.TLSKey != "key.pem" {
		t.Errorf("TLS = %q/%q/%q", cfg.TLSCACert, cfg.TLSCert, cfg.TLSKey)
	}
	if cfg.SSHKeyFile != "id_ed25519" || cfg.SSHPassword != "pw" {
		t.Errorf("SSH = %q/%q", cfg.SSHKeyFile, cfg.SSHPassword)
	}
	if !cfg.ShowAll || !cfg.Demo || !cfg.ShowVersion {
		t.Error("bool flags should be set")
	}
	if cfg.HostsFile != "hosts.json" || cfg.PluginsFile != "plugins.yml" || cfg.ConfigFile != "d9c.yml" {
		t.Errorf("files = %q/%q/%q", cfg.HostsFile, cfg.PluginsFile, cfg.ConfigFile)
	}
	if cfg.RefreshInterval != 5*time.Second {
		t.Errorf("RefreshInterval = %v, want 5s", cfg.RefreshInterval)
	}
}

func TestLoad_EnvFallback(t *testing.T) {
	t.Setenv("DOCKER_HOST", "ssh://root@env-box")
	t.Setenv("DOCKER_SSH_KEY", "env_key")
	cfg := loadWithArgs(t)
	if cfg.Host != "ssh://root@env-box" {
		t.Errorf("Host = %q, want env value", cfg.Host)
	}
	if cfg.SSHKeyFile != "env_key" {
		t.Errorf("SSHKeyFile = %q, want env value", cfg.SSHKeyFile)
	}
}

func TestLoad_FlagOverridesEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "ssh://root@env-box")
	cfg := loadWithArgs(t, "-H", "tcp://flag-box:2375")
	if cfg.Host != "tcp://flag-box:2375" {
		t.Errorf("Host = %q, flag must win over env", cfg.Host)
	}
}
