package daemon

import (
	"encoding/json"
	"net"
	"strconv"
	"testing"

	"github.com/majorcontext/moat/internal/serialbroker"
	"github.com/majorcontext/moat/internal/serialport"
	"github.com/majorcontext/moat/internal/serialtest"
)

func serialSpec(name string) SerialDeviceSpec {
	return SerialDeviceSpec{
		Name: name, Path: "/dev/ttyUSB0",
		VID: "303a", PID: "1001", Serial: "AAA",
	}
}

// newServerWithBroker returns a server whose broker is backed by fake ports.
func newServerWithBroker(t *testing.T) *Server {
	t.Helper()
	s := NewServer("", 0)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return serialtest.NewFakePort(t), nil },
		BindAddr: "127.0.0.1",
	})
	t.Cleanup(func() { b.Close() })
	s.SetSerialBroker(b)
	return s
}

func TestListenSerialReturnsAnAddrPerDevice(t *testing.T) {
	s := newServerWithBroker(t)
	rc := NewRunContext("run-a")

	addrs, err := s.listenSerial(rc, []SerialDeviceSpec{serialSpec("esp32"), serialSpec2("probe")}, "127.0.0.1")
	if err != nil {
		t.Fatalf("listenSerial: %v", err)
	}
	if len(addrs) != 2 {
		t.Fatalf("got %d addresses, want 2: %v", len(addrs), addrs)
	}
	for name, addr := range addrs {
		c, derr := net.Dial("tcp", addr)
		if derr != nil {
			t.Fatalf("device %q is not reachable at %s: %v", name, addr, derr)
		}
		c.Close()
	}
}

func TestListenSerialAllowsTheContainerToReachEachPort(t *testing.T) {
	// The allowance is the access control: RFC2217 has no authentication, so a
	// port the container cannot reach is a device it cannot use, and a port
	// left off the list would be blocked by network policy.
	s := newServerWithBroker(t)
	rc := NewRunContext("run-a")

	addrs, err := s.listenSerial(rc, []SerialDeviceSpec{serialSpec("esp32")}, "127.0.0.1")
	if err != nil {
		t.Fatalf("listenSerial: %v", err)
	}
	_, portStr, err := net.SplitHostPort(addrs["esp32"])
	if err != nil {
		t.Fatal(err)
	}
	want, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range rc.AllowedHostPorts {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("AllowedHostPorts = %v, want it to include the serial port %d", rc.AllowedHostPorts, want)
	}
}

func TestListenSerialWithNoDevicesOpensNothing(t *testing.T) {
	// Companion of the happy path: runs that ask for no devices must pay no
	// cost and gain no extra allowed ports.
	s := newServerWithBroker(t)
	rc := NewRunContext("run-a")

	addrs, err := s.listenSerial(rc, nil, "127.0.0.1")
	if err != nil {
		t.Fatalf("listenSerial: %v", err)
	}
	if len(addrs) != 0 {
		t.Fatalf("got %v, want no addresses", addrs)
	}
	if len(rc.AllowedHostPorts) != 0 {
		t.Fatalf("AllowedHostPorts = %v, want empty", rc.AllowedHostPorts)
	}
}

func TestListenSerialWithoutABrokerIsAnActionableError(t *testing.T) {
	// An older daemon has no broker. Failing loudly is the point: silently
	// starting a run with no device access would look like broken hardware.
	s := NewServer("", 0)
	_, err := s.listenSerial(NewRunContext("run-a"), []SerialDeviceSpec{serialSpec("esp32")}, "127.0.0.1")
	if err == nil {
		t.Fatal("a daemon with no serial broker must reject a run that needs devices")
	}
	if got := err.Error(); !contains(got, "moat proxy restart") {
		t.Fatalf("error %q should tell the user how to fix it", got)
	}
}

