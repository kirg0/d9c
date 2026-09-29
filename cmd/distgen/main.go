// Command distgen renders the Homebrew formula and the Scoop manifest for a
// release from its checksums.txt. It is run by .github/workflows/release.yml:
//
//	go run ./cmd/distgen -version 1.29.0 -checksums dist/checksums.txt -out pkg
//
// and writes pkg/d9c.rb (homebrew-tap: Formula/d9c.rb) and pkg/d9c.json
// (scoop-bucket: bucket/d9c.json).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kirg0/d9c/internal/dist"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "distgen: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("distgen", flag.ContinueOnError)
	version := fs.String("version", "", "release version (1.2.3; a leading v is stripped)")
	sumsPath := fs.String("checksums", "dist/checksums.txt", "sha256sum output for the release assets")
	repo := fs.String("repo", dist.DefaultRepo, "GitHub repository hosting the release (owner/name)")
	out := fs.String("out", "pkg", "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	f, err := os.Open(*sumsPath)
	if err != nil {
		return fmt.Errorf("open checksums: %w", err)
	}
	defer f.Close()
	sums, err := dist.ParseChecksums(f)
	if err != nil {
		return err
	}
	rel := dist.Release{Version: strings.TrimPrefix(*version, "v"), Repo: *repo, Sums: sums}

	formula, err := rel.HomebrewFormula()
	if err != nil {
		return fmt.Errorf("homebrew formula: %w", err)
	}
	manifest, err := rel.ScoopManifest()
	if err != nil {
		return fmt.Errorf("scoop manifest: %w", err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	for name, data := range map[string][]byte{"d9c.rb": []byte(formula), "d9c.json": manifest} {
		path := filepath.Join(*out, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Println("wrote", path)
	}
	return nil
}
