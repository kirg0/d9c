package docker

import (
	"context"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
)

// Runtime identifies the container engine behind the Docker-compatible REST API
// a Backend talks to. Podman ships a Docker-compatible API (`podman system
// service`), so the same dockerBackend drives both engines; Runtime lets the UI
// label the connection and lets host-side compose ops pick the right CLI verb
// (`docker compose` vs `podman compose`).
type Runtime string

const (
	// RuntimeUnknown is the zero value: the engine has not been probed yet (or
	// the probe failed). Callers treat it like Docker for command building.
	RuntimeUnknown Runtime = ""
	RuntimeDocker  Runtime = "docker"
	RuntimePodman  Runtime = "podman"
)

// Label renders the runtime for display in the header; an unknown engine is
// shown as "docker" (the default assumption).
func (r Runtime) Label() string {
	if r == RuntimePodman {
		return "podman"
	}
	return "docker"
}

// detectRuntime classifies the engine from a /version response. Podman's
// Docker-compat endpoint advertises itself in Platform.Name ("Podman Engine")
// and lists a "Podman Engine" component — neither of which Docker ever reports —
// so a case-insensitive "podman" match on either field is a reliable signal.
func detectRuntime(v types.Version) Runtime {
	if strings.Contains(strings.ToLower(v.Platform.Name), "podman") {
		return RuntimePodman
	}
	for _, c := range v.Components {
		if strings.Contains(strings.ToLower(c.Name), "podman") {
			return RuntimePodman
		}
	}
	return RuntimeDocker
}

// Runtime reports which container engine backs the connection, probing the
// daemon's /version endpoint once and caching the result. A failed probe leaves
// the runtime unknown and is retried on the next call (the engine may not be
// reachable yet during an auto-reconnect).
func (b *dockerBackend) Runtime() Runtime {
	b.runtimeMu.Lock()
	defer b.runtimeMu.Unlock()
	if b.runtime != RuntimeUnknown {
		return b.runtime
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v, err := b.cli.ServerVersion(ctx)
	if err != nil {
		return RuntimeUnknown
	}
	b.runtime = detectRuntime(v)
	return b.runtime
}

// engineCmd returns the host CLI verb for the detected engine — "docker" or
// "podman" — used to build the host-side compose/version commands run over SSH.
// Podman exposes `podman compose` (4.1+) and `podman version`, mirroring the
// Docker subcommands, so swapping the verb is enough to reuse the same plumbing.
func (b *dockerBackend) engineCmd() string {
	if b.Runtime() == RuntimePodman {
		return "podman"
	}
	return "docker"
}
