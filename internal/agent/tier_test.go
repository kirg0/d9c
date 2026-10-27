package agent

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTierString(t *testing.T) {
	tests := []struct {
		tier Tier
		want string
	}{
		{TierRead, "read"},
		{TierSafe, "safe"},
		{TierDestructive, "destructive"},
		{Tier(7), "tier(7)"},
		{Tier(-1), "tier(-1)"},
	}
	for _, tt := range tests {
		if got := tt.tier.String(); got != tt.want {
			t.Errorf("Tier(%d).String() = %q, want %q", int(tt.tier), got, tt.want)
		}
	}
}

func TestTierAllows(t *testing.T) {
	tests := []struct {
		ceiling, op Tier
		want        bool
	}{
		{TierRead, TierRead, true},
		{TierRead, TierSafe, false},
		{TierRead, TierDestructive, false},
		{TierSafe, TierRead, true},
		{TierSafe, TierSafe, true},
		{TierSafe, TierDestructive, false},
		{TierDestructive, TierDestructive, true},
		{TierDestructive, Tier(9), false},
		{TierDestructive, Tier(-1), false},
	}
	for _, tt := range tests {
		if got := tt.ceiling.Allows(tt.op); got != tt.want {
			t.Errorf("%v.Allows(%v) = %v, want %v", tt.ceiling, tt.op, got, tt.want)
		}
	}
}

func TestParseTier(t *testing.T) {
	tests := []struct {
		in      string
		want    Tier
		wantErr bool
	}{
		{"read", TierRead, false},
		{"safe", TierSafe, false},
		{"destructive", TierDestructive, false},
		{"Safe", TierRead, true},
		{"", TierRead, true},
		{"write", TierRead, true},
	}
	for _, tt := range tests {
		got, err := ParseTier(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseTier(%q) = %v, %v; want %v, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestTierTextRoundTrip(t *testing.T) {
	type cfg struct {
		MaxTier Tier `json:"maxTier" yaml:"maxTier"`
	}
	b, err := json.Marshal(cfg{MaxTier: TierSafe})
	if err != nil || string(b) != `{"maxTier":"safe"}` {
		t.Fatalf("json.Marshal = %s, %v", b, err)
	}
	var c cfg
	if err := json.Unmarshal([]byte(`{"maxTier":"destructive"}`), &c); err != nil || c.MaxTier != TierDestructive {
		t.Fatalf("json.Unmarshal = %v, %v", c.MaxTier, err)
	}
	if err := json.Unmarshal([]byte(`{"maxTier":"all"}`), &c); err == nil {
		t.Fatal("json.Unmarshal accepted an unknown tier")
	}
	if err := yaml.Unmarshal([]byte("maxTier: safe\n"), &c); err != nil || c.MaxTier != TierSafe {
		t.Fatalf("yaml.Unmarshal = %v, %v", c.MaxTier, err)
	}
	if _, err := json.Marshal(cfg{MaxTier: Tier(5)}); err == nil {
		t.Fatal("json.Marshal accepted an invalid tier")
	}
}
