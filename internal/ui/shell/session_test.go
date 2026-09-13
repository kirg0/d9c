package shell

import "testing"

// TestSessionLifecycleAccessors covers the accessors the root model reads for
// the header and footer, and detaching via CloseSession.
func TestSessionLifecycleAccessors(t *testing.T) {
	m := New()
	m.SetSize(0, 0) // degenerate sizes clamp to a 1x1 grid instead of panicking
	if m.cols != 1 || m.rows != 1 {
		t.Errorf("grid = %dx%d, want 1x1", m.cols, m.rows)
	}
	if m.ResizeRemoteCmd() != nil {
		t.Error("no session: nothing to resize")
	}

	m.SetSize(82, 26)
	sess := &fakeSess{}
	m.Open(sess, "web")
	if m.Title() != "web" || m.ExitErr() != nil || m.Closed() {
		t.Errorf("after open: title=%q exitErr=%v closed=%v", m.Title(), m.ExitErr(), m.Closed())
	}
	if m.ResizeRemoteCmd() == nil {
		t.Error("an open session should get a resize command")
	}

	m.CloseSession()
	if !m.Closed() || !sess.closed {
		t.Errorf("after detach: closed=%v session closed=%v", m.Closed(), sess.closed)
	}
	if m.ResizeRemoteCmd() != nil {
		t.Error("a closed session must not be resized")
	}
	m.CloseSession() // idempotent: the input pump is already stopped
}
