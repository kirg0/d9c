package config

import (
	"os"
	"testing"
)

func TestGetenv_ReturnsEnvVar(t *testing.T) {
	os.Setenv("TEST_VAR", "hello")
	defer os.Unsetenv("TEST_VAR")

	got := getenv("TEST_VAR", "fallback")
	if got != "hello" {
		t.Errorf("getenv() = %q, want %q", got, "hello")
	}
}

func TestGetenv_ReturnsFallback(t *testing.T) {
	os.Unsetenv("TEST_VAR_MISSING")
	got := getenv("TEST_VAR_MISSING", "fallback")
	if got != "fallback" {
		t.Errorf("getenv() = %q, want %q", got, "fallback")
	}
}
