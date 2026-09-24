package portmap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIGD is an HTTP server that answers a UPnP device description and the SOAP
// actions this package sends. The device tree is nested, and the control URL is
// relative, so describeIGD's recursive search and URL resolution are exercised.
type fakeIGD struct {
	srv      *httptest.Server
	location string

	mu            sync.Mutex
	adds          int
	deletes       int
	lastLease     string
	lastClient    string
	lastExtPort   string
	permanentOnly bool // first non-zero-lease AddPortMapping fails with 725
	externalIP    string
}

const igdDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType>
    <deviceList>
      <device>
        <deviceType>urn:schemas-upnp-org:device:WANDevice:1</deviceType>
        <deviceList>
          <device>
            <deviceType>urn:schemas-upnp-org:device:WANConnectionDevice:1</deviceType>
            <serviceList>
              <service>
                <serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>
                <controlURL>/ctl/IPConn</controlURL>
              </service>
            </serviceList>
          </device>
        </deviceList>
      </device>
    </deviceList>
  </device>
</root>`

func newFakeIGD(t *testing.T) *fakeIGD {
	t.Helper()
	f := &fakeIGD{externalIP: "198.51.100.9"}
	mux := http.NewServeMux()
	mux.HandleFunc("/rootDesc.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		io.WriteString(w, igdDescription)
	})
	mux.HandleFunc("/ctl/IPConn", f.soap)
	f.srv = httptest.NewServer(mux)
	f.location = f.srv.URL + "/rootDesc.xml"
	t.Cleanup(f.srv.Close)
	return f
}

var tagRe = map[string]*regexp.Regexp{}

func tagValue(body []byte, tag string) string {
	re, ok := tagRe[tag]
	if !ok {
		re = regexp.MustCompile("<" + tag + ">([^<]*)</" + tag + ">")
		tagRe[tag] = re
	}
	m := re.FindSubmatch(body)
	if m == nil {
		return ""
	}
	return string(m[1])
}

func (f *fakeIGD) soap(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	action := r.Header.Get("SOAPAction")

	switch {
	case strings.Contains(action, "AddPortMapping"):
		lease := tagValue(body, "NewLeaseDuration")
		f.mu.Lock()
		f.adds++
		f.lastLease = lease
		f.lastClient = tagValue(body, "NewInternalClient")
		f.lastExtPort = tagValue(body, "NewExternalPort")
		permanentOnly := f.permanentOnly
		f.mu.Unlock()
		if permanentOnly && lease != "0" {
			writeFault(w, 725, "OnlyPermanentLeasesSupported")
			return
		}
		writeOK(w, "AddPortMapping", "")
	case strings.Contains(action, "DeletePortMapping"):
		f.mu.Lock()
		f.deletes++
		f.mu.Unlock()
		writeOK(w, "DeletePortMapping", "")
	case strings.Contains(action, "GetExternalIPAddress"):
		writeOK(w, "GetExternalIPAddress", "<NewExternalIPAddress>"+f.externalIP+"</NewExternalIPAddress>")
	default:
		writeFault(w, 401, "Invalid Action")
	}
}

func writeOK(w http.ResponseWriter, action, inner string) {
	w.Header().Set("Content-Type", "text/xml")
	io.WriteString(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">`+
		`<s:Body><u:`+action+`Response xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">`+
		inner+`</u:`+action+`Response></s:Body></s:Envelope>`)
}

