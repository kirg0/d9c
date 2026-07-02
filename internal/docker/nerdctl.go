package docker

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"d9c/internal/config"
)

// nerdctlBackend implements Backend by shelling out to the `nerdctl` CLI (the
// Docker-compatible containerd frontend), either locally or over SSH. containerd
// has no Docker REST API, so — unlike Podman — it cannot reuse dockerBackend; but
// nerdctl mirrors the docker CLI closely enough that the same section model
// (containers/images/networks/volumes/compose) maps cleanly. Objects live in a
// namespace (default "default", "k8s.io" for Kubernetes); every command is scoped
// with --namespace.
type nerdctlBackend struct {
	runner nerdctlRunner
	// local reports whether nerdctl runs on this machine (vs over SSH). It gates
	// operations whose semantics depend on where the filesystem lives — `cp`
	// to/from the local host works only for a local runner.
	local bool

	nsMu      sync.RWMutex
	namespace string
}

// defaultNamespace is containerd's default namespace, used until the user picks
// another via :namespace.
const defaultNamespace = "default"

// newNerdctlBackend builds a nerdctl backend for the given host. Two schemes are
// recognized (see New): nerdctl:// (local) and nerdctl+ssh://user@host (remote).
func newNerdctlBackend(cfg *config.Config) (Backend, error) {
	host := cfg.Host
	switch {
	case isNerdctlSSHHost(host):
		sshHost := "ssh://" + strings.TrimPrefix(host, nerdctlSSHScheme)
		client, err := buildSSHClient(sshHost, cfg.SSHKeyFile, cfg.SSHPassword)
		if err != nil {
			return nil, fmt.Errorf("nerdctl ssh: %w", err)
		}
		return &nerdctlBackend{
			runner:    sshRunner{client: client, bin: "nerdctl", closeFn: func() { _ = client.Close() }},
			namespace: defaultNamespace,
		}, nil
	default: // local
		return &nerdctlBackend{
			runner:    localRunner{bin: "nerdctl"},
			local:     true,
			namespace: defaultNamespace,
		}, nil
	}
}

// nerdctl host schemes.
const (
	nerdctlLocalScheme = "nerdctl://"
	nerdctlSSHScheme   = "nerdctl+ssh://"
)

// isNerdctlHost reports whether host selects the nerdctl (containerd) backend.
func isNerdctlHost(host string) bool {
	return isNerdctlSSHHost(host) ||
		host == "nerdctl" || host == "nerdctl:" || strings.HasPrefix(host, nerdctlLocalScheme)
}

func isNerdctlSSHHost(host string) bool { return strings.HasPrefix(host, nerdctlSSHScheme) }

// ── namespace scoping ───────────────────────────────────────────────────────

// ns returns the current namespace under the lock.
func (b *nerdctlBackend) ns() string {
	b.nsMu.RLock()
	defer b.nsMu.RUnlock()
	return b.namespace
}

// args prepends the namespace flag to a nerdctl subcommand, e.g.
// args("ps", "-a") → ["--namespace", "default", "ps", "-a"].
func (b *nerdctlBackend) args(sub ...string) []string {
	return append([]string{"--namespace", b.ns()}, sub...)
}

// run executes a one-shot nerdctl command scoped to the current namespace.
func (b *nerdctlBackend) run(sub ...string) (string, error) {
	out, err := b.runner.output(b.args(sub...))
	return out, cleanNerdctlErr(err)
}

// nerdctlFatalRe matches the logrus-style wrapper nerdctl prints on stderr
// (`time="…" level=fatal msg="…"`); the capture is the quoted msg content.
var nerdctlFatalRe = regexp.MustCompile(`level=(?:fatal|error)\s+msg="((?:[^"\\]|\\.)*)"`)

// errCountPrefixRe strips nerdctl's "N errors:" preamble from a fatal message.
var errCountPrefixRe = regexp.MustCompile(`^\d+ errors?:\s*`)

// cleanNerdctlErr unwraps nerdctl's logrus fatal/error lines into their bare
// messages, so the UI shows "no such image: foo" instead of
// `time="…" level=fatal msg="1 errors:\nno such image: foo"`. Lines without the
// logrus prefix are kept verbatim — they often carry the actual cause (e.g. the
// `ls: /x: No such file or directory` line before an "exec failed" fatal) —
// while warn/info logrus noise is dropped. Errors without any logrus line
// (transport failures, plain CLI output) pass through unchanged.
func cleanNerdctlErr(err error) error {
	if err == nil {
		return nil
	}
	var parts []string
	unwrapped := false
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(line)
		switch m := nerdctlFatalRe.FindStringSubmatch(line); {
		case line == "":
		case m != nil:
			unwrapped = true
			msg := m[1]
			if unquoted, uerr := strconv.Unquote(`"` + msg + `"`); uerr == nil {
				msg = unquoted
			}
			msg = errCountPrefixRe.ReplaceAllString(msg, "")
			msg = strings.TrimSpace(strings.ReplaceAll(msg, "\n", "; "))
			if msg != "" {
				parts = append(parts, msg)
			}
		case strings.Contains(line, "level="):
			// logrus warn/info noise — drop.
		default:
			parts = append(parts, line)
		}
	}
	if !unwrapped || len(parts) == 0 {
		return err
	}
	return errors.New(strings.Join(parts, "; "))
}

// ── NamespacedBackend ───────────────────────────────────────────────────────

// CurrentNamespace reports the active containerd namespace.
func (b *nerdctlBackend) CurrentNamespace() string { return b.ns() }

// SetNamespace switches the active namespace; subsequent commands are scoped to
// it. Unknown names are accepted (nerdctl creates the namespace lazily on write).
func (b *nerdctlBackend) SetNamespace(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	b.nsMu.Lock()
	b.namespace = name
	b.nsMu.Unlock()
}

// Namespaces lists the containerd namespaces (`nerdctl namespace ls -q`).
func (b *nerdctlBackend) Namespaces() ([]string, error) {
	out, err := b.runner.output([]string{"namespace", "ls", "-q"})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", cleanNerdctlErr(err))
	}
	return nonEmptyLines(out), nil
}

// ── engine identity / lifecycle ─────────────────────────────────────────────

// Runtime reports containerd for a nerdctl backend (no probe needed).
func (b *nerdctlBackend) Runtime() Runtime { return RuntimeContainerd }

// Ping verifies the CLI reaches containerd (`nerdctl version` talks to the
// daemon and fails if it is unreachable).
func (b *nerdctlBackend) Ping() error {
	_, err := b.run("version")
	return err
}

func (b *nerdctlBackend) Close() { b.runner.close() }

// nonEmptyLines splits s into trimmed, non-empty lines.
func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}
