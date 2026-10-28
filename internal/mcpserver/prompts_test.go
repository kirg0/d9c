package mcpserver

import (
	"context"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kirg0/d9c/internal/agent"
)

// shippedSkills are the skills of the repository's plugin.
var shippedSkills = os.DirFS("../../plugins/d9c/skills")

func TestParseScenario(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
		want    scenario
	}{
		{"ok", "---\nname: x\ndescription: Does x.\n---\n\n# X\n\nbody\n", "", scenario{Name: "x", Description: "Does x.", body: "# X\n\nbody"}},
		{"crlf", "---\r\nname: x\r\ndescription: D.\r\n---\r\nbody\r\n", "", scenario{Name: "x", Description: "D.", body: "body"}},
		{"no frontmatter", "# X\n", "missing", scenario{}},
		{"unterminated", "---\nname: x\n", "unterminated", scenario{}},
		{"bad yaml", "---\nname: [\n---\nbody", "frontmatter", scenario{}},
		{"no name", "---\ndescription: D.\n---\nbody", "no name", scenario{}},
		{"no description", "---\nname: x\n---\nbody", "no description", scenario{}},
		{"empty body", "---\nname: x\ndescription: D.\n---\n\n", "empty body", scenario{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseScenario([]byte(tt.raw))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestLoadScenariosNameMustMatchDir(t *testing.T) {
	fsys := fstest.MapFS{"a/SKILL.md": {Data: []byte("---\nname: b\ndescription: D.\n---\nbody")}}
	if _, err := loadScenarios(fsys); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("err = %v", err)
	}
}

// The shipped skills parse, and every MCP tool and action they name exists.
func TestShippedSkills(t *testing.T) {
	scenarios, err := loadScenarios(shippedSkills)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, sc := range scenarios {
		names = append(names, sc.Name)
	}
	if !slices.Contains(names, "d9c") || len(names) < 2 {
		t.Fatalf("skills = %v, want the base d9c skill and scenarios", names)
	}
	tools, err := buildTools(agent.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*tool{}
	for _, tl := range tools {
		byName[tl.def.name] = tl
	}
	toolRE := regexp.MustCompile("`(d9c_[a-z_]+)`")
	callRE := regexp.MustCompile("`(d9c_[a-z_]+)` `\\{action: \"([a-z_]+)\"")
	argsRE := regexp.MustCompile("`(d9c_[a-z_]+)` `\\{([^`]*)\\}`")
	keyRE := regexp.MustCompile(`(?:^|, )([a-z_]+): `)
	for _, sc := range scenarios {
		for _, m := range toolRE.FindAllStringSubmatch(sc.body, -1) {
			if byName[m[1]] == nil {
				t.Errorf("%s names the unknown tool %s", sc.Name, m[1])
			}
		}
		for _, m := range callRE.FindAllStringSubmatch(sc.body, -1) {
			if tl := byName[m[1]]; tl != nil && !slices.Contains(tl.actions, m[2]) {
				t.Errorf("%s calls %s with the unknown action %q", sc.Name, m[1], m[2])
			}
		}
		for _, m := range argsRE.FindAllStringSubmatch(sc.body, -1) {
			tl := byName[m[1]]
			if tl == nil {
				continue
			}
			for _, k := range keyRE.FindAllStringSubmatch(m[2], -1) {
				if _, ok := tl.schema.Properties[k[1]]; !ok {
					t.Errorf("%s passes %s the unknown argument %q", sc.Name, m[1], k[1])
				}
			}
		}
	}
}

func TestPrompts(t *testing.T) {
	s := startSession(t, Options{Scenarios: shippedSkills})
	ctx := context.Background()
	list, err := s.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var troubleshoot *mcp.Prompt
	for _, p := range list.Prompts {
		if p.Name == "d9c-troubleshoot" {
			troubleshoot = p
		}
	}
	if troubleshoot == nil || troubleshoot.Description == "" || len(troubleshoot.Arguments) != 1 || troubleshoot.Arguments[0].Required {
		t.Fatalf("d9c-troubleshoot prompt = %+v", troubleshoot)
	}
	res, err := s.GetPrompt(ctx, &mcp.GetPromptParams{Name: "d9c-troubleshoot", Arguments: map[string]string{"focus": "web keeps restarting"}})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Messages[0].Content.(*mcp.TextContent).Text
	raw, _ := os.ReadFile("../../plugins/d9c/skills/d9c-troubleshoot/SKILL.md")
	sc, _ := parseScenario(raw)
	if res.Messages[0].Role != "user" || !strings.HasPrefix(text, sc.body) || !strings.HasSuffix(text, "## Focus\n\nweb keeps restarting") {
		t.Errorf("prompt text = %q", text)
	}
	res, err = s.GetPrompt(ctx, &mcp.GetPromptParams{Name: "d9c-troubleshoot"})
	if err != nil || res.Messages[0].Content.(*mcp.TextContent).Text != sc.body {
		t.Errorf("prompt without focus = %v %+v", err, res)
	}
}

func TestNewRejectsBadScenarios(t *testing.T) {
	bad := fstest.MapFS{"x/SKILL.md": {Data: []byte("no frontmatter")}}
	if _, err := New(Options{Registry: agent.Builtin(), Scenarios: bad}); err == nil {
		t.Error("New accepted a broken skill")
	}
}

func TestLogsTarget(t *testing.T) {
	tests := []struct {
		uri    string
		want   string
		wantOK bool
	}{
		{"d9c://containers/web/logs", "web", true},
		{"d9c://containers/my%20app/logs", "my app", true},
		{"d9c://containers//logs", "", false},
		{"d9c://containers/a/b/logs", "", false},
		{"d9c://containers/web", "", false},
		{"d9c://hosts", "", false},
		{"d9c://containers/%zz/logs", "", false},
	}
	for _, tt := range tests {
		if got, ok := logsTarget(tt.uri); got != tt.want || ok != tt.wantOK {
			t.Errorf("logsTarget(%q) = %q, %v; want %q, %v", tt.uri, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestResources(t *testing.T) {
	s := startSession(t, Options{})
	ctx := context.Background()
	list, err := s.ListResources(ctx, nil)
	if err != nil || len(list.Resources) != 1 || list.Resources[0].URI != hostsURI {
		t.Fatalf("resources = %+v, %v", list, err)
	}
	tpl, err := s.ListResourceTemplates(ctx, nil)
	if err != nil || len(tpl.ResourceTemplates) != 1 || tpl.ResourceTemplates[0].URITemplate != logsURITemplate {
		t.Fatalf("templates = %+v, %v", tpl, err)
	}

	res, err := s.ReadResource(ctx, &mcp.ReadResourceParams{URI: hostsURI})
	if err != nil || res.Contents[0].MIMEType != jsonMIME || !strings.Contains(res.Contents[0].Text, `"runtime":""`) {
		t.Errorf("hosts resource = %+v, %v", res, err)
	}
	res, err = s.ReadResource(ctx, &mcp.ReadResourceParams{URI: "d9c://containers/web/logs"})
	if err != nil || !strings.Contains(res.Contents[0].Text, `"lines"`) || !strings.Contains(res.Contents[0].Text, `"host":"demo"`) {
		t.Errorf("logs resource = %+v, %v", res, err)
	}
	if _, err := s.ReadResource(ctx, &mcp.ReadResourceParams{URI: "d9c://containers/ghost/logs"}); err == nil {
		t.Error("logs of a missing container: no error")
	}
}
