package agent

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
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

// redactComposeConfig masks secret-looking environment values in a compose
// file the way redactEnv does in an inspect tree, but on the YAML nodes, so
// the file keeps its key order and comments. Text that does not parse is an
// error rather than passed through: it may still hold secrets.
func redactComposeConfig(text string) (string, int, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return "", 0, Errorf(CodeInternal, "compose config: cannot parse the file to mask secrets: %v", err).
			WithHint("pass --no-redact to get the file as is")
	}
	n := redactNode(&doc)
	if n == 0 {
		return text, 0, nil
	}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", 0, err
	}
	if err := enc.Close(); err != nil {
		return "", 0, err
	}
	return b.String(), n, nil
}

// redactNode is redactEnv over a YAML node tree.
func redactNode(nd *yaml.Node) int {
	n := 0
	switch nd.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range nd.Content {
			n += redactNode(c)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(nd.Content); i += 2 {
			if envKeys[strings.ToLower(nd.Content[i].Value)] {
				n += redactEnvNode(nd.Content[i+1])
				continue
			}
			n += redactNode(nd.Content[i+1])
		}
	}
	return n
}

// redactEnvNode is redactEnvValue over a YAML node: a list of "NAME=value"
// scalars or a NAME → value mapping (compose `environment`).
func redactEnvNode(nd *yaml.Node) int {
	n := 0
	switch nd.Kind {
	case yaml.SequenceNode:
		for _, item := range nd.Content {
			if item.Kind != yaml.ScalarNode {
				n += redactNode(item)
				continue
			}
			if name, _, ok := strings.Cut(item.Value, "="); ok && isSecretName(name) {
				setScalar(item, name+"="+redactedValue)
				n++
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(nd.Content); i += 2 {
			k, v := nd.Content[i], nd.Content[i+1]
			switch {
			case v.Kind != yaml.ScalarNode:
				n += redactNode(v)
			case v.Tag == "!!null": // `NAME:` with no value passes the host's value through
			case isSecretName(k.Value):
				setScalar(v, redactedValue)
				n++
			}
		}
	default:
		n += redactNode(nd)
	}
	return n
}

// setScalar replaces a scalar node's value with a plain string.
func setScalar(nd *yaml.Node, value string) {
	nd.Value, nd.Tag, nd.Style = value, "!!str", 0
}

// noRedactArg is the opt-out of the masking, shared by the ops that redact.
func noRedactArg() *Schema {
	return Boolean("Show the values of environment variables whose names contain " +
		"PASS, TOKEN, SECRET or KEY (masked by default).")
}

// redactHint tells the caller how many values were masked and how to see them.
func redactHint(n int) string {
	return fmt.Sprintf("%d secret-looking env value(s) masked; pass --no-redact to show them", n)
}
