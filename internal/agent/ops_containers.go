package agent

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kirg0/d9c/internal/docker"
)

// maxLogLines caps the lines one logs call returns, whatever --tail says: an
// agent's context is the scarce resource, and a full log rarely fits it.
const maxLogLines = 10000

// defaultLogTail is the --tail used when the caller gives none.
const defaultLogTail = 200

// statsWindow is the pause between the two samples `stats` takes: engines
// derive CPU% from the counter delta, so a single sample reads 0%. Tests
// shorten it.
var statsWindow = time.Second

// ContainerEntry is one container in the `ps` and `compose ps` output.
type ContainerEntry struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Image    string   `json:"image"`
	State    string   `json:"state"`
	Status   string   `json:"status"`
	Health   string   `json:"health,omitempty"`
	Ports    string   `json:"ports,omitempty"`
	Created  string   `json:"created,omitempty"`
	Networks []string `json:"networks,omitempty"`
}

func containerEntries(list []docker.Container) []ContainerEntry {
	out := make([]ContainerEntry, 0, len(list))
	for _, c := range list {
		out = append(out, ContainerEntry{
			ID:       c.ID,
			Name:     c.Name,
			Image:    c.Image,
			State:    c.State,
			Status:   c.Status,
			Health:   c.Health,
			Ports:    c.Ports,
			Created:  timeString(c.Created),
			Networks: c.Networks,
		})
	}
	return out
}

// timeString renders t as RFC3339 UTC, or "" for the zero time.
func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func psOp() Op {
	return Op{
		Name:    "ps",
		Summary: "List running containers (all of them with --all).",
		Args: Object(map[string]*Schema{
			"all": Boolean("Include stopped containers.").WithShort("a"),
		}),
		Tier: TierRead,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("ps")
			}
			list, err := env.Backend.ListContainers(args.Bool("all"))
			if err != nil {
				return Result{}, err
			}
			return Result{Data: containerEntries(list)}, nil
		},
	}
}

// Inspect target kinds.
const (
	kindContainer = "container"
	kindImage     = "image"
	kindNetwork   = "network"
	kindVolume    = "volume"
	kindCompose   = "compose"
)

// InspectData is the `inspect` output: the engine's full description of one
// object, decoded into JSON-ready values.
type InspectData struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Object any    `json:"object"`
}

func inspectOp() Op {
	return Op{
		Name:    "inspect",
		Summary: "Show the full engine description of a container, image, network, volume or compose project.",
		Args: Object(map[string]*Schema{
			"target": String("ID or name of the object to inspect."),
			"type": String("Kind of object to inspect.").
				WithEnum(kindContainer, kindImage, kindNetwork, kindVolume, kindCompose).
				WithDefault(kindContainer).WithShort("t"),
		}, "target"),
		Tier:      TierRead,
		Untrusted: true,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("inspect")
			}
			b, target, kind := env.Backend, args.String("target"), args.String("type")
			var res *docker.InspectResult
			var err error
			switch kind {
			case kindImage:
				res, err = b.InspectImage(target)
			case kindNetwork:
				res, err = b.InspectNetwork(target)
			case kindVolume:
				res, err = b.InspectVolume(target)
			case kindCompose:
				var p docker.ComposeProject
				if p, err = resolveCompose(b, target); err == nil {
					res, err = b.InspectComposeProject(p.Identity())
				}
			default:
				res, err = b.InspectContainer(target)
			}
			if err != nil {
				return Result{}, err
			}
			return Result{Data: InspectData{Type: kind, Name: res.Name, Object: decodeYAML(res.RawYAML)}}, nil
		},
	}
}

// decodeYAML turns an engine's YAML description into JSON-ready values (maps
// keyed by strings); text that is not a YAML document comes back as-is.
func decodeYAML(raw string) any {
	var v any
	if err := yaml.Unmarshal([]byte(raw), &v); err != nil || v == nil {
		return raw
	}
	return jsonReady(v)
}

