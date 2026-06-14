package docker

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFriendlyImageRemoveErr(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		if err := friendlyImageRemoveErr(nil); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("dependent child becomes hint about prune", func(t *testing.T) {
		in := errors.New("Error response from daemon: conflict: unable to delete a08c488a9779 (cannot be forced) - image has dependent child")
		got := friendlyImageRemoveErr(in)
		if got == nil {
			t.Fatal("expected an error, got nil")
		}
		if !strings.Contains(got.Error(), "prune") {
			t.Errorf("expected hint about prune, got %q", got.Error())
		}
	})

	t.Run("must force becomes hint about rm -f", func(t *testing.T) {
		in := errors.New(`Error response from daemon: conflict: unable to remove repository reference "hello-world:latest" (must force) - container 1a2b is using its referenced image`)
		got := friendlyImageRemoveErr(in)
		if got == nil {
			t.Fatal("expected an error, got nil")
		}
		if !strings.Contains(got.Error(), "rm -f") {
			t.Errorf("expected hint about rm -f, got %q", got.Error())
		}
	})

	t.Run("other errors pass through unchanged", func(t *testing.T) {
		in := errors.New("Error response from daemon: invalid reference format")
		got := friendlyImageRemoveErr(in)
		if got != in {
			t.Errorf("expected original error, got %q", got.Error())
		}
	})
}

