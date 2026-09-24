package servetls

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// flakyListener fails its first Accept with an ordinary error - what running out
// of file descriptors looks like - and then hands out one real connection.
type flakyListener struct {
	mu     sync.Mutex
	failed bool
	conn   net.Conn
	closed chan struct{}
	once   sync.Once
}

func (f *flakyListener) Accept() (net.Conn, error) {
	f.mu.Lock()
	if !f.failed {
		f.failed = true
		f.mu.Unlock()
		return nil, errors.New("accept: too many open files")
	}
	c := f.conn
	f.conn = nil
	f.mu.Unlock()
	if c != nil {
		return c, nil
	}
	<-f.closed
	return nil, net.ErrClosed
}

func (f *flakyListener) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *flakyListener) Addr() net.Addr { return &net.TCPAddr{} }

// A transient accept error must be waited out, not treated as the listener
// closing: closing it ended Serve, and with it the whole server - so a flood of
// connections that exhausted file descriptors became a restart.
func TestATransientAcceptErrorDoesNotStopTheListener(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go client.Write([]byte("GET / HTTP/1.1\r\n\r\n"))

	inner := &flakyListener{conn: server, closed: make(chan struct{})}
	s := &Server{}
	l := s.Listener(inner)
	defer l.Close()

	got := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if c != nil {
			c.Close()
		}
		got <- err
	}()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("Accept after a transient error = %v, want the connection", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no connection was accepted after a transient error")
	}
}
