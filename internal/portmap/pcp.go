package portmap

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// PCP, RFC 6887. The successor to NAT-PMP, on the same UDP port 5351, spoken by
// newer routers. It is tried first because where it works it is the better
// protocol - but it carries a client-address field the gateway checks against
// the packet's source, so from behind Docker's own NAT (where the source is
// SNATed from 172.20.x to the host's LAN address) a strict gateway answers
// ADDRESS_MISMATCH. That is why NAT-PMP remains the fallback rather than the
// other way round, and why the name service's reachability probe, not this
// response, is the final word on whether the port actually opened.

const (
	pcpVersion    = 2
	pcpOpcodeMap  = 1
	pcpResultOK   = 0
	pcpMapReqLen  = 60 // 24-byte header + 36-byte MAP body
	pcpMapRespLen = 60
)

// pcpResultText names the result codes worth telling apart in a log line; the
// rest are reported by number.
func pcpResultText(code byte) string {
	switch code {
	case 1:
		return "unsupported version"
	case 2:
		return "not authorized"
	case 8:
		return "no resources"
	case 12:
		return "address mismatch"
	default:
		return fmt.Sprintf("result code %d", code)
	}
}

// pcpMap requests (or, with lifetime zero, removes) a MAP mapping. nonce
// identifies the mapping and must be the same on the delete as on the create,
// so the caller keeps it. The gateway echoes it back, and a response carrying a
// different nonce is not ours and is refused.
func pcpMap(ctx context.Context, server netip.AddrPort, proto Protocol, internalPort, externalPort uint16, lifetime time.Duration, nonce [12]byte) (Mapping, error) {
	conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(server))
	if err != nil {
		return Mapping{}, err
	}
	defer conn.Close()

	// The client-address field is what the OS chose as the source for this
	// socket, as an IPv4-mapped IPv6 address per the RFC.
	local := conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr()
	clientIP := local.As16()

	req := make([]byte, pcpMapReqLen)
	req[0] = pcpVersion
	req[1] = pcpOpcodeMap // R bit clear = request
	// req[2:4] reserved.
	binary.BigEndian.PutUint32(req[4:8], uint32(lifetime/time.Second))
	copy(req[8:24], clientIP[:])
	// MAP body.
	copy(req[24:36], nonce[:])
	req[36] = byte(proto)
	// req[37:40] reserved.
	binary.BigEndian.PutUint16(req[40:42], internalPort)
	binary.BigEndian.PutUint16(req[42:44], externalPort)
	// req[44:60] suggested external IP left as :: - no preference.

	resp, err := pcpRoundTrip(ctx, conn, req)
	if err != nil {
		return Mapping{}, err
	}
	if resp[0] != pcpVersion {
		return Mapping{}, fmt.Errorf("pcp: response version %d, want %d", resp[0], pcpVersion)
	}
	if resp[1] != pcpOpcodeMap|0x80 {
		return Mapping{}, fmt.Errorf("pcp: unexpected opcode 0x%02x in response", resp[1])
	}
	if code := resp[3]; code != pcpResultOK {
		return Mapping{}, fmt.Errorf("pcp: gateway refused the mapping (%s)", pcpResultText(code))
	}
	if !bytesEqual(resp[24:36], nonce[:]) {
		return Mapping{}, errors.New("pcp: response nonce did not match; not our mapping")
	}
	if resp[36] != byte(proto) {
		return Mapping{}, fmt.Errorf("pcp: response protocol %d, want %d", resp[36], proto)
	}
	return Mapping{
		Method:       "PCP",
		ExternalPort: binary.BigEndian.Uint16(resp[42:44]),
		ExternalIP:   externalIPFrom(resp[44:60]),
		Lifetime:     time.Duration(binary.BigEndian.Uint32(resp[4:8])) * time.Second,
		nonce:        nonce,
	}, nil
}

// externalIPFrom reads the 16-byte assigned-external-IP field, unmapping an
// IPv4-in-IPv6 address back to a plain v4. A zero or unspecified field yields
// an invalid Addr rather than "::".
func externalIPFrom(b []byte) netip.Addr {
	addr, ok := netip.AddrFromSlice(b)
	if !ok || addr.IsUnspecified() {
		return netip.Addr{}
	}
	return addr.Unmap()
}

// pcpRoundTrip sends one request and returns the first well-formed response,
// retransmitting with a doubling timeout. Same shape as NAT-PMP's exchange, but
// PCP builds its request from the connection's own local address, so the socket
// is opened by the caller and passed in.
func pcpRoundTrip(ctx context.Context, conn *net.UDPConn, req []byte) ([]byte, error) {
	buf := make([]byte, 1500)
	timeout := 250 * time.Millisecond
	deadline, hasDeadline := ctx.Deadline()

	for attempt := 0; attempt < 9; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := conn.Write(req); err != nil {
			return nil, err
		}

		readBy := time.Now().Add(timeout)
		if hasDeadline && deadline.Before(readBy) {
			readBy = deadline
		}
		_ = conn.SetReadDeadline(readBy)

		n, err := conn.Read(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				timeout *= 2
				continue
			}
			return nil, err
		}
		if n < pcpMapRespLen {
			return nil, fmt.Errorf("pcp: response too short (%d bytes)", n)
		}
		return buf[:n], nil
	}
	return nil, errors.New("pcp: no response from gateway")
}

// bytesEqual is a tiny equal to keep the nonce check readable without pulling in
// bytes just for this - the comparison is not security-sensitive (the token, not
// the nonce, is what authenticates an install).
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
