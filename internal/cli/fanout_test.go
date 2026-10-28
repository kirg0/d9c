package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/docker"
)

type hostRow struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

func hostResults() []agent.HostResult {
	return []agent.HostResult{
		{Host: "prod", Runtime: "docker", Data: []hostRow{{"web", "running"}, {"db", "exited"}}},
		{Host: "lab", Error: &agent.Error{Code: agent.CodeConnection, Message: "refused\x1b[31m"}},
		{Host: "edge", Runtime: "podman", Data: []hostRow{}},
	}
}

func TestShapeHosts(t *testing.T) {
	results := hostResults()
	key, cols, err := ShapeHosts(results, agent.Shape{Fields: []string{"name"}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if key != "" || strings.Join(cols, ",") != "name" {
		t.Errorf("key %q cols %v", key, cols)
	}
	if !results[0].Truncated || results[2].Truncated || results[1].Data != nil {
		t.Errorf("results = %+v", results)
	}
	if got := results[0].Data.([]any); len(got) != 1 || len(got[0].(map[string]any)) != 1 {
		t.Errorf("prod data = %v", got)
	}
	if _, _, err := ShapeHosts(hostResults(), agent.Shape{Fields: []string{"bogus"}}); err == nil {
		t.Error("unknown field: want a usage error")
	}
}

func TestWriteHosts(t *testing.T) {
	res, err := agent.FanoutResult("ps", hostResults())
	if err != nil {
		t.Fatal(err)
	}
	env := agent.NewEnvelope("prod,lab,edge", docker.RuntimeUnknown, res)
	env.Runtime = ""

	var out, warn bytes.Buffer
	if err := WriteHosts(&out, &warn, env, "", []string{"name", "state"}, FormatText); err != nil {
		t.Fatal(err)
	}
	want := "# prod (docker)\nNAME  STATE\nweb   running\ndb    exited\n\n" +
		"# lab: connection: refused[31m\n\n" +
		"# edge (podman)\nNAME  STATE\n"
	if out.String() != want {
		t.Errorf("text =\n%s\nwant\n%s", out.String(), want)
	}
	if warn.String() != "partial failure: 1 of 3 hosts failed; see data[].error\n" {
		t.Errorf("notice = %q", warn.String())
	}

	out.Reset()
	if err := WriteHosts(&out, &warn, env, "", nil, FormatNDJSON); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], `{"host":"prod","runtime":"docker","data":[`) ||
		!strings.HasPrefix(lines[1], `{"host":"lab","error":{"code":"connection"`) ||
		lines[2] != `{"host":"edge","runtime":"podman","data":[]}` ||
		lines[3] != `{"truncated":false,"partial":true,"hint":"1 of 3 hosts failed; see data[].error"}` {
		t.Errorf("ndjson =\n%s", out.String())
	}

	out.Reset()
	if err := WriteHosts(&out, &warn, env, "", nil, FormatJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), `{"schema":1,"host":"prod,lab,edge","runtime":"","data":[{"host":"prod"`) ||
		!strings.HasSuffix(out.String(), `"partial":true,"hint":"1 of 3 hosts failed; see data[].error"}`+"\n") {
		t.Errorf("json = %s", out.String())
	}
}
