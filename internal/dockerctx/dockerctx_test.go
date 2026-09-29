package dockerctx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kirg0/d9c/internal/hosts"
)

// writeContext lays out a context the way the Docker CLI does: meta.json under
// contexts/meta/<id>/ plus the given TLS files under contexts/tls/<id>/docker/.
func writeContext(t *testing.T, dir, name, meta string, tlsFiles ...string) {
	t.Helper()
	metaDir := filepath.Join(dir, "contexts", "meta", ID(name))
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(tlsFiles) == 0 {
		return
	}
	tlsDir := filepath.Join(dir, "contexts", "tls", ID(name), "docker")
	if err := os.MkdirAll(tlsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range tlsFiles {
		if err := os.WriteFile(filepath.Join(tlsDir, f), []byte("pem"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixture builds a store with a TLS tcp context, an ssh context, a context
// without a docker endpoint, a broken meta.json and a stray file.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeContext(t, dir, "prod", `{"Name":"prod","Metadata":{"Description":"production"},"Endpoints":{"docker":{"Host":"tcp://prod:2376","SkipTLSVerify":true}}}`,
		"ca.pem", "cert.pem", "key.pem")
	writeContext(t, dir, "lab", `{"Name":"lab","Metadata":{},"Endpoints":{"docker":{"Host":" ssh://ops@lab ","SkipTLSVerify":false}}}`)
	writeContext(t, dir, "k8s-only", `{"Name":"k8s-only","Metadata":{},"Endpoints":{"kubernetes":{"Host":"https://k8s"}}}`)
	writeContext(t, dir, "broken", `{not json`)
	if err := os.WriteFile(filepath.Join(dir, "contexts", "meta", "stray.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, dir, `{"auths":{},"currentContext":"lab"}`)
	return dir
}

func TestID(t *testing.T) {
	// Real Docker Desktop store directory for the "desktop-linux" context.
	if got, want := ID("desktop-linux"), "fe9c6bd7a66301f49ca9b6a70b217107cd1284598bfc254700c989b916da791e"; got != want {
		t.Errorf("ID = %s, want %s", got, want)
	}
}

func TestList(t *testing.T) {
	dir := fixture(t)
	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("List = %+v, want lab and prod only", list)
	}
	lab, prod := list[0], list[1]
	if lab.Name != "lab" || lab.Host != "ssh://ops@lab" || !lab.Current || lab.HasTLS() {
		t.Errorf("lab = %+v", lab)
	}
	tlsDir := filepath.Join(dir, "contexts", "tls", ID("prod"), "docker")
	if prod.Name != "prod" || prod.Description != "production" || prod.Current || !prod.SkipTLSVerify ||
		prod.TLSCACert != filepath.Join(tlsDir, "ca.pem") ||
		prod.TLSCert != filepath.Join(tlsDir, "cert.pem") ||
		prod.TLSKey != filepath.Join(tlsDir, "key.pem") {
		t.Errorf("prod = %+v", prod)
	}
}

func TestList_MissingStore(t *testing.T) {
	list, err := List(filepath.Join(t.TempDir(), "nope"))
	if err != nil || list != nil {
		t.Errorf("List(missing) = %v, %v; want nil, nil", list, err)
	}
}

func TestFind(t *testing.T) {
	dir := fixture(t)
	c, err := Find(dir, "lab")
	if err != nil || c.Host != "ssh://ops@lab" || !c.Current {
		t.Errorf("Find(lab) = %+v, %v", c, err)
	}
	c, err = Find(dir, "prod")
	if err != nil || !c.HasTLS() || c.Current {
		t.Errorf("Find(prod) = %+v, %v", c, err)
	}
	for _, name := range []string{"missing", "k8s-only"} {
		if _, err := Find(dir, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Find(%s) err = %v, want ErrNotFound", name, err)
		}
	}
	if _, err := Find(dir, "broken"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Find(broken) err = %v, want a parse error", err)
	}
}

func TestCurrentName(t *testing.T) {
	dir := t.TempDir()
	if got := CurrentName(dir); got != "" {
		t.Errorf("missing config.json = %q", got)
	}
	writeConfig(t, dir, `{broken`)
	if got := CurrentName(dir); got != "" {
		t.Errorf("broken config.json = %q", got)
	}
	writeConfig(t, dir, `{"currentContext":"desktop-linux"}`)
	if got := CurrentName(dir); got != "desktop-linux" {
		t.Errorf("CurrentName = %q", got)
	}
}

func TestDir(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", filepath.Join("x", "docker"))
	if got := Dir(); got != filepath.Join("x", "docker") {
		t.Errorf("Dir with DOCKER_CONFIG = %q", got)
	}
	t.Setenv("DOCKER_CONFIG", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := Dir(); got != filepath.Join(home, ".docker") {
		t.Errorf("Dir = %q", got)
	}
}

func TestSelect(t *testing.T) {
	tests := []struct {
		name        string
		flag        string
		hostFlagSet bool
		env         map[string]string
		want        string
		wantErr     bool
	}{
		{name: "nothing", want: ""},
		{name: "flag", flag: "prod", want: "prod"},
		{name: "flag beats env", flag: "prod", env: map[string]string{"DOCKER_CONTEXT": "lab", "DOCKER_HOST": "tcp://x"}, want: "prod"},
		{name: "flag conflicts with -H", flag: "prod", hostFlagSet: true, wantErr: true},
		{name: "env context", env: map[string]string{"DOCKER_CONTEXT": " lab "}, want: "lab"},
		{name: "DOCKER_HOST beats DOCKER_CONTEXT", env: map[string]string{"DOCKER_CONTEXT": "lab", "DOCKER_HOST": "tcp://x"}, want: ""},
		{name: "-H beats DOCKER_CONTEXT", hostFlagSet: true, env: map[string]string{"DOCKER_CONTEXT": "lab"}, want: ""},
		{name: "default flag", flag: "default", want: ""},
		{name: "default env", env: map[string]string{"DOCKER_CONTEXT": "default"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			got, err := Select(tt.flag, tt.hostFlagSet, getenv)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("Select = %q, %v; want %q, err=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestSavedHost(t *testing.T) {
	c := Context{Name: "prod", Host: "tcp://prod:2376", TLSCACert: "ca", TLSCert: "cert", TLSKey: "key", Current: true}
	want := hosts.Host{Name: "prod", Host: "tcp://prod:2376", TLSCACert: "ca", TLSCert: "cert", TLSKey: "key"}
	if got := c.SavedHost(); got != want {
		t.Errorf("SavedHost = %+v, want %+v", got, want)
	}
}
