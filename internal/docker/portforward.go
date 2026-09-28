package docker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kirg0/d9c/internal/i18n"

	"github.com/docker/go-connections/nat"
)

// PortForwarder is an optional capability a Backend may implement when it can
// reach a container's TCP port from the local machine: over the already open
// SSH connection (any port — the container IP or a published port is dialed
// from the remote host) or, on a tcp:// connection, straight to a port the
// container publishes on the daemon host. The UI type-asserts it to offer
// port-forwarding; backends without it (CRI, nerdctl, disconnected) report a
// friendly "not supported" error instead.
type PortForwarder interface {
	// PortTarget resolves where containerPort (TCP) of the container is reached
	// and returns it for display (e.g. "172.17.0.2:80 via SSH"). It fails with a
	// readable error when the port cannot be forwarded (container not running,
	// unpublished port on a non-SSH connection), so the form can report it
	// before a local port is opened.
	PortTarget(containerID string, containerPort int) (string, error)
	// DialPort opens a TCP connection to containerPort of the container. The
	// target is re-resolved on every call, so a restarted container (new IP) is
	// still reached.
	DialPort(containerID string, containerPort int) (net.Conn, error)
}

// portDialTimeout bounds one direct (non-SSH) dial to a published port.
const portDialTimeout = 10 * time.Second

// portInfo is the slice of `docker inspect` that decides how a container port
// is reachable — split out so the resolution rules are unit-testable without a
// daemon.
type portInfo struct {
	Running     bool
	HostNetwork bool
	// IPs are the container's addresses on its attached networks (name order).
	IPs []string
	// Bindings are the host bindings published for the requested port/tcp.
	Bindings []nat.PortBinding
}

// pickForwardAddr decides which address (host:port) to dial for containerPort.
// viaSSH means the dial happens from the remote host over the SSH connection;
// otherwise it happens from this machine, where only published ports on
// daemonHost are reachable.
//
// A published port wins in both cases: it is reachable from the host even when
// the container network is not (rootless Docker, Docker Desktop VMs). Over SSH
// an unpublished port falls back to the container IP (or 127.0.0.1 for a
// host-network container).
func pickForwardAddr(info portInfo, containerPort int, viaSSH bool, daemonHost string) (string, error) {
	if !info.Running {
		return "", errors.New(i18n.T("контейнер не запущен", "container is not running"))
	}
	for _, b := range info.Bindings {
		if b.HostPort == "" {
			continue
		}
		host := b.HostIP
		if viaSSH {
			if isUnspecifiedIP(host) {
				host = "127.0.0.1"
			}
		} else if isUnspecifiedIP(host) || isLoopbackIP(host) {
			host = daemonHost
		}
		return net.JoinHostPort(host, b.HostPort), nil
	}
	port := strconv.Itoa(containerPort)
	if !viaSSH {
		return "", fmt.Errorf(i18n.T(
			"порт %d не опубликован на хосте — проброс неопубликованных портов работает только через SSH-подключение (ssh://)",
			"port %d is not published on the host — forwarding unpublished ports needs an SSH connection (ssh://)"), containerPort)
	}
	if info.HostNetwork {
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	if len(info.IPs) > 0 {
		return net.JoinHostPort(info.IPs[0], port), nil
	}
	return "", errors.New(i18n.T("у контейнера нет IP-адреса в сети", "the container has no network IP address"))
}

// isUnspecifiedIP reports whether a binding HostIP means "all interfaces"
// (empty, 0.0.0.0 or ::).
func isUnspecifiedIP(ip string) bool {
	if ip == "" {
		return true
	}
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsUnspecified()
}

// isLoopbackIP reports whether ip is a loopback address (127.0.0.0/8, ::1).
func isLoopbackIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

// daemonHostname extracts the host part of a Docker daemon URL for direct dials
// to published ports: tcp://10.0.0.5:2375 → 10.0.0.5; local sockets (unix://,
// npipe://) and unparsable values map to 127.0.0.1.
func daemonHostname(daemonURL string) string {
	u, err := url.Parse(daemonURL)
	if err != nil || u.Hostname() == "" {
		return "127.0.0.1"
	}
	switch u.Scheme {
	case "unix", "npipe":
		return "127.0.0.1"
	}
	return u.Hostname()
}

// inspectPortInfo loads the port-resolution facts for containerPort/tcp.
func (b *dockerBackend) inspectPortInfo(containerID string, containerPort int) (portInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := b.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return portInfo{}, fmt.Errorf("inspect container: %w", err)
	}
	var info portInfo
	if c.State != nil {
		info.Running = c.State.Running
	}
	if c.HostConfig != nil {
		info.HostNetwork = c.HostConfig.NetworkMode.IsHost()
	}
	if ns := c.NetworkSettings; ns != nil {
		names := make([]string, 0, len(ns.Networks))
		for name := range ns.Networks {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if ep := ns.Networks[name]; ep != nil && ep.IPAddress != "" {
				info.IPs = append(info.IPs, ep.IPAddress)
			}
		}
		info.Bindings = ns.Ports[nat.Port(strconv.Itoa(containerPort)+"/tcp")]
	}
	return info, nil
}

