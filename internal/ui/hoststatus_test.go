package ui

import (
	"strings"
	"testing"
	"time"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/hosts"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSummarizeHostsCmd checks the batch summarizes every host and maps URLs to
// their daemon snapshot.
func TestSummarizeHostsCmd(t *testing.T) {
	summarize := func(h string) docker.HostSummary {
		if strings.Contains(h, "dead") {
			return docker.HostSummary{Host: h, Err: "connection refused"}
		}
		return docker.HostSummary{Host: h, Reachable: true, Containers: 4, Version: "27.4.0"}
	}
	cmd := summarizeHostsCmd(summarize, []string{"tcp://live:2375", "ssh://user@dead"})
	if cmd == nil {
		t.Fatal("expected a summary command")
	}
	msg, ok := cmd().(hostSummariesMsg)
	if !ok {
		t.Fatalf("expected hostSummariesMsg, got %T", cmd())
	}
	if !msg.summaries["tcp://live:2375"].Reachable {
		t.Error("live host should be reported reachable")
	}
	if msg.summaries["ssh://user@dead"].Reachable {
		t.Error("dead host should be reported unreachable")
	}
}

func TestSummarizeHostsCmd_NoWork(t *testing.T) {
	if summarizeHostsCmd(func(string) docker.HostSummary { return docker.HostSummary{} }, nil) != nil {
		t.Error("no hosts should yield no command")
	}
	if summarizeHostsCmd(nil, []string{"tcp://h:2375"}) != nil {
		t.Error("no summarizer should yield no command")
	}
}

// TestHostsViewSummarizesAndRendersStatus walks the full flow: a hosts update
// kicks off one summary batch (in-flight and interval guarded), and the results
// render in the STATUS column plus the aggregate counts.
func TestHostsViewSummarizesAndRendersStatus(t *testing.T) {
	store := &hosts.Store{}
	_ = store.Add("prod", "tcp://prod:2375")
	_ = store.Add("lab", "ssh://user@lab")

	var tm tea.Model = NewModel(&config.Config{Demo: true}, docker.NewDisconnected(nil), store, nil, true)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	tm, cmd := tm.Update(hostsUpdatedMsg{store.List()})
	m := tm.(Model)
	if !m.summaryInFlight || cmd == nil {
		t.Fatalf("hosts update should start a summary batch (inFlight=%v, cmd nil=%v)", m.summaryInFlight, cmd == nil)
	}
	if !strings.Contains(m.View(), "…") {
		t.Error("un-summarized hosts should show the pending placeholder")
	}

	// A second update while the batch runs must not start another one.
	tm, cmd = tm.Update(hostsUpdatedMsg{store.List()})
	if cmd != nil {
		t.Error("no second batch may start while one is in flight")
	}

	tm, _ = tm.Update(hostSummariesMsg{summaries: map[string]docker.HostSummary{
		"tcp://prod:2375": {Reachable: true, Containers: 3, Version: "27.4.0"},
		"ssh://user@lab":  {Err: "down"},
	}})
	m = tm.(Model)
	if m.summaryInFlight {
		t.Error("summary results should release the in-flight guard")
	}
	view := m.View()
	if !strings.Contains(view, "● up") || !strings.Contains(view, "● down") {
		t.Errorf("STATUS column should show up and down, got:\n%s", view)
	}
	if !strings.Contains(view, "27.4.0") {
		t.Errorf("aggregate VERSION column should render, got:\n%s", view)
	}

	// Right after a batch the interval hasn't elapsed — no new summaries yet.
	tm, cmd = tm.Update(hostsUpdatedMsg{store.List()})
	if cmd != nil {
		t.Error("no new batch before hostSummaryInterval elapses")
	}

	// Once the interval passes, the next hosts update summarizes again.
	m = tm.(Model)
	m.lastHostSummary = time.Now().Add(-hostSummaryInterval)
	tm = m
	tm, cmd = tm.Update(hostsUpdatedMsg{store.List()})
	if cmd == nil || !tm.(Model).summaryInFlight {
		t.Error("expected a new summary batch after the interval")
	}
}
