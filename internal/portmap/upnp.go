package portmap

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// UPnP-IGD: the widest-reaching of the three, spoken by most consumer routers
// that predate PCP, but the fiddliest - SSDP multicast discovery, then an HTTP
// device description to parse, then SOAP to a control URL. Everything here is
// stdlib plus encoding/xml.
//
// Two things about it and Docker. SSDP is multicast to 239.255.255.250, which
// does not cross the Docker bridge from inside the container - so discovery runs
// on the host (the installer) and the resulting device-description URL is passed
// in, the same division as the gateway address. But the SOAP calls afterwards
// are ordinary unicast HTTP to the router, which a request from the container
// reaches through Docker's NAT - so once the URL is known, the mapping itself
// works from in there. AddPortMapping also needs the LAN address of the box to
// forward to, which the installer already discovers as the LAN address; it is
// passed in as the internal client. On a host-network or native run, discovery
// works from the process too, so a configured URL is an override, not a
// requirement.

const (
	ssdpAddr                   = "239.255.255.250:1900"
	upnpDevIGD1                = "urn:schemas-upnp-org:device:InternetGatewayDevice:1"
	upnpDevIGD2                = "urn:schemas-upnp-org:device:InternetGatewayDevice:2"
	upnpErrOnlyPermanentLeases = 725
)

// wanServiceTypes are the connection services that can add a port mapping, most
// capable first. The device description is searched for these in order.
var wanServiceTypes = []string{
	"urn:schemas-upnp-org:service:WANIPConnection:2",
	"urn:schemas-upnp-org:service:WANIPConnection:1",
	"urn:schemas-upnp-org:service:WANPPPConnection:1",
}

// igd is a discovered gateway's one control endpoint: where to POST SOAP and
// which service to name in it.
type igd struct {
	controlURL  string
	serviceType string
}

// upnpMap discovers (or uses the configured location for) an IGD and adds a port
// mapping. internalClient is the LAN address the router should forward to - the
// host's, not the container's, which is why it is passed in rather than read
// from the socket. A zero internalClient is an error: UPnP cannot map without
// knowing the box to map to.
func upnpMap(ctx context.Context, location string, internalClient netip.Addr, proto Protocol, internalPort, externalPort uint16, lifetime time.Duration) (Mapping, error) {
	if !internalClient.IsValid() {
		return Mapping{}, fmt.Errorf("upnp: no internal client address configured")
	}
	client := &http.Client{Timeout: 8 * time.Second}

	g, err := findIGD(ctx, client, location)
	if err != nil {
		return Mapping{}, err
	}
	external, err := upnpAdd(ctx, client, g, proto, internalClient, internalPort, externalPort, lifetime)
	if err != nil {
		return Mapping{}, err
	}
	m := Mapping{
		Method:       "UPnP",
		ExternalPort: externalPort,
		Lifetime:     lifetime,
		igd:          g,
	}
	if external.IsValid() {
		m.ExternalIP = external
	}
	return m, nil
}

// findIGD resolves an IGD from a configured device-description URL, or discovers
// one over SSDP when no URL is configured.
func findIGD(ctx context.Context, client *http.Client, location string) (igd, error) {
	if location != "" {
		return describeIGD(ctx, client, location)
	}
	locations, err := ssdpSearch(ctx, 2*time.Second)
	if err != nil {
		return igd{}, err
	}
	var lastErr error
	for _, loc := range locations {
		g, err := describeIGD(ctx, client, loc)
		if err == nil {
			return g, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return igd{}, lastErr
	}
	return igd{}, fmt.Errorf("upnp: no gateway found")
}

// ssdpSearch sends an M-SEARCH and collects the device-description URLs from the
// responses. It only ever runs where multicast reaches the LAN - a host-network
// or native process - so on the ordinary bridged container it returns nothing
// and the configured URL is used instead.
func ssdpSearch(ctx context.Context, timeout time.Duration) ([]string, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	dst, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return nil, err
	}
	for _, st := range []string{upnpDevIGD1, upnpDevIGD2} {
		msg := "M-SEARCH * HTTP/1.1\r\n" +
			"HOST: " + ssdpAddr + "\r\n" +
			"MAN: \"ssdp:discover\"\r\n" +
			"MX: 2\r\n" +
			"ST: " + st + "\r\n\r\n"
		if _, err := conn.WriteToUDP([]byte(msg), dst); err != nil {
			return nil, err
		}
	}

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)

	seen := map[string]bool{}
	var locations []string
	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // deadline: done collecting
		}
		if loc := ssdpLocation(buf[:n]); loc != "" && !seen[loc] {
			seen[loc] = true
			locations = append(locations, loc)
		}
	}
	return locations, nil
}

