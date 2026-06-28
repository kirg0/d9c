// Package i18n provides the application's UI language selection. The active
// language lives in a package-level variable (set once at startup from the
// config file and again on a runtime `:lang` switch), mirroring the global
// styling pattern in internal/ui/styles: the TUI is a single instance, so a
// process-wide current language is both sufficient and idiomatic here.
//
// Translations are kept inline at the call site via T(ru, en) rather than in a
// key-indexed catalog: there are only two languages and the set of strings is
// small, so pairing the two variants where the text is used keeps them visible
// and easy to review. The default language is Russian, so T returns the Russian
// variant until something calls Set(EN).
package i18n

import (
	"fmt"
	"strings"
)

// Lang is a supported UI language code.
type Lang string

const (
	// RU is Russian, the default language.
	RU Lang = "ru"
	// EN is English.
	EN Lang = "en"
)

// current is the active UI language. It is read by T on every call; writers are
// Set (startup + `:lang`). The TUI runs on a single goroutine (the bubbletea
// event loop) for all user-facing rendering, so no synchronization is needed
// for normal use; tests that flip it should not run in parallel.
var current = RU

// Set makes l the active language. Any value other than EN falls back to RU, so
// an unexpected code can never leave the UI in a blank state.
func Set(l Lang) {
	if l == EN {
		current = EN
		return
	}
	current = RU
}

// Current returns the active language.
func Current() Lang { return current }

// T returns the variant for the active language: en when English is active,
// otherwise ru. It is the primary translation helper used throughout the UI.
func T(ru, en string) string {
	if current == EN {
		return en
	}
	return ru
}

// Resolve validates a configured language string and returns the matching Lang.
// An empty value resolves to the default (RU); an unknown value is an error so
// a typo in the config is reported rather than silently ignored.
func Resolve(s string) (Lang, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "ru", "rus", "russian", "русский":
		return RU, nil
	case "en", "eng", "english", "английский":
		return EN, nil
	default:
		return RU, fmt.Errorf("unknown language %q (ru | en)", s)
	}
}

// Names returns the selectable languages in display order, for the picker.
func Names() []Lang { return []Lang{RU, EN} }

// Display returns the human-readable name of the language (in its own language).
func (l Lang) Display() string {
	switch l {
	case EN:
		return "English"
	default:
		return "Русский"
	}
}
