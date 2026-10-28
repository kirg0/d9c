package agent

import (
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/hosts"
)

func boolPtr(b bool) *bool { return &b }

func TestPolicyCeiling(t *testing.T) {
	host := func(ro bool, pol *hosts.AgentPolicy) *hosts.Host {
		return &hosts.Host{Name: "h", Host: "tcp://h:2375", ReadOnly: ro, Agent: pol}
	}
	tests := []struct {
		name string
		p    Policy
		want Tier
	}{
		{"nothing configured", Policy{}, TierRead},
		{"flag", Policy{AllowWriteFlag: true}, DefaultMaxTier},
		{"global allow", Policy{Global: &hosts.AgentPolicy{AllowWrite: boolPtr(true)}}, TierSafe},
		{"global allow + destructive", Policy{Global: &hosts.AgentPolicy{AllowWrite: boolPtr(true), MaxTier: "destructive"}}, TierDestructive},
		{"maxTier without allow", Policy{Global: &hosts.AgentPolicy{MaxTier: "destructive"}}, TierRead},
		{"maxTier read with allow", Policy{Global: &hosts.AgentPolicy{AllowWrite: boolPtr(true), MaxTier: "read"}}, TierRead},
		{"explicit false beats flag", Policy{Global: &hosts.AgentPolicy{AllowWrite: boolPtr(false)}, AllowWriteFlag: true}, TierRead},
		{"host allow over global false", Policy{Global: &hosts.AgentPolicy{AllowWrite: boolPtr(false)}, Host: host(false, &hosts.AgentPolicy{AllowWrite: boolPtr(true)})}, TierSafe},
		{"host false over global allow", Policy{Global: &hosts.AgentPolicy{AllowWrite: boolPtr(true)}, Host: host(false, &hosts.AgentPolicy{AllowWrite: boolPtr(false)})}, TierRead},
		{"host maxTier over global", Policy{Global: &hosts.AgentPolicy{AllowWrite: boolPtr(true), MaxTier: "safe"}, Host: host(false, &hosts.AgentPolicy{MaxTier: "destructive"})}, TierDestructive},
		{"host without block inherits", Policy{Global: &hosts.AgentPolicy{AllowWrite: boolPtr(true)}, Host: host(false, nil)}, TierSafe},
		{"host read_only", Policy{Host: host(true, &hosts.AgentPolicy{AllowWrite: boolPtr(true), MaxTier: "destructive"})}, TierRead},
		{"global read-only", Policy{ReadOnly: "-read-only flag", Global: &hosts.AgentPolicy{AllowWrite: boolPtr(true)}, AllowWriteFlag: true}, TierRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.p.Ceiling()
			if err != nil || got != tt.want {
				t.Errorf("Ceiling() = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestPolicyInvalidMaxTier(t *testing.T) {
	for _, p := range []Policy{
		{Global: &hosts.AgentPolicy{MaxTier: "all"}, ConfigPath: "cfg.yaml"},
		{Host: &hosts.Host{Name: "h", Agent: &hosts.AgentPolicy{MaxTier: "Safe"}}, ConfigPath: "cfg.yaml"},
	} {
		_, err := p.Ceiling()
		e, ok := err.(*Error)
		if !ok || e.Code != CodeUsage || !strings.Contains(e.Hint, "cfg.yaml") {
			t.Errorf("Ceiling() error = %v, want usage naming the file", err)
		}
		if err := p.Check(Op{Name: "ps", Tier: TierRead}); err == nil {
			t.Error("Check() passed with an invalid maxTier")
		}
	}
}

func TestPolicyCheck(t *testing.T) {
	stop := Op{Name: "stop", Tier: TierSafe}
	rm := Op{Name: "rm", Tier: TierDestructive}
	tests := []struct {
		name     string
		p        Policy
		op       Op
		wantMsg  string // "" = allowed
		wantHint string
	}{
		{"read always passes", Policy{}, Op{Name: "ps", Tier: TierRead}, "", ""},
		{"allowed", Policy{AllowWriteFlag: true}, stop, "", ""},
		{"read-only", Policy{ReadOnly: "readOnly: true in cfg.yaml"}, stop,
			"read-only mode is on (readOnly: true in cfg.yaml)", "agents cannot override it"},
		{"host read-only", Policy{Host: &hosts.Host{Name: "prod", ReadOnly: true}, AllowWriteFlag: true}, stop,
			`saved host "prod" has read_only: true`, "agents cannot override it"},
		{"explicit false", Policy{Host: &hosts.Host{Name: "prod", Agent: &hosts.AgentPolicy{AllowWrite: boolPtr(false)}}, ConfigPath: "cfg.yaml"}, stop,
			`agent.allowWrite is false for saved host "prod" in cfg.yaml`, "--allow-write does not override"},
		{"off, flag would do", Policy{ConfigPath: "cfg.yaml"}, stop,
			"off by default", "retry with --allow-write (allows up to tier safe)"},
		{"off, flag would not do", Policy{ConfigPath: "cfg.yaml"}, rm,
			"off by default", "maxTier: destructive} in cfg.yaml"},
		{"off, configured destructive", Policy{Global: &hosts.AgentPolicy{MaxTier: "destructive"}}, rm,
			"off by default", "--allow-write (allows up to tier destructive)"},
		{"above default maxTier", Policy{AllowWriteFlag: true, ConfigPath: "cfg.yaml"}, rm,
			"above agent.maxTier safe (the default)", "maxTier: destructive in the agent: block in cfg.yaml"},
		{"above host maxTier", Policy{AllowWriteFlag: true, Host: &hosts.Host{Name: "dev", Agent: &hosts.AgentPolicy{MaxTier: "safe"}}}, rm,
			`above agent.maxTier safe (set for saved host "dev")`, "ask the user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.p.Check(tt.op)
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("Check() = %v, want nil", err)
				}
				return
			}
			e, ok := err.(*Error)
			if !ok || e.Code != CodeForbidden {
				t.Fatalf("Check() = %#v, want forbidden", err)
			}
			if !strings.Contains(e.Message, tt.wantMsg) || !strings.Contains(e.Hint, tt.wantHint) {
				t.Errorf("message %q / hint %q; want %q / %q", e.Message, e.Hint, tt.wantMsg, tt.wantHint)
			}
		})
	}
}
