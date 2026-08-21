package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Nikkkaaws/wsit/internal/config"
	cryptox "github.com/Nikkkaaws/wsit/internal/cryptox"
	"github.com/Nikkkaaws/wsit/internal/frame"
	"github.com/Nikkkaaws/wsit/internal/imapc"
	"github.com/Nikkkaaws/wsit/internal/netroute"
	"github.com/Nikkkaaws/wsit/internal/socks5"
)

const (
	batchMagic         = "WSTB"
	frameQueueOverhead = 64
	deliveryQueueSize  = 64
	maxLanePingGap     = 40 * time.Second
	laneOpenParallel   = 4
	localStreamMask    = uint32(0x00ffffff)
)

type Engine struct {
	cfg      *config.Config
	key      [32]byte
	log      *slog.Logger
	streams  sync.Map
	next     atomic.Uint32
	lanes    []*lane
	laneMu   sync.RWMutex
	txBytes  atomic.Uint64
	rxBytes  atomic.Uint64
	active   atomic.Int64
	pingSent sync.Map
	rttMS    atomic.Uint64
	underlay netroute.Selection
}

type Stats struct {
	TXBytes       uint64
	RXBytes       uint64
	ActiveStreams int64
	RTTMS         uint64
	LiveLanes     int
	PendingBytes  int64
	Appends       uint64
}

type lane struct {
	id      int
	acc     config.IMAPAccount
	send    *imapc.Client
	recv    *imapc.Client
	janitor *imapc.Client
	sendCh  chan frame.Frame
	ctrlCh  chan frame.Frame
	lastUID uint32
	imapMu  sync.Mutex
	pending atomic.Int64
	appends atomic.Uint64
	aux     []*appendWorker
}

type appendWorker struct {
	id     int
	mu     sync.Mutex
	client *imapc.Client
	sendCh chan frame.Frame
}

type stream struct {
	id          uint32
	conn        net.Conn
	seqOut      atomic.Uint32
	acked       atomic.Uint32
	mu          sync.Mutex
	expect      uint32
	hold        map[uint32][]byte
	holdBytes   int
	closeSeen   bool
	closeAt     uint32
	closeQueued bool
	deliverCh   chan delivery
	done        chan struct{}
	opened      chan struct{}
	ackCh       chan struct{}
	once        sync.Once
}

type delivery struct {
	data    []byte
	nextSeq uint32
	close   bool
}

func New(cfg *config.Config, log *slog.Logger) (*Engine, error) {
	key, err := cryptox.DeriveKey(cfg.Passphrase)
	if err != nil {
		return nil, err
	}
	underlay, err := netroute.Resolve(cfg.IMAP.DirectInterface)
	if err != nil {
		return nil, err
	}
	return &Engine{cfg: cfg, key: key, log: log, underlay: underlay}, nil
}

func (e *Engine) Stats() Stats {
	stats := Stats{
		TXBytes:       e.txBytes.Load(),
		RXBytes:       e.rxBytes.Load(),
		ActiveStreams: e.active.Load(),
		RTTMS:         e.rttMS.Load(),
	}
	e.laneMu.RLock()
	stats.LiveLanes = len(e.lanes)
	for _, ln := range e.lanes {
		stats.PendingBytes += ln.pending.Load()
		stats.Appends += ln.appends.Load()
	}
	e.laneMu.RUnlock()
	return stats
}

func (e *Engine) Probe(ctx context.Context) error {
	accs := e.cfg.AccountList()
	var last error
	ok := 0
	for _, acc := range accs {
		c, err := e.dialIMAP(ctx, acc)
		if err != nil {
			last = err
			e.log.Warn("probe dial", "user", acc.Username, "err", err)
			continue
		}
		if err := c.Login(ctx, acc.Username, acc.Password); err != nil {
			last = err
			e.log.Warn("probe login", "user", acc.Username, "err", err)
			c.Close()
			continue
		}
		for _, folder := range []string{e.cfg.IMAP.FolderSend, e.cfg.IMAP.FolderRecv} {
			if err := c.Create(ctx, folder); err != nil {
				last = err
				e.log.Warn("probe create", "user", acc.Username, "folder", folder, "err", err)
				continue
			}
			if err := c.Select(ctx, folder); err != nil {
				last = err
				e.log.Warn("probe select", "user", acc.Username, "folder", folder, "err", err)
				continue
			}
			e.log.Info("folder", "user", acc.Username, "name", folder, "uidnext", c.UIDNext(), "exists", c.Exists())
		}
		c.Logout(ctx)
		c.Close()
		ok++
	}
	if ok == 0 {
		if last == nil {
			last = fmt.Errorf("no imap accounts")
		}
		return last
	}
	e.log.Info("probe done", "ok", ok, "total", len(accs))
	return nil
}

func (e *Engine) Run(ctx context.Context) error {
	accs := e.cfg.AccountList()
	e.lanes = e.openLanes(ctx, accs)
	if len(e.lanes) == 0 {
		return fmt.Errorf("wsit: no live imap lanes")
	}
	e.log.Info("wsit up",
		"mode", e.cfg.Mode,
		"client_id", e.cfg.ClientID,
		"lanes", len(e.lanes),
		"append_workers", e.cfg.IMAPAppendWorkers,
		"stream_window_kb", e.cfg.StreamWindowKB,
		"send", e.cfg.SendFolder(),
		"recv", e.cfg.RecvFolder(),
		"purge_after_sec", e.cfg.PurgeAfterSec,
		"purge_owner", e.cfg.PurgeOwner,
		"imap_if", e.underlay.Name,
		"imap_if_index", e.underlay.Index,
		"imap_local_ip", e.underlay.LocalIP.String(),
	)

	gctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, len(e.lanes)*(e.cfg.IMAPAppendWorkers+1)+1)
	for _, ln := range e.lanes {
		go func(ln *lane) { errCh <- e.sendLoop(gctx, ln, nil) }(ln)
		for _, worker := range ln.aux {
			go func(ln *lane, worker *appendWorker) { errCh <- e.sendLoop(gctx, ln, worker) }(ln, worker)
		}
		go func(ln *lane) { errCh <- e.recvLoop(gctx, ln) }(ln)
		go e.janitorLoop(gctx, ln)
	}
	if e.cfg.Mode == "client" {
		go func() { errCh <- e.listenSOCKS(gctx) }()
		go e.pingLoop(gctx)
	}
	if e.cfg.StatsIntervalSec > 0 {
		go e.statsLoop(gctx)
	}
	select {
	case <-ctx.Done():
		cancel()
		e.shutdown()
		return ctx.Err()
	case err := <-errCh:
		cancel()
		e.shutdown()
		return err
	}
}

