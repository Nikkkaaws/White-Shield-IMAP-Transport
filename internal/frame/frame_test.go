package frame

import "testing"

func TestRoundTrip(t *testing.T) {
	in := Frame{Type: TypeData, Flags: 0, StreamID: MakeStreamID(2, 7), Seq: 9, Payload: []byte("hello")}
	raw, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != in.Type || out.StreamID != in.StreamID || out.Seq != in.Seq || string(out.Payload) != "hello" {
		t.Fatalf("%+v", out)
	}
}

func TestAckRoundTrip(t *testing.T) {
	in := Frame{Type: TypeAck, StreamID: MakeStreamID(3, 19), Seq: 42}
	raw, err := Encode(in)
	if err != nil {
		t.Fatalf("encode ACK: %v", err)
	}
	out, err := Decode(raw)
	if err != nil {
		t.Fatalf("decode ACK: %v", err)
	}
	if out.Type != TypeAck || out.StreamID != in.StreamID || out.Seq != 42 {
		t.Fatalf("ACK mismatch: %+v", out)
	}
}

func TestOpenPayload(t *testing.T) {
	p := OpenPayloadWithData("example.com", 443, []byte("client hello"))
	h, port, err := ParseOpen(p)
	if err != nil {
		t.Fatal(err)
	}
	if h != "example.com" || port != 443 {
		t.Fatalf("%s %d", h, port)
	}
	h, port, initial, err := ParseOpenData(p)
	if err != nil || h != "example.com" || port != 443 || string(initial) != "client hello" {
		t.Fatalf("open data: host=%q port=%d initial=%q err=%v", h, port, initial, err)
	}
	if ClientID(MakeStreamID(2, 99)) != 2 {
		t.Fatal("client id")
	}
}