func TestFormatJSONProgress(t *testing.T) {
	tests := []struct {
		name string
		in   jsonProgress
		want string
	}{
		{"build stream", jsonProgress{Stream: "Step 1/3 : FROM alpine\n"}, "Step 1/3 : FROM alpine"},
		{"push status with id+progress", jsonProgress{Status: "Pushing", ID: "5f70bf18", Progress: "[===>]"}, "5f70bf18: Pushing [===>]"},
		{"status only", jsonProgress{Status: "Preparing"}, "Preparing"},
		{"error wins", jsonProgress{Stream: "ignored", Error: "denied: requested access to the resource is denied"}, "error: denied: requested access to the resource is denied"},
		{"empty keepalive skipped", jsonProgress{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatJSONProgress(tt.in); got != tt.want {
				t.Errorf("formatJSONProgress(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStreamDockerJSON(t *testing.T) {
	const body = `{"stream":"Step 1/2 : FROM alpine\n"}
{"status":"Pulling","id":"abc","progress":"[==>]"}
{"error":"boom"}
`
	ch := streamDockerJSON(io.NopCloser(strings.NewReader(body)))
	var got []string
	for line := range ch {
		got = append(got, line)
	}
	want := []string{"Step 1/2 : FROM alpine", "abc: Pulling [==>]", "error: boom"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCleanHistoryCmd(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/bin/sh -c #(nop)  CMD [\"/run\"]", `CMD ["/run"]`},
		{"/bin/sh -c apk add curl", "apk add curl"},
		{"COPY dir:abc in /app", "COPY dir:abc in /app"},
	}
	for _, tt := range tests {
		if got := cleanHistoryCmd(tt.in); got != tt.want {
			t.Errorf("cleanHistoryCmd(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRegistryFromRef(t *testing.T) {
	tests := []struct{ ref, want string }{
		{"nginx:1.25", ""},
		{"library/nginx:latest", ""},
		{"myregistry:5000/app:1", "myregistry:5000"},
		{"registry.example.com/team/app:1", "registry.example.com"},
		{"localhost/app", "localhost"},
		{"localhost:5000/app:dev", "localhost:5000"},
		{"gcr.io/project/app", "gcr.io"},
	}
	for _, tt := range tests {
		if got := RegistryFromRef(tt.ref); got != tt.want {
			t.Errorf("RegistryFromRef(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestTarDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "app")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := tarDir(dir)
	defer func() { _ = rc.Close() }()

	found := map[string]bool{}
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		found[hdr.Name] = true
	}
	// Paths must be relative to dir and use forward slashes.
	for _, want := range []string{"Dockerfile", "app", "app/main.go"} {
		if !found[want] {
			t.Errorf("entry %q missing from tar (have %v)", want, found)
		}
	}
}

func TestFakeImageOps(t *testing.T) {
	f := NewFakeBackend()

	t.Run("tag adds a row", func(t *testing.T) {
		before := len(f.Images)
		if err := f.TagImage("nginx:1.25", "nginx:test"); err != nil {
			t.Fatalf("tag: %v", err)
		}
		if len(f.Images) != before+1 {
			t.Errorf("image count = %d, want %d", len(f.Images), before+1)
		}
	})

	t.Run("tag unknown errors", func(t *testing.T) {
		if err := f.TagImage("nope:nope", "x:y"); err == nil {
			t.Error("expected error for unknown source image")
		}
	})

	t.Run("build appends image and streams", func(t *testing.T) {
		before := len(f.Images)
		ch, err := f.BuildImage("/some/dir", "myapp:latest")
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		var lines int
		for range ch {
			lines++
		}
		if lines == 0 {
			t.Error("expected streamed build output")
		}
		if len(f.Images) != before+1 {
			t.Errorf("image count = %d, want %d", len(f.Images), before+1)
		}
	})

	t.Run("push streams and echoes auth", func(t *testing.T) {
		ch, err := f.PushImage("myreg:5000/app:1", RegistryAuth{Registry: "myreg:5000", Username: "alice", Password: "s3cret"})
		if err != nil {
			t.Fatalf("push: %v", err)
		}
		var sawAuth bool
		var lines int
		for line := range ch {
			lines++
			if strings.Contains(line, "alice@myreg:5000") {
				sawAuth = true
			}
		}
		if lines == 0 {
			t.Error("expected streamed push output")
		}
		if !sawAuth {
			t.Error("expected auth line to echo username@registry")
		}
	})

	t.Run("history of known image", func(t *testing.T) {
		res, err := f.ImageHistory("nginx:1.25")
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if res == nil || res.RawYAML == "" {
			t.Error("expected non-empty history content")
		}
	})

	t.Run("history of unknown errors", func(t *testing.T) {
		if _, err := f.ImageHistory("nope"); err == nil {
			t.Error("expected error for unknown image")
		}
	})
}

func TestFakeCreateNetwork(t *testing.T) {
	t.Run("appends a row with defaulted driver", func(t *testing.T) {
		f := NewFakeBackend()
		before := len(f.Networks)
		if err := f.CreateNetwork(NetworkCreateOptions{Name: "app-tier", Subnet: "10.5.0.0/16"}); err != nil {
			t.Fatalf("create: %v", err)
		}
		if len(f.Networks) != before+1 {
			t.Fatalf("network count = %d, want %d", len(f.Networks), before+1)
		}
		got := f.Networks[len(f.Networks)-1]
		if got.Name != "app-tier" || got.Driver != "bridge" || got.Subnet != "10.5.0.0/16" {
			t.Errorf("created network = %+v, want name=app-tier driver=bridge subnet=10.5.0.0/16", got)
		}
	})

	t.Run("honours explicit driver", func(t *testing.T) {
		f := NewFakeBackend()
		if err := f.CreateNetwork(NetworkCreateOptions{Name: "ov", Driver: "overlay"}); err != nil {
			t.Fatalf("create: %v", err)
		}
		if got := f.Networks[len(f.Networks)-1]; got.Driver != "overlay" {
			t.Errorf("driver = %q, want overlay", got.Driver)
		}
	})

	t.Run("rejects empty name", func(t *testing.T) {
		f := NewFakeBackend()
		if err := f.CreateNetwork(NetworkCreateOptions{Name: "  "}); err == nil {
			t.Error("expected error for empty name")
		}
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		f := NewFakeBackend()
		if err := f.CreateNetwork(NetworkCreateOptions{Name: "bridge"}); err == nil {
			t.Error("expected error for duplicate name")
		}
	})
}

func TestFakeCreateVolume(t *testing.T) {
	t.Run("appends a row with defaulted driver", func(t *testing.T) {
		f := NewFakeBackend()
		before := len(f.Volumes)
		if err := f.CreateVolume(VolumeCreateOptions{Name: "data"}); err != nil {
			t.Fatalf("create: %v", err)
		}
		if len(f.Volumes) != before+1 {
			t.Fatalf("volume count = %d, want %d", len(f.Volumes), before+1)
		}
		got := f.Volumes[len(f.Volumes)-1]
		if got.Name != "data" || got.Driver != "local" {
			t.Errorf("created volume = %+v, want name=data driver=local", got)
		}
	})

	t.Run("rejects empty name", func(t *testing.T) {
		f := NewFakeBackend()
		if err := f.CreateVolume(VolumeCreateOptions{Name: ""}); err == nil {
			t.Error("expected error for empty name")
		}
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		f := NewFakeBackend()
		if err := f.CreateVolume(VolumeCreateOptions{Name: "pgdata"}); err == nil {
			t.Error("expected error for duplicate name")
		}
	})
}