type laneOpenResult struct {
	index int
	lane  *lane
	err   error
}

func (e *Engine) openLanes(ctx context.Context, accs []config.IMAPAccount) []*lane {
	results := make(chan laneOpenResult, len(accs))
	limit := make(chan struct{}, min(laneOpenParallel, len(accs)))
	for i, acc := range accs {
		go func(index int, account config.IMAPAccount) {
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				results <- laneOpenResult{index: index, err: ctx.Err()}
				return
			}
			ln, err := e.openLane(ctx, index, account)
			results <- laneOpenResult{index: index, lane: ln, err: err}
		}(i, acc)
	}

	ordered := make([]*lane, len(accs))
	for range accs {
		result := <-results
		if result.err != nil {
			e.log.Warn("lane skip", "user", accs[result.index].Username, "err", result.err)
			continue
		}
		ordered[result.index] = result.lane
	}
	lanes := ordered[:0]
	for _, ln := range ordered {
		if ln != nil {
			lanes = append(lanes, ln)
		}
	}
	return lanes
}

func (e *Engine) openLane(ctx context.Context, id int, acc config.IMAPAccount) (*lane, error) {
	ln := &lane{
		id:     id,
		acc:    acc,
		sendCh: make(chan frame.Frame, e.cfg.SendQueueFrames),
		ctrlCh: make(chan frame.Frame, 64),
	}
	for workerID := 1; workerID < e.cfg.IMAPAppendWorkers; workerID++ {
		ln.aux = append(ln.aux, &appendWorker{
			id:     workerID,
			sendCh: make(chan frame.Frame, e.cfg.SendQueueFrames),
		})
	}
	send, err := e.dialIMAP(ctx, acc)
	if err != nil {
		return nil, err
	}
	if err := send.Login(ctx, acc.Username, acc.Password); err != nil {
		send.Close()
		return nil, fmt.Errorf("send login: %w", err)
	}
	ln.send = send
	recv, err := e.dialIMAP(ctx, acc)
	if err != nil {
		e.log.Warn("second imap failed, using one connection", "user", acc.Username, "err", err)
		ln.recv = send
	} else if err := recv.Login(ctx, acc.Username, acc.Password); err != nil {
		recv.Close()
		e.log.Warn("recv login failed, using one connection", "user", acc.Username, "err", err)
		ln.recv = send
	} else {
		ln.recv = recv
	}
	for _, folder := range []string{e.cfg.SendFolder(), e.cfg.RecvFolder()} {
		if err := ln.send.Create(ctx, folder); err != nil {
			ln.close()
			return nil, err
		}
	}
	if err := ln.recv.Select(ctx, e.cfg.RecvFolder()); err != nil {
		ln.close()
		return nil, fmt.Errorf("select recv: %w", err)
	}
	if n := ln.recv.UIDNext(); n > 1 {
		ln.lastUID = n - 1
	}
	e.log.Info("lane up", "user", acc.Username, "last_uid", ln.lastUID)
	return ln, nil
}

func (ln *lane) close() {
	if ln.send != nil {
		ln.send.Close()
	}
	if ln.recv != nil && ln.recv != ln.send {
		ln.recv.Close()
	}
	if ln.janitor != nil {
		ln.janitor.Close()
	}
	for _, worker := range ln.aux {
		worker.close()
	}
}

func (e *Engine) shutdown() {
	e.streams.Range(func(_, v any) bool {
		s := v.(*stream)
		s.close()
		return true
	})
	e.laneMu.Lock()
	defer e.laneMu.Unlock()
	for _, ln := range e.lanes {
		ln.close()
	}
}

func (e *Engine) dialIMAP(ctx context.Context, account config.IMAPAccount) (*imapc.Client, error) {
	underlay := e.underlay
	if account.DirectInterface != "" && account.DirectInterface != e.cfg.IMAP.DirectInterface {
		selection, err := netroute.Resolve(account.DirectInterface)
		if err != nil {
			return nil, err
		}
		underlay = selection
	}
	return imapc.Dial(ctx, imapc.DialOpts{
		DialHost:       account.DialHost(),
		SNI:            account.Host,
		Port:           account.Port,
		InterfaceIndex: underlay.Index,
		LocalIP:        underlay.LocalIP,
	})
}

func (e *Engine) listenSOCKS(ctx context.Context) error {
	ln, err := net.Listen("tcp", e.cfg.Listen)
	if err != nil {
		return err
	}
	e.log.Info("socks", "addr", e.cfg.Listen)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go e.handleSOCKS(ctx, c)
	}
}

func (e *Engine) handleSOCKS(ctx context.Context, c net.Conn) {
	req, err := socks5.ServeConn(c)
	if err != nil {
		_ = c.Close()
		e.log.Debug("socks handshake", "err", err)
		return
	}
	if req.Command == socks5.CommandUDPAssociate {
		e.handleSOCKSUDP(ctx, c)
		return
	}
	e.handleClientConnect(ctx, c, req)
}

