package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
)

func TestResolveImage(t *testing.T) {
	list := []docker.Image{
		{ID: "a08c488a9779", Tags: "nginx:1.25, nginx:latest"},
		{ID: "a0ffee000001", Tags: "registry.local:5000/app:2"},
		{ID: "b1d3f9e7c4a2", Tags: "<none>:<none>"},
	}
	tests := []struct {
		ref      string
		wantID   string
		wantCode Code
	}{
		{"nginx:1.25", "a08c488a9779", ""},
		{"nginx", "a08c488a9779", ""}, // bare repository = :latest
		{"registry.local:5000/app:2", "a0ffee000001", ""},
		{"a08c", "a08c488a9779", ""},
		{"sha256:b1d3f9e7c4a2", "b1d3f9e7c4a2", ""},
		{"a08c488a9779deadbeef", "a08c488a9779", ""}, // full ID longer than the listed short one
		{"registry.local:5000/app", "", CodeNotFound},
		{"a0", "", CodeUsage}, // ambiguous prefix
		{"redis", "", CodeNotFound},
		{" ", "", CodeUsage},
	}
	for _, tt := range tests {
		img, err := resolveImage(list, tt.ref)
		if tt.wantCode != "" {
			if err == nil || err.Code != tt.wantCode {
				t.Errorf("resolveImage(%q) err = %v, want %s", tt.ref, err, tt.wantCode)
			}
			continue
		}
		if err != nil || img.ID != tt.wantID {
			t.Errorf("resolveImage(%q) = %s, %v; want %s", tt.ref, img.ID, err, tt.wantID)
		}
	}
}

func TestParseHistory(t *testing.T) {
	docker := "2026-06-01 10:00       42 MB  CMD [\"/app/run\"]\n" +
		"2026-05-20 08:30      7.8 MB  FROM  alpine:3.20\n"
	got, ok := parseHistory(docker)
	want := []HistoryLayer{
		{Created: "2026-06-01 10:00", Size: "42 MB", Command: `CMD ["/app/run"]`},
		{Created: "2026-05-20 08:30", Size: "7.8 MB", Command: "FROM  alpine:3.20"},
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("parseHistory = %+v, %v", got, ok)
	}
	if _, ok := parseHistory("SNAPSHOT  CREATED  CREATED BY  SIZE\nsha256:1  2 days ago  /bin/sh  5 MB\n"); ok {
		t.Error("a nerdctl table parsed as Docker layers")
	}
	if got, ok := parseHistory(""); !ok || len(got) != 0 {
		t.Errorf("empty history = %v, %v", got, ok)
	}
}

func TestImageHistory(t *testing.T) {
	res := invoke(t, docker.NewFakeBackend(), "image history", Args{"image": "nginx:1.25"})
	d := res.Data.(ImageHistoryData)
	if d.Image != "nginx:1.25" || len(d.Layers) != 3 || d.Layers[2].Command != "FROM alpine:3.20" || d.Report != "" {
		t.Errorf("history = %+v", d)
	}
	if e := invokeErr(t, docker.NewFakeBackend(), "image history", Args{"image": "redis"}); e.Code != CodeNotFound {
		t.Errorf("unknown image: %v", e)
	}
}