// jsonReady converts the map[any]any yaml.v3 produces for non-string keys into
// map[string]any, which encoding/json can marshal.
func jsonReady(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = jsonReady(e)
		}
		return t
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[fmt.Sprint(k)] = jsonReady(e)
		}
		return out
	case []any:
		for i, e := range t {
			t[i] = jsonReady(e)
		}
		return t
	}
	return v
}

// logArgs returns the log-window arguments shared by `logs` and
// `compose logs`, plus the operation's own target properties.
func logArgs(target map[string]*Schema) map[string]*Schema {
	props := map[string]*Schema{
		"tail": Integer("Number of most recent lines to read from the engine (0 = all, capped at " +
			fmt.Sprint(maxLogLines) + ").").WithDefault(defaultLogTail).WithMin(0).WithShort("n"),
		"since": String("Only lines after this point: a duration back from now (10m, 2h) or an RFC3339 timestamp."),
		"until": String("Only lines before this point: a duration back from now (10m, 2h) or an RFC3339 timestamp."),
		"grep": String("Keep only the lines matching this regular expression (RE2 syntax), applied after --tail; " +
			"combine with --tail 0 and --since to search a wider window."),
	}
	for k, v := range target {
		props[k] = v
	}
	return props
}

// LogsData is the `logs` / `compose logs` output. Each line starts with its
// RFC3339 timestamp; compose lines are prefixed with "<service> | ".
type LogsData struct {
	Target string   `json:"target"`
	Lines  []string `json:"lines"`
}

// ItemsKey implements Lister: the log lines are the items.
func (LogsData) ItemsKey() string { return "lines" }

func logsOp() Op {
	return Op{
		Name:    "logs",
		Summary: "Read the recent log lines of a container (last 200 by default).",
		Args: Object(logArgs(map[string]*Schema{
			"container": String("ID or name of the container."),
		}), "container"),
		Tier:      TierRead,
		Untrusted: true,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("logs")
			}
			grep, err := compileGrep(args)
			if err != nil {
				return Result{}, err
			}
			target := args.String("container")
			ch, stop, err := env.Backend.ContainerLogs(target, logOptions(args))
			if err != nil {
				return Result{}, err
			}
			return collectLogs(ctx, target, ch, stop, args.Int("tail"), grep)
		},
	}
}

// logOptions maps the shared log arguments onto a one-shot engine query.
func logOptions(args Args) docker.LogOptions {
	return docker.LogOptions{
		Tail:     args.Int("tail"),
		Since:    args.String("since"),
		Until:    args.String("until"),
		NoFollow: true,
	}
}

// compileGrep compiles the --grep pattern (nil when absent).
func compileGrep(args Args) (*regexp.Regexp, error) {
	pat := args.String("grep")
	if pat == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, Errorf(CodeUsage, "argument \"grep\": %v", err).
			WithHint("grep takes an RE2 regular expression, e.g. --grep 'error|panic'")
	}
	return re, nil
}

// collectLogs drains a non-following log stream into LogsData, filters it by
// grep and flags truncation: the engine returned a full --tail (older lines
// may exist) or the hard cap was hit.
func collectLogs(ctx context.Context, target string, ch <-chan string, stop func(), tail int, grep *regexp.Regexp) (Result, error) {
	lines, capped, err := drain(ctx, ch, stop, maxLogLines)
	if err != nil {
		return Result{}, err
	}
	res := Result{}
	switch {
	case capped:
		res.Truncated = true
		res.Hint = fmt.Sprintf("output stopped at %d lines; narrow the window with --since/--until or lower --tail", maxLogLines)
	case tail > 0 && len(lines) >= tail:
		res.Truncated = true
		res.Hint = "older lines may exist; raise --tail (0 = all) or set --since"
	}
	if grep != nil {
		kept := lines[:0]
		for _, l := range lines {
			if grep.MatchString(l) {
				kept = append(kept, l)
			}
		}
		lines = kept
	}
	res.Data = LogsData{Target: target, Lines: lines}
	return res, nil
}