func (e *Engine) handleClientConnect(ctx context.Context, c net.Conn, req *socks5.Request) {
	id, st, installed := e.newClientStream(c)
	if !installed {
		_ = c.Close()
		return
	}
	initial, err := e.readOptimistic(c)
	if err != nil {
		e.drop(id)
		return
	}
	var flags uint16
	if len(initial) > 0 {
		flags |= frame.FlagOpenData
		st.seqOut.Store(1)
	}
	open := frame.Frame{
		Type:     frame.TypeOpen,
		Flags:    flags,
		StreamID: id,
		Seq:      0,
		Payload:  frame.OpenPayloadWithData(req.Host, req.Port, initial),
	}
	if err := e.enqueue(ctx, open); err != nil {
		e.drop(id)
		return
	}
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	select {
	case <-st.opened:
	case <-timer.C:
		e.log.Warn("open timeout", "host", req.Host)
		e.drop(id)
		return
	case <-ctx.Done():
		e.drop(id)
		return
	}
	e.splice(ctx, st)
}

func (e *Engine) newClientStream(c net.Conn) (uint32, *stream, bool) {
	// Local IDs are a 24-bit ring. Retrying occupied slots keeps a long-lived
	// client working after wrap instead of dropping the first reused stream.
	for attempts := uint32(0); attempts < localStreamMask; attempts++ {
		local := e.next.Add(1) & localStreamMask
		if local == 0 {
			continue
		}
		id := frame.MakeStreamID(e.cfg.ClientID, local)
		st, installed := e.newStream(id, c)
		if installed {
			return id, st, true
		}
	}
	return 0, nil, false
}

func (e *Engine) readOptimistic(c net.Conn) ([]byte, error) {
	if e.cfg.OptimisticOpenMS <= 0 {
		return nil, nil
	}
	maxBytes := e.cfg.StreamReadKB * 1024
	if limit := frame.MaxPayload - 258; maxBytes > limit {
		maxBytes = limit
	}
	buf := make([]byte, maxBytes)
	if err := c.SetReadDeadline(time.Now().Add(time.Duration(e.cfg.OptimisticOpenMS) * time.Millisecond)); err != nil {
		return nil, err
	}
	n, err := c.Read(buf)
	if resetErr := c.SetReadDeadline(time.Time{}); resetErr != nil {
		return nil, resetErr
	}
	if n > 0 {
		initial := make([]byte, n)
		copy(initial, buf[:n])
		return initial, nil
	}
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, nil
		}
		return nil, err
	}
	return nil, nil
}

func (e *Engine) newStream(id uint32, c net.Conn) (*stream, bool) {
	st := &stream{
		id:        id,
		conn:      c,
		hold:      map[uint32][]byte{},
		deliverCh: make(chan delivery, deliveryQueueSize),
		done:      make(chan struct{}),
		opened:    make(chan struct{}),
		ackCh:     make(chan struct{}, 1),
	}
	actual, loaded := e.streams.LoadOrStore(id, st)
	if loaded {
		return actual.(*stream), false
	}
	e.active.Add(1)
	go e.deliveryLoop(st)
	return st, true
}

func (e *Engine) drop(id uint32) {
	if v, ok := e.streams.LoadAndDelete(id); ok {
		e.active.Add(-1)
		v.(*stream).close()
	}
}

func (s *stream) close() {
	s.once.Do(func() {
		close(s.done)
		if s.conn != nil {
			_ = s.conn.Close()
		}
		select {
		case <-s.opened:
		default:
			close(s.opened)
		}
	})
}

func (s *stream) markOpen() {
	select {
	case <-s.opened:
	default:
		close(s.opened)
	}
}

