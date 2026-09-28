package statgraph

import (
	"math"
	"reflect"
	"testing"
	"unicode/utf8"
)

func TestRing(t *testing.T) {
	tests := []struct {
		name     string
		capacity int
		push     []float64
		want     []float64
		last     float64
		lastOK   bool
		peak     float64
	}{
		{name: "empty", capacity: 3, want: []float64{}, peak: 0},
		{name: "partial", capacity: 3, push: []float64{1, 5}, want: []float64{1, 5}, last: 5, lastOK: true, peak: 5},
		{name: "exactly full", capacity: 3, push: []float64{1, 2, 3}, want: []float64{1, 2, 3}, last: 3, lastOK: true, peak: 3},
		{name: "wraps evicting oldest", capacity: 3, push: []float64{1, 9, 3, 4, 5}, want: []float64{3, 4, 5}, last: 5, lastOK: true, peak: 5},
		{name: "wraps twice", capacity: 2, push: []float64{1, 2, 3, 4, 5, 6, 7}, want: []float64{6, 7}, last: 7, lastOK: true, peak: 7},
		{name: "capacity clamped to 1", capacity: 0, push: []float64{4, 8}, want: []float64{8}, last: 8, lastOK: true, peak: 8},
		{name: "negative peak is 0", capacity: 2, push: []float64{-3, -1}, want: []float64{-3, -1}, last: -1, lastOK: true, peak: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRing(tt.capacity)
			for _, v := range tt.push {
				r.Push(v)
			}
			if got := r.Values(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Values() = %v, want %v", got, tt.want)
			}
			if r.Len() != len(tt.want) {
				t.Errorf("Len() = %d, want %d", r.Len(), len(tt.want))
			}
			last, ok := r.Last()
			if last != tt.last || ok != tt.lastOK {
				t.Errorf("Last() = %v,%v, want %v,%v", last, ok, tt.last, tt.lastOK)
			}
			if got := r.Peak(); got != tt.peak {
				t.Errorf("Peak() = %v, want %v", got, tt.peak)
			}
			if r.Cap() != max(tt.capacity, 1) {
				t.Errorf("Cap() = %d, want %d", r.Cap(), max(tt.capacity, 1))
			}
		})
	}
}

func TestRing_ZeroValueIgnoresPush(t *testing.T) {
	var r Ring
	r.Push(1)
	if r.Len() != 0 || len(r.Values()) != 0 {
		t.Fatalf("zero Ring stored a sample: %v", r.Values())
	}
	if _, ok := r.Last(); ok {
		t.Fatal("zero Ring reported a last sample")
	}
}

func TestPeak(t *testing.T) {
	tests := []struct {
		name string
		vs   []float64
		want float64
	}{
		{"nil", nil, 0},
		{"positive", []float64{1, 7, 3}, 7},
		{"skips NaN and Inf", []float64{2, math.NaN(), math.Inf(1), 4}, 4},
		{"all negative", []float64{-1, -2}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Peak(tt.vs); got != tt.want {
				t.Errorf("Peak(%v) = %v, want %v", tt.vs, got, tt.want)
			}
		})
	}
}

func TestMin(t *testing.T) {
	tests := []struct {
		name string
		vs   []float64
		want float64
	}{
		{"nil", nil, 0},
		{"smallest", []float64{7, 3, 9}, 3},
		{"negative", []float64{2, -4}, -4},
		{"skips NaN and Inf", []float64{math.NaN(), math.Inf(-1), 6, 5}, 5},
		{"only NaN", []float64{math.NaN()}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Min(tt.vs); got != tt.want {
				t.Errorf("Min(%v) = %v, want %v", tt.vs, got, tt.want)
			}
		})
	}
}

// br builds a string of Braille cells from their dot-pattern bits.
func br(bits ...rune) string {
	out := make([]rune, len(bits))
	for i, b := range bits {
		out[i] = brailleBase + b
	}
	return string(out)
}

func TestRender(t *testing.T) {
	// Bits: left column bottom→top 0x40,0x04,0x02,0x01; right 0x80,0x20,0x10,0x08.
	const rightFull, bothFull = 0xB8, 0xFF
	tests := []struct {
		name   string
		values []float64
		width  int
		height int
		lo, hi float64
		want   []string
	}{
		{name: "zero width", values: []float64{1}, width: 0, height: 1, want: nil},
		{name: "zero height", values: []float64{1}, width: 3, height: 0, want: nil},
		{name: "empty is blank", width: 4, height: 1, want: []string{"    "}},
		{name: "single sample on right column", values: []float64{8}, width: 1, height: 1, hi: 8,
			want: []string{br(rightFull)}},
		{name: "right-aligned with padding", values: []float64{8}, width: 3, height: 1, hi: 8,
			want: []string{"  " + br(rightFull)}},
		{name: "rise interpolates midpoint", values: []float64{0, 8}, width: 2, height: 1, hi: 8,
			want: []string{br(0x80, 0x44|rightFull)}},
		{name: "peak slopes both ways", values: []float64{0, 8, 0}, width: 3, height: 1, hi: 8,
			want: []string{br(0x80, 0x44|rightFull, 0x44|0x80)}},
		{name: "keeps newest samples", values: []float64{8, 8, 0, 4}, width: 2, height: 1, hi: 8,
			want: []string{br(0x80, 0x40|0xA0)}},
		{name: "clamps above hi", values: []float64{100}, width: 1, height: 1, hi: 8,
			want: []string{br(rightFull)}},
		{name: "range above baseline", values: []float64{40, 48}, width: 2, height: 1, lo: 40, hi: 48,
			want: []string{br(0x80, 0x44|rightFull)}},
		{name: "empty range draws baseline", values: []float64{5, 5}, width: 2, height: 1, lo: 5, hi: 5,
			want: []string{br(0x80, 0xC0)}},
		{name: "NaN range draws baseline", values: []float64{5}, width: 1, height: 1, hi: math.NaN(),
			want: []string{br(0x80)}},
		{name: "bad values as baseline", values: []float64{math.NaN(), math.Inf(1), -3, 8}, width: 4, height: 1, hi: 8,
			want: []string{br(0x80, 0xC0, 0xC0, 0x44|rightFull)}},
		{name: "multi-row", values: []float64{8, 4}, width: 2, height: 2, hi: 8,
			want: []string{br(rightFull, 0x44), br(rightFull, bothFull)}},
		{name: "empty upper cell is blank", values: []float64{0}, width: 1, height: 2, hi: 8,
			want: []string{" ", br(0x80)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Render(tt.values, tt.width, tt.height, tt.lo, tt.hi)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
			for i, row := range got {
				if n := utf8.RuneCountInString(row); n != tt.width {
					t.Errorf("row %d has %d runes, want %d", i, n, tt.width)
				}
			}
		})
	}
}
