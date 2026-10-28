package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/kirg0/d9c/internal/docker"
)

// The states `wait` can wait for.
const (
	untilRunning = "running"
	untilHealthy = "healthy"
	untilExited  = "exited"
)

// defaultWaitTimeout is the default --timeout of `wait`, in seconds.
const defaultWaitTimeout = 60

// waitPoll is how often `wait` re-reads the container list; tests shorten it.
var waitPoll = 500 * time.Millisecond

// WaitData is the output of `wait`: the container's state once the condition
// held, and how long that took.
type WaitData struct {
	Container string `json:"container"`
	ID        string `json:"id"`
	Until     string `json:"until"`
	State     string `json:"state"`
	Health    string `json:"health"`
	// WaitedMS is 0 when the condition already held on the first check.
	WaitedMS int64 `json:"waited_ms"`
}

func waitOp() Op {
	return Op{
		Name: "wait",
		Summary: "Wait until a container is running, healthy or exited, polling its state; " +
			"fails with timeout (exit 9) when --timeout runs out first.",
		Args: Object(map[string]*Schema{
			"container": String("Container ID, unique ID prefix or name."),
			"until": String("State to wait for; healthy needs a container healthcheck.").
				WithEnum(untilRunning, untilHealthy, untilExited).WithDefault(untilRunning),
			"timeout": Integer("Seconds to wait; 0 checks once without waiting.").
				WithDefault(defaultWaitTimeout).WithMin(0),
		}, "container"),
		Tier: TierRead,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("wait")
			}
			until, ref, seconds := args.String("until"), args.String("container"), args.Int("timeout")
			start := time.Now()
			deadline := start.Add(time.Duration(seconds) * time.Second)
			id := ""
			for polls := 0; ; polls++ {
				list, err := env.Backend.ListContainers(true)
				if err != nil {
					return Result{}, err
				}
				c, err := waitFind(list, ref, id)
				if err != nil {
					return Result{}, err
				}
				id = c.ID
				done, err := waitReached(c, until)
				if err != nil {
					return Result{}, err
				}
				if done {
					data := WaitData{Container: c.Name, ID: c.ID, Until: until, State: c.State, Health: c.Health}
					if polls > 0 {
						data.WaitedMS = time.Since(start).Milliseconds()
					}
					return Result{Data: data}, nil
				}
				left := time.Until(deadline)
				if left <= 0 {
					return Result{}, waitTimeout(c, until, seconds)
				}
				select {
				case <-ctx.Done():
					return Result{}, ctx.Err()
				case <-time.After(min(waitPoll, left)):
				}
			}
		},
	}
}

// waitFind resolves ref on the first poll and follows the container by its ID
// afterwards, so a rename or a new container taking the name cannot switch
// the target mid-wait. A container that disappears is not_found.
func waitFind(list []docker.Container, ref, id string) (docker.Container, error) {
	if id == "" {
		c, err := resolveContainer(list, ref)
		if err != nil {
			return c, err
		}
		return c, nil
	}
	for _, c := range list {
		if c.ID == id {
			return c, nil
		}
	}
	return docker.Container{}, Errorf(CodeNotFound, "container %s (%s) was removed while waiting", ref, id).
		WithHint("it may have been started with --rm or removed by another client")
}

// waitReached reports whether c is in the until state. Waiting for health on
// a running container without a healthcheck can never succeed, so it is a
// usage error at once.
func waitReached(c docker.Container, until string) (bool, error) {
	switch until {
	case untilRunning:
		return isRunning(c.State), nil
	case untilExited:
		return c.State == "exited" || c.State == "dead" || c.State == "stopped", nil
	default: // untilHealthy
		if isRunning(c.State) && c.Health == "" {
			return false, Errorf(CodeUsage, "container %s has no healthcheck", c.Name).
				WithHint("wait with --until running instead, or add a HEALTHCHECK to the image or compose service")
		}
		return isRunning(c.State) && c.Health == "healthy", nil
	}
}

// waitTimeout is the error of a wait that ran out of time, with the state the
// container was last seen in.
func waitTimeout(c docker.Container, until string, seconds int) *Error {
	now := c.State
	if c.Health != "" {
		now += ", health " + c.Health
	}
	return Errorf(CodeTimeout, "container %s did not become %s within %ds (now %s)", c.Name, until, seconds, now).
		WithHint(fmt.Sprintf("raise --timeout, or look for the cause with `d9c logs %s` and `d9c inspect %s`", c.Name, c.Name))
}