func (e *Engine) splice(ctx context.Context, st *stream) {
	defer func() {
		_ = e.enqueue(ctx, frame.Frame{
			Type:     frame.TypeClose,
			StreamID: st.id,
			Seq:      st.seqOut.Load(),
		})
		e.drop(st.id)
	}()
	buf := make([]byte, e.cfg.StreamReadKB*1024)
	for {
		if ctx.Err() != nil {
			return
		}
		if err := st.waitSendWindow(ctx, e.streamWindowFrames()); err != nil {
			return
		}
		n, err := st.conn.Read(buf)
		if n > 0 {
			p := make([]byte, n)
			copy(p, buf[:n])
			f := frame.Frame{
				Type:     frame.TypeData,
				StreamID: st.id,
				Seq:      st.seqOut.Add(1) - 1,
				Payload:  p,
			}
			if qerr := e.enqueue(ctx, f); qerr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (e *Engine) enqueue(ctx context.Context, f frame.Frame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.laneMu.RLock()
	lanes := e.lanes
	e.laneMu.RUnlock()
	if len(lanes) == 0 {
		return fmt.Errorf("wsit: no live lanes")
	}
	size := int64(len(f.Payload) + frameQueueOverhead)
	if f.Type != frame.TypeData || !e.cfg.StripeData || len(lanes) == 1 {
		laneIndex := int(f.StreamID) % len(lanes)
		if f.Type == frame.TypePing || f.Type == frame.TypePong {
			laneIndex = int(f.Seq) % len(lanes)
		}
		ln := lanes[laneIndex]
		return queueFrame(ctx, ln, f, size, true)
	}

	// A busy stream uses every healthy mailbox. Pending bytes include queued,
	// batched, and in-flight APPEND data, so a slow lane naturally receives less.
	// A linear scan avoids sorting and allocating the complete lane list for
	// every DATA frame when account pools grow into the dozens.
	best := 0
	bestPending := lanes[0].pending.Load()
	for i := 1; i < len(lanes); i++ {
		if pending := lanes[i].pending.Load(); pending < bestPending {
			best = i
			bestPending = pending
		}
	}
	if err := queueFrame(ctx, lanes[best], f, size, false); err == nil {
		return nil
	}
	for i, ln := range lanes {
		if i == best {
			continue
		}
		if err := queueFrame(ctx, ln, f, size, false); err == nil {
			return nil
		}
	}
	return queueFrame(ctx, lanes[best], f, size, true)
}

func queueFrame(ctx context.Context, ln *lane, f frame.Frame, size int64, block bool) error {
	var ch chan frame.Frame
	if f.Type == frame.TypeData {
		ch = ln.dataQueue(f.StreamID)
	} else {
		ch = ln.ctrlCh
	}
	ln.pending.Add(size)
	if !block {
		select {
		case ch <- f:
			return nil
		default:
			ln.pending.Add(-size)
			return fmt.Errorf("wsit: lane queue full")
		}
	}
	select {
	case ch <- f:
		return nil
	case <-ctx.Done():
		ln.pending.Add(-size)
		return ctx.Err()
	}
}

func (ln *lane) dataQueue(streamID uint32) chan frame.Frame {
	workerCount := len(ln.aux) + 1
	workerID := int(streamID % uint32(workerCount))
	if workerID == 0 {
		return ln.sendCh
	}
	return ln.aux[workerID-1].sendCh
}

func (e *Engine) reconnect(ctx context.Context, ln *lane, failed *imapc.Client) error {
	ln.imapMu.Lock()
	defer ln.imapMu.Unlock()
	if failed != nil && ln.send != failed && ln.recv != failed {
		return nil
	}
	e.log.Warn("imap reconnect", "user", ln.acc.Username)
	if ln.send != nil {
		ln.send.Close()
	}
	if ln.recv != nil && ln.recv != ln.send {
		ln.recv.Close()
	}
	ln.send = nil
	ln.recv = nil
	send, err := e.dialIMAP(ctx, ln.acc)
	if err != nil {
		return err
	}
	if err := send.Login(ctx, ln.acc.Username, ln.acc.Password); err != nil {
		send.Close()
		return err
	}
	recv, err := e.dialIMAP(ctx, ln.acc)
	if err != nil {
		ln.send = send
		ln.recv = send
	} else if err := recv.Login(ctx, ln.acc.Username, ln.acc.Password); err != nil {
		recv.Close()
		ln.send = send
		ln.recv = send
	} else {
		ln.send = send
		ln.recv = recv
	}
	for _, folder := range []string{e.cfg.SendFolder(), e.cfg.RecvFolder()} {
		_ = ln.send.Create(ctx, folder)
	}
	if err := ln.recv.Select(ctx, e.cfg.RecvFolder()); err != nil {
		return err
	}
	e.log.Info("imap reconnected", "user", ln.acc.Username, "last_uid", ln.lastUID)
	return nil
}

func (e *Engine) sendLoop(ctx context.Context, ln *lane, worker *appendWorker) error {
	delay := time.Duration(e.cfg.BatchDelayMS) * time.Millisecond
	minBytes := int64(e.cfg.BatchMinKB * 1024)
	maxBytes := int64(e.cfg.BatchMaxKB * 1024)
	targetBytes := minBytes
	var batch []frame.Frame
	var batchBytes int64
	var ctrlCh <-chan frame.Frame
	dataCh := (<-chan frame.Frame)(ln.sendCh)
	if worker == nil {
		ctrlCh = ln.ctrlCh
	} else {
		dataCh = worker.sendCh
	}
	var idleTimer *time.Timer
	var idleC <-chan time.Time
	if worker != nil {
		idleTimer = time.NewTimer(time.Hour)
		if !idleTimer.Stop() {
			<-idleTimer.C
		}
		defer idleTimer.Stop()
	}
	resetIdle := func() {
		if idleTimer == nil {
			return
		}
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(30 * time.Second)
		idleC = idleTimer.C
	}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		body, err := e.packMail(ln.acc.Username, batch)
		if err != nil {
			return err
		}
		if worker == nil {
			err = e.appendWithRetry(ctx, ln, body)
		} else {
			err = e.appendAuxWithRetry(ctx, ln, worker, body)
		}
		if err != nil {
			return err
		}
		resetIdle()
		var payloadBytes uint64
		var queuedBytes int64
		for _, f := range batch {
			payloadBytes += uint64(len(f.Payload))
			queuedBytes += int64(len(f.Payload) + frameQueueOverhead)
		}
		remaining := ln.pending.Add(-queuedBytes)
		if remaining >= maxBytes {
			targetBytes = maxBytes
		} else {
			targetBytes = minBytes
		}
		ln.appends.Add(1)
		e.txBytes.Add(payloadBytes)
		batch = batch[:0]
		batchBytes = 0
		return nil
	}
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	addFrame := func(f frame.Frame) error {
		batch = append(batch, f)
		batchBytes += int64(len(f.Payload) + frameQueueOverhead)
		latencySensitive := f.Type != frame.TypeData && f.Type != frame.TypeAck
		if batchBytes >= targetBytes || len(batch) >= 256 || latencySensitive {
			return flush()
		}
		if delay == 0 {
			return flush()
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
		return nil
	}
	for {
		// Control frames bypass a saturated data queue so OPEN/CLOSE/PING do not
		// wait behind megabytes of bulk transfer.
		select {
		case f := <-ctrlCh:
			if err := addFrame(f); err != nil {
				return err
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return nil
		case f := <-ctrlCh:
			if err := addFrame(f); err != nil {
				return err
			}
		case f := <-dataCh:
			if err := addFrame(f); err != nil {
				return err
			}
		case <-timer.C:
			if err := flush(); err != nil {
				return err
			}
		case <-idleC:
			worker.close()
			idleC = nil
		}
	}
}

func (e *Engine) appendWithRetry(ctx context.Context, ln *lane, body []byte) error {
	backoff := 250 * time.Millisecond
	for ctx.Err() == nil {
		send := ln.sendClient()
		if send == nil {
			if err := e.reconnect(ctx, ln, nil); err != nil {
				e.log.Warn("imap reconnect", "user", ln.acc.Username, "err", err)
				if err := waitContext(ctx, backoff); err != nil {
					return err
				}
				backoff = minDuration(backoff*2, 5*time.Second)
				continue
			}
			send = ln.sendClient()
			if send == nil {
				continue
			}
		}

		started := time.Now()
		if err := send.Append(ctx, e.cfg.SendFolder(), body); err == nil {
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				e.log.Debug("slow append", "user", ln.acc.Username, "bytes", len(body), "elapsed", elapsed)
			}
			return nil
		} else {
			e.log.Warn("append", "user", ln.acc.Username, "bytes", len(body), "err", err)
			if rerr := e.reconnect(ctx, ln, send); rerr != nil {
				e.log.Warn("reconnect", "user", ln.acc.Username, "err", rerr)
			}
		}
		if err := waitContext(ctx, backoff); err != nil {
			return err
		}
		backoff = minDuration(backoff*2, 5*time.Second)
	}
	return ctx.Err()
}

func (e *Engine) appendAuxWithRetry(ctx context.Context, ln *lane, worker *appendWorker, body []byte) error {
	backoff := 250 * time.Millisecond
	for ctx.Err() == nil {
		send := worker.current()
		if send == nil {
			candidate, err := e.dialIMAP(ctx, ln.acc)
			if err == nil {
				err = candidate.Login(ctx, ln.acc.Username, ln.acc.Password)
			}
			if err != nil {
				if candidate != nil {
					_ = candidate.Close()
				}
				e.log.Warn("append worker connect", "user", ln.acc.Username, "worker", worker.id, "err", err)
				if err := waitContext(ctx, backoff); err != nil {
					return err
				}
				backoff = minDuration(backoff*2, 5*time.Second)
				continue
			}
			send = worker.install(candidate)
		}

		started := time.Now()
		if err := send.Append(ctx, e.cfg.SendFolder(), body); err == nil {
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				e.log.Debug("slow append", "user", ln.acc.Username, "worker", worker.id, "bytes", len(body), "elapsed", elapsed)
			}
			return nil
		} else {
			e.log.Warn("append", "user", ln.acc.Username, "worker", worker.id, "bytes", len(body), "err", err)
			worker.drop(send)
		}
		if err := waitContext(ctx, backoff); err != nil {
			return err
		}
		backoff = minDuration(backoff*2, 5*time.Second)
	}
	return ctx.Err()
}

func (w *appendWorker) current() *imapc.Client {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.client
}

func (w *appendWorker) install(candidate *imapc.Client) *imapc.Client {
	w.mu.Lock()
	if w.client == nil {
		w.client = candidate
		candidate = nil
	}
	current := w.client
	w.mu.Unlock()
	if candidate != nil {
		_ = candidate.Close()
	}
	return current
}

func (w *appendWorker) drop(failed *imapc.Client) {
	w.mu.Lock()
	if w.client != failed {
		w.mu.Unlock()
		return
	}
	w.client = nil
	w.mu.Unlock()
	if failed != nil {
		_ = failed.Close()
	}
}

func (w *appendWorker) close() {
	w.mu.Lock()
	client := w.client
	w.client = nil
	w.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

func waitContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (e *Engine) packMail(from string, frames []frame.Frame) ([]byte, error) {
	sealed := make([][]byte, 0, len(frames))
	for _, f := range frames {
		raw, err := frame.Encode(f)
		if err != nil {
			return nil, err
		}
		blob, err := cryptox.Seal(e.key, raw)
		if err != nil {
			return nil, err
		}
		sealed = append(sealed, blob)
	}
	payload := packBatch(sealed)
	b64 := make([]byte, base64.StdEncoding.EncodedLen(len(payload)))
	base64.StdEncoding.Encode(b64, payload)
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", from)
	fmt.Fprintf(&b, "Subject: Re: photos\r\n")
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: text/plain; charset=us-ascii\r\n")
	fmt.Fprintf(&b, "Content-Transfer-Encoding: base64\r\n\r\n")
	b.Write(b64)
	b.WriteString("\r\n")
	return b.Bytes(), nil
}

func packBatch(items [][]byte) []byte {
	n := 4 + 2
	for _, it := range items {
		n += 4 + len(it)
	}
	buf := make([]byte, n)
	copy(buf[0:4], batchMagic)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(len(items)))
	off := 6
	for _, it := range items {
		binary.LittleEndian.PutUint32(buf[off:off+4], uint32(len(it)))
		off += 4
		copy(buf[off:], it)
		off += len(it)
	}
	return buf
}

func unpackBatch(b []byte) ([][]byte, error) {
	if len(b) >= 6 && string(b[:4]) == batchMagic {
		count := int(binary.LittleEndian.Uint16(b[4:6]))
		off := 6
		out := make([][]byte, 0, count)
		for i := 0; i < count; i++ {
			if off+4 > len(b) {
				return nil, fmt.Errorf("wsit: truncated batch length")
			}
			n := int(binary.LittleEndian.Uint32(b[off : off+4]))
			off += 4
			if n <= 0 || off+n > len(b) {
				return nil, fmt.Errorf("wsit: invalid batch item size %d", n)
			}
			out = append(out, b[off:off+n])
			off += n
		}
		if off != len(b) {
			return nil, fmt.Errorf("wsit: trailing batch data")
		}
		return out, nil
	}
	return [][]byte{b}, nil
}

func (ln *lane) sendClient() *imapc.Client {
	ln.imapMu.Lock()
	defer ln.imapMu.Unlock()
	return ln.send
}

func (ln *lane) recvClient() *imapc.Client {
	ln.imapMu.Lock()
	defer ln.imapMu.Unlock()
	return ln.recv
}

func (e *Engine) recvLoop(ctx context.Context, ln *lane) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		send, recv := ln.sendClient(), ln.recvClient()
		if recv == nil {
			if rerr := e.reconnect(ctx, ln, nil); rerr != nil {
				e.log.Warn("reconnect", "user", ln.acc.Username, "err", rerr)
				_ = waitContext(ctx, 500*time.Millisecond)
			}
			continue
		}
		if recv == send {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(400 * time.Millisecond):
			}
		}

		// Fetch before IDLE closes the notification gap: a message arriving
		// between the previous FETCH and the next IDLE can otherwise wait until
		// an unrelated mailbox change wakes the connection.
		msgs, err := recv.UIDFetchText(ctx, ln.lastUID+1)
		if err != nil {
			e.log.Warn("fetch", "user", ln.acc.Username, "from_uid", ln.lastUID+1, "err", err)
			if rerr := e.reconnect(ctx, ln, recv); rerr != nil {
				e.log.Warn("reconnect", "user", ln.acc.Username, "err", rerr)
				_ = waitContext(ctx, 500*time.Millisecond)
			}
			continue
		}
		ingestFailed := false
		for _, m := range msgs {
			if m.UID <= ln.lastUID {
				continue
			}
			if err := e.ingest(ctx, m.Body); err != nil {
				e.log.Warn("ingest", "user", ln.acc.Username, "uid", m.UID, "err", err)
				ingestFailed = true
				break
			}
			ln.lastUID = m.UID
		}
		if ingestFailed {
			_ = waitContext(ctx, 500*time.Millisecond)
			continue
		}
		if len(msgs) > 0 {
			continue
		}
		if recv == send {
			if err := recv.Noop(ctx); err != nil {
				e.log.Warn("noop", "user", ln.acc.Username, "err", err)
				_ = e.reconnect(ctx, ln, recv)
			}
			continue
		}
		if err := recv.IdleUntilExists(ctx, time.Duration(e.cfg.IMAPIdleRefreshSec)*time.Second); err != nil && ctx.Err() == nil {
			e.log.Debug("idle", "user", ln.acc.Username, "err", err)
		}
	}
}