func TestListenSerialRollsBackOnConflict(t *testing.T) {
	// A partial failure must not leave the first device claimed by a run that
	// never starts.
	s := newServerWithBroker(t)
	first := NewRunContext("run-a")
	if _, err := s.listenSerial(first, []SerialDeviceSpec{serialSpec("esp32")}, "127.0.0.1"); err != nil {
		t.Fatalf("first listenSerial: %v", err)
	}

	second := NewRunContext("run-b")
	_, err := s.listenSerial(second, []SerialDeviceSpec{serialSpec2("probe"), serialSpec("esp32")}, "127.0.0.1")
	if err == nil {
		t.Fatal("claiming a device held by another run must fail")
	}

	// "probe" was claimed before the conflict; it must have been released.
	third := NewRunContext("run-c")
	if _, err := s.listenSerial(third, []SerialDeviceSpec{serialSpec2("probe")}, "127.0.0.1"); err != nil {
		t.Fatalf("rolled-back device should be claimable again: %v", err)
	}
}

func TestHealthAdvertisesSerialCapabilityOnlyWithABroker(t *testing.T) {
	withBroker := newServerWithBroker(t)
	if !containsString(withBroker.capabilities(), CapSerialDevices) {
		t.Fatalf("capabilities %v should advertise %q", withBroker.capabilities(), CapSerialDevices)
	}
	// Companion: a daemon with no broker must not claim the capability, or the
	// CLI's pre-flight check would pass and the run would fail later.
	without := NewServer("", 0)
	if containsString(without.capabilities(), CapSerialDevices) {
		t.Fatalf("capabilities %v must not advertise %q without a broker", without.capabilities(), CapSerialDevices)
	}
}

func TestRegisterRequestSerialDevicesSurviveJSON(t *testing.T) {
	// The daemon API must stay wire-compatible; a spec that loses fields in
	// transit would produce audit entries with no device identity.
	req := RegisterRequest{RunID: "run-a", SerialDevices: []SerialDeviceSpec{serialSpec("esp32")}}
	got := roundTripRegisterRequest(t, req)
	if len(got.SerialDevices) != 1 {
		t.Fatalf("got %d devices after round trip, want 1", len(got.SerialDevices))
	}
	if got.SerialDevices[0] != req.SerialDevices[0] {
		t.Fatalf("round trip gave %+v, want %+v", got.SerialDevices[0], req.SerialDevices[0])
	}
}

