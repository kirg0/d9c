package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/cli"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/version"
)

// skillsCommand installs the embedded agent skills for agents that read
// skill directories.
const skillsCommand = "skills"

// skillsHelp is the usage text of `d9c skills -h`.
const skillsHelp = `usage: d9c skills ls
       d9c skills install (--target <agent> | --dir <path>) [--dry-run]

Install the agent skills shipped with this d9c binary (same version) as
<dir>/<name>/SKILL.md. --target picks the user-level skills directory of an
agent; --dir any directory, e.g. .claude/skills of a project. Existing d9c
skills there are replaced; other skills are left alone.

Targets:
`

// skillTarget is an agent's user-level skills directory, relative to home.
type skillTarget struct {
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Agents string `json:"agents"`
}

// skillTargets are the agents `--target` knows, from their documentation.
var skillTargets = []skillTarget{
	{Name: "claude", Dir: ".claude/skills", Agents: "Claude Code"},
	{Name: "agents", Dir: ".agents/skills", Agents: "the shared directory of Codex, Gemini CLI, Cursor, GitHub Copilot and OpenCode"},
	{Name: "codex", Dir: ".agents/skills", Agents: "OpenAI Codex"},
	{Name: "gemini", Dir: ".gemini/skills", Agents: "Gemini CLI"},
	{Name: "cursor", Dir: ".cursor/skills", Agents: "Cursor"},
	{Name: "copilot", Dir: ".copilot/skills", Agents: "GitHub Copilot"},
	{Name: "opencode", Dir: ".config/opencode/skills", Agents: "OpenCode"},
}

// userHome locates the home directory the targets are relative to.
var userHome = os.UserHomeDir

// SkillEntry is one embedded skill in `skills ls`.
type SkillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SkillsListData is the `skills ls` output.
type SkillsListData struct {
	Version string        `json:"version"`
	Skills  []SkillEntry  `json:"skills"`
	Targets []skillTarget `json:"targets"`
}

// InstalledSkill is one skill written by `skills install`.
type InstalledSkill struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Result string `json:"result"` // created, updated or unchanged
}

// SkillsInstallData is the `skills install` output.
type SkillsInstallData struct {
	Version string           `json:"version"`
	Dir     string           `json:"dir"`
	DryRun  bool             `json:"dry_run,omitempty"`
	Skills  []InstalledSkill `json:"skills"`
}

// ItemsKey implements agent.Lister: the installed skills are the items.
func (SkillsInstallData) ItemsKey() string { return "skills" }

// runSkills serves `d9c skills ...` and returns the exit status.
func runSkills(fsys fs.FS, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == cli.HelpCommand {
		_, _ = fmt.Fprint(stdout, skillsHelp+targetsUsage())
		return 0
	}
	var data any
	var err error
	switch args[0] {
	case "ls":
		if len(args) > 1 {
			err = agent.Errorf(agent.CodeUsage, "skills ls: unexpected argument %q", args[1])
			break
		}
		data, err = listSkills(fsys)
	case "install":
		data, err = installSkills(fsys, args[1:], stdout)
	default:
		err = agent.Errorf(agent.CodeUsage, "unknown skills command %q", args[0]).
			WithHint("usage: d9c skills ls | d9c skills install --target <agent>")
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return cli.WriteError(stderr, err)
	}
	env := agent.NewEnvelope("", docker.RuntimeUnknown, agent.Result{Data: data})
	env.Runtime = ""
	if err := cli.WriteResult(stdout, stderr, env, cli.Output{}); err != nil {
		return cli.WriteError(stderr, err)
	}
	return 0
}

// targetsUsage lists the --target names for the help text.
func targetsUsage() string {
	var b strings.Builder
	for _, t := range skillTargets {
		fmt.Fprintf(&b, "  %-9s ~/%s (%s)\n", t.Name, t.Dir, t.Agents)
	}
	return b.String()
}

// skillMeta is the frontmatter of a SKILL.md as far as d9c reads it.
type skillMeta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// readSkills returns the embedded skills by name, sorted, with their raw text.
func readSkills(fsys fs.FS) ([]SkillEntry, map[string][]byte, error) {
	files, err := fs.Glob(fsys, "*/SKILL.md")
	if err != nil {
		return nil, nil, err
	}
	var list []SkillEntry
	raw := map[string][]byte{}
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, nil, err
		}
		front, _, err := splitFrontmatter(b)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f, err)
		}
		var m skillMeta
		if err := yaml.Unmarshal(front, &m); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f, err)
		}
		name := path.Dir(f)
		list = append(list, SkillEntry{Name: name, Description: m.Description})
		raw[name] = b
	}
	slices.SortFunc(list, func(a, b SkillEntry) int { return strings.Compare(a.Name, b.Name) })
	return list, raw, nil
}

