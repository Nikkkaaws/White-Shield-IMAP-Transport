//go:build windows

package netroute

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const adapterBufferSize = 15 * 1024

type adapterCandidate struct {
	selection   Selection
	description string
	ifType      uint32
	metric      uint32
	physical    bool
}

func resolve(spec string) (Selection, error) {
	spec = strings.TrimSpace(spec)
	if strings.EqualFold(spec, "off") || strings.EqualFold(spec, "none") {
		return Selection{}, nil
	}
	if spec == "" {
		spec = "auto"
	}
	candidates, err := windowsAdapters()
	if err != nil {
		return Selection{}, err
	}

	if !strings.EqualFold(spec, "auto") {
		requestedIndex, indexErr := strconv.ParseUint(spec, 10, 32)
		for _, candidate := range candidates {
			indexMatch := indexErr == nil && candidate.selection.Index == uint32(requestedIndex)
			nameMatch := strings.EqualFold(spec, candidate.selection.Name) || strings.EqualFold(spec, candidate.description)
			if indexMatch || nameMatch {
				if candidate.selection.LocalIP == nil {
					return Selection{}, fmt.Errorf("wsit: interface %q has no usable IPv4 address", spec)
				}
				return candidate.selection, nil
			}
		}
		return Selection{}, fmt.Errorf("wsit: direct interface %q not found", spec)
	}

	best := -1
	for i, candidate := range candidates {
		if !autoEligible(candidate) {
			continue
		}
		if best < 0 || betterAdapter(candidate, candidates[best]) {
			best = i
		}
	}
	if best < 0 {
		return Selection{}, fmt.Errorf("wsit: no physical IPv4 adapter with a default gateway")
	}
	return candidates[best].selection, nil
}

func windowsAdapters() ([]adapterCandidate, error) {
	size := uint32(adapterBufferSize)
	for attempts := 0; attempts < 3; attempts++ {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(
			windows.AF_INET,
			windows.GAA_FLAG_INCLUDE_GATEWAYS|
				windows.GAA_FLAG_SKIP_ANYCAST|
				windows.GAA_FLAG_SKIP_MULTICAST|
				windows.GAA_FLAG_SKIP_DNS_SERVER,
			0,
			first,
			&size,
		)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("wsit: enumerate network adapters: %w", err)
		}

		var out []adapterCandidate
		for adapter := first; adapter != nil; adapter = adapter.Next {
			if adapter.OperStatus != windows.IfOperStatusUp || adapter.IfIndex == 0 {
				continue
			}
			name := windows.UTF16PtrToString(adapter.FriendlyName)
			description := windows.UTF16PtrToString(adapter.Description)
			out = append(out, adapterCandidate{
				selection: Selection{
					Index:   adapter.IfIndex,
					Name:    name,
					LocalIP: firstUsableIPv4(adapter.FirstUnicastAddress),
				},
				description: description,
				ifType:      adapter.IfType,
				metric:      adapter.Ipv4Metric,
				physical:    adapter.PhysicalAddressLength > 0,
			})
			if out[len(out)-1].selection.LocalIP == nil || !hasIPv4Gateway(adapter.FirstGatewayAddress) {
				// Keep it for an explicit name/index lookup, but mark it ineligible for auto.
				out[len(out)-1].metric = ^uint32(0)
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("wsit: network adapter list changed repeatedly")
}

func firstUsableIPv4(first *windows.IpAdapterUnicastAddress) net.IP {
	for current := first; current != nil; current = current.Next {
		ip := current.Address.IP().To4()
		if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		return append(net.IP(nil), ip...)
	}
	return nil
}

func hasIPv4Gateway(first *windows.IpAdapterGatewayAddress) bool {
	for current := first; current != nil; current = current.Next {
		ip := current.Address.IP().To4()
		if ip != nil && !ip.IsUnspecified() {
			return true
		}
	}
	return false
}

func autoEligible(candidate adapterCandidate) bool {
	if candidate.selection.LocalIP == nil || candidate.metric == ^uint32(0) {
		return false
	}
	if candidate.ifType == windows.IF_TYPE_TUNNEL || candidate.ifType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
		return false
	}
	combined := strings.ToLower(candidate.selection.Name + " " + candidate.description)
	for _, marker := range []string{
		"tun", "tap", "vpn", "wintun", "sing-box", "singbox", "wireguard",
		"tailscale", "zerotier", "hyper-v", "vethernet", "vmware", "virtualbox",
		"host-only", "loopback",
	} {
		if strings.Contains(combined, marker) {
			return false
		}
	}
	return true
}

func betterAdapter(candidate, current adapterCandidate) bool {
	candidatePreferred := candidate.ifType == windows.IF_TYPE_ETHERNET_CSMACD || candidate.ifType == windows.IF_TYPE_IEEE80211
	currentPreferred := current.ifType == windows.IF_TYPE_ETHERNET_CSMACD || current.ifType == windows.IF_TYPE_IEEE80211
	if candidatePreferred != currentPreferred {
		return candidatePreferred
	}
	if candidate.physical != current.physical {
		return candidate.physical
	}
	if candidate.metric != current.metric {
		return candidate.metric < current.metric
	}
	return candidate.selection.Index < current.selection.Index
}