func serialSpec2(name string) SerialDeviceSpec {
	return SerialDeviceSpec{
		Name: name, Path: "/dev/ttyUSB1",
		VID: "1a86", PID: "7523", PortPath: "1-3",
	}
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// roundTripRegisterRequest encodes and decodes a request, as the socket does.
func roundTripRegisterRequest(t *testing.T, req RegisterRequest) RegisterRequest {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got RegisterRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}

func TestListenSerialBindsTheRequestedAddress(t *testing.T) {
	// RFC2217 has no authentication, so where the listener binds is the
	// reachability control. The register request carries the container-facing
	// address for exactly this reason; a daemon that ignored it and bound
	// wildcard would expose every attached device to the network.
	s := newServerWithBroker(t)
	rc := NewRunContext("run-a")

	addrs, err := s.listenSerial(rc, []SerialDeviceSpec{serialSpec("esp32")}, "127.0.0.1")
	if err != nil {
		t.Fatalf("listenSerial: %v", err)
	}
	host, _, err := net.SplitHostPort(addrs["esp32"])
	if err != nil {
		t.Fatal(err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Fatalf("listener bound %q, want the requested loopback address", addrs["esp32"])
	}
	// Companion: an empty request field falls back to the broker's configured
	// default — this broker is loopback, so the listener must still not be
	// wildcard. An older CLI omits the field; the daemon must fail closed.
	addrs2, err := s.listenSerial(NewRunContext("run-b"), []SerialDeviceSpec{serialSpec2("probe")}, "")
	if err != nil {
		t.Fatalf("listenSerial with empty bind: %v", err)
	}
	host2, _, err := net.SplitHostPort(addrs2["probe"])
	if err != nil {
		t.Fatal(err)
	}
	if ip := net.ParseIP(host2); ip == nil || !ip.IsLoopback() {
		t.Fatalf("empty bind fell back to %q, want the broker's loopback default", addrs2["probe"])
	}
}

func TestRegisterRequestSerialBindAddrSurvivesJSON(t *testing.T) {
	// The daemon API must stay wire-compatible; a bind address lost in transit
	// would silently fall back to the broker default.
	req := RegisterRequest{RunID: "run-a", SerialBindAddr: "172.17.0.1"}
	got := roundTripRegisterRequest(t, req)
	if got.SerialBindAddr != "172.17.0.1" {
		t.Fatalf("round trip gave SerialBindAddr %q, want 172.17.0.1", got.SerialBindAddr)
	}
}

func TestListenSerialPinnedRebindsTheContainerAddress(t *testing.T) {
	// A daemon restart must not move a device: the container's
	// MOAT_SERIAL_*_URL froze the port at create, so restore re-opens the
	// listener on that exact number. This is the daemon-restart half of the
	// "advertised URLs must stay stable" constraint.
	s := newServerWithBroker(t)
	rc := NewRunContext("run-a")
	orig, err := s.listenSerial(rc, []SerialDeviceSpec{serialSpec("esp32")}, "127.0.0.1")
	if err != nil {
		t.Fatalf("listenSerial: %v", err)
	}

	// A restart drops the old broker's listeners; simulate it by rebuilding
	// the server with a fresh broker (same fake device), after releasing the
	// old one's listener.
	s.serial.Close() //nolint:errcheck — Close on an open broker does not fail
	s2 := newServerWithBroker(t)
	rc2 := NewRunContext("run-a")
	got, err := s2.ListenSerialPinned(rc2, []SerialDeviceSpec{serialSpec("esp32")}, "127.0.0.1", orig)
	if err != nil {
		t.Fatalf("ListenSerialPinned: %v", err)
	}
	if got["esp32"] != orig["esp32"] {
		t.Fatalf("restored device at %q, want the container's frozen address %q", got["esp32"], orig["esp32"])
	}
	// The device is reachable at that address — the whole point of the pin.
	c, derr := net.Dial("tcp", got["esp32"])
	if derr != nil {
		t.Fatalf("device unreachable at the pinned address: %v", derr)
	}
	c.Close()
}

func TestListenSerialPinnedRefusesAnOccupiedPort(t *testing.T) {
	// Companion of the happy path: a pinned port now held by something else
	// must fail with a named error, not silently rebind elsewhere — the
	// container could never reach the new port, and the failure would be
	// invisible.
	s := newServerWithBroker(t)
	holder := NewRunContext("run-holder")
	if _, err := s.listenSerial(holder, []SerialDeviceSpec{serialSpec2("probe")}, "127.0.0.1"); err != nil {
		t.Fatalf("listenSerial: %v", err)
	}
	_, probeAddr, _ := func() (map[string]string, string, error) {
		addrs, err := s.listenSerial(holder, []SerialDeviceSpec{serialSpec2("probe")}, "127.0.0.1")
		return addrs, addrs["probe"], err
	}()
	if probeAddr == "" {
		t.Fatal("probe listener did not open")
	}

	restored := NewRunContext("run-a")
	_, err := s.ListenSerialPinned(restored, []SerialDeviceSpec{serialSpec2("probe")}, "127.0.0.1",
		map[string]string{"probe": probeAddr})
	if err == nil {
		t.Fatal("pinning a port another listener holds must fail")
	}
	if !contains(err.Error(), "listen") && !contains(err.Error(), "use") {
		t.Fatalf("error %q should name the bind failure", err)
	}
}

func TestRegisterRequestSerialPinsSurviveJSON(t *testing.T) {
	// The daemon API must stay wire-compatible; pins lost in transit would
	// silently downgrade a re-registration to ephemeral ports.
	req := RegisterRequest{RunID: "run-a", SerialPins: map[string]int{"esp32": 41234}}
	got := roundTripRegisterRequest(t, req)
	if got.SerialPins["esp32"] != 41234 {
		t.Fatalf("round trip gave SerialPins %v, want esp32=41234", got.SerialPins)
	}
}
