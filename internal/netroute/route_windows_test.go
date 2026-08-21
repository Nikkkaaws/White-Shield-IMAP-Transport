//go:build windows

package netroute

import (
	"net"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestAutoEligibleRejectsTunnelAndVirtualAdapters(t *testing.T) {
	base := adapterCandidate{
		selection: Selection{Index: 7, Name: "Ethernet", LocalIP: net.IPv4(192, 168, 1, 20)},
		ifType:    windows.IF_TYPE_ETHERNET_CSMACD,
		metric:    25,
		physical:  true,
	}
	if !autoEligible(base) {
		t.Fatal("physical Ethernet adapter was rejected")
	}

	tunnel := base
	tunnel.ifType = windows.IF_TYPE_TUNNEL
	if autoEligible(tunnel) {
		t.Fatal("tunnel adapter was accepted")
	}

	virtual := base
	virtual.description = "VMware Virtual Ethernet Adapter"
	if autoEligible(virtual) {
		t.Fatal("virtual adapter was accepted")
	}
}

func TestResolveAutoOnConnectedHost(t *testing.T) {
	if os.Getenv("WSIT_ROUTE_INTEGRATION") != "1" {
		t.Skip("set WSIT_ROUTE_INTEGRATION=1 for a live adapter check")
	}
	selection, err := Resolve("auto")
	if err != nil {
		t.Fatalf("resolve auto: %v", err)
	}
	if selection.Index == 0 || selection.LocalIP == nil || selection.Name == "" {
		t.Fatalf("incomplete selection: %+v", selection)
	}
	if selection.LocalIP.IsLoopback() || selection.LocalIP.IsLinkLocalUnicast() {
		t.Fatalf("unusable local address: %s", selection.LocalIP)
	}
	t.Logf("selected interface index=%d name=%q local_ip=%s", selection.Index, selection.Name, selection.LocalIP)
}

func TestBetterAdapterPrefersPhysicalAndLowerMetric(t *testing.T) {
	current := adapterCandidate{
		selection: Selection{Index: 9, LocalIP: net.IPv4(10, 0, 0, 2)},
		ifType:    windows.IF_TYPE_ETHERNET_CSMACD,
		metric:    40,
		physical:  true,
	}
	candidate := current
	candidate.selection.Index = 10
	candidate.metric = 15
	if !betterAdapter(candidate, current) {
		t.Fatal("lower-metric physical adapter was not preferred")
	}
}

func TestResolveOffDoesNotInspectAdapters(t *testing.T) {
	selection, err := Resolve("off")
	if err != nil {
		t.Fatalf("resolve off: %v", err)
	}
	if selection.Index != 0 || selection.LocalIP != nil {
		t.Fatalf("off returned a bound interface: %+v", selection)
	}
}
