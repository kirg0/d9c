package agent

import (
	"fmt"
	"strings"
)

// redactedValue replaces the value of a secret-looking environment variable.
const redactedValue = "<redacted>"

// secretMarkers are the substrings that make an environment variable name
// look like a secret (matched case-insensitively).
var secretMarkers = []string{"PASS", "TOKEN", "SECRET", "KEY"}

// envKeys are the object keys, compared case-insensitively, that hold an
// environment: docker/podman/nerdctl `Config.Env` and `Process.Env`, CRI
// `config.envs`, compose `environment`.
var envKeys = map[string]bool{"env": true, "envs": true, "environment": true}

// isSecretName reports whether an environment variable name looks like it
// carries a secret.
func isSecretName(name string) bool {
	up := strings.ToUpper(name)
	for _, m := range secretMarkers {
		if strings.Contains(up, m) {
			return true
		}
	}
	return false
}

// redactEnv masks, in place, the values of secret-looking variables in every
// environment found in a decoded inspect tree, and returns how many it masked.
// It understands the three shapes engines use: a list of "NAME=value"
// strings, a list of {key|name, value} objects, and a NAME → value map.
func redactEnv(v any) int {
	n := 0
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if envKeys[strings.ToLower(k)] {
				n += redactEnvValue(e)
				continue
			}
			n += redactEnv(e)
		}
	case []any:
		for _, e := range t {
			n += redactEnv(e)
		}
	}
	return n
}

// redactEnvValue masks one environment in any of the shapes redactEnv lists;
// anything else is walked like an ordinary subtree.
func redactEnvValue(v any) int {
	n := 0
	switch t := v.(type) {
	case []any:
		for i, e := range t {
			switch item := e.(type) {
			case string:
				if name, _, ok := strings.Cut(item, "="); ok && isSecretName(name) {
					t[i] = name + "=" + redactedValue
					n++
				}
			case map[string]any:
				name, ok := envItemName(item)
				if _, has := item["value"]; ok && has && isSecretName(name) {
					item["value"] = redactedValue
					n++
				}
			}
		}
	case map[string]any:
		for k, e := range t {
			switch e.(type) {
			case map[string]any, []any:
				n += redactEnv(e)
			case nil: // `NAME:` with no value passes the host's value through
			default:
				if isSecretName(k) {
					t[k] = redactedValue
					n++
				}
			}
		}
	default:
		n += redactEnv(v)
	}
	return n
}

// envItemName returns the variable name of a {key|name, value} entry.
func envItemName(item map[string]any) (string, bool) {
	for _, k := range []string{"key", "name"} {
		if s, ok := item[k].(string); ok {
			return s, true
		}
	}
	return "", false
}

// redactHint tells the caller how many values were masked and how to see them.
func redactHint(n int) string {
	return fmt.Sprintf("%d secret-looking env value(s) masked; pass --no-redact to show them", n)
}
