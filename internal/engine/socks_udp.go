package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Nikkkaaws/wsit/internal/socks5"
)

const (
	maxSOCKSUDPDatagram = 64 * 1024
	maxDNSInFlight      = 64
	dnsExchangeTimeout  = 25 * time.Second
)

func (e *Engine) handleSOCKSUDP(parent context.Context, control net.Conn) {
	ctx, cancel := context.WithCancel(parent)

	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		_ = socks5.Reply(control, 0x01, nil)
		e.log.Debug("socks UDP listen", "err", err)
		cancel()
		_ = control.Close()
		return
	}
	var workers sync.WaitGroup
	defer func() {
		cancel()
		_ = relay.Close()
		_ = control.Close()
		workers.Wait()
	}()
	if err := socks5.Reply(control, 0x00, relay.LocalAddr()); err != nil {
		e.log.Debug("socks UDP reply", "err", err)
		return
	}

	// RFC 1928 ties the UDP relay lifetime to its TCP control connection.
	go func() {
		var one [1]byte
		_, _ = control.Read(one[:])
		cancel()
		_ = relay.Close()
	}()
	go func() {
		<-ctx.Done()
		_ = relay.Close()
	}()

	buf := make([]byte, maxSOCKSUDPDatagram)
	limit := make(chan struct{}, maxDNSInFlight)
	var client *net.UDPAddr

	for {
		n, source, err := relay.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() == nil && !isClosedNetworkError(err) {
				e.log.Debug("socks UDP read", "err", err)
			}
			return
		}
		if client == nil {
			client = cloneUDPAddr(source)
		} else if !sameUDPAddr(client, source) {
			continue
		}
		datagram, err := socks5.ParseUDPDatagram(buf[:n])
		if err != nil {
			e.log.Debug("socks UDP packet", "err", err)
			continue
		}
		if datagram.Port != 53 {
			e.log.Debug("socks UDP unsupported", "host", datagram.Host, "port", datagram.Port)
			continue
		}
		payload := append([]byte(nil), datagram.Payload...)
		destinationHost := datagram.Host
		destinationPort := datagram.Port
		destination := cloneUDPAddr(source)

		select {
		case limit <- struct{}{}:
		case <-ctx.Done():
			return
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-limit }()
			response, exchangeErr := e.exchangeDNS(ctx, payload)
			if exchangeErr != nil {
				e.log.Debug("socks UDP DNS", "err", exchangeErr)
				return
			}
			packet, encodeErr := socks5.EncodeUDPDatagram(destinationHost, destinationPort, response)
			if encodeErr != nil {
				e.log.Debug("socks UDP encode", "err", encodeErr)
				return
			}
			if _, writeErr := relay.WriteToUDP(packet, destination); writeErr != nil && ctx.Err() == nil {
				e.log.Debug("socks UDP write", "err", writeErr)
			}
		}()
	}
}

func (e *Engine) exchangeDNS(parent context.Context, query []byte) ([]byte, error) {
	if len(query) < 12 || len(query) > 65535 {
		return nil, fmt.Errorf("DNS query length %d", len(query))
	}
	host, portText, err := net.SplitHostPort(e.cfg.DNSResolver)
	if err != nil {
		return nil, fmt.Errorf("DNS resolver: %w", err)
	}
	port64, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port64 == 0 {
		return nil, fmt.Errorf("DNS resolver port %q", portText)
	}

	ctx, cancel := context.WithTimeout(parent, dnsExchangeTimeout)
	defer cancel()
	application, transport := net.Pipe()
	defer application.Close()
	go e.handleClientConnect(ctx, transport, &socks5.Request{
		Command: socks5.CommandConnect,
		Host:    host,
		Port:    uint16(port64),
	})
	if deadline, ok := ctx.Deadline(); ok {
		_ = application.SetDeadline(deadline)
	}

	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(query)))
	if err := writeFull(application, append(size[:], query...)); err != nil {
		return nil, fmt.Errorf("DNS tunnel write: %w", err)
	}
	if _, err := io.ReadFull(application, size[:]); err != nil {
		return nil, fmt.Errorf("DNS tunnel header: %w", err)
	}
	responseSize := int(binary.BigEndian.Uint16(size[:]))
	if responseSize < 12 {
		return nil, fmt.Errorf("DNS response length %d", responseSize)
	}
	response := make([]byte, responseSize)
	if _, err := io.ReadFull(application, response); err != nil {
		return nil, fmt.Errorf("DNS tunnel response: %w", err)
	}
	return response, nil
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	if addr == nil {
		return nil
	}
	return &net.UDPAddr{IP: append(net.IP(nil), addr.IP...), Port: addr.Port, Zone: addr.Zone}
}

func sameUDPAddr(a, b *net.UDPAddr) bool {
	return a != nil && b != nil && a.Port == b.Port && a.Zone == b.Zone && a.IP.Equal(b.IP)
}

func isClosedNetworkError(err error) bool {
	return errors.Is(err, net.ErrClosed)
}
