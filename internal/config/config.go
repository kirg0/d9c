package config

import (
	"flag"
	"os"
	"time"
)

// DefaultHost is the Docker host used when neither -H nor DOCKER_HOST is set.
const DefaultHost = "unix:///var/run/docker.sock"

// DefaultRefreshInterval is the auto-refresh cadence used when -interval is not set.
const DefaultRefreshInterval = 3 * time.Second

type Config struct {
	Host            string
	TLSCACert       string
	TLSCert         string
	TLSKey          string
	SSHKeyFile      string
	SSHPassword     string
	ShowAll         bool
	Demo            bool
	ShowVersion     bool
	HostsFile       string
	PluginsFile     string
	ConfigFile      string
	RefreshInterval time.Duration
	// ReadOnly forbids every mutating action (stop/rm/prune/run/exec/…) for the
	// whole session, regardless of the config file and per-host settings.
	ReadOnly bool
	// AllowWrite lets agent CLI commands change state for this invocation, up
	// to the configured agent.maxTier, unless the config sets allowWrite: false.
	AllowWrite bool
	// Context names the Docker CLI context to start with (-context). Empty
	// means none; DOCKER_CONTEXT is resolved later, honoring DOCKER_HOST.
	Context string
	// SavedHost names a host from the saved-host store (-host) to connect to
	// with its stored SSH key and TLS files; it cannot be combined with
	// -H/-context.
	SavedHost string
	// AllHosts runs a CLI read command on every saved host (--all-hosts); a
	// comma-separated SavedHost list does the same for the named ones.
	AllHosts bool
	// HostFlagSet records whether -H was given explicitly on the command line
	// (as opposed to the DOCKER_HOST / default fallback), so -H and -context
	// can be reported as conflicting like the Docker CLI does.
	HostFlagSet bool
}

func Load() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.Host, "H", getenv("DOCKER_HOST", DefaultHost), "Docker host (tcp://host:port, ssh://user@host, nerdctl[+ssh]:// or crio[+ssh]://)")
	flag.StringVar(&cfg.TLSCACert, "tlscacert", getenv("DOCKER_TLS_CACERT", ""), "TLS CA certificate")
	flag.StringVar(&cfg.TLSCert, "tlscert", getenv("DOCKER_TLS_CERT", ""), "TLS certificate")
	flag.StringVar(&cfg.TLSKey, "tlskey", getenv("DOCKER_TLS_KEY", ""), "TLS key")
	flag.StringVar(&cfg.SSHKeyFile, "ssh-key", getenv("DOCKER_SSH_KEY", ""), "Path to SSH private key")
	flag.StringVar(&cfg.SSHPassword, "ssh-password", getenv("DOCKER_SSH_PASSWORD", ""), "SSH password (insecure, prefer key auth)")
	flag.BoolVar(&cfg.ShowAll, "a", false, "Show all containers (default: running only)")
	flag.BoolVar(&cfg.Demo, "demo", false, "Run with built-in sample data (no Docker connection)")
	flag.BoolVar(&cfg.ShowVersion, "version", false, "Print the version and exit")
	flag.StringVar(&cfg.HostsFile, "hosts-file", "", "Path to the legacy d9c-hosts.json to migrate into the config (default: next to the binary)")
	flag.StringVar(&cfg.PluginsFile, "plugins-file", "", "Path to the plugins file (default: ~/.d9c/d9c-plugins.yaml)")
	flag.StringVar(&cfg.ConfigFile, "config", "", "Path to the unified config file (theme/colors/keys/alerts/hosts; default: ~/.d9c/d9c-config.yaml)")
	flag.DurationVar(&cfg.RefreshInterval, "interval", DefaultRefreshInterval, "Auto-refresh interval (e.g. 1s, 5s); toggle pause at runtime with 'p'")
	flag.BoolVar(&cfg.ReadOnly, "read-only", false, "Read-only mode: forbid every mutating action (stop/kill/rm/prune/run/exec/cp/compose/build/push/create)")
	flag.BoolVar(&cfg.AllowWrite, "allow-write", false, "CLI commands only: allow state-changing commands for this invocation (up to agent.maxTier in the config, default safe)")
	flag.StringVar(&cfg.Context, "context", "", "Docker CLI context to connect with (from ~/.docker/contexts; overrides DOCKER_HOST and DOCKER_CONTEXT)")
	flag.StringVar(&cfg.SavedHost, "host", "", "Saved host to connect to, by name, with its stored SSH key and TLS files (list them with: d9c hosts ls); CLI read commands take a comma-separated list")
	flag.BoolVar(&cfg.AllHosts, "all-hosts", false, "CLI read commands only: run on every saved host")
	flag.Parse()
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "H" {
			cfg.HostFlagSet = true
		}
	})

	return cfg
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