func (e *Engine) ingest(ctx context.Context, body []byte) error {
	body = bytes.TrimSpace(body)
	raw := make([]byte, base64.StdEncoding.DecodedLen(len(body)))
	n, err := base64.StdEncoding.Decode(raw, body)
	if err != nil {
		compact := bytes.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, body)
		raw = make([]byte, base64.StdEncoding.DecodedLen(len(compact)))
		n, err = base64.StdEncoding.Decode(raw, compact)
		if err != nil {
			return err
		}
	}
	items, err := unpackBatch(raw[:n])
	if err != nil {
		return err
	}
	frames := make([]frame.Frame, 0, len(items))
	for _, sealed := range items {
		pt, err := cryptox.Open(e.key, sealed)
		if err != nil {
			return err
		}
		f, err := frame.Decode(pt)
		if err != nil {
			return err
		}
		frames = append(frames, f)
	}
	for _, f := range frames {
		e.dispatch(ctx, f)
	}
	return nil
}

func (e *Engine) dispatch(ctx context.Context, f frame.Frame) {
	switch f.Type {
	case frame.TypePing:
		_ = e.enqueue(ctx, frame.Frame{Type: frame.TypePong, StreamID: f.StreamID, Seq: f.Seq})
	case frame.TypePong:
		if frame.ClientID(f.StreamID) != e.cfg.ClientID {
			return
		}
		if sent, ok := e.pingSent.LoadAndDelete(f.Seq); ok {
			e.rttMS.Store(uint64(time.Since(sent.(time.Time)).Milliseconds()))
		}
		return
	case frame.TypeAck:
		if v, ok := e.streams.Load(f.StreamID); ok {
			v.(*stream).ack(f.Seq)
		}
		return
	case frame.TypeOpen:
		if e.cfg.Mode != "server" {
			return
		}
		go e.acceptOpen(ctx, f)
	case frame.TypeOpenOK:
		if v, ok := e.streams.Load(f.StreamID); ok {
			st := v.(*stream)
			if f.Flags&frame.FlagOpenData != 0 && len(f.Payload) > 0 {
				e.writeData(ctx, frame.Frame{
					Type:     frame.TypeData,
					StreamID: f.StreamID,
					Seq:      0,
					Payload:  f.Payload,
				})
			}
			st.markOpen()
		}
	case frame.TypeClose:
		e.handleRemoteClose(f)
	case frame.TypeData:
		e.writeData(ctx, f)
	}
}

