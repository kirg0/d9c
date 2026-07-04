package styles

import "testing"

func TestStateColor(t *testing.T) {
	for _, state := range []string{"running", "exited", "paused", "created", "dead", ""} {
		_ = StateColor(state) // must map every state without panicking
	}
	if StateColor("running").GetForeground() == StateColor("exited").GetForeground() {
		t.Error("running and exited should use different colors")
	}
}

func TestHealthColor(t *testing.T) {
	for _, health := range []string{"healthy", "unhealthy", "starting", ""} {
		_ = HealthColor(health)
	}
	if HealthColor("healthy").GetForeground() == HealthColor("unhealthy").GetForeground() {
		t.Error("healthy and unhealthy should use different colors")
	}
}

func TestComposeStatusColor(t *testing.T) {
	for _, status := range []string{"running", "error", "paused", "partial", "stopped", "unknown"} {
		_ = ComposeStatusColor(status)
	}
	if ComposeStatusColor("running").GetForeground() == ComposeStatusColor("error").GetForeground() {
		t.Error("running and error should use different colors")
	}
}

func TestSwatch(t *testing.T) {
	if got := Swatch(DefaultPalette()); got == "" {
		t.Error("swatch should render color blocks")
	}
}