func writeFault(w http.ResponseWriter, code int, desc string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusInternalServerError)
	io.WriteString(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">`+
		`<s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring>`+
		`<detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0">`+
		`<errorCode>`+strconv.Itoa(code)+`</errorCode><errorDescription>`+desc+`</errorDescription>`+
		`</UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
}

func TestUPnPDescribeFindsNestedRelativeControlURL(t *testing.T) {
	f := newFakeIGD(t)
	g, err := describeIGD(context.Background(), &http.Client{Timeout: 5 * time.Second}, f.location)
	if err != nil {
		t.Fatalf("describeIGD: %v", err)
	}
	if g.serviceType != "urn:schemas-upnp-org:service:WANIPConnection:1" {
		t.Errorf("service type = %q", g.serviceType)
	}
	if g.controlURL != f.srv.URL+"/ctl/IPConn" {
		t.Errorf("control URL = %q, want it resolved against the description URL", g.controlURL)
	}
}

func TestUPnPMapSendsTheMappingAndReadsTheWANIP(t *testing.T) {
	f := newFakeIGD(t)
	client := netip.MustParseAddr("192.168.1.50")

	m, err := upnpMap(context.Background(), f.location, client, TCP, 8080, 8099, 2*time.Hour)
	if err != nil {
		t.Fatalf("upnpMap: %v", err)
	}
	if m.Method != "UPnP" {
		t.Errorf("method = %q", m.Method)
	}
	if m.ExternalPort != 8099 {
		t.Errorf("external port = %d", m.ExternalPort)
	}
	if m.ExternalIP != netip.MustParseAddr("198.51.100.9") {
		t.Errorf("external IP = %v", m.ExternalIP)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastClient != "192.168.1.50" {
		t.Errorf("internal client sent = %q, want the host LAN address", f.lastClient)
	}
	if f.lastExtPort != "8099" {
		t.Errorf("external port sent = %q", f.lastExtPort)
	}
	if f.lastLease != "7200" {
		t.Errorf("lease sent = %q, want 7200 seconds", f.lastLease)
	}
}

// A router that supports only permanent leases (error 725) is retried with a
// zero lease rather than failing.
func TestUPnPRetriesWithPermanentLease(t *testing.T) {
	f := newFakeIGD(t)
	f.permanentOnly = true
	client := netip.MustParseAddr("192.168.1.50")

	m, err := upnpMap(context.Background(), f.location, client, TCP, 8080, 8099, time.Hour)
	if err != nil {
		t.Fatalf("upnpMap: %v", err)
	}
	if m.ExternalPort != 8099 {
		t.Errorf("external port = %d", m.ExternalPort)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.adds != 2 {
		t.Errorf("AddPortMapping called %d times, want 2 (the 725 retry)", f.adds)
	}
	if f.lastLease != "0" {
		t.Errorf("retry lease = %q, want 0 (permanent)", f.lastLease)
	}
}

func TestUPnPMapNeedsAnInternalClient(t *testing.T) {
	f := newFakeIGD(t)
	if _, err := upnpMap(context.Background(), f.location, netip.Addr{}, TCP, 8080, 8099, time.Hour); err == nil {
		t.Fatal("mapping without an internal client was allowed")
	}
}

func TestUPnPDeleteRemovesTheMapping(t *testing.T) {
	f := newFakeIGD(t)
	client := netip.MustParseAddr("192.168.1.50")
	m, err := upnpMap(context.Background(), f.location, client, TCP, 8080, 8099, time.Hour)
	if err != nil {
		t.Fatalf("upnpMap: %v", err)
	}
	if err := unmapTarget(context.Background(), target{}, m, TCP, 8080); err != nil {
		t.Fatalf("unmapTarget: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deletes != 1 {
		t.Errorf("DeletePortMapping called %d times, want 1", f.deletes)
	}
}

// With no gateway but a UPnP location and internal client, mapTarget falls all
// the way through to UPnP - the fallback-of-last-resort path.
func TestMapTargetFallsThroughToUPnP(t *testing.T) {
	f := newFakeIGD(t)
	t2 := target{
		internalClient: netip.MustParseAddr("192.168.1.50"),
		upnpLocation:   f.location,
	}
	m, err := mapTarget(context.Background(), t2, TCP, 8080, 8099, time.Hour)
	if err != nil {
		t.Fatalf("mapTarget: %v", err)
	}
	if m.Method != "UPnP" {
		t.Errorf("method = %q, want UPnP", m.Method)
	}
}
