package frame

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	Magic   = "WST1"
	Version = 2

	TypeOpen   uint8 = 1
	TypeData   uint8 = 2
	TypeClose  uint8 = 3
	TypePing   uint8 = 4
	TypePong   uint8 = 5
	TypeOpenOK uint8 = 6
	TypeAck    uint8 = 7

	FlagOpenData uint16 = 1 << 0

	HeaderSize = 20
	MaxPayload = 256 * 1024
)

var (
	ErrMagic   = errors.New("wsit: bad magic")
	ErrVersion = errors.New("wsit: unsupported version")
	ErrType    = errors.New("wsit: unknown type")
	ErrSize    = errors.New("wsit: payload too large")
	ErrShort   = errors.New("wsit: truncated frame")
)

type Frame struct {
	Type     uint8
	Flags    uint16
	StreamID uint32
	Seq      uint32
	Payload  []byte
}

func Encode(f Frame) ([]byte, error) {
	if f.Type < TypeOpen || f.Type > TypeAck {
		return nil, ErrType
	}
	if len(f.Payload) > MaxPayload {
		return nil, ErrSize
	}
	buf := make([]byte, HeaderSize+len(f.Payload))
	copy(buf[0:4], Magic)
	buf[4] = Version
	buf[5] = f.Type
	binary.LittleEndian.PutUint16(buf[6:8], f.Flags)
	binary.LittleEndian.PutUint32(buf[8:12], f.StreamID)
	binary.LittleEndian.PutUint32(buf[12:16], f.Seq)
	binary.LittleEndian.PutUint32(buf[16:20], uint32(len(f.Payload)))
	copy(buf[20:], f.Payload)
	return buf, nil
}

func Decode(buf []byte) (Frame, error) {
	if len(buf) < HeaderSize {
		return Frame{}, ErrShort
	}
	if string(buf[0:4]) != Magic {
		return Frame{}, ErrMagic
	}
	if buf[4] != Version {
		return Frame{}, fmt.Errorf("%w: %d", ErrVersion, buf[4])
	}
	typ := buf[5]
	if typ < TypeOpen || typ > TypeAck {
		return Frame{}, ErrType
	}
	n := binary.LittleEndian.Uint32(buf[16:20])
	if n > MaxPayload {
		return Frame{}, ErrSize
	}
	if int(n) > len(buf)-HeaderSize {
		return Frame{}, ErrShort
	}
	payload := make([]byte, n)
	copy(payload, buf[HeaderSize:HeaderSize+int(n)])
	return Frame{
		Type:     typ,
		Flags:    binary.LittleEndian.Uint16(buf[6:8]),
		StreamID: binary.LittleEndian.Uint32(buf[8:12]),
		Seq:      binary.LittleEndian.Uint32(buf[12:16]),
		Payload:  payload,
	}, nil
}

func MakeStreamID(clientID uint8, local uint32) uint32 {
	if clientID == 0 {
		clientID = 1
	}
	return uint32(clientID)<<24 | (local & 0x00ffffff)
}

func ClientID(streamID uint32) uint8 {
	return uint8(streamID >> 24)
}

func OpenPayload(host string, port uint16) []byte {
	return OpenPayloadWithData(host, port, nil)
}

func OpenPayloadWithData(host string, port uint16, initial []byte) []byte {
	hb := []byte(host)
	if len(hb) > 255 {
		hb = hb[:255]
	}
	maxInitial := MaxPayload - 1 - 2 - len(hb)
	if len(initial) > maxInitial {
		initial = initial[:maxInitial]
	}
	buf := make([]byte, 1+2+len(hb)+len(initial))
	buf[0] = byte(len(hb))
	copy(buf[1:], hb)
	binary.LittleEndian.PutUint16(buf[1+len(hb):], port)
	copy(buf[1+len(hb)+2:], initial)
	return buf
}

func ParseOpen(p []byte) (host string, port uint16, err error) {
	if len(p) < 3 {
		return "", 0, ErrShort
	}
	n := int(p[0])
	if n == 0 || 1+n+2 > len(p) {
		return "", 0, ErrShort
	}
	host = string(p[1 : 1+n])
	port = binary.LittleEndian.Uint16(p[1+n : 1+n+2])
	if port == 0 {
		return "", 0, errors.New("wsit: port 0")
	}
	return host, port, nil
}

func ParseOpenData(p []byte) (host string, port uint16, initial []byte, err error) {
	host, port, err = ParseOpen(p)
	if err != nil {
		return "", 0, nil, err
	}
	off := 1 + int(p[0]) + 2
	return host, port, p[off:], nil
}
