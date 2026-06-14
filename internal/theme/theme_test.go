package theme

import (
	"os"
	"path/filepath"
	"testing"

	"d9c/internal/ui/styles"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		want    styles.Palette
		wantErr bool
	}{
		{
			name: "empty config uses default theme",
			cfg:  Config{},
			want: styles.DefaultPalette(),
		},
		{
			name: "named built-in theme",
			cfg:  Config{Theme: "dracula"},
			want: builtins["dracula"],
		},
		{
			name: "theme name is case-insensitive and trimmed",
			cfg:  Config{Theme: "  Nord  "},
			want: builtins["nord"],
		},
		{
			name: "color override on top of theme",
			cfg:  Config{Theme: "dracula", Colors: map[string]string{"primary": "#abcdef"}},
			want: func() styles.Palette {
				p := builtins["dracula"]
				p.Primary = "#abcdef"
				return p
			}(),
		},
		{
			name: "ansi index override",
			cfg:  Config{Colors: map[string]string{"bgalt": "236"}},
			want: func() styles.Palette {
				p := styles.DefaultPalette()
				p.BgAlt = "236"
				return p
			}(),
		},
		{
			name: "short hex override",
			cfg:  Config{Colors: map[string]string{"fg": "#fff"}},
			want: func() styles.Palette {
				p := styles.DefaultPalette()
				p.Fg = "#fff"
				return p
			}(),
		},
		{
			name:    "unknown theme",
			cfg:     Config{Theme: "monokai"},
			wantErr: true,
		},
		{
			name:    "unknown color key",
			cfg:     Config{Colors: map[string]string{"accent": "#fff"}},
			wantErr: true,
		},
		{
			name:    "invalid hex color",
			cfg:     Config{Colors: map[string]string{"primary": "#xyz"}},
			wantErr: true,
		},
		{
			name:    "ansi index out of range",
			cfg:     Config{Colors: map[string]string{"primary": "300"}},
			wantErr: true,
		},
		{
			name:    "empty color value",
			cfg:     Config{Colors: map[string]string{"primary": "  "}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Resolve(%+v) = %v, want error", tt.cfg, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%+v) unexpected error: %v", tt.cfg, err)
			}
			if got != tt.want {
				t.Errorf("Resolve(%+v) = %+v, want %+v", tt.cfg, got, tt.want)
			}
		})
	}
}

func TestLoadMissingFileIsDefault(t *testing.T) {
	pal, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load(missing) error: %v", err)
	}
	if pal != styles.DefaultPalette() {
		t.Errorf("Load(missing) = %+v, want default palette", pal)
	}
}

func TestLoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d9c-config.yaml")
	content := "theme: nord\ncolors:\n  primary: \"#abcdef\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	pal, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := builtins["nord"]
	want.Primary = "#abcdef"
	if pal != want {
		t.Errorf("Load = %+v, want %+v", pal, want)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("theme: [unterminated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load(malformed) = nil error, want error")
	}
}

func TestByName(t *testing.T) {
	if p, ok := ByName("  DRACULA "); !ok || p != builtins["dracula"] {
		t.Errorf("ByName(dracula) = %+v, %v; want dracula palette, true", p, ok)
	}
	if _, ok := ByName("monokai"); ok {
		t.Error("ByName(monokai) ok = true, want false")
	}
}

func TestNameOf(t *testing.T) {
	if got := NameOf(builtins["nord"]); got != "nord" {
		t.Errorf("NameOf(nord palette) = %q, want nord", got)
	}
	custom := styles.DefaultPalette()
	custom.Primary = "#000000"
	if got := NameOf(custom); got != "" {
		t.Errorf("NameOf(custom) = %q, want empty", got)
	}
}

func TestNamesSortedAndComplete(t *testing.T) {
	names := Names()
	if len(names) != len(builtins) {
		t.Fatalf("Names() len = %d, want %d", len(names), len(builtins))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("Names() not sorted: %v", names)
		}
	}
}
