package servetls

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"
)

// Listener serves plain HTTP and TLS on the same port, telling them apart by
// the first byte a client sends.
//
// Auto mode needs both at once. The address everybody already has is
// http://localhost:8099, printed by the installer and saved in shortcuts, and
// it has to keep working; the address the certificate is for is
// https://<name>:8099, on the same published port, because the port mapping
// lives in compose and changing it would mean every existing install editing
// a file. A TLS connection always opens with a handshake record, whose first
// byte is 0x16; no HTTP request starts with that byte, so one byte is enough
// to decide.
//
// The byte is read off the accept path, on a goroutine per connection, with a
// deadline: a client that connects and says nothing must not hold up every
// connection behind it.
func (s *Server) Listener(inner net.Listener) net.Listener {
	l := &sniffListener{
		inner: inner,
		cfg:   s.cfg,
		conns: make(chan net.Conn),
		done:  make(chan struct{}),
	}
	go l.acceptLoop()
	return l
}

// sniffTimeout bounds how long a connection may stay silent before its first
// byte. Browsers send immediately; ten seconds is generous.
const sniffTimeout = 10 * time.Second

type sniffListener struct {
	inner net.Listener
	cfg   *tls.Config
	conns chan net.Conn
	done  chan struct{}

	once sync.Once
	mu   sync.Mutex
	err  error
}

func (l *sniffListener) acceptLoop() {
	var backoff time.Duration
	for {
		c, err := l.inner.Accept()
		if err != nil {
			// Only a closed listener ends the loop. Anything else - running out
			// of file descriptors under a flood of connections, most likely - is
			// waited out and retried, the way net/http's own accept loop does.
			// Closing on it instead handed the error to Serve, which returned,
			// and the whole server exited: a connection flood became a restart.
			if !errors.Is(err, net.ErrClosed) {
				select {
				case <-l.done:
				default:
					if backoff == 0 {
						backoff = 5 * time.Millisecond
					} else if backoff *= 2; backoff > time.Second {
						backoff = time.Second
					}
					time.Sleep(backoff)
					continue
				}
			}
			l.mu.Lock()
			l.err = err
			l.mu.Unlock()
			l.Close()
			return
		}
		backoff = 0
		go l.classify(c)
	}
}

func (l *sniffListener) classify(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(sniffTimeout))
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return
	}

	var out net.Conn = &peekedConn{Conn: c, r: br}
	if first[0] == 0x16 {
		// A *tls.Conn specifically, not a wrapper around one: net/http checks
		// for that type to run the handshake and fill in r.TLS, and the
		// session cookie's Secure flag is decided from r.TLS.
		out = tls.Server(out, l.cfg)
	}
	select {
	case l.conns <- out:
	case <-l.done:
		out.Close()
	}
}

func (l *sniffListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.err != nil {
			return nil, l.err
		}
		return nil, net.ErrClosed
	}
}

func (l *sniffListener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.done)
		err = l.inner.Close()
		if errors.Is(err, net.ErrClosed) {
			err = nil
		}
	})
	return err
}

func (l *sniffListener) Addr() net.Addr { return l.inner.Addr() }

// peekedConn gives back the byte that was peeked before anything else.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
