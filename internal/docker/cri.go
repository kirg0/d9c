package docker

import (
	"fmt"
	"strings"
	"sync"

	"d9c/internal/config"
	"d9c/internal/i18n"
)

// criBackend implements Backend by shelling out to `crictl`, the official CRI
// client, either locally or over SSH. It targets CRI-O and any other runtime
// exposing the CRI gRPC socket (containerd's CRI plugin, cri-dockerd). A pure
// gRPC client was considered and rejected for the same reason nerdctl won over
// the containerd API: CRI carries no log stream (logs are files on the host,
// read by kubelet/crictl) and its Exec RPC returns a URL to an SPDY streaming
// server (needing k8s.io/client-go), so a remote host requires SSH access
// anyway — at which point crictl covers everything in one transport.
//
// CRI manages pods, containers and images only: networks, volumes, compose,
// build/tag/push and `run` don't exist at this layer. Those sections degrade
// softly (empty lists) and the operations fail with a clear explanation.
type criBackend struct {
	runner nerdctlRunner
	// endpoint is the CRI runtime socket passed via `-r` (empty = crictl's own
	// config /etc/crictl.yaml or its default endpoint probing).
	endpoint string
	// local reports whether crictl runs on this machine (vs over SSH); it gates
	// interactive exec exactly like the nerdctl backend.
	local bool

	// runtimeMu guards runtimeName, the RuntimeName reported by `crictl
	// version` ("cri-o", "containerd"), probed once and cached.
	runtimeMu   sync.Mutex
	runtimeName string

	// statsMu guards cpuPrev, the previous cumulative CPU counter per container
	// used to derive CPU% as a delta between refresh ticks (CRI stats report
	// only cumulative usageCoreNanoSeconds, no precomputed percentage).
	statsMu sync.Mutex
	cpuPrev map[string]criCPUSample
}

// CRI host schemes. crio:// and cri:// are synonyms (the backend speaks
// generic CRI; "crio" is just the friendlier spelling for CRI-O hosts). An
// optional path selects the runtime socket: crio:///var/run/crio/crio.sock,
// crio+ssh://user@host/run/containerd/containerd.sock.
const (
	crioLocalScheme = "crio://"
	crioSSHScheme   = "crio+ssh://"
	criLocalScheme  = "cri://"
	criSSHScheme    = "cri+ssh://"
)

// isCRIHost reports whether host selects the crictl (CRI) backend.
func isCRIHost(host string) bool {
	return isCRISSHHost(host) ||
		host == "crio" || host == "crio:" || strings.HasPrefix(host, crioLocalScheme) ||
		host == "cri" || host == "cri:" || strings.HasPrefix(host, criLocalScheme)
}

func isCRISSHHost(host string) bool {
	return strings.HasPrefix(host, crioSSHScheme) || strings.HasPrefix(host, criSSHScheme)
}

// splitCRIHost separates a CRI host string into its SSH target (empty for a
// local connection) and the runtime socket endpoint (empty = crictl defaults).
// The socket path is whatever follows the authority as a path component.
func splitCRIHost(host string) (sshTarget, endpoint string) {
	rest := host
	ssh := false
	for _, scheme := range []string{crioSSHScheme, criSSHScheme, crioLocalScheme, criLocalScheme} {
		if strings.HasPrefix(rest, scheme) {
			ssh = strings.Contains(scheme, "+ssh")
			rest = strings.TrimPrefix(rest, scheme)
			break
		}
	}
	rest = strings.TrimSuffix(rest, "/") // bare "crio:///" → no endpoint
	if !ssh {
		// Local: everything after the scheme is the socket path.
		if strings.TrimSpace(rest) != "" {
			endpoint = "unix://" + ensureLeadingSlash(rest)
		}
		return "", endpoint
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		endpoint = "unix://" + ensureLeadingSlash(rest[i:])
		rest = rest[:i]
	}
	return rest, endpoint
}

func ensureLeadingSlash(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

// newCRIBackend builds a crictl backend for the given host (see isCRIHost for
// the recognized schemes).
func newCRIBackend(cfg *config.Config) (Backend, error) {
	sshTarget, endpoint := splitCRIHost(cfg.Host)
	if sshTarget == "" {
		return &criBackend{
			runner:   localRunner{bin: "crictl"},
			endpoint: endpoint,
			local:    true,
		}, nil
	}
	client, err := buildSSHClient("ssh://"+sshTarget, cfg.SSHKeyFile, cfg.SSHPassword)
	if err != nil {
		return nil, fmt.Errorf("crictl ssh: %w", err)
	}
	return &criBackend{
		runner:   sshRunner{client: client, bin: "crictl", closeFn: func() { _ = client.Close() }},
		endpoint: endpoint,
	}, nil
}

// args prepends the runtime-endpoint flag (when set) to a crictl subcommand.
func (b *criBackend) args(sub ...string) []string {
	if b.endpoint == "" {
		return sub
	}
	return append([]string{"-r", b.endpoint}, sub...)
}

// run executes a one-shot crictl command.
func (b *criBackend) run(sub ...string) (string, error) {
	return b.runner.output(b.args(sub...))
}

// errCRIUnsupported builds the standard "not part of CRI" error for operations
// the runtime API simply does not model.
func errCRIUnsupported(what string) error {
	return fmt.Errorf(i18n.T(
		"%s недоступно: CRI управляет только pod'ами, контейнерами и образами",
		"%s is not available: CRI manages only pods, containers and images"), what)
}

// ── engine identity / lifecycle ─────────────────────────────────────────────

// criVersionInfo is the parsed output of `crictl version` (plain "Key: value"
// lines; crictl's -o json applies to object listings, not version).
type criVersionInfo struct {
	RuntimeName    string
	RuntimeVersion string
}

// parseCRIVersion extracts RuntimeName/RuntimeVersion from `crictl version`.
func parseCRIVersion(out string) criVersionInfo {
	var v criVersionInfo
	for _, line := range strings.Split(out, "\n") {
		k, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(k) {
		case "RuntimeName":
			v.RuntimeName = val
		case "RuntimeVersion":
			v.RuntimeVersion = val
		}
	}
	return v
}

// versionInfo probes `crictl version`, caching the runtime name.
func (b *criBackend) versionInfo() (criVersionInfo, error) {
	out, err := b.run("version")
	if err != nil {
		return criVersionInfo{}, err
	}
	v := parseCRIVersion(out)
	if v.RuntimeName != "" {
		b.runtimeMu.Lock()
		b.runtimeName = v.RuntimeName
		b.runtimeMu.Unlock()
	}
	return v, nil
}

// Runtime reports CRI-O when the probed runtime identifies as such, and the
// generic CRI label otherwise (containerd's CRI plugin, cri-dockerd, …). A
// failed probe still returns RuntimeCRI — the scheme alone pins the engine
// family, unlike the docker/podman autodetection.
func (b *criBackend) Runtime() Runtime {
	b.runtimeMu.Lock()
	name := b.runtimeName
	b.runtimeMu.Unlock()
	if name == "" {
		if v, err := b.versionInfo(); err == nil {
			name = v.RuntimeName
		}
	}
	if strings.Contains(strings.ToLower(name), "cri-o") {
		return RuntimeCRIO
	}
	return RuntimeCRI
}

// Ping verifies crictl reaches the runtime socket (`crictl version` performs a
// live Version RPC).
func (b *criBackend) Ping() error {
	_, err := b.run("version")
	return err
}

func (b *criBackend) Close() { b.runner.close() }