func (e *Engine) acceptOpen(ctx context.Context, f frame.Frame) {
	host, port, initial, err := frame.ParseOpenData(f.Payload)
	if err != nil {
		e.log.Warn("open parse", "err", err)
		return
	}
	if f.Flags&frame.FlagOpenData == 0 {
		initial = nil
	}
	var c net.Conn
	if e.cfg.Target == "direct" {
		c, err = net.DialTimeout("tcp", socks5.JoinHostPort(host, port), 15*time.Second)
	} else {
		c, err = socks5.DialThrough(e.cfg.Target, host, port, 15*time.Second)
	}
	if err != nil {
		e.log.Warn("exit dial", "host", host, "err", err)
		_ = e.enqueue(ctx, frame.Frame{Type: frame.TypeClose, StreamID: f.StreamID, Seq: 0})
		return
	}
	st, installed := e.newStream(f.StreamID, c)
	if !installed {
		_ = c.Close()
		_ = e.enqueue(ctx, frame.Frame{Type: frame.TypeOpenOK, StreamID: f.StreamID})
		return
	}
	if len(initial) > 0 {
		st.expect = 1
		if err := writeFull(c, initial); err != nil {
			e.drop(f.StreamID)
			_ = e.enqueue(ctx, frame.Frame{Type: frame.TypeClose, StreamID: f.StreamID})
			return
		}
	}
	response, err := e.readOptimistic(c)
	if err != nil {
		e.drop(f.StreamID)
		_ = e.enqueue(ctx, frame.Frame{Type: frame.TypeClose, StreamID: f.StreamID})
		return
	}
	var openOKFlags uint16
	if len(response) > 0 {
		openOKFlags |= frame.FlagOpenData
		st.seqOut.Store(1)
	}
	st.markOpen()
	if err := e.enqueue(ctx, frame.Frame{
		Type:     frame.TypeOpenOK,
		Flags:    openOKFlags,
		StreamID: f.StreamID,
		Seq:      0,
		Payload:  response,
	}); err != nil {
		e.drop(f.StreamID)
		return
	}
	e.log.Info("opened", "id", f.StreamID, "dest", socks5.JoinHostPort(host, port))
	e.splice(ctx, st)
}

