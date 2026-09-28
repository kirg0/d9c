package docker

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/kirg0/d9c/internal/i18n"
)

// fakeContainer returns the demo container with the given ID.
func (f *FakeBackend) fakeContainer(id string) (Container, bool) {
	for _, c := range f.Containers {
		if c.ID == id {
			return c, true
		}
	}
	return Container{}, false
}

// fakeForwardAddr mirrors the real resolution rules for the demo: the target
// must exist and run; the address is a made-up bridge IP.
func (f *FakeBackend) fakeForwardAddr(containerID string, containerPort int) (string, error) {
	c, ok := f.fakeContainer(containerID)
	if !ok {
		return "", fmt.Errorf("no such container: %s", containerID)
	}
	if c.State != "running" {
		return "", errors.New(i18n.T("контейнер не запущен", "container is not running"))
	}
	return net.JoinHostPort("172.17.0.2", strconv.Itoa(containerPort)), nil
}

// PortTarget implements PortForwarder for the demo backend.
func (f *FakeBackend) PortTarget(containerID string, containerPort int) (string, error) {
	addr, err := f.fakeForwardAddr(containerID, containerPort)
	if err != nil {
		return "", err
	}
	return addr + " (demo)", nil
}

// DialPort implements PortForwarder for the demo backend: instead of a real
// container it answers every connection with a tiny canned HTTP response, so
// `curl localhost:<port>` against a demo tunnel shows something meaningful.
func (f *FakeBackend) DialPort(containerID string, containerPort int) (net.Conn, error) {
	if _, err := f.fakeForwardAddr(containerID, containerPort); err != nil {
		return nil, err
	}
	c, _ := f.fakeContainer(containerID)
	client, server := net.Pipe()
	go serveFakeHTTP(server, c.Name, containerPort)
	return client, nil
}

// serveFakeHTTP reads one request head from conn and answers with a short
// plain-text body naming the demo container, then closes the connection.
func serveFakeHTTP(conn net.Conn, name string, port int) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}
	body := fmt.Sprintf("d9c demo: hello from %s:%d\n", name, port)
	_, _ = fmt.Fprintf(conn, "HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
}
