// Package statgraph keeps a bounded history of numeric samples and renders it
// as a compact Braille area chart. It is used by the containers stats view to
// plot CPU/MEM over the last N refreshes.
package statgraph

import (
	"math"
	"strings"
)

// Braille cells pack a 2×4 dot matrix: dotsX columns by dotsY rows.
const (
	dotsX       = 2
	dotsY       = 4
	brailleBase = 0x2800 // U+2800, the empty Braille pattern
)

// brailleDot holds the pattern bit of each dot, indexed [column][row] with row
// 0 at the bottom of the cell (dots 7,3,2,1 on the left, 8,6,5,4 on the right).
var brailleDot = [dotsX][dotsY]rune{
	{0x40, 0x04, 0x02, 0x01},
	{0x80, 0x20, 0x10, 0x08},
}

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

// Render draws values as a filled area chart of Braille dots, height rows
// tall and width cells wide, and returns the rows top to bottom, each exactly
// width runes. Every cell is 2×4 dots: one sample per cell sits on its right
// dot column, and the left one holds the midpoint to the previous sample, so
// the outline climbs and falls in slopes instead of steps. Only the newest
// width samples are drawn, right-aligned, so the chart scrolls left as samples
// arrive; unused leading columns are blank. Heights scale linearly from lo
// (the baseline) to hi (full height): values at or below lo, NaN and
// infinities sit on the baseline, values above hi are clipped. Every drawn
// column lights at least its bottom dot, so idle periods remain visible
// against the blank padding; when hi <= lo every sample sits on the baseline.
// Returns nil when width or height < 1.
func Render(values []float64, width, height int, lo, hi float64) []string {
	if width < 1 || height < 1 {
		return nil
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	dotsH := height * dotsY

	// Fractional heights (in dots) of the samples.
	heights := make([]float64, len(values))
	for i, v := range values {
		if !(hi > lo) || math.IsNaN(v) || math.IsInf(v, 0) || v <= lo {
			continue
		}
		heights[i] = min((v-lo)/(hi-lo), 1) * float64(dotsH)
	}

	// Lit dots per dot column. Sample i lands on the right column of its cell,
	// the left column interpolates between it and sample i-1; the first
	// sample's left column is padding. -1 marks an empty (padding) column.
	pad := width - len(values)
	levels := make([]int, width*dotsX)
	for x := range levels {
		levels[x] = -1
	}
	level := func(h float64) int { return max(int(math.Round(h)), 1) }
	for i, h := range heights {
		x := (pad + i) * dotsX
		if i > 0 {
			levels[x] = level((heights[i-1] + h) / 2)
		}
		levels[x+1] = level(h)
	}

	rows := make([]string, height)
	for r := range height {
		// r = 0 is the top row; floor is the number of dots below this row.
		floor := (height - 1 - r) * dotsY
		var sb strings.Builder
		for c := range width {
			var cell rune
			for dx := range dotsX {
				fill := min(levels[c*dotsX+dx]-floor, dotsY)
				for dy := 0; dy < fill; dy++ {
					cell |= brailleDot[dx][dy]
				}
			}
			if cell == 0 {
				sb.WriteByte(' ')
			} else {
				sb.WriteRune(brailleBase + cell)
			}
		}
		rows[r] = sb.String()
	}
	return rows
}
