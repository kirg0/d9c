package filter

import "testing"

func TestMatch(t *testing.T) {
	web := Target{
		Text:     "webnginx:1.25Up 2 hours (healthy)",
		Status:   "Up 2 hours (healthy) running",
		Labels:   map[string]string{"env": "prod", "tier": "frontend"},
		Networks: []string{"bridge", "frontend"},
	}
	db := Target{
		Text:     "dbpostgres:16Exited (0) 1 hour ago",
		Status:   "Exited (0) 1 hour ago exited",
		Labels:   map[string]string{"env": "staging", "tier": "backend"},
		Networks: []string{"backend"},
	}

	tests := []struct {
		name   string
		query  string
		target Target
		want   bool
	}{
		{"empty matches all", "", web, true},
		{"substring hit", "nginx", web, true},
		{"substring miss", "redis", web, false},
		{"substring case-insensitive", "NGINX", web, true},
		{"two words both match (AND)", "web nginx", web, true},
		{"two words one misses", "web redis", web, false},

		{"regex hit", `re:^web`, web, true},
		{"regex anchored miss", `re:^db`, web, false},
		{"regex case-insensitive", `re:NGINX`, web, true},
		{"regex digits", `re:postgres:\d+`, db, true},

		{"status hit", "status:running", web, true},
		{"status miss", "status:running", db, false},
		{"status exited", "status:exited", db, true},

		{"label key hit", "label:env", web, true},
		{"label key miss", "label:missing", web, false},
		{"label kv hit", "label:env=prod", web, true},
		{"label kv wrong value", "label:env=prod", db, false},
		{"label kv case-insensitive", "label:ENV=PROD", web, true},

		{"network hit", "network:frontend", web, true},
		{"network miss", "network:frontend", db, false},
		{"network partial", "net:front", web, true},

		{"combined predicates hit", "status:running label:tier=frontend net:bridge", web, true},
		{"combined predicates miss", "status:running label:tier=backend", web, false},

		{"predicate on missing field", "label:env", Target{Text: "img"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Compile(tt.query).Match(tt.target); got != tt.want {
				t.Errorf("Compile(%q).Match() = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestCompileBadRegexp(t *testing.T) {
	m := Compile("re:[unterminated")
	if m.Err() == nil {
		t.Fatal("expected an error for an invalid regexp")
	}
	// A broken query rejects every row rather than silently ignoring the term.
	if m.Match(Target{Text: "anything"}) {
		t.Error("Match should be false when the query has a parse error")
	}
}

func TestNilMatcherMatchesAll(t *testing.T) {
	var m *Matcher
	if !m.Match(Target{Text: "x"}) {
		t.Error("nil matcher should match everything")
	}
	if m.Err() != nil {
		t.Error("nil matcher should have no error")
	}
}
