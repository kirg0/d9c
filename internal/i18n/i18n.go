// Package i18n provides the application's UI language selection. The active
// language lives in a package-level variable (set once at startup from the
// config file and again on a runtime `:lang` switch), mirroring the global
// styling pattern in internal/ui/styles: the TUI is a single instance, so a
// process-wide current language is both sufficient and idiomatic here.
//
// Translations are kept inline at the call site via T(ru, en) rather than in a
// key-indexed catalog: there are only two languages and the set of strings is
// small, so pairing the two variants where the text is used keeps them visible
// and easy to review. The default language is English, so T returns the English
// variant until something calls Set(RU).
package i18n

import (
	"fmt"
	"strings"
)

// Lang is a supported UI language code.
type Lang string

const (
	// EN is English, the default language.
	EN Lang = "en"
	// RU is Russian.
	RU Lang = "ru"
)

// current is the active UI language. It is read by T on every call; writers are
// Set (startup + `:lang`). The TUI runs on a single goroutine (the bubbletea
// event loop) for all user-facing rendering, so no synchronization is needed
// for normal use; tests that flip it should not run in parallel.
var current = EN

// Set makes l the active language. Any value other than RU falls back to EN, so
// an unexpected code can never leave the UI in a blank state.
func Set(l Lang) {
	if l == RU {
		current = RU
		return
	}
	current = EN
}

// Current returns the active language.
func Current() Lang { return current }

// T returns the variant for the active language: ru when Russian is active,
// otherwise en. It is the primary translation helper used throughout the UI.
func T(ru, en string) string {
	if current == RU {
		return ru
	}
	return en
}

// Resolve validates a configured language string and returns the matching Lang.
// An empty value resolves to the default (EN); an unknown value is an error so
// a typo in the config is reported rather than silently ignored.
func Resolve(s string) (Lang, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "en", "eng", "english", "английский":
		return EN, nil
	case "ru", "rus", "russian", "русский":
		return RU, nil
	default:
		return EN, fmt.Errorf("unknown language %q (en | ru)", s)
	}
}

// Names returns the selectable languages in display order, for the picker.
func Names() []Lang { return []Lang{EN, RU} }

// Display returns the human-readable name of the language (in its own language).
func (l Lang) Display() string {
	switch l {
	case RU:
		return "Русский"
	default:
		return "English"
	}
}
