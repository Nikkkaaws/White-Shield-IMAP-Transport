package engine

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/Nikkkaaws/wsit/internal/config"
	"github.com/Nikkkaaws/wsit/internal/frame"
	"github.com/Nikkkaaws/wsit/internal/imapc"
)

func TestEnqueueStripesBusyStreamAcrossLanes(t *testing.T) {
	e := &Engine{cfg: &config.Config{StripeData: true}}
	for i := 0; i < 4; i++ {
		e.lanes = append(e.lanes, &lane{
			id:     i,
			sendCh: make(chan frame.Frame, 16),
		})
	}

	for seq := uint32(0); seq < 12; seq++ {
		err := e.enqueue(context.Background(), frame.Frame{
			Type:     frame.TypeData,
			StreamID: 0x01000001,
			Seq:      seq,
			Payload:  bytes.Repeat([]byte{byte(seq)}, 1024),
		})
		if err != nil {
			t.Fatalf("enqueue seq %d: %v", seq, err)
		}
	}

	for _, ln := range e.lanes {
		if got := len(ln.sendCh); got != 3 {
			t.Fatalf("lane %d got %d frames, want 3", ln.id, got)
		}
	}
}

func TestQueueFrameBackpressureDoesNotDrop(t *testing.T) {
	ln := &lane{sendCh: make(chan frame.Frame, 1), ctrlCh: make(chan frame.Frame, 1)}
	first := frame.Frame{Type: frame.TypeData, Payload: []byte("first")}
	if err := queueFrame(context.Background(), ln, first, 5, true); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	second := frame.Frame{Type: frame.TypeData, Payload: []byte("second")}
	if err := queueFrame(ctx, ln, second, 6, true); err == nil {
		t.Fatal("second enqueue unexpectedly succeeded")
	}
	if got := len(ln.sendCh); got != 1 {
		t.Fatalf("queue contains %d frames, want 1", got)
	}
	if got := ln.pending.Load(); got != 5 {
		t.Fatalf("pending=%d, want 5", got)
	}
}

func TestControlFrameBypassesSaturatedDataQueue(t *testing.T) {
	ln := &lane{sendCh: make(chan frame.Frame, 1), ctrlCh: make(chan frame.Frame, 1)}
	if err := queueFrame(context.Background(), ln, frame.Frame{Type: frame.TypeData}, 1, true); err != nil {
		t.Fatalf("fill data queue: %v", err)
	}
	closeFrame := frame.Frame{Type: frame.TypeClose, StreamID: 7, Seq: 3}
	if err := queueFrame(context.Background(), ln, closeFrame, 1, false); err != nil {
		t.Fatalf("control enqueue behind saturated data queue: %v", err)
	}
	select {
	case got := <-ln.ctrlCh:
		if got.Type != frame.TypeClose || got.StreamID != 7 || got.Seq != 3 {
			t.Fatalf("unexpected control frame: %+v", got)
		}
	default:
		t.Fatal("control frame was not routed to the priority queue")
	}
}

func TestDataStreamStaysOnOneAppendWorker(t *testing.T) {
	ln := &lane{
		sendCh: make(chan frame.Frame, 8),
		ctrlCh: make(chan frame.Frame, 8),
		aux: []*appendWorker{
			{id: 1, sendCh: make(chan frame.Frame, 8)},
		},
	}
	firstID := frame.MakeStreamID(1, 1)
	secondID := frame.MakeStreamID(1, 2)
	for seq := uint32(0); seq < 4; seq++ {
		if err := queueFrame(context.Background(), ln, frame.Frame{
			Type: frame.TypeData, StreamID: firstID, Seq: seq,
		}, 1, true); err != nil {
			t.Fatalf("first stream seq %d: %v", seq, err)
		}
		if err := queueFrame(context.Background(), ln, frame.Frame{
			Type: frame.TypeData, StreamID: secondID, Seq: seq,
		}, 1, true); err != nil {
			t.Fatalf("second stream seq %d: %v", seq, err)
		}
	}

	if got := len(ln.aux[0].sendCh); got != 4 {
		t.Fatalf("odd stream worker frames=%d, want 4", got)
	}
	if got := len(ln.sendCh); got != 4 {
		t.Fatalf("even stream primary frames=%d, want 4", got)
	}
	for want := uint32(0); want < 4; want++ {
		if got := (<-ln.aux[0].sendCh).Seq; got != want {
			t.Fatalf("aux seq=%d, want %d", got, want)
		}
		if got := (<-ln.sendCh).Seq; got != want {
			t.Fatalf("primary seq=%d, want %d", got, want)
		}
	}
}