func TestImageTag(t *testing.T) {
	fb := docker.NewFakeBackend()
	res, err := invokeWrite(fb, "image tag", Args{"source": "nginx:1.25", "target": "registry.example.com/web:1"})
	if err != nil {
		t.Fatal(err)
	}
	if d := res.Data.(ImageTagData); d.ID != "a08c488a9779" || d.Target != "registry.example.com/web:1" {
		t.Errorf("tag = %+v", d)
	}
	list, _ := fb.ListImages()
	if _, e := resolveImage(list, "registry.example.com/web:1"); e != nil {
		t.Errorf("new tag not listed: %v", e)
	}
	for _, args := range []Args{{"source": "nginx:1.25", "target": "bad ref"}, {"source": "redis", "target": "x:1"}} {
		if _, err := invokeWrite(fb, "image tag", args); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}

func TestImageTagPlanMovesTag(t *testing.T) {
	res, err := Builtin().Invoke(context.Background(), &Env{Backend: docker.NewFakeBackend()}, "image tag",
		Args{"source": "postgres:16", "target": "nginx:1.25"}, Call{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if steps := res.Data.(PlanData).Steps; len(steps) != 1 || !strings.Contains(steps[0], "moves off image a08c488a9779") {
		t.Errorf("steps = %v", steps)
	}
}

func TestImagePush(t *testing.T) {
	t.Setenv(envRegistryUser, "ci")
	t.Setenv(envRegistryPassword, "secret")
	res, err := invokeWrite(docker.NewFakeBackend(), "image push", Args{"image": "nginx:1.25"})
	if err != nil {
		t.Fatal(err)
	}
	d := res.Data.(ImageActionData)
	if d.Action != "image push" || d.Image != "nginx:1.25" || !strings.Contains(strings.Join(d.Output, "\n"), "auth: ci@docker.io") {
		t.Errorf("push = %+v", d)
	}
	plan, err := Builtin().Invoke(context.Background(), &Env{Backend: docker.NewFakeBackend()}, "image push",
		Args{"image": "nginx:1.25"}, Call{DryRun: true})
	if err != nil || !strings.Contains(plan.Data.(PlanData).Steps[0], "to docker.io as ci") {
		t.Errorf("plan = %+v, %v", plan.Data, err)
	}
	if _, err := invokeWrite(docker.NewFakeBackend(), "image push", Args{"image": "redis:7"}); Classify(err).Code != CodeNotFound {
		t.Errorf("push of a missing image: %v", err)
	}
}

func TestImageBuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fb := docker.NewFakeBackend()
	res, err := invokeWrite(fb, "image build", Args{"dir": dir, "tag": "app:dev"})
	if err != nil {
		t.Fatal(err)
	}
	if d := res.Data.(ImageActionData); d.Image != "app:dev" || len(d.Output) == 0 {
		t.Errorf("build = %+v", d)
	}
	list, _ := fb.ListImages()
	if _, e := resolveImage(list, "app:dev"); e != nil {
		t.Errorf("built image not listed: %v", e)
	}
	if _, err := invokeWrite(fb, "image build", Args{"dir": filepath.Join(dir, "missing")}); Classify(err).Code != CodeNotFound {
		t.Errorf("missing dir: %v", err)
	}
	if _, err := invokeWrite(fb, "image build", Args{"dir": t.TempDir()}); err == nil || !strings.Contains(err.Error(), "no Dockerfile") {
		t.Errorf("dir without Dockerfile: %v", err)
	}
}

func TestImageRm(t *testing.T) {
	fb := docker.NewFakeBackend()
	res, err := invokeWrite(fb, "image rm", Args{"image": "postgres:16"})
	if err != nil {
		t.Fatal(err)
	}
	if d := res.Data.(ImageRemoveData); d.ID != "c7e8a2b4d6f1" {
		t.Errorf("rm = %+v", d)
	}
	// The fake reproduces the daemon's "in use" conflict for hello-world.
	if _, err := invokeWrite(fb, "image rm", Args{"image": "hello-world:latest"}); err == nil {
		t.Error("removing an image in use without --force succeeded")
	}
	if _, err := invokeWrite(fb, "image rm", Args{"image": "hello-world:latest", "force": true}); err != nil {
		t.Errorf("forced rm: %v", err)
	}
	if _, err := Builtin().Invoke(context.Background(), &Env{Backend: fb}, "image rm", Args{"image": "nginx:1.25"},
		Call{Ceiling: TierDestructive}); Classify(err).Code != CodeConfirmationRequired {
		t.Errorf("unconfirmed rm: %v", err)
	}
}

func TestImageRmPlanNamesUsers(t *testing.T) {
	res, err := Builtin().Invoke(context.Background(), &Env{Backend: docker.NewFakeBackend()}, "image rm",
		Args{"image": "nginx:1.25"}, Call{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	steps := res.Data.(PlanData).Steps
	if len(steps) != 2 || !strings.Contains(steps[1], "used by containers: web") || !strings.Contains(steps[1], "without --force") {
		t.Errorf("steps = %v", steps)
	}
}
