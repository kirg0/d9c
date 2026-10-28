package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var update = flag.Bool("update", false, "rewrite testdata/tools.json")

// TestToolCatalogGolden pins tools/list of a session that admits every tier:
// names, descriptions, input/output schemas and annotations are the contract
// MCP clients and the skills depend on. Rewrite with -update.
func TestToolCatalogGolden(t *testing.T) {
	s := startSession(t, Options{Policy: writePolicy()})
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(res.Tools, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "tools.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Errorf("tools/list changed; review the diff and rerun with -update:\n%s", got)
	}
}

// actionCases holds working arguments for every tool action on the demo
// backend; TestEveryActionRuns fails for an action without a case.
var actionCases = map[string]map[string]any{
	"d9c_hosts":                   {},
	"d9c_system/info":             {},
	"d9c_system/df":               {},
	"d9c_system/events":           {"since": "5m"},
	"d9c_system/stats":            {"container": []string{"web"}},
	"d9c_containers/ps":           {"all": true},
	"d9c_containers/inspect":      {"target": "web"},
	"d9c_containers/wait":         {"container": "web", "until": "running", "timeout": 0},
	"d9c_logs/container":          {"container": "web", "tail": 5},
	"d9c_logs/compose":            {"project": "webapp", "tail": 5},
	"d9c_resources/images":        {},
	"d9c_resources/networks":      {},
	"d9c_resources/volumes":       {},
	"d9c_resources/image_history": {"image": "nginx:1.25"},
	"d9c_compose/ls":              {},
	"d9c_compose/ps":              {"project": "webapp"},
	"d9c_compose/config":          {"project": "webapp"},
	"d9c_lifecycle/start":         {"targets": []string{"db"}},
	"d9c_lifecycle/stop":          {"targets": []string{"web"}},
	"d9c_lifecycle/restart":       {"filter": []string{"label=env=prod"}},
	"d9c_deploy/pull":             {"image": "nginx:1.27"},
	"d9c_deploy/image_build":      {"dir": "app", "tag": "app:dev"},
	"d9c_deploy/image_tag":        {"source": "nginx:1.25", "target": "registry.example.com/web:1"},
	"d9c_deploy/image_push":       {"image": "nginx:1.25"},
	"d9c_deploy/compose_up":       {"project": "webapp"},
	"d9c_deploy/compose_pull":     {"project": "webapp"},
	"d9c_deploy/compose_restart":  {"project": "webapp"},
	"d9c_deploy/compose_backup":   {"project": "webapp"},
	"d9c_create/network":          {"name": "mcp-net", "subnet": "10.42.0.0/24"},
	"d9c_create/volume":           {"name": "mcp-vol"},
	"d9c_remove/kill":             {"targets": []string{"api"}, "signal": "TERM"},
	"d9c_remove/rm":               {"targets": []string{"db"}, "force": true},
	"d9c_remove/image_rm":         {"image": "postgres:16"},
	"d9c_remove/compose_down":     {"project": "monitoring"},
	"d9c_remove/compose_restore":  {"project": "webapp", "file": "backup.tar.gz"},
	"d9c_prune/images":            {},
	"d9c_prune/volumes":           {},
	"d9c_prune/system":            {},
	"d9c_exec":                    {"container": "web", "cmd": []string{"echo", "hi"}},
}

// TestEveryActionRuns calls every action of every tool through the MCP client
// on the demo backend and expects a successful envelope.
func TestEveryActionRuns(t *testing.T) {
	t.Chdir(t.TempDir()) // compose backup writes its archive here
	if err := os.WriteFile("backup.tar.gz", []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("app", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("app/Dockerfile", []byte("FROM alpine"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startSession(t, Options{Policy: writePolicy()})
	seen := map[string]bool{}
	for _, tl := range s.srv.tools {
		for _, action := range tl.actions {
			key := tl.def.name + "/" + action
			args := map[string]any{}
			if tl.single() {
				key = tl.def.name
			}
			c, ok := actionCases[key]
			if !ok {
				t.Errorf("%s has no case in actionCases", key)
				continue
			}
			seen[key] = true
			for k, v := range c {
				args[k] = v
			}
			if !tl.single() {
				args[argAction] = action
			}
			t.Run(key, func(t *testing.T) {
				res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tl.def.name, Arguments: args})
				if err != nil {
					t.Fatal(err)
				}
				if res.IsError {
					t.Fatalf("error: %s", res.Content[0].(*mcp.TextContent).Text)
				}
			})
		}
	}
	for key := range actionCases {
		if !seen[key] {
			t.Errorf("actionCases has %s, which is no tool action", key)
		}
	}
}
