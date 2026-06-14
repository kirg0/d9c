package filter

import (
	"fmt"
	"regexp"
	"strings"
)

// Target holds the searchable attributes of a single table row. Resources fill
// in whatever they have: every resource sets Text (the free-text haystack);
// containers/compose also set Status, and containers set Labels and Networks.
// Field predicates against attributes a resource doesn't carry simply never
// match (e.g. label: on an image), which keeps the matcher generic.
type Target struct {
	Text     string            // free-text haystack (name+image+status…), matched case-insensitively
	Status   string            // status/state text for status: predicates
	Labels   map[string]string // container labels for label: predicates
	Networks []string          // attached network names for network: predicates
}

// Matcher is a compiled filter query. It is built once per refresh by Compile
// and reused across all rows. The zero value matches everything.
//
// Query syntax (terms separated by spaces, all ANDed together):
//
//	foo                bare word — case-insensitive substring of the haystack
//	re:^web-[0-9]+$    regexp (always case-insensitive) over the haystack
//	status:running     substring of the status/state text
//	label:env          row has a label with this key…
//	label:env=prod     …optionally constrained to a value
//	network:bridge     row is attached to a network whose name contains this
//	net:bridge         alias for network:
//
// A bare word containing none of the prefixes is a plain substring, so the
// common case ("just type part of a name") keeps working unchanged.
type Matcher struct {
	terms []term
	err   error
}

type term struct {
	kind     termKind
	text     string         // lowercased needle for substring/label-key/value/network
	value    string         // lowercased label value (kind == termLabel, hasValue)
	hasValue bool           // label:key=value vs label:key
	re       *regexp.Regexp // kind == termRegex
}

type termKind int

const (
	termText termKind = iota
	termRegex
	termStatus
	termLabel
	termNetwork
)

// Compile parses a filter query into a Matcher. A malformed regexp leaves the
// Matcher in an error state (Err != nil); Match then rejects every row so the
// table goes empty rather than ignoring the broken term. Compile never returns
// nil — callers use the zero-term Matcher for an empty query.
func Compile(query string) *Matcher {
	m := &Matcher{}
	for tok := range strings.FieldsSeq(query) {
		t, err := parseTerm(tok)
		if err != nil {
			m.err = err
			return m
		}
		m.terms = append(m.terms, t)
	}
	return m
}

func parseTerm(tok string) (term, error) {
	switch {
	case strings.HasPrefix(tok, "re:"):
		pat := strings.TrimPrefix(tok, "re:")
		re, err := regexp.Compile("(?i)" + pat)
		if err != nil {
			return term{}, fmt.Errorf("invalid regexp %q: %w", pat, err)
		}
		return term{kind: termRegex, re: re}, nil
	case strings.HasPrefix(tok, "status:"):
		return term{kind: termStatus, text: strings.ToLower(strings.TrimPrefix(tok, "status:"))}, nil
	case strings.HasPrefix(tok, "label:"):
		kv := strings.TrimPrefix(tok, "label:")
		if key, val, ok := strings.Cut(kv, "="); ok {
			return term{kind: termLabel, text: strings.ToLower(key), value: strings.ToLower(val), hasValue: true}, nil
		}
		return term{kind: termLabel, text: strings.ToLower(kv)}, nil
	case strings.HasPrefix(tok, "network:"):
		return term{kind: termNetwork, text: strings.ToLower(strings.TrimPrefix(tok, "network:"))}, nil
	case strings.HasPrefix(tok, "net:"):
		return term{kind: termNetwork, text: strings.ToLower(strings.TrimPrefix(tok, "net:"))}, nil
	default:
		return term{kind: termText, text: strings.ToLower(tok)}, nil
	}
}

// Err returns the parse error (currently only a bad regexp), or nil.
func (m *Matcher) Err() error {
	if m == nil {
		return nil
	}
	return m.err
}

// Match reports whether t satisfies every term in the query. An empty query
// (no terms) matches everything; a query with a parse error matches nothing.
func (m *Matcher) Match(t Target) bool {
	if m == nil {
		return true
	}
	if m.err != nil {
		return false
	}
	for _, term := range m.terms {
		if !term.match(t) {
			return false
		}
	}
	return true
}

func (t term) match(tgt Target) bool {
	switch t.kind {
	case termRegex:
		return t.re.MatchString(tgt.Text)
	case termStatus:
		return strings.Contains(strings.ToLower(tgt.Status), t.text)
	case termLabel:
		for k, v := range tgt.Labels {
			if !strings.EqualFold(k, t.text) {
				continue
			}
			if !t.hasValue {
				return true
			}
			if strings.EqualFold(v, t.value) {
				return true
			}
		}
		return false
	case termNetwork:
		for _, n := range tgt.Networks {
			if strings.Contains(strings.ToLower(n), t.text) {
				return true
			}
		}
		return false
	default: // termText
		return strings.Contains(strings.ToLower(tgt.Text), t.text)
	}
}
