package table

import "testing"

// TestSetColumnsRecomputesViewportHeight checks that the viewport height does
// not depend on whether columns were set before or after SetSize. bubbles
// computes it from the header height at SetHeight time, and width-0
// placeholder columns render a shorter header than real ones.
func TestSetColumnsRecomputesViewportHeight(t *testing.T) {
	const w, h = 120, 36

	placeholderFirst := New()
	placeholderFirst.SetColumns(ContainerColumns(0))
	placeholderFirst.SetSize(w, h)
	placeholderFirst.SetColumns(ContainerColumns(w))

	columnsFirst := New()
	columnsFirst.SetColumns(ContainerColumns(w))
	columnsFirst.SetSize(w, h)

	got, want := placeholderFirst.InnerTable().Height(), columnsFirst.InnerTable().Height()
	if got != want {
		t.Errorf("viewport height after SetSize then SetColumns = %d, want %d", got, want)
	}
}
