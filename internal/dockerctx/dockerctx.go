// Package dockerctx reads the Docker CLI context store (~/.docker/contexts) so
// d9c can import `docker context` endpoints as saved hosts and start straight
// into one with -context / DOCKER_CONTEXT.
//
// The store layout mirrors the Docker CLI: every context lives under
// contexts/meta/<sha256(name)>/meta.json, and its TLS material (if any) under
// contexts/tls/<sha256(name)>/docker/{ca,cert,key}.pem. The active context is
// the "currentContext" key of config.json. Only the "docker" endpoint is read.
package dockerctx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kirg0/d9c/internal/hosts"
)

// DefaultName is the implicit context backed by DOCKER_HOST / the local
// socket. It has no on-disk metadata and is never listed or imported.
const DefaultName = "default"

// ErrNotFound is wrapped when a named context has no metadata in the store.
var ErrNotFound = errors.New("not found")

// Context is one Docker CLI context reduced to what d9c needs to connect.
type Context struct {
	Name        string
	Description string
	// Host is the docker endpoint URL (tcp://, ssh://, unix://, npipe://).
	Host string
	// SkipTLSVerify mirrors the endpoint's SkipTLSVerify flag. d9c does not
	// support skipping verification; callers surface it as a warning.
	SkipTLSVerify bool
	// TLSCACert/TLSCert/TLSKey are paths to the context's TLS files; each is
	// empty when the file is absent from the store.
	TLSCACert string
	TLSCert   string
	TLSKey    string
	// Current is true for the context named by config.json "currentContext".
	Current bool
}

// HasTLS reports whether the context ships any TLS material.
func (c Context) HasTLS() bool {
	return c.TLSCACert != "" || c.TLSCert != "" || c.TLSKey != ""
}

// Dir returns the Docker CLI config directory: $DOCKER_CONFIG when set,
// otherwise ~/.docker. It returns "" when the home directory is unknown.
func Dir() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker")
}

// ID returns the store directory name of a context: hex sha256 of its name.
func ID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// metaFile is the subset of meta.json that d9c reads.
type metaFile struct {
	Name     string `json:"Name"`
	Metadata struct {
		Description string `json:"Description"`
	} `json:"Metadata"`
	Endpoints map[string]struct {
		Host          string `json:"Host"`
		SkipTLSVerify bool   `json:"SkipTLSVerify"`
	} `json:"Endpoints"`
}

// List returns every stored context with a docker endpoint, sorted by name.
// A missing store yields no contexts (not an error); an unreadable or
// malformed meta.json is skipped so one broken context doesn't hide the rest.
func List(dir string) ([]Context, error) {
	metaDir := filepath.Join(dir, "contexts", "meta")
	entries, err := os.ReadDir(metaDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read docker contexts: %w", err)
	}
	current := CurrentName(dir)
	var out []Context
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := load(dir, e.Name())
		if err != nil || c.Host == "" {
			continue
		}
		c.Current = c.Name == current
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Find loads the context with the given name. A context without a docker
// endpoint, or one missing from the store, wraps ErrNotFound. Every error
// names the context.
func Find(dir, name string) (Context, error) {
	c, err := load(dir, ID(name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Context{}, fmt.Errorf("docker context %q %w (see `docker context ls`)", name, ErrNotFound)
		}
		return Context{}, fmt.Errorf("docker context %q: %w", name, err)
	}
	if c.Host == "" {
		return Context{}, fmt.Errorf("docker context %q %w: it has no docker endpoint", name, ErrNotFound)
	}
	c.Current = c.Name == CurrentName(dir)
	return c, nil
}

// load parses contexts/meta/<id>/meta.json and attaches the TLS file paths
// present under contexts/tls/<id>/docker.
func load(dir, id string) (Context, error) {
	data, err := os.ReadFile(filepath.Join(dir, "contexts", "meta", id, "meta.json"))
	if err != nil {
		return Context{}, fmt.Errorf("read context metadata: %w", err)
	}
	var mf metaFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return Context{}, fmt.Errorf("parse context metadata %s: %w", id, err)
	}
	c := Context{Name: mf.Name, Description: mf.Metadata.Description}
	if ep, ok := mf.Endpoints["docker"]; ok {
		c.Host = strings.TrimSpace(ep.Host)
		c.SkipTLSVerify = ep.SkipTLSVerify
	}
	tlsDir := filepath.Join(dir, "contexts", "tls", id, "docker")
	c.TLSCACert = existing(filepath.Join(tlsDir, "ca.pem"))
	c.TLSCert = existing(filepath.Join(tlsDir, "cert.pem"))
	c.TLSKey = existing(filepath.Join(tlsDir, "key.pem"))
	return c, nil
}

// existing returns path when it names a regular file, "" otherwise.
func existing(path string) string {
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return path
	}
	return ""
}

// CurrentName returns config.json's "currentContext", or "" when unset or the
// file is missing/unreadable.
func CurrentName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return ""
	}
	var cf struct {
		CurrentContext string `json:"currentContext"`
	}
	if json.Unmarshal(data, &cf) != nil {
		return ""
	}
	return cf.CurrentContext
}

// Select picks the context d9c should start with, following the Docker CLI
// precedence: an explicit -context flag wins (and conflicts with an explicit
// -H); otherwise an explicit -H or DOCKER_HOST disables contexts; otherwise
// DOCKER_CONTEXT names one. The "default" context, or no selection at all,
// returns "". config.json's currentContext is deliberately NOT consulted, so a
// bare launch keeps opening the Hosts view instead of auto-connecting.
func Select(flagContext string, hostFlagSet bool, getenv func(string) string) (string, error) {
	name := strings.TrimSpace(flagContext)
	if name != "" {
		if hostFlagSet {
			return "", errors.New("conflicting options: either specify -H or -context, not both")
		}
	} else {
		if hostFlagSet || getenv("DOCKER_HOST") != "" {
			return "", nil
		}
		name = strings.TrimSpace(getenv("DOCKER_CONTEXT"))
	}
	if name == DefaultName {
		return "", nil
	}
	return name, nil
}

// SavedHost converts the context into a hosts entry named after the context,
// carrying its TLS file paths (kept only for tcp:// endpoints by the store).
func (c Context) SavedHost() hosts.Host {
	return hosts.Host{
		Name:      c.Name,
		Host:      c.Host,
		TLSCACert: c.TLSCACert,
		TLSCert:   c.TLSCert,
		TLSKey:    c.TLSKey,
	}
}
