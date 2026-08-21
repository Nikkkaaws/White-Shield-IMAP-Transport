package socks5

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	CommandConnect      byte = 0x01
	CommandUDPAssociate byte = 0x03
)

type Request struct {
	Command byte
	Host    string
	Port    uint16
}

type Datagram struct {
	Host    string
	Port    uint16
	Payload []byte
}

func ServeConn(c net.Conn) (*Request, error) {
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return nil, err
	}
	if hdr[0] != 0x05 {
		return nil, fmt.Errorf("socks: ver %d", hdr[0])
	}
	nmethods := int(hdr[1])
	if nmethods < 1 {
		return nil, fmt.Errorf("socks: no methods")
	}
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(c, methods); err != nil {
		return nil, err
	}
	noAuth := false
	for _, method := range methods {
		if method == 0x00 {
			noAuth = true
			break
		}
	}
	if !noAuth {
		_, _ = c.Write([]byte{0x05, 0xff})
		return nil, fmt.Errorf("socks: no supported authentication method")
	}
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return nil, err
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return nil, err
	}
	if req[0] != 0x05 {
		_ = Reply(c, 0x01, nil)
		return nil, fmt.Errorf("socks: request ver %d", req[0])
	}
	if req[1] != CommandConnect && req[1] != CommandUDPAssociate {
		_ = fail(c, 0x07)
		return nil, fmt.Errorf("socks: command %d unsupported", req[1])
	}
	host, port, err := readAddr(c, req[3])
	if err != nil {
		_ = fail(c, 0x01)
		return nil, err
	}
	_ = c.SetDeadline(time.Time{})
	if req[1] == CommandConnect {
		if err := Reply(c, 0x00, nil); err != nil {
			return nil, err
		}
	}
	return &Request{Command: req[1], Host: host, Port: port}, nil
}

func DialThrough(proxy string, host string, port uint16, timeout time.Duration) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", proxy, timeout)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = c.Close()
		}
	}()
	_ = c.SetDeadline(time.Now().Add(timeout))
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return nil, err
	}
	ack := make([]byte, 2)
	if _, err := io.ReadFull(c, ack); err != nil {
		return nil, err
	}
	if ack[0] != 0x05 || ack[1] != 0x00 {
		return nil, fmt.Errorf("socks proxy auth %v", ack)
	}
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			v6 := ip.To16()
			req = append(req, 0x04)
			req = append(req, v6...)
		}
	} else {
		if len(host) > 255 {
			return nil, fmt.Errorf("socks: host too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, []byte(host)...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], port)
	req = append(req, pb[:]...)
	if _, err := c.Write(req); err != nil {
		return nil, err
	}
	rh := make([]byte, 4)
	if _, err := io.ReadFull(c, rh); err != nil {
		return nil, err
	}
	if rh[1] != 0x00 {
		return nil, fmt.Errorf("socks connect status %d", rh[1])
	}
	if _, _, err := readAddr(c, rh[3]); err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Time{})
	ok = true
	return c, nil
}

func readAddr(r io.Reader, atyp byte) (string, uint16, error) {
	switch atyp {
	case 0x01:
		b := make([]byte, 4+2)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host := net.IP(b[:4]).String()
		port := binary.BigEndian.Uint16(b[4:])
		return host, port, nil
	case 0x04:
		b := make([]byte, 16+2)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host := net.IP(b[:16]).String()
		port := binary.BigEndian.Uint16(b[16:])
		return host, port, nil
	case 0x03:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(r, lb); err != nil {
			return "", 0, err
		}
		b := make([]byte, int(lb[0])+2)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host := string(b[:len(b)-2])
		port := binary.BigEndian.Uint16(b[len(b)-2:])
		return host, port, nil
	default:
		return "", 0, fmt.Errorf("socks: atyp %d", atyp)
	}
}

func Reply(c net.Conn, code byte, bound net.Addr) error {
	host := "0.0.0.0"
	port := uint16(0)
	switch addr := bound.(type) {
	case *net.TCPAddr:
		if addr != nil {
			host = addr.IP.String()
			port = uint16(addr.Port)
		}
	case *net.UDPAddr:
		if addr != nil {
			host = addr.IP.String()
			port = uint16(addr.Port)
		}
	}
	payload, err := appendAddr([]byte{0x05, code, 0x00}, host, port)
	if err != nil {
		return err
	}
	_, err = c.Write(payload)
	return err
}

func ParseUDPDatagram(packet []byte) (*Datagram, error) {
	if len(packet) < 4 {
		return nil, fmt.Errorf("socks: short UDP datagram")
	}
	if packet[0] != 0 || packet[1] != 0 {
		return nil, fmt.Errorf("socks: invalid UDP reserved field")
	}
	if packet[2] != 0 {
		return nil, fmt.Errorf("socks: fragmented UDP is unsupported")
	}
	r := bytes.NewReader(packet[4:])
	host, port, err := readAddr(r, packet[3])
	if err != nil {
		return nil, err
	}
	payload := make([]byte, r.Len())
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return &Datagram{Host: host, Port: port, Payload: payload}, nil
}

func EncodeUDPDatagram(host string, port uint16, payload []byte) ([]byte, error) {
	packet, err := appendAddr([]byte{0x00, 0x00, 0x00}, host, port)
	if err != nil {
		return nil, err
	}
	return append(packet, payload...), nil
}

func appendAddr(dst []byte, host string, port uint16) ([]byte, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			dst = append(dst, 0x01)
			dst = append(dst, v4...)
		} else {
			dst = append(dst, 0x04)
			dst = append(dst, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("socks: invalid host length %d", len(host))
		}
		dst = append(dst, 0x03, byte(len(host)))
		dst = append(dst, host...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], port)
	return append(dst, pb[:]...), nil
}

func fail(c net.Conn, code byte) error {
	return Reply(c, code, nil)
}

func JoinHostPort(host string, port uint16) string {
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}
