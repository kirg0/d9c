package agent

import (
	"context"
)

// Builtin returns a registry holding d9c's built-in operations — the table
// every frontend (CLI, MCP) serves.
func Builtin() *Registry {
	r := NewRegistry()
	r.MustRegister(
		hostsLsOp(), infoOp(),
		psOp(), inspectOp(), logsOp(), statsOp(), waitOp(),
		imagesOp(), networksOp(), volumesOp(), systemDFOp(), eventsOp(),
		composeLsOp(), composePsOp(), composeConfigOp(), composeLogsOp(),
		startOp(), stopOp(), restartOp(), killOp(), rmOp(),
		pullOp(), execOp(),
		composeUpOp(), composeDownOp(), composePullOp(), composeRestartOp(),
		composeBackupOp(), composeRestoreOp(),
		networkCreateOp(), volumeCreateOp(),
		imagePruneOp(), volumePruneOp(), systemPruneOp(),
	)
	return r
}

// HostEntry is one saved host in the `hosts ls` output.
type HostEntry struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	ReadOnly bool   `json:"read_only"`
	// AgentTier is the highest operation tier the write policy admits on
	// the host for this invocation (read unless writes are allowed).
	AgentTier Tier `json:"agent_tier"`
}

func hostsLsOp() Op {
	return Op{
		Name:    "hosts ls",
		Summary: "List the saved hosts (names usable with --host).",
		Args:    NoArgs(),
		Tier:    TierRead,
		Local:   true,
		Handler: func(_ context.Context, env *Env, _ Args) (Result, error) {
			out := []HostEntry{}
			if env.Hosts != nil {
				for _, h := range env.Hosts.List() {
					pol := env.Policy
					pol.Host = &h
					tier, err := pol.Ceiling()
					if err != nil {
						return Result{}, err
					}
					out = append(out, HostEntry{Name: h.Name, Host: h.Host, ReadOnly: h.ReadOnly, AgentTier: tier})
				}
			}
			return Result{Data: out}, nil
		},
	}
}

// InfoData is the `info` output: a one-shot daemon summary.
type InfoData struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Containers int    `json:"containers"`
	Running    int    `json:"running"`
	Paused     int    `json:"paused"`
	Stopped    int    `json:"stopped"`
	Images     int    `json:"images"`
	NCPU       int    `json:"ncpu"`
	MemTotal   int64  `json:"mem_total"`
}

func infoOp() Op {
	return Op{
		Name:    "info",
		Summary: "Show the engine summary: version, container and image counts, CPUs and memory.",
		Args:    NoArgs(),
		Tier:    TierRead,
		Handler: func(_ context.Context, env *Env, _ Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("info")
			}
			s, err := env.Backend.Info()
			if err != nil {
				return Result{}, err
			}
			return Result{Data: InfoData{
				Name:       s.Name,
				Version:    s.Version,
				Containers: s.Containers,
				Running:    s.Running,
				Paused:     s.Paused,
				Stopped:    s.Stopped,
				Images:     s.Images,
				NCPU:       s.NCPU,
				MemTotal:   s.MemTotal,
			}}, nil
		},
	}
}
