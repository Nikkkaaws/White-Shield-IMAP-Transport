package socks5

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestUDPDatagramRoundTrip(t *testing.T) {
	want := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01}
	packet, err := EncodeUDPDatagram("resolver.example", 53, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseUDPDatagram(packet)
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "resolver.example" || got.Port != 53 || !bytes.Equal(got.Payload, want) {
		t.Fatalf("unexpected datagram: %+v", got)
	}
}

func TestUDPDatagramRejectsFragments(t *testing.T) {
	packet, err := EncodeUDPDatagram("1.1.1.1", 53, []byte("dns"))
	if err != nil {
		t.Fatal(err)
	}
	packet[2] = 1
	if _, err := ParseUDPDatagram(packet); err == nil {
		t.Fatal("fragmented datagram was accepted")
	}
}

func TestServeConnUDPAssociateDefersReply(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	result := make(chan *Request, 1)
	errs := make(chan error, 1)
	go func() {
		req, err := ServeConn(server)
		result <- req
		errs <- err
	}()

	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(client, method); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(method, []byte{0x05, 0x00}) {
		t.Fatalf("method response %v", method)
	}
	request := []byte{0x05, CommandUDPAssociate, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	req := <-result
	if req.Command != CommandUDPAssociate || req.Host != "0.0.0.0" || req.Port != 0 {
		t.Fatalf("unexpected request: %+v", req)
	}
	if err := client.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	one := make([]byte, 1)
	if _, err := client.Read(one); err == nil {
		t.Fatal("UDP ASSOCIATE received a reply before the relay was bound")
	}
}

func TestReplyEncodesBoundUDPAddress(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Reply(server, 0, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 45678})
	}()
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reply[:4], []byte{0x05, 0x00, 0x00, 0x01}) {
		t.Fatalf("reply header %v", reply[:4])
	}
	if got := net.IP(reply[4:8]).String(); got != "127.0.0.1" {
		t.Fatalf("bound IP %s", got)
	}
	if got := binary.BigEndian.Uint16(reply[8:]); got != 45678 {
		t.Fatalf("bound port %d", got)
	}
}

func TestServeConnRejectsMissingNoAuthMethod(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	errs := make(chan error, 1)
	go func() {
		_, err := ServeConn(server)
		errs <- err
	}()
	if _, err := client.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reply, []byte{0x05, 0xff}) {
		t.Fatalf("method rejection %v", reply)
	}
	if err := <-errs; err == nil {
		t.Fatal("unsupported authentication methods were accepted")
	}
}
