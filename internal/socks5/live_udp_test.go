package socks5

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestLiveUDPAssociateDNS(t *testing.T) {
	proxy := os.Getenv("WSIT_INTEGRATION_PROXY")
	if proxy == "" {
		t.Skip("WSIT_INTEGRATION_PROXY is not set")
	}
	control, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if err := control.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(control, method); err != nil {
		t.Fatal(err)
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		t.Fatalf("SOCKS method reply %v", method)
	}
	if _, err := control.Write([]byte{0x05, CommandUDPAssociate, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(control, header); err != nil {
		t.Fatal(err)
	}
	if header[0] != 0x05 || header[1] != 0x00 {
		t.Fatalf("UDP ASSOCIATE reply %v", header)
	}
	host, port, err := readAddr(control, header[3])
	if err != nil {
		t.Fatal(err)
	}
	relay, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	if err := udp.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 64*1024)
	for i, name := range []string{"example.com", "iana.org", "cloudflare.com", "example.net"} {
		queryID := uint16(time.Now().UnixNano()) + uint16(i)
		query := dnsAQuery(queryID, name)
		packet, err := EncodeUDPDatagram("8.8.8.8", 53, query)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := udp.Write(packet); err != nil {
			t.Fatal(err)
		}
		n, err := udp.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		response, err := ParseUDPDatagram(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		if response.Host != "8.8.8.8" || response.Port != 53 {
			t.Fatalf("unexpected UDP response source %s:%d", response.Host, response.Port)
		}
		if len(response.Payload) < 12 {
			t.Fatalf("short DNS response: %d", len(response.Payload))
		}
		if got := binary.BigEndian.Uint16(response.Payload[:2]); got != queryID {
			t.Fatalf("DNS ID %x, want %x", got, queryID)
		}
		if response.Payload[2]&0x80 == 0 {
			t.Fatal("DNS packet is not a response")
		}
	}
}

func dnsAQuery(id uint16, host string) []byte {
	query := make([]byte, 12)
	binary.BigEndian.PutUint16(query[0:2], id)
	query[2] = 0x01
	query[5] = 0x01
	start := 0
	for i := 0; i <= len(host); i++ {
		if i != len(host) && host[i] != '.' {
			continue
		}
		query = append(query, byte(i-start))
		query = append(query, host[start:i]...)
		start = i + 1
	}
	query = append(query, 0x00, 0x00, 0x01, 0x00, 0x01)
	return query
}
