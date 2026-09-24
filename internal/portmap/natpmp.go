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

// NAT-PMP, RFC 6886. The gateway listens on UDP 5351 and everything is
// big-endian. It is the older of the two protocols this package speaks and the
// more robust one behind a NAT of our own (Docker's): unlike PCP it carries no
// client-address field, so a request that leaves the container as 172.20.x and
// reaches the router SNATed to the host's LAN address still maps the right box.
//
// We only ever map a single TCP port - this install's own - and only at the
// owner's explicit request.

// natpmpOpcode turns a transport into NAT-PMP's opcode: 1 for UDP, 2 for TCP.
// (PCP reuses the IANA protocol numbers instead, which is why Protocol's own
// values are 6 and 17.)
func natpmpOpcode(proto Protocol) byte {
	if proto == UDP {
		return 1
	}
	return 2
}

// natpmpMap requests (or, with lifetime zero and externalPort zero, removes) a
// mapping. The gateway chooses the external port; it need not be the one asked
// for, so the granted port is read back from the response.
func natpmpMap(ctx context.Context, server netip.AddrPort, proto Protocol, internalPort, externalPort uint16, lifetime time.Duration) (Mapping, error) {
	op := natpmpOpcode(proto)

	req := make([]byte, 12)
	req[0] = 0 // version
	req[1] = op
	// req[2:4] reserved, must be zero.
	binary.BigEndian.PutUint16(req[4:6], internalPort)
	binary.BigEndian.PutUint16(req[6:8], externalPort)
	binary.BigEndian.PutUint32(req[8:12], uint32(lifetime/time.Second))

	resp, err := natpmpExchange(ctx, server, req, 16)
	if err != nil {
		return Mapping{}, err
	}
	if resp[1] != op+128 {
		return Mapping{}, fmt.Errorf("nat-pmp: unexpected opcode %d in response", resp[1])
	}
	if code := binary.BigEndian.Uint16(resp[2:4]); code != 0 {
		return Mapping{}, fmt.Errorf("nat-pmp: gateway refused the mapping (result code %d)", code)
	}
	return Mapping{
		Method:       "NAT-PMP",
		ExternalPort: binary.BigEndian.Uint16(resp[10:12]),
		Lifetime:     time.Duration(binary.BigEndian.Uint32(resp[12:16])) * time.Second,
	}, nil
}

// natpmpExternalAddr asks the gateway for its public IPv4 address. It is a
// cheap way to learn whether NAT-PMP works at all and what the world sees,
// though the name service's reachability probe is the real confirmation.
func natpmpExternalAddr(ctx context.Context, server netip.AddrPort) (netip.Addr, error) {
	resp, err := natpmpExchange(ctx, server, []byte{0, 0}, 12)
	if err != nil {
		return netip.Addr{}, err
	}
	if resp[1] != 128 {
		return netip.Addr{}, fmt.Errorf("nat-pmp: unexpected opcode %d in response", resp[1])
	}
	if code := binary.BigEndian.Uint16(resp[2:4]); code != 0 {
		return netip.Addr{}, fmt.Errorf("nat-pmp: gateway refused (result code %d)", code)
	}
	return netip.AddrFrom4([4]byte(resp[8:12])), nil
}

// natpmpExchange sends one request and returns the first well-formed response,
// retransmitting with a doubling timeout as the RFC prescribes. A connected
// UDP socket means an ICMP port-unreachable from a gateway that does not speak
// NAT-PMP comes back as a read error rather than a silent timeout, so we fail
// fast to the next method instead of waiting out every retry.
func natpmpExchange(ctx context.Context, server netip.AddrPort, req []byte, wantLen int) ([]byte, error) {
	conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(server))
	if err != nil {
		return nil, err
	}
	defer conn.Close()

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
		if n < wantLen {
			return nil, fmt.Errorf("nat-pmp: response too short (%d bytes)", n)
		}
		if buf[0] != 0 {
			return nil, fmt.Errorf("nat-pmp: response version %d, want 0", buf[0])
		}
		return buf[:n], nil
	}
	return nil, errors.New("nat-pmp: no response from gateway")
}
