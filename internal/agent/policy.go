package agent

import (
	"fmt"

	"github.com/kirg0/d9c/internal/hosts"
)

// DefaultMaxTier is the tier ceiling once writes are allowed and no maxTier is
// configured: destructive operations always need an explicit
// `maxTier: destructive`.
const DefaultMaxTier = TierSafe

// Policy holds the inputs of the agent write policy for one command. Writes
// are off by default; `agent.allowWrite` (or the --allow-write flag) turns
// them on up to `agent.maxTier`. Any read-only switch caps everything at
// TierRead, whatever the agent settings say.
type Policy struct {
	// ReadOnly names the global read-only switch that is on — the -read-only
	// flag or `readOnly: true` in the config — or is empty.
	ReadOnly string
	// Global is the config's top-level agent: block (nil when absent).
	Global *hosts.AgentPolicy
	// Host is the saved host the command targets; nil for an ad-hoc URL, a
	// Docker context or the demo backend. Its read_only flag caps the tier and
	// its agent: block overrides Global field by field.
	Host *hosts.Host
	// AllowWriteFlag is --allow-write: writes for this command when the config
	// leaves allowWrite unset. An explicit `allowWrite: false` wins over it.
	AllowWriteFlag bool
	// ConfigPath is the config file named in the hints.
	ConfigPath string
}

// decision is a resolved Policy plus where each value came from, for hints.
type decision struct {
	ceiling   Tier
	readOnly  string // the read-only switch in effect, if any
	allow     *bool  // the configured allowWrite (nil = unset)
	allowFrom string // scope of allow
	maxTier   Tier
	maxFrom   string // scope of maxTier; empty for DefaultMaxTier
}

// Ceiling returns the highest tier the policy admits. An invalid maxTier in
// the config is a CodeUsage error naming the file.
func (p Policy) Ceiling() (Tier, error) {
	d, err := p.resolve()
	return d.ceiling, err
}

// Check returns nil when op fits under the policy, else a CodeForbidden error
// whose hint says what would allow it and who may change that.
func (p Policy) Check(op Op) error {
	d, err := p.resolve()
	if err != nil {
		return err
	}
	if d.ceiling.Allows(op.Tier) {
		return nil
	}
	return d.forbidden(op, p.ConfigPath)
}

func (p Policy) resolve() (decision, error) {
	type layer struct {
		pol   *hosts.AgentPolicy
		scope string
	}
	d := decision{maxTier: DefaultMaxTier}
	layers := []layer{{p.Global, "globally"}}
	if p.Host != nil {
		layers = append(layers, layer{p.Host.Agent, fmt.Sprintf("for saved host %q", p.Host.Name)})
	}
	for _, l := range layers {
		if l.pol == nil {
			continue
		}
		if l.pol.AllowWrite != nil {
			d.allow, d.allowFrom = l.pol.AllowWrite, l.scope
		}
		if l.pol.MaxTier != "" {
			t, err := ParseTier(l.pol.MaxTier)
			if err != nil {
				return decision{}, Errorf(CodeUsage, "config: agent.maxTier %s: %v", l.scope, err).
					WithHint("fix the agent: block in " + p.ConfigPath)
			}
			d.maxTier, d.maxFrom = t, l.scope
		}
	}

	switch {
	case p.ReadOnly != "":
		d.readOnly = p.ReadOnly
	case p.Host != nil && p.Host.ReadOnly:
		d.readOnly = fmt.Sprintf("saved host %q has read_only: true", p.Host.Name)
	}
	allowed := p.AllowWriteFlag
	if d.allow != nil {
		allowed = *d.allow
	}
	if d.readOnly == "" && allowed {
		d.ceiling = d.maxTier
	}
	return d, nil
}

// forbidden builds the CodeForbidden error for op, naming the setting that
// blocks it. The hints steer an agent to the user: the policy exists so that
// writes happen only with the user's consent.
func (d decision) forbidden(op Op, path string) *Error {
	what := fmt.Sprintf("operation %q is %s", op.Name, op.Tier)
	switch {
	case d.readOnly != "":
		return Errorf(CodeForbidden, "%s, but read-only mode is on (%s)", what, d.readOnly).
			WithHint("only the user can turn read-only mode off; agents cannot override it")
	case d.allow != nil && !*d.allow:
		return Errorf(CodeForbidden, "%s, but agent.allowWrite is false %s in %s", what, d.allowFrom, path).
			WithHint("only the user can change that setting; --allow-write does not override an explicit allowWrite: false")
	case d.ceiling == TierRead && d.maxTier.Allows(op.Tier):
		return Errorf(CodeForbidden, "%s; write operations are off by default", what).
			WithHint(fmt.Sprintf("confirm with the user first, then retry with --allow-write (allows up to tier %s), "+
				"or have the user set agent: {allowWrite: true} in %s, globally or under the saved host", d.maxTier, path))
	case d.ceiling == TierRead:
		return Errorf(CodeForbidden, "%s; write operations are off by default", what).
			WithHint(fmt.Sprintf("this needs the user's consent: they can set agent: {allowWrite: true, maxTier: %s} in %s, "+
				"globally or under the saved host (maxTier is now %s, %s)", op.Tier, path, d.maxTier, d.maxScope()))
	default:
		return Errorf(CodeForbidden, "%s, above agent.maxTier %s (%s)", what, d.maxTier, d.maxScope()).
			WithHint(fmt.Sprintf("ask the user; they can set maxTier: %s in the agent: block in %s", op.Tier, path))
	}
}

// maxScope describes where maxTier came from.
func (d decision) maxScope() string {
	if d.maxFrom == "" {
		return "the default"
	}
	return "set " + d.maxFrom
}
