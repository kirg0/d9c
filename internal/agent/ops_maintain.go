package agent

import (
	"context"
	"net"
	"strings"

	"github.com/kirg0/d9c/internal/docker"
)

func networkCreateOp() Op {
	return Op{
		Name:    "network create",
		Summary: "Create a user-defined network.",
		Args: Object(map[string]*Schema{
			"name":    String("Name of the new network."),
			"driver":  String("Network driver; the engine default (bridge) when omitted."),
			"subnet":  String("IPAM subnet in CIDR form, e.g. 172.28.0.0/16."),
			"gateway": String("Gateway address inside --subnet, e.g. 172.28.0.1; needs --subnet."),
		}, "name"),
		Tier: TierSafe,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("network create")
			}
			opts, err := networkCreateOptions(args)
			if err != nil {
				return Result{}, err
			}
			nets, err := env.Backend.ListNetworks()
			if err != nil {
				return Result{}, err
			}
			if _, ok := findNetwork(nets, opts.Name); ok {
				return Result{}, Errorf(CodeUsage, "network %q already exists", opts.Name).
					WithHint("pick another name; `d9c networks` lists the existing ones")
			}
			if err := env.Backend.CreateNetwork(opts); err != nil {
				return Result{}, err
			}
			out := NetworkEntry{Name: opts.Name, Driver: opts.Driver, Subnet: opts.Subnet}
			// The backend does not return the new object: read it back for its
			// ID; a failed read-back still reports the network as created.
			if nets, err := env.Backend.ListNetworks(); err == nil {
				if n, ok := findNetwork(nets, opts.Name); ok {
					out = NetworkEntry{ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Subnet: n.Subnet}
				}
			}
			return Result{Data: out}, nil
		},
	}
}

// networkCreateOptions validates the `network create` arguments: the engine
// silently ignores a gateway without a subnet, so that is refused up front.
func networkCreateOptions(args Args) (docker.NetworkCreateOptions, error) {
	opts := docker.NetworkCreateOptions{
		Name:    strings.TrimSpace(args.String("name")),
		Driver:  strings.TrimSpace(args.String("driver")),
		Subnet:  strings.TrimSpace(args.String("subnet")),
		Gateway: strings.TrimSpace(args.String("gateway")),
	}
	if opts.Name == "" {
		return opts, Errorf(CodeUsage, "network create: empty network name")
	}
	var subnet *net.IPNet
	if opts.Subnet != "" {
		var err error
		if _, subnet, err = net.ParseCIDR(opts.Subnet); err != nil {
			return opts, Errorf(CodeUsage, "network create: invalid subnet %q", opts.Subnet).
				WithHint("use CIDR form, e.g. 172.28.0.0/16")
		}
	}
	if opts.Gateway != "" {
		gw := net.ParseIP(opts.Gateway)
		switch {
		case gw == nil:
			return opts, Errorf(CodeUsage, "network create: invalid gateway %q", opts.Gateway).
				WithHint("use an IP address, e.g. 172.28.0.1")
		case subnet == nil:
			return opts, Errorf(CodeUsage, "network create: --gateway needs --subnet")
		case !subnet.Contains(gw):
			return opts, Errorf(CodeUsage, "network create: gateway %s is outside subnet %s", opts.Gateway, opts.Subnet)
		}
	}
	return opts, nil
}

// findNetwork looks a network up by its exact name.
func findNetwork(nets []docker.Network, name string) (docker.Network, bool) {
	for _, n := range nets {
		if n.Name == name {
			return n, true
		}
	}
	return docker.Network{}, false
}

func volumeCreateOp() Op {
	return Op{
		Name:    "volume create",
		Summary: "Create a named volume.",
		Args: Object(map[string]*Schema{
			"name":   String("Name of the new volume."),
			"driver": String("Volume driver; the engine default (local) when omitted."),
		}, "name"),
		Tier: TierSafe,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("volume create")
			}
			opts := docker.VolumeCreateOptions{
				Name:   strings.TrimSpace(args.String("name")),
				Driver: strings.TrimSpace(args.String("driver")),
			}
			if opts.Name == "" {
				return Result{}, Errorf(CodeUsage, "volume create: empty volume name")
			}
			vols, err := env.Backend.ListVolumes()
			if err != nil {
				return Result{}, err
			}
			if _, ok := findVolume(vols, opts.Name); ok {
				return Result{}, Errorf(CodeUsage, "volume %q already exists", opts.Name).
					WithHint("pick another name; `d9c volumes` lists the existing ones")
			}
			if err := env.Backend.CreateVolume(opts); err != nil {
				return Result{}, err
			}
			out := VolumeEntry{Name: opts.Name, Driver: opts.Driver}
			if vols, err := env.Backend.ListVolumes(); err == nil {
				if v, ok := findVolume(vols, opts.Name); ok {
					out = VolumeEntry{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Created: v.Created}
				}
			}
			return Result{Data: out}, nil
		},
	}
}

// findVolume looks a volume up by its exact name.
func findVolume(vols []docker.Volume, name string) (docker.Volume, bool) {
	for _, v := range vols {
		if v.Name == name {
			return v, true
		}
	}
	return docker.Volume{}, false
}

// PruneData is the output of `image prune` / `volume prune`: how many objects
// were removed.
type PruneData struct {
	Pruned  string `json:"pruned"`
	Deleted int    `json:"deleted"`
}

// pruneOp declares a single-kind prune command.
func pruneOp(name, summary, kind string, prune func(docker.Backend) (int, error)) Op {
	return Op{
		Name:    name,
		Summary: summary,
		Args:    NoArgs(),
		Tier:    TierDestructive,
		Handler: func(_ context.Context, env *Env, _ Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend(name)
			}
			n, err := prune(env.Backend)
			if err != nil {
				return Result{}, err
			}
			return Result{Data: PruneData{Pruned: kind, Deleted: n}}, nil
		},
	}
}

func imagePruneOp() Op {
	return pruneOp("image prune", "Remove dangling (untagged) images no container uses.", "images",
		docker.Backend.PruneImages)
}

func volumePruneOp() Op {
	return pruneOp("volume prune",
		"Remove volumes no container uses; their data is lost. "+
			"Docker Engine 23+ removes only anonymous volumes this way.",
		"volumes", docker.Backend.PruneVolumes)
}

// SystemPruneData is the `system prune` output: the engine's summary of what
// was removed and the space reclaimed.
type SystemPruneData struct {
	Summary string `json:"summary"`
}

func systemPruneOp() Op {
	return Op{
		Name: "system prune",
		Summary: "Remove stopped containers, unused networks, dangling images and the build cache; " +
			"volumes are kept (see `volume prune`).",
		Args: NoArgs(),
		Tier: TierDestructive,
		Handler: func(_ context.Context, env *Env, _ Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("system prune")
			}
			summary, err := env.Backend.SystemPrune()
			if err != nil {
				return Result{}, systemPruneError(summary, err)
			}
			return Result{Data: SystemPruneData{Summary: strings.TrimSpace(summary)}}, nil
		},
	}
}

// systemPruneError reports a failed prune step. The steps run independently,
// so the summary of the ones that did complete is kept in the message — the
// objects they removed are gone either way.
func systemPruneError(summary string, err error) *Error {
	e := Classify(err)
	msg := "system prune failed: " + e.Message
	if s := strings.TrimSpace(summary); s != "" {
		msg += "; completed steps: " + s
	}
	return &Error{Code: e.Code, Message: msg, Hint: e.Hint, Err: e}
}
