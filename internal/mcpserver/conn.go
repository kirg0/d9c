package mcpserver

import (
	"sync"

	"github.com/kirg0/d9c/internal/docker"
)

// conn is the session's one connection to the target host: dialed on first
// use, shared by every call and redialed after it is dropped.
type conn struct {
	dial func() (docker.Backend, error)

	mu sync.Mutex
	b  docker.Backend
}

// get returns the live backend, dialing when there is none; fresh reports
// that it was dialed by this call.
func (c *conn) get() (b docker.Backend, fresh bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.b != nil {
		return c.b, false, nil
	}
	b, err = c.dial()
	if err != nil {
		return nil, false, err
	}
	c.b = b
	return b, true, nil
}

// drop closes b if it is still the live backend, so the next get redials.
// Calls that still hold b see their own errors; a concurrent call that
// already redialed keeps its new connection.
func (c *conn) drop(b docker.Backend) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.b == b && b != nil {
		b.Close()
		c.b = nil
	}
}

// close closes the live backend at the end of the session.
func (c *conn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.b != nil {
		c.b.Close()
		c.b = nil
	}
}