func (e *Engine) writeData(ctx context.Context, f frame.Frame) {
	v, ok := e.streams.Load(f.StreamID)
	if !ok {
		return
	}
	st := v.(*stream)
	st.mu.Lock()
	if f.Seq < st.expect {
		st.mu.Unlock()
		return
	}
	if f.Seq > st.expect {
		if _, exists := st.hold[f.Seq]; exists {
			st.mu.Unlock()
			return
		}
		limit := e.cfg.ReorderMaxKB * 1024
		if st.holdBytes+len(f.Payload) > limit {
			st.mu.Unlock()
			e.log.Warn("reorder overflow", "id", f.StreamID, "expect", st.expect, "got", f.Seq, "limit_kb", e.cfg.ReorderMaxKB)
			e.drop(f.StreamID)
			_ = e.enqueue(ctx, frame.Frame{Type: frame.TypeClose, StreamID: f.StreamID})
			return
		}
		st.hold[f.Seq] = f.Payload
		st.holdBytes += len(f.Payload)
		st.mu.Unlock()
		return
	}
	if !st.queueDeliveryLocked(delivery{data: f.Payload, nextSeq: f.Seq + 1}) {
		st.mu.Unlock()
		return
	}
	e.rxBytes.Add(uint64(len(f.Payload)))
	st.expect++
	for {
		p, ok := st.hold[st.expect]
		if !ok {
			break
		}
		delete(st.hold, st.expect)
		st.holdBytes -= len(p)
		next := st.expect + 1
		if !st.queueDeliveryLocked(delivery{data: p, nextSeq: next}) {
			st.mu.Unlock()
			return
		}
		e.rxBytes.Add(uint64(len(p)))
		st.expect++
	}
	st.queueRemoteCloseLocked()
	st.mu.Unlock()
}

func (e *Engine) handleRemoteClose(f frame.Frame) {
	v, ok := e.streams.Load(f.StreamID)
	if !ok {
		return
	}
	st := v.(*stream)
	st.mu.Lock()
	if f.Seq == 0 {
		st.closeSeen = true
		st.closeAt = st.expect
	} else if !st.closeSeen || f.Seq < st.closeAt {
		st.closeSeen = true
		st.closeAt = f.Seq
	}
	st.queueRemoteCloseLocked()
	st.mu.Unlock()
}

func (s *stream) queueDeliveryLocked(item delivery) bool {
	select {
	case s.deliverCh <- item:
		return true
	case <-s.done:
		return false
	}
}

func (s *stream) queueRemoteCloseLocked() {
	if !s.closeSeen || s.closeQueued || s.expect < s.closeAt {
		return
	}
	if s.queueDeliveryLocked(delivery{close: true}) {
		s.closeQueued = true
	}
}

func (e *Engine) deliveryLoop(st *stream) {
	var lastAck uint32
	for {
		select {
		case <-st.done:
			return
		case item := <-st.deliverCh:
			if item.close {
				e.drop(st.id)
				return
			}
			if err := writeFull(st.conn, item.data); err != nil {
				e.log.Debug("stream write", "id", st.id, "err", err)
				e.drop(st.id)
				qctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = e.enqueue(qctx, frame.Frame{Type: frame.TypeClose, StreamID: st.id})
				cancel()
				return
			}
			if item.nextSeq-lastAck >= uint32(e.cfg.AckEveryFrames) {
				ackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				err := e.enqueue(ackCtx, frame.Frame{
					Type:     frame.TypeAck,
					StreamID: st.id,
					Seq:      item.nextSeq,
				})
				cancel()
				if err == nil {
					lastAck = item.nextSeq
				}
			}
		}
	}
}

func (e *Engine) streamWindowFrames() uint32 {
	frames := e.cfg.StreamWindowKB / e.cfg.StreamReadKB
	if frames < 4 {
		frames = 4
	}
	return uint32(frames)
}

func (s *stream) waitSendWindow(ctx context.Context, window uint32) error {
	for {
		sent := s.seqOut.Load()
		acked := s.acked.Load()
		if sent-acked < window {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return net.ErrClosed
		case <-s.ackCh:
		}
	}
}

func (s *stream) ack(nextSeq uint32) {
	for {
		current := s.acked.Load()
		if nextSeq <= current || s.acked.CompareAndSwap(current, nextSeq) {
			break
		}
	}
	select {
	case s.ackCh <- struct{}{}:
	default:
	}
}