func TestReadOptimisticCarriesInitialBytes(t *testing.T) {
	e := &Engine{cfg: &config.Config{OptimisticOpenMS: 50, StreamReadKB: 64}}
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()

	written := make(chan error, 1)
	go func() {
		_, err := remote.Write([]byte("client hello"))
		written <- err
	}()
	got, err := e.readOptimistic(local)
	if err != nil {
		t.Fatalf("read optimistic: %v", err)
	}
	if err := <-written; err != nil {
		t.Fatalf("write initial bytes: %v", err)
	}
	if string(got) != "client hello" {
		t.Fatalf("got %q, want client hello", got)
	}
}

func TestReadOptimisticTimeoutIsNotAnError(t *testing.T) {
	e := &Engine{cfg: &config.Config{OptimisticOpenMS: 5, StreamReadKB: 64}}
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	got, err := e.readOptimistic(local)
	if err != nil {
		t.Fatalf("timeout: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d bytes on timeout", len(got))
	}
}

func TestOpenOKCarriesInitialResponse(t *testing.T) {
	e := &Engine{
		cfg: &config.Config{ReorderMaxKB: 1024},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	local, remote := net.Pipe()
	defer remote.Close()
	const id = uint32(0x01000007)
	st, installed := e.newStream(id, local)
	if !installed {
		t.Fatal("stream was not installed")
	}

	e.dispatch(context.Background(), frame.Frame{
		Type:     frame.TypeOpenOK,
		Flags:    frame.FlagOpenData,
		StreamID: id,
		Payload:  []byte("server hello"),
	})
	select {
	case <-st.opened:
	case <-time.After(time.Second):
		t.Fatal("OPEN_OK did not mark the stream open")
	}
	_ = remote.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len("server hello"))
	if _, err := io.ReadFull(remote, got); err != nil {
		t.Fatalf("read inline response: %v", err)
	}
	if string(got) != "server hello" {
		t.Fatalf("got %q", got)
	}
	st.close()
}

func TestReorderAndCloseBarrier(t *testing.T) {
	e := &Engine{
		cfg: &config.Config{ReorderMaxKB: 1024},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	local, remote := net.Pipe()
	defer remote.Close()
	const id = uint32(0x01000001)
	if _, installed := e.newStream(id, local); !installed {
		t.Fatal("stream was not installed")
	}

	e.writeData(context.Background(), frame.Frame{
		Type: frame.TypeData, StreamID: id, Seq: 1, Payload: []byte("world"),
	})
	e.handleRemoteClose(frame.Frame{Type: frame.TypeClose, StreamID: id, Seq: 2})
	e.writeData(context.Background(), frame.Frame{
		Type: frame.TypeData, StreamID: id, Seq: 0, Payload: []byte("hello "),
	})

	_ = remote.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len("hello world"))
	if _, err := io.ReadFull(remote, got); err != nil {
		t.Fatalf("read reassembled stream: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
	one := make([]byte, 1)
	if _, err := remote.Read(one); err == nil {
		t.Fatal("stream remained open after close barrier")
	}
}

func TestBatchRoundTripAndTruncation(t *testing.T) {
	want := [][]byte{[]byte("one"), []byte("two"), bytes.Repeat([]byte{0xa5}, 1024)}
	packed := packBatch(want)
	got, err := unpackBatch(packed)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("item %d mismatch", i)
		}
	}
	if _, err := unpackBatch(packed[:len(packed)-1]); err == nil {
		t.Fatal("truncated batch was accepted")
	}
}

