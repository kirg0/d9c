package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
)

func TestIsSecretName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"POSTGRES_PASSWORD", true},
		{"db_pass", true},
		{"GITHUB_TOKEN", true},
		{"client_secret", true},
		{"AWS_ACCESS_KEY_ID", true},
		{"ApiKey", true},
		{"PATH", false},
		{"HOME", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isSecretName(tt.name); got != tt.want {
			t.Errorf("isSecretName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRedactEnv(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  any
		count int
	}{
		{
			name: "docker list of NAME=value",
			in:   "config:\n  env:\n    - PATH=/usr/bin\n    - DB_PASSWORD=hunter2\n    - EMPTY_TOKEN=\n    - BROKEN\n",
			want: map[string]any{"config": map[string]any{"env": []any{
				"PATH=/usr/bin", "DB_PASSWORD=<redacted>", "EMPTY_TOKEN=<redacted>", "BROKEN"}}},
			count: 2,
		},
		{
			name:  "capitalised keys",
			in:    "Config:\n  Env:\n    - API_KEY=abc\n",
			want:  map[string]any{"Config": map[string]any{"Env": []any{"API_KEY=<redacted>"}}},
			count: 1,
		},
		{
			name: "CRI list of key/value objects",
			in:   "info:\n  config:\n    envs:\n      - key: SECRET_X\n        value: s\n      - key: LANG\n        value: C\n      - name: MY_TOKEN\n        value: t\n      - key: NO_VALUE_KEY\n",
			want: map[string]any{"info": map[string]any{"config": map[string]any{"envs": []any{
				map[string]any{"key": "SECRET_X", "value": "<redacted>"},
				map[string]any{"key": "LANG", "value": "C"},
				map[string]any{"name": "MY_TOKEN", "value": "<redacted>"},
				map[string]any{"key": "NO_VALUE_KEY"},
			}}}},
			count: 2,
		},
		{
			name: "compose environment map",
			in:   "environment:\n  APP_SECRET: s\n  PORT: 8080\n  PASSTHROUGH_TOKEN:\n",
			want: map[string]any{"environment": map[string]any{
				"APP_SECRET": "<redacted>", "PORT": 8080, "PASSTHROUGH_TOKEN": nil}},
			count: 1,
		},
		{
			name: "nested in lists, several environments",
			in:   "services:\n  - name: db\n    env:\n      - PG_PASS=x\n  - name: web\n    env:\n      - WEB_KEY=y\nprocess:\n  env:\n    - TOKEN=z\n",
			want: map[string]any{
				"services": []any{
					map[string]any{"name": "db", "env": []any{"PG_PASS=<redacted>"}},
					map[string]any{"name": "web", "env": []any{"WEB_KEY=<redacted>"}},
				},
				"process": map[string]any{"env": []any{"TOKEN=<redacted>"}},
			},
			count: 3,
		},
		{
			name:  "secret-looking names outside env stay",
			in:    "labels:\n  api_key: visible\nenv: production\n",
			want:  map[string]any{"labels": map[string]any{"api_key": "visible"}, "env": "production"},
			count: 0,
		},
		{
			name:  "plain text",
			in:    "not yaml: [",
			want:  "not yaml: [",
			count: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeYAML(tt.in)
			if n := redactEnv(got); n != tt.count {
				t.Errorf("count = %d, want %d", n, tt.count)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("redacted = %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestInspectRedacts(t *testing.T) {
	fb := docker.NewFakeBackend()
	env := func(r Result) []any {
		obj := r.Data.(InspectData).Object.(map[string]any)
		return obj["Config"].(map[string]any)["Env"].([]any)
	}

	res := invoke(t, fb, "inspect", Args{"target": "web"})
	if got := env(res); got[0] != "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin" || got[1] != "API_TOKEN=<redacted>" {
		t.Errorf("default env = %v, want API_TOKEN masked", got)
	}
	if !strings.Contains(res.Hint, "1 secret-looking") || !strings.Contains(res.Hint, "--no-redact") {
		t.Errorf("hint = %q", res.Hint)
	}

	res = invoke(t, fb, "inspect", Args{"target": "web", "no_redact": true})
	if got := env(res); !strings.HasPrefix(got[1].(string), "API_TOKEN=demo-token-") || res.Hint != "" {
		t.Errorf("--no-redact env = %v, hint %q", got, res.Hint)
	}

	if res := invoke(t, fb, "inspect", Args{"target": "a08c488a9779", "type": "image"}); res.Hint != "" {
		t.Errorf("nothing masked, hint = %q", res.Hint)
	}
}