func writeFull(c net.Conn, p []byte) error {
	for len(p) > 0 {
		n, err := c.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 {
			return fmt.Errorf("wsit: zero-byte stream write")
		}
		p = p[n:]
	}
	return nil
}

func (e *Engine) pingLoop(ctx context.Context) {
	ms := e.cfg.PingIntervalMS
	if ms <= 0 {
		return
	}
	e.laneMu.RLock()
	laneCount := len(e.lanes)
	e.laneMu.RUnlock()
	configured := time.Duration(ms) * time.Millisecond
	interval := effectivePingInterval(configured, laneCount)
	if interval != configured {
		e.log.Info("ping interval scaled", "configured_ms", ms, "actual_ms", interval.Milliseconds(), "lanes", laneCount)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	var n uint32
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n++
			e.pingSent.Store(n, time.Now())
			if n > 64 {
				e.pingSent.Delete(n - 64)
			}
			_ = e.enqueue(ctx, frame.Frame{Type: frame.TypePing, StreamID: frame.MakeStreamID(e.cfg.ClientID, 0), Seq: n})
		}
	}
}

func effectivePingInterval(configured time.Duration, laneCount int) time.Duration {
	if laneCount <= 0 {
		return configured
	}
	maxInterval := maxLanePingGap / time.Duration(laneCount)
	if configured > maxInterval {
		return maxInterval
	}
	return configured
}

func (e *Engine) statsLoop(ctx context.Context) {
	interval := time.Duration(e.cfg.StatsIntervalSec) * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	lastTX := e.txBytes.Load()
	lastRX := e.rxBytes.Load()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tx := e.txBytes.Load()
			rx := e.rxBytes.Load()
			var pending int64
			var appends uint64
			e.laneMu.RLock()
			for _, ln := range e.lanes {
				pending += ln.pending.Load()
				appends += ln.appends.Load()
			}
			e.laneMu.RUnlock()
			e.log.Info("transport stats",
				"tx_mbps", float64(tx-lastTX)*8/interval.Seconds()/1_000_000,
				"rx_mbps", float64(rx-lastRX)*8/interval.Seconds()/1_000_000,
				"pending_kb", pending/1024,
				"appends", appends,
				"streams", e.active.Load(),
				"rtt_ms", e.rttMS.Load(),
			)
			lastTX, lastRX = tx, rx
		}
	}
}

func (e *Engine) janitorLoop(ctx context.Context, ln *lane) {
	if e.cfg.PurgeAfterSec <= 0 || (e.cfg.PurgeOwner == "server" && e.cfg.Mode != "server") {
		return
	}
	e.purgeOnce(ctx, ln)
	t := time.NewTicker(time.Duration(e.cfg.PurgeEverySec) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.purgeOnce(ctx, ln)
		}
	}
}

func (e *Engine) purgeOnce(ctx context.Context, ln *lane) {
	if ctx.Err() != nil {
		return
	}
	c, err := e.ensureJanitor(ctx, ln)
	if err != nil {
		e.log.Warn("purge dial", "user", ln.acc.Username, "err", err)
		return
	}
	for _, folder := range []string{e.cfg.IMAP.FolderSend, e.cfg.IMAP.FolderRecv} {
		if err := c.Select(ctx, folder); err != nil {
			e.log.Warn("purge select", "user", ln.acc.Username, "folder", folder, "err", err)
			e.dropJanitor(ln)
			return
		}
		older, err := c.UIDSearchOlder(ctx, e.cfg.PurgeAfterSec)
		if err != nil {
			e.log.Warn("purge search", "user", ln.acc.Username, "folder", folder, "err", err)
			older = nil
		}
		deleted, err := c.UIDSearchDeleted(ctx)
		if err != nil {
			e.log.Warn("purge search deleted", "user", ln.acc.Username, "folder", folder, "err", err)
			deleted = nil
		}
		uids := append(older, deleted...)
		const keep = 48
		if c.Exists() > 80 && len(older) == 0 {
			all, searchErr := c.UIDSearchAll(ctx)
			if searchErr != nil {
				e.log.Warn("purge search all", "user", ln.acc.Username, "folder", folder, "err", searchErr)
			} else if len(all) > keep {
				uids = append(uids, all[:len(all)-keep]...)
			}
		}
		if len(uids) == 0 {
			continue
		}
		n := len(uids)
		if err := c.UIDDelete(ctx, uids); err != nil {
			e.log.Warn("purge delete", "user", ln.acc.Username, "folder", folder, "n", n, "exists", c.Exists(), "err", err)
			e.dropJanitor(ln)
			return
		}
		e.log.Info("purge", "user", ln.acc.Username, "folder", folder, "n", n, "exists_before", c.Exists(), "older_than_sec", e.cfg.PurgeAfterSec)
	}
}

func (e *Engine) ensureJanitor(ctx context.Context, ln *lane) (*imapc.Client, error) {
	ln.imapMu.Lock()
	c := ln.janitor
	ln.imapMu.Unlock()
	if c != nil {
		if err := c.Noop(ctx); err == nil {
			return c, nil
		}
		e.dropJanitor(ln)
	}
	c, err := e.dialIMAP(ctx, ln.acc)
	if err != nil {
		return nil, err
	}
	if err := c.Login(ctx, ln.acc.Username, ln.acc.Password); err != nil {
		c.Close()
		return nil, err
	}
	ln.imapMu.Lock()
	if ln.janitor != nil {
		ln.janitor.Close()
	}
	ln.janitor = c
	ln.imapMu.Unlock()
	return c, nil
}

func (e *Engine) dropJanitor(ln *lane) {
	ln.imapMu.Lock()
	defer ln.imapMu.Unlock()
	if ln.janitor != nil {
		ln.janitor.Close()
		ln.janitor = nil
	}
}