func TestAppendWorkerInstallDropAndClose(t *testing.T) {
	worker := &appendWorker{id: 1}
	first := &imapc.Client{}
	second := &imapc.Client{}

	if got := worker.install(first); got != first {
		t.Fatalf("installed client=%p, want %p", got, first)
	}
	if got := worker.install(second); got != first {
		t.Fatalf("replacement changed active client to %p", got)
	}
	worker.drop(second)
	if got := worker.current(); got != first {
		t.Fatalf("stale failure dropped active client: got %p", got)
	}
	worker.drop(first)
	if got := worker.current(); got != nil {
		t.Fatalf("failed client remained installed: %p", got)
	}

	worker.install(second)
	worker.close()
	worker.close()
	if got := worker.current(); got != nil {
		t.Fatalf("closed worker retained client: %p", got)
	}
}

func TestEffectivePingIntervalScalesWithLaneCount(t *testing.T) {
	tests := []struct {
		name       string
		configured time.Duration
		lanes      int
		want       time.Duration
	}{
		{name: "four lanes unchanged", configured: 10 * time.Second, lanes: 4, want: 10 * time.Second},
		{name: "eight lanes reduced", configured: 10 * time.Second, lanes: 8, want: 5 * time.Second},
		{name: "fast setting preserved", configured: time.Second, lanes: 64, want: 625 * time.Millisecond},
		{name: "no lanes", configured: 10 * time.Second, lanes: 0, want: 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectivePingInterval(tt.configured, tt.lanes); got != tt.want {
				t.Fatalf("interval=%s, want %s", got, tt.want)
			}
		})
	}
}

func TestPongFromAnotherClientDoesNotConsumeRTTSample(t *testing.T) {
	e := &Engine{
		cfg: &config.Config{ClientID: 1},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	e.pingSent.Store(uint32(7), time.Now().Add(-time.Second))
	e.dispatch(context.Background(), frame.Frame{
		Type:     frame.TypePong,
		StreamID: frame.MakeStreamID(2, 0),
		Seq:      7,
	})
	if _, ok := e.pingSent.Load(uint32(7)); !ok {
		t.Fatal("another client's PONG consumed the local RTT sample")
	}
	if got := e.rttMS.Load(); got != 0 {
		t.Fatalf("rtt=%d, want 0", got)
	}
}

func TestStreamWindowUnblocksOnAck(t *testing.T) {
	st := &stream{
		done:  make(chan struct{}),
		ackCh: make(chan struct{}, 1),
	}
	st.seqOut.Store(8)
	finished := make(chan error, 1)
	go func() {
		finished <- st.waitSendWindow(context.Background(), 8)
	}()

	select {
	case err := <-finished:
		t.Fatalf("full window returned before ACK: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	st.ack(4)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("wait after ACK: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ACK did not unblock the send window")
	}
}

func TestStreamAckOnlyMovesForward(t *testing.T) {
	st := &stream{ackCh: make(chan struct{}, 1)}
	st.ack(12)
	st.ack(7)
	if got := st.acked.Load(); got != 12 {
		t.Fatalf("acked=%d, want 12", got)
	}
}

func TestClientStreamAllocatorSkipsOccupiedIDAfterWrap(t *testing.T) {
	e := &Engine{
		cfg: &config.Config{ClientID: 7},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	occupiedLocal, occupiedRemote := net.Pipe()
	defer occupiedRemote.Close()
	occupiedID := frame.MakeStreamID(7, 1)
	if _, installed := e.newStream(occupiedID, occupiedLocal); !installed {
		t.Fatal("failed to reserve occupied stream ID")
	}
	e.next.Store(localStreamMask)

	newLocal, newRemote := net.Pipe()
	defer newRemote.Close()
	id, _, installed := e.newClientStream(newLocal)
	if !installed {
		t.Fatal("allocator failed after counter wrap")
	}
	if want := frame.MakeStreamID(7, 2); id != want {
		t.Fatalf("allocated id=%08x, want %08x", id, want)
	}
	e.drop(occupiedID)
	e.drop(id)
}