// drain reads ch until it closes, ctx ends or max lines arrived (capped =
// true), and always releases the stream with stop. The result is never nil.
func drain(ctx context.Context, ch <-chan string, stop func(), max int) (lines []string, capped bool, err error) {
	defer stop()
	lines = []string{}
	for {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case l, ok := <-ch:
			if !ok {
				return lines, false, nil
			}
			if len(lines) == max {
				return lines, true, nil
			}
			lines = append(lines, l)
		}
	}
}

// StatsEntry is one container's resource sample in the `stats` output. Sizes
// are bytes; percentages are rounded to two decimals.
type StatsEntry struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	CPUPerc    float64 `json:"cpu_percent"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
	MemPerc    float64 `json:"mem_percent"`
	NetRx      uint64  `json:"net_rx"`
	NetTx      uint64  `json:"net_tx"`
	BlockRead  uint64  `json:"block_read"`
	BlockWrite uint64  `json:"block_write"`
}

func statsOp() Op {
	return Op{
		Name:    "stats",
		Summary: "Sample CPU, memory, network and block I/O of running containers once (takes about a second).",
		Args: Object(map[string]*Schema{
			"once": Boolean("Take a single sample; required, as streaming stats are not available yet.").
				WithDefault(true),
			"container": StringArray("Only these containers (ID, ID prefix or name); repeatable. Default: all running.").
				WithShort("c"),
		}),
		Tier: TierRead,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("stats")
			}
			if !args.Bool("once") {
				return Result{}, Errorf(CodeUsage, "streaming stats are not supported").
					WithHint("use `d9c stats --once` and call it again for a new sample")
			}
			list, err := env.Backend.ListContainers(false)
			if err != nil {
				return Result{}, err
			}
			list, err = pickContainers(list, args.Strings("container"))
			if err != nil {
				return Result{}, err
			}
			out := []StatsEntry{}
			if len(list) == 0 {
				return Result{Data: out}, nil
			}
			ids := make([]string, len(list))
			for i, c := range list {
				ids[i] = c.ID
			}
			// Prime the engine's CPU baseline, then take the sample we report.
			if _, err := env.Backend.ContainerStats(ids); err != nil {
				return Result{}, err
			}
			if err := sleepCtx(ctx, statsWindow); err != nil {
				return Result{}, err
			}
			stats, err := env.Backend.ContainerStats(ids)
			if err != nil {
				return Result{}, err
			}
			for _, c := range list {
				s, ok := stats[c.ID]
				if !ok {
					continue // stopped between listing and sampling
				}
				out = append(out, StatsEntry{
					ID: c.ID, Name: c.Name,
					CPUPerc: round2(s.CPUPerc), MemUsage: s.MemUsage, MemLimit: s.MemLimit, MemPerc: round2(s.MemPerc),
					NetRx: s.NetRx, NetTx: s.NetTx, BlockRead: s.BlockRead, BlockWrite: s.BlockWrite,
				})
			}
			return Result{Data: out}, nil
		},
	}
}

// pickContainers narrows list to the containers refs name (exact name, ID or
// ID prefix); with no refs it returns list. An unmatched ref is not_found.
func pickContainers(list []docker.Container, refs []string) ([]docker.Container, error) {
	if len(refs) == 0 {
		return list, nil
	}
	var out []docker.Container
	seen := map[string]bool{}
	for _, ref := range refs {
		found := false
		for _, c := range list {
			if c.Name == ref || strings.TrimPrefix(c.Name, "/") == ref || strings.HasPrefix(c.ID, ref) {
				found = true
				if !seen[c.ID] {
					seen[c.ID] = true
					out = append(out, c)
				}
			}
		}
		if !found {
			return nil, Errorf(CodeNotFound, "no running container matches %q", ref).
				WithHint("run `d9c ps` to list the running containers")
		}
	}
	return out, nil
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// errNoBackend reports a frontend that ran a connected operation without a
// connection — a programming bug, surfaced as internal rather than a panic.
func errNoBackend(op string) error {
	return Errorf(CodeInternal, "%s: no engine connection", op)
}
