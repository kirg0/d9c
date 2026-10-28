package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// skillFile is the file name of a scenario inside its directory: the prompts
// are the agent skills shipped with d9c, so both read one text.
const skillFile = "SKILL.md"

// focusArg is the one argument of every prompt.
const focusArg = "focus"

// scenario is one parsed SKILL.md: frontmatter name and description, and the
// Markdown body that becomes the prompt.
type scenario struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	body        string
}

// loadScenarios reads every <name>/SKILL.md in fsys. A file without a
// frontmatter name matching its directory, a description or a body is an
// error: the skills are part of the build.
func loadScenarios(fsys fs.FS) ([]scenario, error) {
	files, err := fs.Glob(fsys, "*/"+skillFile)
	if err != nil {
		return nil, err
	}
	out := make([]scenario, 0, len(files))
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		sc, err := parseScenario(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if dir := path.Dir(f); sc.Name != dir {
			return nil, fmt.Errorf("%s: name %q does not match the directory %q", f, sc.Name, dir)
		}
		out = append(out, sc)
	}
	return out, nil
}

// parseScenario splits a SKILL.md into its YAML frontmatter and body.
func parseScenario(raw []byte) (scenario, error) {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(raw, []byte("---\n"))
	if !ok {
		return scenario{}, fmt.Errorf("missing the --- frontmatter")
	}
	front, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return scenario{}, fmt.Errorf("unterminated frontmatter")
	}
	var sc scenario
	if err := yaml.Unmarshal(front, &sc); err != nil {
		return scenario{}, fmt.Errorf("frontmatter: %w", err)
	}
	sc.body = strings.TrimSpace(string(body))
	switch {
	case sc.Name == "":
		return scenario{}, fmt.Errorf("frontmatter has no name")
	case sc.Description == "":
		return scenario{}, fmt.Errorf("frontmatter has no description")
	case sc.body == "":
		return scenario{}, fmt.Errorf("empty body")
	}
	return sc, nil
}

// addPrompts registers one prompt per scenario.
func (s *Server) addPrompts(scenarios []scenario) {
	for _, sc := range scenarios {
		s.mcp.AddPrompt(&mcp.Prompt{
			Name:        sc.Name,
			Description: sc.Description,
			Arguments: []*mcp.PromptArgument{{
				Name:        focusArg,
				Description: "What to look at: a container, a compose project, a symptom or a question (optional).",
			}},
		}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{
				Description: sc.Description,
				Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: sc.text(req.Params.Arguments[focusArg])}}},
			}, nil
		})
	}
}

// text renders the prompt message: the skill body, then the user's focus.
func (sc scenario) text(focus string) string {
	if focus = strings.TrimSpace(focus); focus == "" {
		return sc.body
	}
	return sc.body + "\n\n## Focus\n\n" + focus
}
