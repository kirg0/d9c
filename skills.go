package main

import (
	"embed"
	"io/fs"
)

// skillFiles holds the agent skills of this repository's plugin. The MCP
// server serves them as prompts, so a scenario has one text in both places.
//
//go:embed plugins/d9c/skills/*/SKILL.md
var skillFiles embed.FS

// skills returns the skill directories (<name>/SKILL.md).
func skills() fs.FS {
	sub, err := fs.Sub(skillFiles, "plugins/d9c/skills")
	if err != nil {
		panic(err) // the embedded path is fixed at build time
	}
	return sub
}
