// Package hosts persists the list of Docker hosts the user has connected to,
// in a small JSON file stored next to the d9c binary.
package hosts

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Host is a single saved connection target.
type Host struct {
	Name string `json:"name"`
	Host string `json:"host"`
}

// Store holds the saved hosts and the file they are persisted to.
type Store struct {
	path  string
	Hosts []Host
}

type fileFormat struct {
	Hosts []Host `json:"hosts"`
}

// DefaultPath returns the hosts file location next to the running binary,
// falling back to the current directory if the executable path is unavailable.
func DefaultPath() string {
	const name = "d9c-hosts.json"
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	return filepath.Join(filepath.Dir(exe), name)
}

// Load reads the hosts file at path. A missing file yields an empty store
// bound to that path (so a later Save creates it); malformed JSON is an error.
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read hosts file: %w", err)
	}
	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("parse hosts file %s: %w", path, err)
	}
	s.Hosts = ff.Hosts
	return s, nil
}

// Save writes the store back to its file.
func (s *Store) Save() error {
	data, err := json.MarshalIndent(fileFormat{Hosts: s.Hosts}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode hosts: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write hosts file: %w", err)
	}
	return nil
}

// Path returns the file the store is bound to.
func (s *Store) Path() string { return s.path }

// List returns a copy of the saved hosts.
func (s *Store) List() []Host {
	out := make([]Host, len(s.Hosts))
	copy(out, s.Hosts)
	return out
}

// Find returns the host with the given name.
func (s *Store) Find(name string) (Host, bool) {
	for _, h := range s.Hosts {
		if h.Name == name {
			return h, true
		}
	}
	return Host{}, false
}

// Add appends a new host, rejecting empty fields and duplicate names.
func (s *Store) Add(name, host string) error {
	name = strings.TrimSpace(name)
	host = strings.TrimSpace(host)
	if name == "" || host == "" {
		return fmt.Errorf("name and host are required")
	}
	if _, ok := s.Find(name); ok {
		return fmt.Errorf("host %q already exists", name)
	}
	s.Hosts = append(s.Hosts, Host{Name: name, Host: host})
	return nil
}

// Edit replaces the name and host URL of the entry identified by name.
func (s *Store) Edit(name, newName, newHost string) error {
	newName = strings.TrimSpace(newName)
	newHost = strings.TrimSpace(newHost)
	if newName == "" || newHost == "" {
		return fmt.Errorf("name and host are required")
	}
	idx := -1
	for i, h := range s.Hosts {
		if h.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("host %q not found", name)
	}
	if newName != name {
		if _, ok := s.Find(newName); ok {
			return fmt.Errorf("host %q already exists", newName)
		}
	}
	s.Hosts[idx] = Host{Name: newName, Host: newHost}
	return nil
}

// Remove deletes the entry identified by name.
func (s *Store) Remove(name string) error {
	for i, h := range s.Hosts {
		if h.Name == name {
			s.Hosts = append(s.Hosts[:i], s.Hosts[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("host %q not found", name)
}

// UpsertByHost ensures a host with the given URL exists, generating a unique
// name from the URL when adding. It returns true if a new entry was added.
func (s *Store) UpsertByHost(hostURL string) bool {
	hostURL = strings.TrimSpace(hostURL)
	if hostURL == "" {
		return false
	}
	for _, h := range s.Hosts {
		if h.Host == hostURL {
			return false
		}
	}
	s.Hosts = append(s.Hosts, Host{Name: uniqueName(s, deriveName(hostURL)), Host: hostURL})
	return true
}

// deriveName builds a readable label from a connection URL.
func deriveName(hostURL string) string {
	u, err := url.Parse(hostURL)
	if err != nil || u.Hostname() == "" {
		return hostURL
	}
	name := u.Hostname()
	if u.User != nil && u.User.Username() != "" {
		name = u.User.Username() + "@" + name
	}
	return name
}

// uniqueName appends a numeric suffix until the name is free in the store.
func uniqueName(s *Store, base string) string {
	if base == "" {
		base = "host"
	}
	name := base
	for i := 2; ; i++ {
		if _, ok := s.Find(name); !ok {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}
