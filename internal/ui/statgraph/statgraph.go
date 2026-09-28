// Package statgraph keeps a bounded history of numeric samples and renders it
// as a compact block-character chart (a sparkline when one row tall). It is
// used by the containers stats view to plot CPU/MEM over the last N refreshes.
package statgraph

import (
	"math"
	"strings"
)

// blocks are the eighth-height bar glyphs, from 1/8 to a full cell.
var blocks = []rune("▁▂▃▄▅▆▇█")

// Ring is a fixed-capacity ring buffer of samples: once full, Push overwrites
// the oldest one. The zero value has no capacity and ignores pushes; use
// NewRing.
type Ring struct {
	buf   []float64
	start int // index of the oldest sample
	n     int // number of stored samples
}

// NewRing returns an empty ring holding at most capacity samples (min 1).
func NewRing(capacity int) *Ring {
	return &Ring{buf: make([]float64, max(capacity, 1))}
}

// Push appends a sample, evicting the oldest one when the ring is full.
func (r *Ring) Push(v float64) {
	if len(r.buf) == 0 {
		return
	}
	if r.n < len(r.buf) {
		r.buf[(r.start+r.n)%len(r.buf)] = v
		r.n++
		return
	}
	r.buf[r.start] = v
	r.start = (r.start + 1) % len(r.buf)
}

// Len returns the number of stored samples.
func (r *Ring) Len() int { return r.n }

// Cap returns the maximum number of samples the ring holds.
func (r *Ring) Cap() int { return len(r.buf) }

// Values returns a copy of the stored samples, oldest first.
func (r *Ring) Values() []float64 {
	out := make([]float64, r.n)
	for i := range r.n {
		out[i] = r.buf[(r.start+i)%len(r.buf)]
	}
	return out
}

// Last returns the newest sample; ok is false when the ring is empty.
func (r *Ring) Last() (v float64, ok bool) {
	if r.n == 0 {
		return 0, false
	}
	return r.buf[(r.start+r.n-1)%len(r.buf)], true
}

// Peak returns the largest stored sample (0 when empty or all non-positive).
func (r *Ring) Peak() float64 {
	return Peak(r.Values())
}

// Peak returns the largest finite positive value of vs, or 0.
func Peak(vs []float64) float64 {
	p := 0.0
	for _, v := range vs {
		if !math.IsNaN(v) && !math.IsInf(v, 0) && v > p {
			p = v
		}
	}
	return p
}

// Min returns the smallest finite value of vs, or 0 when there is none.
func Min(vs []float64) float64 {
	lo, found := 0.0, false
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if !found || v < lo {
			lo, found = v, true
		}
	}
	return lo
}

// Render draws values as a bar chart height rows tall and width cells wide and
// returns the rows top to bottom, each exactly width runes. Only the newest
// width samples are drawn, right-aligned, so the chart scrolls left as samples
// arrive; unused leading columns are blank. Bars scale linearly from lo (the
// baseline) to hi (full height): values at or below lo, NaN and infinities sit
// on the baseline, values above hi are clipped. A baseline sample still shows
// the lowest glyph on the bottom row, so idle periods remain visible against
// the blank padding; when hi <= lo every sample is drawn at the baseline.
// Returns nil when width or height < 1.
func Render(values []float64, width, height int, lo, hi float64) []string {
	if width < 1 || height < 1 {
		return nil
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	pad := width - len(values)
	steps := height * len(blocks) // total eighths in a column

	levels := make([]int, len(values))
	for i, v := range values {
		if !(hi > lo) || math.IsNaN(v) || math.IsInf(v, 0) || v <= lo {
			continue
		}
		levels[i] = min(int(math.Round((v-lo)/(hi-lo)*float64(steps))), steps)
	}

	rows := make([]string, height)
	for r := range height {
		// r = 0 is the top row; floor is the number of eighths below this row.
		floor := (height - 1 - r) * len(blocks)
		var sb strings.Builder
		sb.WriteString(strings.Repeat(" ", pad))
		for _, lv := range levels {
			fill := lv - floor
			switch {
			case fill >= len(blocks):
				sb.WriteRune(blocks[len(blocks)-1])
			case fill > 0:
				sb.WriteRune(blocks[fill-1])
			case floor == 0:
				sb.WriteRune(blocks[0]) // baseline for a present, ~zero sample
			default:
				sb.WriteByte(' ')
			}
		}
		rows[r] = sb.String()
	}
	return rows
}
