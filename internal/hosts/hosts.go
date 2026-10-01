// Package hosts manages the list of Docker hosts the user has connected to.
// The in-memory store is the same regardless of where the data lives; it
// persists through an injected callback so the actual file format (today the
// unified d9c-config.yaml owned by internal/settings) stays out of this package.
// A legacy JSON reader is kept only to migrate the old standalone d9c-hosts.json.
package hosts

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/kirg0/d9c/internal/appdir"
)

// SSH authentication methods stored per host. The empty value means the legacy
// default (ssh-agent plus the keys in ~/.ssh), kept so configs written before
// this field stay backward compatible.
const (
	// SSHAuthKey authenticates with a private key — either the one at SSHKeyPath
	// or, when that is empty, ssh-agent / the default ~/.ssh keys.
	SSHAuthKey = "key"
	// SSHAuthPassword authenticates with a password prompted at connect time.
	// Only the login is saved; the password is never written to disk.
	SSHAuthPassword = "password"
)

// Host is a single saved connection target.
type Host struct {
	Name string `json:"name" yaml:"name"`
	Host string `json:"host" yaml:"host"`
	// SSHAuth selects the SSH authentication method (SSHAuthKey/SSHAuthPassword);
	// empty means key-based (agent + default keys). Ignored for non-ssh hosts.
	SSHAuth string `json:"ssh_auth,omitempty" yaml:"ssh_auth,omitempty"`
	// SSHKeyPath is the private-key path used when SSHAuth is key-based; empty
	// falls back to ssh-agent and the default ~/.ssh keys.
	SSHKeyPath string `json:"ssh_key_path,omitempty" yaml:"ssh_key_path,omitempty"`
	// ReadOnly forbids mutating actions while connected to this host. It is set
	// only in the config file (the host form doesn't expose it), so edits made
	// from the UI keep the stored value.
	ReadOnly bool `json:"read_only,omitempty" yaml:"read_only,omitempty"`
	// TLSCACert/TLSCert/TLSKey are client TLS file paths for a tcp:// host
	// (typically imported from a Docker context). Like ReadOnly they are not
	// exposed by the host form, so UI edits keep the stored values; they are
	// dropped when the URL is no longer tcp://.
	TLSCACert string `json:"tls_ca_cert,omitempty" yaml:"tls_ca_cert,omitempty"`
	TLSCert   string `json:"tls_cert,omitempty" yaml:"tls_cert,omitempty"`
	TLSKey    string `json:"tls_key,omitempty" yaml:"tls_key,omitempty"`
}

// HasTLS reports whether the host carries any client TLS material.
func (h Host) HasTLS() bool {
	return h.TLSCACert != "" || h.TLSCert != "" || h.TLSKey != ""
}

// Store holds the saved hosts and a callback that persists them. The zero value
// is a usable in-memory store with no persistence (Save is a no-op), which suits
// tests and demo mode.
type Store struct {
	Hosts   []Host
	persist func([]Host) error
}

// NewStore builds a store seeded with initial hosts that persists changes via
// the given callback. A nil callback yields an in-memory-only store.
func NewStore(initial []Host, persist func([]Host) error) *Store {
	return &Store{Hosts: append([]Host(nil), initial...), persist: persist}
}

// LegacyDefaultPath returns the location of the old standalone hosts file next
// to the running binary, used only for one-time migration into the config.
func LegacyDefaultPath() string {
	return appdir.LegacyPath("d9c-hosts.json")
}

type legacyFile struct {
	Hosts []Host `json:"hosts"`
}

// LoadLegacy reads the old standalone JSON hosts file at path. A missing file
// yields no hosts (not an error); malformed JSON is an error. It is used to
// migrate pre-unified-config installs.
func LoadLegacy(path string) ([]Host, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read hosts file: %w", err)
	}
	var ff legacyFile
	if err := json.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("parse hosts file %s: %w", path, err)
	}
	return ff.Hosts, nil
}

// Save persists the current hosts through the store's callback. With no callback
// (zero-value store) it is a no-op.
func (s *Store) Save() error {
	if s.persist == nil {
		return nil
	}
	return s.persist(s.List())
}

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

// AddHost appends h (carrying SSH auth metadata), rejecting empty Name/Host and
// duplicate names. It is the metadata-aware counterpart of Add.
func (s *Store) AddHost(h Host) error {
	h = h.normalized()
	if h.Name == "" || h.Host == "" {
		return fmt.Errorf("name and host are required")
	}
	if _, ok := s.Find(h.Name); ok {
		return fmt.Errorf("host %q already exists", h.Name)
	}
	s.Hosts = append(s.Hosts, h)
	return nil
}

// Edit replaces the name and host URL of the entry identified by name. It clears
// any stored SSH auth metadata; use EditHost to preserve or change it.
func (s *Store) Edit(name, newName, newHost string) error {
	return s.EditHost(name, Host{Name: newName, Host: newHost})
}