// splitFrontmatter cuts a SKILL.md into its YAML frontmatter and the body.
func splitFrontmatter(raw []byte) (front, body []byte, err error) {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(raw, []byte("---\n"))
	if !ok {
		return nil, nil, errors.New("missing the --- frontmatter")
	}
	front, body, ok = bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return nil, nil, errors.New("unterminated frontmatter")
	}
	return front, body, nil
}

// stampVersion records the d9c version in the skill's frontmatter metadata
// (the Agent Skills metadata map), so an installed copy says which binary it
// belongs to.
func stampVersion(raw []byte, v string) ([]byte, error) {
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(front)
	fmt.Fprintf(&b, "\nmetadata:\n  d9c-version: %s\n---\n", strconv.Quote(v))
	b.Write(body)
	return b.Bytes(), nil
}

func listSkills(fsys fs.FS) (SkillsListData, error) {
	list, _, err := readSkills(fsys)
	if err != nil {
		return SkillsListData{}, err
	}
	return SkillsListData{Version: version.Version, Skills: list, Targets: skillTargets}, nil
}

// skillsDir resolves --target / --dir to the directory skills go into.
func skillsDir(target, dir string) (string, error) {
	switch {
	case target != "" && dir != "":
		return "", agent.Errorf(agent.CodeUsage, "skills install: pass --target or --dir, not both")
	case dir != "":
		return filepath.Abs(dir)
	case target == "":
		return "", agent.Errorf(agent.CodeUsage, "skills install: pass --target <agent> or --dir <path>").
			WithHint("targets: " + targetNames())
	}
	i := slices.IndexFunc(skillTargets, func(t skillTarget) bool { return t.Name == target })
	if i < 0 {
		return "", agent.Errorf(agent.CodeUsage, "skills install: unknown target %q", target).
			WithHint("targets: " + targetNames() + "; or --dir <path> for another agent")
	}
	home, err := userHome()
	if err != nil {
		return "", agent.Errorf(agent.CodeInternal, "skills install: no home directory: %v", err).
			WithHint("pass --dir <path> instead")
	}
	return filepath.Join(home, filepath.FromSlash(skillTargets[i].Dir)), nil
}

func targetNames() string {
	names := make([]string, len(skillTargets))
	for i, t := range skillTargets {
		names[i] = t.Name
	}
	return strings.Join(names, ", ")
}

// installSkills writes every embedded skill into the chosen directory.
func installSkills(fsys fs.FS, args []string, stdout io.Writer) (SkillsInstallData, error) {
	fl := flag.NewFlagSet("skills install", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	target := fl.String("target", "", "agent whose skills directory to use")
	dir := fl.String("dir", "", "directory to install into")
	dryRun := fl.Bool("dry-run", false, "report what would be written, write nothing")
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(stdout, skillsHelp+targetsUsage())
			return SkillsInstallData{}, err
		}
		return SkillsInstallData{}, agent.Errorf(agent.CodeUsage, "skills install: %v", err).
			WithHint("usage: d9c skills install (--target <agent> | --dir <path>) [--dry-run]")
	}
	if fl.NArg() > 0 {
		return SkillsInstallData{}, agent.Errorf(agent.CodeUsage, "skills install: unexpected argument %q", fl.Arg(0))
	}
	root, err := skillsDir(*target, *dir)
	if err != nil {
		return SkillsInstallData{}, err
	}
	list, raw, err := readSkills(fsys)
	if err != nil {
		return SkillsInstallData{}, err
	}
	out := SkillsInstallData{Version: version.Version, Dir: root, DryRun: *dryRun, Skills: []InstalledSkill{}}
	for _, s := range list {
		content, err := stampVersion(raw[s.Name], version.Version)
		if err != nil {
			return SkillsInstallData{}, err
		}
		file := filepath.Join(root, s.Name, "SKILL.md")
		res := "created"
		if old, err := os.ReadFile(file); err == nil {
			res = "updated"
			if bytes.Equal(old, content) {
				res = "unchanged"
			}
		}
		if !*dryRun && res != "unchanged" {
			if err := writeSkill(file, content); err != nil {
				return SkillsInstallData{}, err
			}
		}
		out.Skills = append(out.Skills, InstalledSkill{Name: s.Name, Path: file, Result: res})
	}
	return out, nil
}

// writeSkill writes one SKILL.md, creating its directory.
func writeSkill(file string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return agent.Errorf(agent.CodeInternal, "skills install: %v", err)
	}
	if err := os.WriteFile(file, content, 0o644); err != nil {
		return agent.Errorf(agent.CodeInternal, "skills install: %v", err)
	}
	return nil
}
