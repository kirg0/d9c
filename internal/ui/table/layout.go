package table

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/kirg0/d9c/internal/i18n"

	"github.com/charmbracelet/bubbles/table"
)

// Section names a table layout that the "columns:" config section can
// customize. The value is the key used in d9c-config.yaml.
type Section string

// Configurable table layouts.
const (
	SectionContainers Section = "containers"
	SectionStats      Section = "stats"
	SectionImages     Section = "images"
	SectionNetworks   Section = "networks"
	SectionVolumes    Section = "volumes"
	SectionCompose    Section = "compose"
	SectionHosts      Section = "hosts"
)

// sectionSpec describes a configurable layout: its default columns and the
// index of the identity column (the one Model.selectedID reads), which can never
// be hidden.
type sectionSpec struct {
	columns  func(w int) []table.Column
	identity int
}

var sectionSpecs = map[Section]sectionSpec{
	SectionContainers: {ContainerColumns, idCol},
	SectionStats:      {ContainerStatsColumns, 6},
	SectionImages:     {ImageColumns, 3},
	SectionNetworks:   {NetworkColumns, 4},
	SectionVolumes:    {VolumeColumns, 0},
	SectionCompose:    {ComposeColumns, ComposeIDColumn},
	SectionHosts:      {HostColumns, 0},
}

// Layouts maps a section to its projection: the default-column indices to show,
// in display order. A section without an entry uses its default layout.
type Layouts map[Section][]int

// For returns the projection for s, or nil (the default layout). Nil-safe.
func (l Layouts) For(s Section) []int {
	if l == nil {
		return nil
	}
	return l[s]
}

// columnAliases maps extra (normalized) spellings to a normalized column title.
var columnAliases = map[string]string{
	"repository": "repositorytag",
	"repo":       "repositorytag",
	"tag":        "repositorytag",
	"net":        "netio",
	"block":      "blockio",
}

// normalizeColumn folds a column name for case- and punctuation-insensitive
// matching: letters and digits are lowercased, "%" becomes "pct" (so "MEM %"
// stays distinct from "MEM"), everything else is dropped — "CPU %", "cpu%" and
// "Cpu %" all compare equal, as do "NET I/O" and "net_io".
func normalizeColumn(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r == '%':
			b.WriteString("pct")
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// findColumn returns the index of the column matching name, or -1. Besides the
// exact normalized title it accepts the aliases above and a bare percent column
// without its sign ("cpu" → "CPU %") when no plain column of that name exists.
func findColumn(cols []table.Column, name string) int {
	key := normalizeColumn(name)
	if key == "" {
		return -1
	}
	candidates := []string{key}
	if a, ok := columnAliases[key]; ok {
		candidates = append(candidates, a)
	}
	candidates = append(candidates, key+"pct")
	for _, k := range candidates {
		for i, c := range cols {
			if normalizeColumn(c.Title) == k {
				return i
			}
		}
	}
	return -1
}

// ResolveLayouts validates the "columns:" config section (section → ordered
// column names) and returns the projection per section plus human-readable
// warnings. It never fails: an unknown section or column and a duplicate are
// skipped with a warning; an empty (or all-invalid) list keeps the default
// layout; a missing identity column is re-inserted at its default position (it
// is required to address the selected row) with a warning.
func ResolveLayouts(cfg map[string][]string) (Layouts, []string) {
	var warns []string
	out := Layouts{}
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic warning order
	for _, key := range keys {
		names := cfg[key]
		sec := Section(strings.ToLower(strings.TrimSpace(key)))
		spec, ok := sectionSpecs[sec]
		if !ok {
			warns = append(warns, fmt.Sprintf(i18n.T("columns: неизвестный раздел %q (допустимо: %s)", "columns: unknown section %q (valid: %s)"), key, sectionList()))
			continue
		}
		if len(names) == 0 {
			continue
		}
		cols := spec.columns(0)
		idx, w := resolveSection(sec, cols, spec.identity, names)
		warns = append(warns, w...)
		if idx != nil {
			out[sec] = idx
		}
	}
	if len(out) == 0 {
		out = nil
	}
	return out, warns
}

// resolveSection maps one section's column names to default-column indices.
// It returns nil when the result equals the default layout.
func resolveSection(sec Section, cols []table.Column, identity int, names []string) ([]int, []string) {
	var warns []string
	seen := map[int]bool{}
	idx := make([]int, 0, len(names))
	for _, n := range names {
		i := findColumn(cols, n)
		switch {
		case i < 0:
			warns = append(warns, fmt.Sprintf(i18n.T("columns.%s: неизвестная колонка %q (допустимо: %s)", "columns.%s: unknown column %q (valid: %s)"), sec, n, titleList(cols)))
		case seen[i]:
			warns = append(warns, fmt.Sprintf(i18n.T("columns.%s: колонка %q указана дважды", "columns.%s: duplicate column %q"), sec, n))
		default:
			seen[i] = true
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return nil, warns
	}
	if !seen[identity] {
		warns = append(warns, fmt.Sprintf(i18n.T("columns.%s: колонка %s обязательна и добавлена", "columns.%s: column %s is required and was added back"), sec, cols[identity].Title))
		pos := min(identity, len(idx))
		idx = append(idx[:pos], append([]int{identity}, idx[pos:]...)...)
	}
	if isIdentity(idx, len(cols)) {
		return nil, warns
	}
	return idx, warns
}

// isIdentity reports whether idx lists every one of n columns in default order.
func isIdentity(idx []int, n int) bool {
	if len(idx) != n {
		return false
	}
	for i, v := range idx {
		if v != i {
			return false
		}
	}
	return true
}

func sectionList() string {
	names := make([]string, 0, len(sectionSpecs))
	for s := range sectionSpecs {
		names = append(names, string(s))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func titleList(cols []table.Column) string {
	titles := make([]string, len(cols))
	for i, c := range cols {
		titles[i] = c.Title
	}
	return strings.Join(titles, ", ")
}

// projectColumns keeps the columns listed in idx, in that order, and scales
// their widths up so the visible set fills the width the full layout had.
// A nil idx returns cols unchanged.
func projectColumns(cols []table.Column, idx []int) []table.Column {
	if idx == nil {
		return cols
	}
	total, kept := 0, 0
	for _, c := range cols {
		total += c.Width
	}
	out := make([]table.Column, 0, len(idx))
	for _, i := range idx {
		if i < len(cols) {
			out = append(out, cols[i])
			kept += cols[i].Width
		}
	}
	if kept > 0 && kept < total {
		for i := range out {
			out[i].Width = out[i].Width * total / kept
		}
	}
	return out
}

// projectRow returns the cells of row listed in idx (missing cells become "").
// A nil idx returns row unchanged.
func projectRow(row table.Row, idx []int) table.Row {
	if idx == nil {
		return row
	}
	out := make(table.Row, len(idx))
	for j, i := range idx {
		if i < len(row) {
			out[j] = row[i]
		}
	}
	return out
}

// projectColorizers reorders per-column colorizers to match a projection.
func projectColorizers(cs []Colorizer, idx []int) []Colorizer {
	if idx == nil || cs == nil {
		return cs
	}
	out := make([]Colorizer, len(idx))
	for j, i := range idx {
		if i < len(cs) {
			out[j] = cs[i]
		}
	}
	return out
}
