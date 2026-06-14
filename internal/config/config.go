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
	HostsFile       string
	PluginsFile     string
	ConfigFile      string
	RefreshInterval time.Duration
}

func Load() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.Host, "H", getenv("DOCKER_HOST", DefaultHost), "Docker host (tcp://host:port or ssh://user@host)")
	flag.StringVar(&cfg.TLSCACert, "tlscacert", getenv("DOCKER_TLS_CACERT", ""), "TLS CA certificate")
	flag.StringVar(&cfg.TLSCert, "tlscert", getenv("DOCKER_TLS_CERT", ""), "TLS certificate")
	flag.StringVar(&cfg.TLSKey, "tlskey", getenv("DOCKER_TLS_KEY", ""), "TLS key")
	flag.StringVar(&cfg.SSHKeyFile, "ssh-key", getenv("DOCKER_SSH_KEY", ""), "Path to SSH private key")
	flag.StringVar(&cfg.SSHPassword, "ssh-password", getenv("DOCKER_SSH_PASSWORD", ""), "SSH password (insecure, prefer key auth)")
	flag.BoolVar(&cfg.ShowAll, "a", false, "Show all containers (default: running only)")
	flag.BoolVar(&cfg.Demo, "demo", false, "Run with built-in sample data (no Docker connection)")
	flag.StringVar(&cfg.HostsFile, "hosts-file", "", "Path to the saved-hosts file (default: next to the binary)")
	flag.StringVar(&cfg.PluginsFile, "plugins-file", "", "Path to the plugins file (default: next to the binary)")
	flag.StringVar(&cfg.ConfigFile, "config", "", "Path to the config file with theme/colors/keybindings (default: next to the binary)")
	flag.DurationVar(&cfg.RefreshInterval, "interval", DefaultRefreshInterval, "Auto-refresh interval (e.g. 1s, 5s); toggle pause at runtime with 'p'")
	flag.Parse()

	return cfg
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
