package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kirg0/d9c/internal/docker"
)

// maxEvents caps the events one `events` call returns.
const maxEvents = 1000

// ImageEntry is one image in the `images` output.
type ImageEntry struct {
	ID      string `json:"id"`
	Tags    string `json:"tags"`
	Size    string `json:"size"`
	Created string `json:"created,omitempty"`
}

func imagesOp() Op {
	return Op{
		Name:    "images",
		Summary: "List the images on the host.",
		Args:    NoArgs(),
		Tier:    TierRead,
		Handler: func(_ context.Context, env *Env, _ Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("images")
			}
			list, err := env.Backend.ListImages()
			if err != nil {
				return Result{}, err
			}
			out := make([]ImageEntry, 0, len(list))
			for _, im := range list {
				out = append(out, ImageEntry{ID: im.ID, Tags: im.Tags, Size: im.Size, Created: timeString(im.Created)})
			}
			return Result{Data: out}, nil
		},
	}
}

// NetworkEntry is one network in the `networks` output.
type NetworkEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope,omitempty"`
	Subnet string `json:"subnet,omitempty"`
}

func networksOp() Op {
	return Op{
		Name:    "networks",
		Summary: "List the networks on the host.",
		Args:    NoArgs(),
		Tier:    TierRead,
		Handler: func(_ context.Context, env *Env, _ Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("networks")
			}
			list, err := env.Backend.ListNetworks()
			if err != nil {
				return Result{}, err
			}
			out := make([]NetworkEntry, 0, len(list))
			for _, n := range list {
				out = append(out, NetworkEntry{ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Subnet: n.Subnet})
			}
			return Result{Data: out}, nil
		},
	}
}

// VolumeEntry is one volume in the `volumes` output.
type VolumeEntry struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint,omitempty"`
	Created    string `json:"created,omitempty"`
}

func volumesOp() Op {
	return Op{
		Name:    "volumes",
		Summary: "List the volumes on the host.",
		Args:    NoArgs(),
		Tier:    TierRead,
		Handler: func(_ context.Context, env *Env, _ Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("volumes")
			}
			list, err := env.Backend.ListVolumes()
			if err != nil {
				return Result{}, err
			}
			out := make([]VolumeEntry, 0, len(list))
			for _, v := range list {
				out = append(out, VolumeEntry{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Created: v.Created})
			}
			return Result{Data: out}, nil
		},
	}
}

// SystemDFData is the `system df` output: one row per object type keyed by
// the lower-cased column names the engine reports (type, total, active,
// size, and reclaimable where known). Report carries the raw text instead
// when it is not a table d9c can split.
type SystemDFData struct {
	Rows   []map[string]string `json:"rows"`
	Report string              `json:"report,omitempty"`
}

func systemDFOp() Op {
	return Op{
		Name:    "system df",
		Summary: "Show the engine's disk usage by object type (images, containers, volumes, build cache).",
		Args:    NoArgs(),
		Tier:    TierRead,
		Handler: func(_ context.Context, env *Env, _ Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("system df")
			}
			res, err := env.Backend.SystemDF()
			if err != nil {
				return Result{}, err
			}
			return Result{Data: parseDF(res.RawYAML)}, nil
		},
	}
}

// dfColumnSep splits the `system df` table: columns are padded with at least
// two spaces, while values such as "Local Volumes" or "631.0 MB" hold one.
var dfColumnSep = regexp.MustCompile(`\s{2,}`)

// parseDF splits the `system df` text table every backend renders into rows
// keyed by the header; anything else is returned verbatim as Report.
func parseDF(text string) SystemDFData {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	raw := SystemDFData{Rows: []map[string]string{}, Report: strings.TrimSpace(text)}
	if len(lines) < 2 {
		return raw
	}
	header := dfColumnSep.Split(lines[0], -1)
	if header[0] != "TYPE" {
		return raw
	}
	rows := make([]map[string]string, 0, len(lines)-1)
	for _, l := range lines[1:] {
		cells := dfColumnSep.Split(l, -1)
		if len(cells) != len(header) {
			return raw
		}
		row := make(map[string]string, len(cells))
		for i, c := range cells {
			row[strings.ToLower(header[i])] = c
		}
		rows = append(rows, row)
	}
	return SystemDFData{Rows: rows}
}

// EventsData is the `events` output: the window queried (as passed to the
// engine) and the events in it, oldest first, each prefixed with its RFC3339
// time.
type EventsData struct {
	Since  string   `json:"since"`
	Until  string   `json:"until"`
	Events []string `json:"events"`
}

func eventsOp() Op {
	return Op{
		Name: "events",
		Summary: "List the engine events (create, start, die, oom, health…) of a past time window; " +
			"live following is not available.",
		Args: Object(map[string]*Schema{
			"since": String("Window start: a duration back from now (10m, 2h), an RFC3339 timestamp or Unix seconds.").
				WithDefault("10m"),
			"until": String("Window end, same syntax as since (default: now)."),
		}),
		Tier: TierRead,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("events")
			}
			opts := docker.EventOptions{Since: args.String("since"), Until: args.String("until")}
			if opts.Until == "" {
				// An open end would follow the live stream forever.
				opts.Until = time.Now().UTC().Format(time.RFC3339)
			}
			ch, stop, err := env.Backend.Events(opts)
			if err != nil {
				return Result{}, err
			}
			lines, capped, err := drain(ctx, ch, stop, maxEvents)
			if err != nil {
				return Result{}, err
			}
			res := Result{Data: EventsData{Since: opts.Since, Until: opts.Until, Events: lines}}
			if capped {
				res.Truncated = true
				res.Hint = fmt.Sprintf("output stopped at %d events; narrow the window with --since/--until", maxEvents)
			}
			return res, nil
		},
	}
}
