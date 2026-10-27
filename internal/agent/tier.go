// Package agent is the operation registry behind d9c's agent-facing interfaces
// (the non-interactive CLI subcommands and the MCP server). Every operation is
// declared once as an Op — name, argument schema, safety tier and handler — and
// the frontends stay thin: they parse input into Args, call Registry.Invoke and
// render the Result or Error. Handlers reach the engine only through
// docker.Backend, so every runtime (docker/podman/nerdctl/CRI) and the demo
// FakeBackend work the same way.
//
// Agent output is always English: nothing in this package goes through i18n.
package agent

import "fmt"

// Tier is the safety class of an operation. Tiers are ordered: a policy
// ceiling of TierSafe admits TierRead and TierSafe operations but not
// TierDestructive ones.
type Tier int

const (
	// TierRead never changes engine state (ps, inspect, logs, df…).
	TierRead Tier = iota
	// TierSafe changes state in a recoverable way (start/stop/restart, pull,
	// create).
	TierSafe
	// TierDestructive loses data or running state irreversibly (rm, kill,
	// prune, compose down -v, restore).
	TierDestructive
)

var tierNames = [...]string{
	TierRead:        "read",
	TierSafe:        "safe",
	TierDestructive: "destructive",
}

// String returns the lower-case config/CLI spelling of the tier.
func (t Tier) String() string {
	if t.Valid() {
		return tierNames[t]
	}
	return fmt.Sprintf("tier(%d)", int(t))
}

// Valid reports whether t is one of the declared tiers.
func (t Tier) Valid() bool {
	return t >= TierRead && t <= TierDestructive
}

// Allows reports whether an operation of tier op fits under the ceiling t.
func (t Tier) Allows(op Tier) bool {
	return op.Valid() && op <= t
}

// ParseTier converts "read", "safe" or "destructive" into a Tier.
func ParseTier(s string) (Tier, error) {
	for i, name := range tierNames {
		if s == name {
			return Tier(i), nil
		}
	}
	return TierRead, fmt.Errorf("unknown tier %q (want read, safe or destructive)", s)
}

// MarshalText implements encoding.TextMarshaler, so a Tier serializes by name
// in JSON and YAML.
func (t Tier) MarshalText() ([]byte, error) {
	if !t.Valid() {
		return nil, fmt.Errorf("invalid tier %d", int(t))
	}
	return []byte(t.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler (used by the config's
// `maxTier:` key).
func (t *Tier) UnmarshalText(b []byte) error {
	v, err := ParseTier(string(b))
	if err != nil {
		return err
	}
	*t = v
	return nil
}