// EditHost replaces the entry identified by name with h (carrying SSH auth
// metadata), rejecting empty Name/Host and a rename onto an existing name.
func (s *Store) EditHost(name string, h Host) error {
	h = h.normalized()
	if h.Name == "" || h.Host == "" {
		return fmt.Errorf("name and host are required")
	}
	idx := -1
	for i, existing := range s.Hosts {
		if existing.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("host %q not found", name)
	}
	if h.Name != name {
		if _, ok := s.Find(h.Name); ok {
			return fmt.Errorf("host %q already exists", h.Name)
		}
	}
	old := s.Hosts[idx]
	h.ReadOnly = old.ReadOnly
	if !h.HasTLS() {
		h.TLSCACert, h.TLSCert, h.TLSKey = old.TLSCACert, old.TLSCert, old.TLSKey
		h = h.normalized() // drop the carried TLS if the URL is no longer tcp://
	}
	s.Hosts[idx] = h
	return nil
}

// ReadOnlyFor reports whether the saved host with the given URL is marked
// read_only. An unknown URL (e.g. an ad-hoc -H host) is not read-only.
func (s *Store) ReadOnlyFor(hostURL string) bool {
	if s == nil || hostURL == "" {
		return false
	}
	for _, h := range s.Hosts {
		if h.Host == hostURL && h.ReadOnly {
			return true
		}
	}
	return false
}

// sshSchemes are the host-URL prefixes reached over SSH: a plain Docker ssh://
// host, a nerdctl+ssh:// containerd host and a crio+ssh:// / cri+ssh:// CRI
// host. SSH auth metadata (key/password) applies to all. Order matters — the
// longer prefix is tried first so the remaining "user@host:port" body is
// extracted correctly.
var sshSchemes = []string{"nerdctl+ssh://", "crio+ssh://", "cri+ssh://", "ssh://"}

// IsSSH reports whether a host URL is reached over SSH (ssh://, nerdctl+ssh://,
// crio+ssh:// or cri+ssh://), so the UI shows the auth selector / credential
// prompt and the store keeps the SSH auth fields for it.
func IsSSH(hostURL string) bool {
	_, _, ok := sshParts(hostURL)
	return ok
}

// sshParts splits an SSH host URL into its scheme prefix and "user@host:port"
// body, reporting false for non-SSH schemes.
func sshParts(hostURL string) (prefix, body string, ok bool) {
	for _, p := range sshSchemes {
		if strings.HasPrefix(hostURL, p) {
			return p, strings.TrimPrefix(hostURL, p), true
		}
	}
	return "", "", false
}

// normalized trims fields and drops SSH auth metadata that does not apply to the
// host (non-ssh hosts, or a key path stored under password auth).
func (h Host) normalized() Host {
	h.Name = strings.TrimSpace(h.Name)
	h.Host = strings.TrimSpace(h.Host)
	h.SSHKeyPath = strings.TrimSpace(h.SSHKeyPath)
	if !IsSSH(h.Host) {
		h.SSHAuth = ""
		h.SSHKeyPath = ""
	}
	if h.SSHAuth == SSHAuthPassword {
		h.SSHKeyPath = "" // password auth never carries a key
	}
	if !strings.HasPrefix(h.Host, "tcp://") {
		h.TLSCACert, h.TLSCert, h.TLSKey = "", "", "" // TLS applies to tcp:// only
	}
	return h
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

// FindByURL returns the first saved host whose URL equals hostURL.
func (s *Store) FindByURL(hostURL string) (Host, bool) {
	if s == nil {
		return Host{}, false
	}
	for _, h := range s.Hosts {
		if h.Host == hostURL {
			return h, true
		}
	}
	return Host{}, false
}

// ImportResult classifies the outcome of Import for one host.
type ImportResult int

// Import outcomes.
const (
	// Imported: a new entry was appended (possibly under a suffixed name).
	Imported ImportResult = iota
	// ImportSkipped: an entry with the same URL already exists; nothing changed.
	ImportSkipped
	// ImportInvalid: the host had an empty name or URL.
	ImportInvalid
)

// Import appends h unless a host with the same URL is already saved. A name
// clash with a different URL gets a numeric suffix ("prod" → "prod-2"), so an
// import never overwrites an existing entry. It returns the outcome and the
// name the host was stored (or already exists) under.
func (s *Store) Import(h Host) (ImportResult, string) {
	h = h.normalized()
	if h.Name == "" || h.Host == "" {
		return ImportInvalid, ""
	}
	if existing, ok := s.FindByURL(h.Host); ok {
		return ImportSkipped, existing.Name
	}
	h.Name = uniqueName(s, h.Name)
	s.Hosts = append(s.Hosts, h)
	return Imported, h.Name
}

// SSHUser extracts the login from an SSH URL ("ssh://user@host:22" → "user",
// "nerdctl+ssh://user@host" → "user"). It returns "" for a non-SSH scheme or
// when no user part is present.
func SSHUser(hostURL string) string {
	_, rest, ok := sshParts(hostURL)
	if !ok {
		return ""
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		return rest[:at]
	}
	return ""
}

// WithSSHUser returns hostURL with its login replaced by user, preserving the
// scheme, host and port ("ssh://old@host:22", "new" → "ssh://new@host:22";
// nerdctl+ssh:// is preserved too). A non-ssh URL or an empty user is returned
// unchanged (minus any now-empty "@").
func WithSSHUser(hostURL, user string) string {
	prefix, rest, ok := sshParts(hostURL)
	if !ok {
		return hostURL
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	if user == "" {
		return prefix + rest
	}
	return prefix + user + "@" + rest
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
