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

	addrs, err := s.listenSerial(rc, []SerialDeviceSpec{serialSpec("esp32"), serialSpec2("probe")})
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

	addrs, err := s.listenSerial(rc, []SerialDeviceSpec{serialSpec("esp32")})
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

	addrs, err := s.listenSerial(rc, nil)
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
	_, err := s.listenSerial(NewRunContext("run-a"), []SerialDeviceSpec{serialSpec("esp32")})
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
	if _, err := s.listenSerial(first, []SerialDeviceSpec{serialSpec("esp32")}); err != nil {
		t.Fatalf("first listenSerial: %v", err)
	}

	second := NewRunContext("run-b")
	_, err := s.listenSerial(second, []SerialDeviceSpec{serialSpec2("probe"), serialSpec("esp32")})
	if err == nil {
		t.Fatal("claiming a device held by another run must fail")
	}

	// "probe" was claimed before the conflict; it must have been released.
	third := NewRunContext("run-c")
	if _, err := s.listenSerial(third, []SerialDeviceSpec{serialSpec2("probe")}); err != nil {
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
