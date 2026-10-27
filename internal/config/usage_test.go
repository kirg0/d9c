package config_test

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/appdir"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/hosts"
	"github.com/kirg0/d9c/internal/plugins"
	"github.com/kirg0/d9c/internal/settings"
)

// TestFileFlagUsageMatchesDefaults pins the defaults that -help advertises for
// the file flags to the real file names and per-user directory, so the texts
// cannot drift from appdir/settings/plugins/hosts again.
func TestFileFlagUsageMatchesDefaults(t *testing.T) {
	oldCmdLine, oldArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = oldCmdLine, oldArgs })
	flag.CommandLine = flag.NewFlagSet("d9c-test", flag.ContinueOnError)
	os.Args = []string{"d9c"}
	config.Load()

	userDir := "~/" + appdir.DirName + "/"
	tests := []struct {
		flag    string
		want    []string
		notWant []string
	}{
		{"config", []string{"default: " + userDir + settings.FileName}, nil},
		{"plugins-file", []string{"default: " + userDir + plugins.FileName}, []string{"next to the binary"}},
		{"hosts-file", []string{hosts.LegacyFileName, "default: next to the binary"}, []string{userDir}},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			f := flag.CommandLine.Lookup(tt.flag)
			if f == nil {
				t.Fatalf("flag -%s is not registered", tt.flag)
			}
			for _, s := range tt.want {
				if !strings.Contains(f.Usage, s) {
					t.Errorf("-%s usage %q does not mention %q", tt.flag, f.Usage, s)
				}
			}
			for _, s := range tt.notWant {
				if strings.Contains(f.Usage, s) {
					t.Errorf("-%s usage %q must not mention %q", tt.flag, f.Usage, s)
				}
			}
		})
	}
}