// ssdpLocation pulls the LOCATION header out of an SSDP response, case-insensitively.
func ssdpLocation(resp []byte) string {
	for _, line := range strings.Split(string(resp), "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "location") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// describeIGD fetches a device description and finds a WAN connection service in
// it, returning where to send SOAP. The control URL in the description may be
// relative, so it is resolved against URLBase or the description's own URL.
func describeIGD(ctx context.Context, client *http.Client, location string) (igd, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return igd{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return igd{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return igd{}, fmt.Errorf("upnp: device description at %s: HTTP %d", location, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return igd{}, err
	}

	var root upnpRoot
	if err := xml.Unmarshal(body, &root); err != nil {
		return igd{}, fmt.Errorf("upnp: parse device description: %w", err)
	}

	base := strings.TrimSpace(root.URLBase)
	if base == "" {
		base = location
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return igd{}, fmt.Errorf("upnp: bad base URL %q: %w", base, err)
	}

	svc := findWANService(&root.Device)
	if svc == nil {
		return igd{}, fmt.Errorf("upnp: no WAN connection service at %s", location)
	}
	ctrl, err := baseURL.Parse(strings.TrimSpace(svc.ControlURL))
	if err != nil {
		return igd{}, fmt.Errorf("upnp: bad control URL %q: %w", svc.ControlURL, err)
	}
	return igd{controlURL: ctrl.String(), serviceType: svc.ServiceType}, nil
}

// findWANService walks the device tree for a connection service that can add a
// mapping, preferring the more capable service types.
func findWANService(dev *upnpDevice) *upnpService {
	for _, want := range wanServiceTypes {
		if s := serviceOfType(dev, want); s != nil {
			return s
		}
	}
	return nil
}

func serviceOfType(dev *upnpDevice, want string) *upnpService {
	for i := range dev.Services.Services {
		if strings.EqualFold(strings.TrimSpace(dev.Services.Services[i].ServiceType), want) {
			return &dev.Services.Services[i]
		}
	}
	for i := range dev.Devices.Devices {
		if s := serviceOfType(&dev.Devices.Devices[i], want); s != nil {
			return s
		}
	}
	return nil
}

// upnpAdd sends AddPortMapping, returning the WAN address when the router will
// report it. A router that only supports permanent leases (error 725) is
// retried with a zero lease - the mapping is dropped on teardown regardless.
func upnpAdd(ctx context.Context, client *http.Client, g igd, proto Protocol, internalClient netip.Addr, internalPort, externalPort uint16, lifetime time.Duration) (netip.Addr, error) {
	lease := uint32(lifetime / time.Second)
	err := upnpAddOnce(ctx, client, g, proto, internalClient, internalPort, externalPort, lease)
	if err != nil {
		var fe *soapError
		if lease != 0 && asSOAPError(err, &fe) && fe.code == upnpErrOnlyPermanentLeases {
			err = upnpAddOnce(ctx, client, g, proto, internalClient, internalPort, externalPort, 0)
		}
	}
	if err != nil {
		return netip.Addr{}, err
	}
	// External IP is a nicety, not required; ignore a failure to read it.
	external, _ := upnpExternalIP(ctx, client, g)
	return external, nil
}

func upnpAddOnce(ctx context.Context, client *http.Client, g igd, proto Protocol, internalClient netip.Addr, internalPort, externalPort uint16, lease uint32) error {
	body := fmt.Sprintf(""+
		"<NewRemoteHost></NewRemoteHost>"+
		"<NewExternalPort>%d</NewExternalPort>"+
		"<NewProtocol>%s</NewProtocol>"+
		"<NewInternalPort>%d</NewInternalPort>"+
		"<NewInternalClient>%s</NewInternalClient>"+
		"<NewEnabled>1</NewEnabled>"+
		"<NewPortMappingDescription>SoundStorm</NewPortMappingDescription>"+
		"<NewLeaseDuration>%d</NewLeaseDuration>",
		externalPort, protoName(proto), internalPort, internalClient.String(), lease)
	_, err := soapCall(ctx, client, g, "AddPortMapping", body)
	return err
}

// upnpDelete removes a mapping by its external port and protocol.
func upnpDelete(ctx context.Context, client *http.Client, g igd, proto Protocol, externalPort uint16) error {
	body := fmt.Sprintf(""+
		"<NewRemoteHost></NewRemoteHost>"+
		"<NewExternalPort>%d</NewExternalPort>"+
		"<NewProtocol>%s</NewProtocol>",
		externalPort, protoName(proto))
	_, err := soapCall(ctx, client, g, "DeletePortMapping", body)
	return err
}

// upnpExternalIP reads the router's WAN address.
func upnpExternalIP(ctx context.Context, client *http.Client, g igd) (netip.Addr, error) {
	resp, err := soapCall(ctx, client, g, "GetExternalIPAddress", "")
	if err != nil {
		return netip.Addr{}, err
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(resp.Body.ExternalIP))
	if err != nil {
		return netip.Addr{}, err
	}
	return addr, nil
}

// soapCall POSTs one SOAP action and decodes the response envelope, turning a
// SOAP fault into a soapError carrying the UPnP error code.
func soapCall(ctx context.Context, client *http.Client, g igd, action, inner string) (*soapEnvelope, error) {
	payload := `<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body><u:` + action + ` xmlns:u="` + g.serviceType + `">` +
		inner +
		`</u:` + action + `></s:Body></s:Envelope>`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.controlURL, bytes.NewReader([]byte(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+g.serviceType+`#`+action+`"`)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}

	var env soapEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("upnp: %s: parse response: %w", action, err)
	}
	if resp.StatusCode != http.StatusOK {
		code := env.Body.Fault.Detail.UPnPError.Code
		desc := env.Body.Fault.Detail.UPnPError.Desc
		if code != 0 {
			return nil, &soapError{action: action, code: code, desc: desc}
		}
		return nil, fmt.Errorf("upnp: %s: HTTP %d", action, resp.StatusCode)
	}
	return &env, nil
}

func protoName(p Protocol) string {
	if p == UDP {
		return "UDP"
	}
	return "TCP"
}

// soapError is a UPnP fault with its numeric code, so callers can single out the
// ones worth handling (725 for permanent-only leases).
type soapError struct {
	action string
	code   int
	desc   string
}

func (e *soapError) Error() string {
	if e.desc != "" {
		return fmt.Sprintf("upnp: %s refused (error %d: %s)", e.action, e.code, e.desc)
	}
	return fmt.Sprintf("upnp: %s refused (error %d)", e.action, e.code)
}

// asSOAPError reports whether err is a *soapError, writing it through target.
func asSOAPError(err error, target **soapError) bool {
	se, ok := err.(*soapError)
	if ok {
		*target = se
	}
	return ok
}

// --- device description and SOAP envelope shapes ---

type upnpRoot struct {
	XMLName xml.Name   `xml:"root"`
	URLBase string     `xml:"URLBase"`
	Device  upnpDevice `xml:"device"`
}

type upnpDevice struct {
	DeviceType string `xml:"deviceType"`
	Services   struct {
		Services []upnpService `xml:"service"`
	} `xml:"serviceList"`
	Devices struct {
		Devices []upnpDevice `xml:"device"`
	} `xml:"deviceList"`
}

type upnpService struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

type soapEnvelope struct {
	Body struct {
		ExternalIP string `xml:"GetExternalIPAddressResponse>NewExternalIPAddress"`
		Fault      struct {
			Detail struct {
				UPnPError struct {
					Code int    `xml:"errorCode"`
					Desc string `xml:"errorDescription"`
				} `xml:"UPnPError"`
			} `xml:"detail"`
		} `xml:"Fault"`
	} `xml:"Body"`
}