// resolveForward returns the address to dial for containerPort.
func (b *dockerBackend) resolveForward(containerID string, containerPort int) (string, error) {
	info, err := b.inspectPortInfo(containerID, containerPort)
	if err != nil {
		return "", err
	}
	return pickForwardAddr(info, containerPort, b.sshClient != nil, daemonHostname(b.cli.DaemonHost()))
}

// PortTarget implements PortForwarder.
func (b *dockerBackend) PortTarget(containerID string, containerPort int) (string, error) {
	addr, err := b.resolveForward(containerID, containerPort)
	if err != nil {
		return "", err
	}
	if b.sshClient != nil {
		return addr + " via SSH", nil
	}
	return addr, nil
}

// DialPort implements PortForwarder: over SSH the connection is opened by the
// remote sshd (a direct-tcpip channel on the existing client), otherwise it is
// a plain TCP dial to the published port on the daemon host.
func (b *dockerBackend) DialPort(containerID string, containerPort int) (net.Conn, error) {
	addr, err := b.resolveForward(containerID, containerPort)
	if err != nil {
		return nil, err
	}
	var conn net.Conn
	if b.sshClient != nil {
		conn, err = b.sshClient.Dial("tcp", addr)
	} else {
		conn, err = net.DialTimeout("tcp", addr, portDialTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, friendlyDialErr(err))
	}
	return conn, nil
}

// friendlyDialErr adds a hint to the typical "nothing listens there" failure.
func friendlyDialErr(err error) error {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "connect failed") {
		return fmt.Errorf("%w (%s)", err, i18n.T(
			"в контейнере никто не слушает этот порт",
			"nothing in the container listens on this port"))
	}
	return err
}

// ContainerPorts extracts the TCP container-side ports from a formatted PORTS
// cell ("8080->80/tcp, 5432/tcp", "0.0.0.0:8080->80/tcp") in first-seen order,
// without duplicates. The UI uses it to pre-fill the port-forward form.
func ContainerPorts(ports string) []int {
	var out []int
	seen := map[int]bool{}
	for part := range strings.SplitSeq(ports, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, after, ok := strings.Cut(part, "->"); ok {
			part = after
		}
		spec, proto, _ := strings.Cut(part, "/")
		if proto != "" && proto != "tcp" {
			continue
		}
		if i := strings.LastIndex(spec, ":"); i >= 0 {
			spec = spec[i+1:]
		}
		// A range ("8000-8002") pre-fills its first port.
		spec, _, _ = strings.Cut(spec, "-")
		p, err := strconv.Atoi(spec)
		if err != nil || p <= 0 || p > 65535 || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
