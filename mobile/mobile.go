package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Nikkkaaws/wsit/internal/config"
	"github.com/Nikkkaaws/wsit/internal/engine"
	"github.com/Nikkkaaws/wsit/internal/imapc"
	"github.com/Nikkkaaws/wsit/internal/netroute"
	"github.com/Nikkkaaws/wsit/internal/pairing"
	"github.com/Nikkkaaws/wsit/internal/socks5"
)

const Version = "0.1.0"

type accountDocument struct {
	ID              string `json:"id"`
	Enabled         bool   `json:"enabled"`
	Provider        string `json:"provider"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	PinIP           string `json:"pin_ip"`
	DirectInterface string `json:"direct_interface"`
	Username        string `json:"username"`
	Password        string `json:"password"`
}

type tuningDocument struct {
	BatchDelayMS       int    `json:"batch_delay_ms"`
	BatchMinKB         int    `json:"batch_min_kb"`
	BatchMaxKB         int    `json:"batch_max_kb"`
	StripeData         *bool  `json:"stripe_data"`
	StreamReadKB       int    `json:"stream_read_kb"`
	StreamWindowKB     int    `json:"stream_window_kb"`
	AckEveryFrames     int    `json:"ack_every_frames"`
	SendQueueFrames    int    `json:"send_queue_frames"`
	ReorderMaxKB       int    `json:"reorder_max_kb"`
	IMAPIdleRefreshSec int    `json:"imap_idle_refresh_sec"`
	IMAPAppendWorkers  int    `json:"imap_append_workers"`
	StatsIntervalSec   int    `json:"stats_interval_sec"`
	OptimisticOpenMS   int    `json:"optimistic_open_ms"`
	PingIntervalMS     int    `json:"ping_interval_ms"`
	PurgeAfterSec      int    `json:"purge_after_sec"`
	PurgeEverySec      int    `json:"purge_every_sec"`
	PurgeOwner         string `json:"purge_owner"`
}

type configDocument struct {
	Mode        string            `json:"mode"`
	Listen      string            `json:"listen"`
	Target      string            `json:"target"`
	DNSResolver string            `json:"dns_resolver"`
	Passphrase  string            `json:"passphrase"`
	ClientID    int               `json:"client_id"`
	FolderSend  string            `json:"folder_send"`
	FolderRecv  string            `json:"folder_recv"`
	LogLevel    string            `json:"log_level"`
	Accounts    []accountDocument `json:"accounts"`
	Tuning      tuningDocument    `json:"tuning"`
}

type statusDocument struct {
	Phase         string   `json:"phase"`
	Stage         string   `json:"stage"`
	Error         string   `json:"error,omitempty"`
	Mode          string   `json:"mode"`
	Listen        string   `json:"listen"`
	ClientID      int      `json:"client_id"`
	StartedAtMS   int64    `json:"started_at_ms,omitempty"`
	TXBytes       uint64   `json:"tx_bytes"`
	RXBytes       uint64   `json:"rx_bytes"`
	ActiveStreams int64    `json:"active_streams"`
	RTTMS         uint64   `json:"rtt_ms"`
	LiveLanes     int      `json:"live_lanes"`
	PendingBytes  int64    `json:"pending_bytes"`
	Appends       uint64   `json:"appends"`
	Logs          []string `json:"logs"`
}

type Controller struct {
	mu        sync.RWMutex
	cfg       *config.Config
	eng       *engine.Engine
	cancel    context.CancelFunc
	done      chan struct{}
	runID     uint64
	phase     string
	stage     string
	lastError string
	startedAt time.Time
	logs      []string
}

func NewController(configJSON string) (*Controller, error) {
	cfg, err := parseConfig(configJSON)
	if err != nil {
		return nil, err
	}
	return newController(cfg), nil
}

func NewServerController(configPath string) (*Controller, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	cfg.Mode = "server"
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return newController(cfg), nil
}

func newController(cfg *config.Config) *Controller {
	return &Controller{
		cfg:   cfg,
		phase: "stopped",
		stage: "Остановлен",
		logs:  make([]string, 0, 200),
	}
}

func DefaultConfig(mode string) string {
	doc := configDocument{
		Mode:        normalizedMode(mode),
		Listen:      "127.0.0.1:1080",
		Target:      "direct",
		DNSResolver: "1.1.1.1:53",
		ClientID:    1,
		FolderSend:  "Notes",
		FolderRecv:  "Journal",
		LogLevel:    "info",
		Accounts:    []accountDocument{},
	}
	encoded, _ := json.Marshal(doc)
	return string(encoded)
}

func ConfigFromYAML(path, mode string) (string, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return "", err
	}
	doc := documentFromConfig(cfg)
	doc.Mode = normalizedMode(mode)
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func ValidateConfig(configJSON string) string {
	if _, err := parseConfig(configJSON); err != nil {
		return err.Error()
	}
	return ""
}

// DecodePairingCode validates a server-generated connection code and returns
// only the fields required to configure a client transport.
func DecodePairingCode(code string) string {
	profile, err := pairing.Decode(code)
	if err != nil {
		return encodeJSON(map[string]any{"ok": false, "detail": err.Error()})
	}
	return encodeJSON(map[string]any{
		"ok": true, "passphrase": profile.Passphrase,
		"folder_send": profile.FolderSend, "folder_recv": profile.FolderRecv,
		"dns_resolver": profile.DNSResolver,
	})
}

func encodeJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func documentFromConfig(cfg *config.Config) configDocument {
	stripe := cfg.StripeData
	doc := configDocument{
		Mode: cfg.Mode, Listen: cfg.Listen, Target: cfg.Target, DNSResolver: cfg.DNSResolver,
		Passphrase: cfg.Passphrase, ClientID: int(cfg.ClientID),
		FolderSend: cfg.IMAP.FolderSend, FolderRecv: cfg.IMAP.FolderRecv, LogLevel: cfg.LogLevel,
		Accounts: make([]accountDocument, 0, len(cfg.AccountList())),
		Tuning: tuningDocument{
			BatchDelayMS: cfg.BatchDelayMS, BatchMinKB: cfg.BatchMinKB, BatchMaxKB: cfg.BatchMaxKB,
			StripeData: &stripe, StreamReadKB: cfg.StreamReadKB, StreamWindowKB: cfg.StreamWindowKB,
			AckEveryFrames: cfg.AckEveryFrames, SendQueueFrames: cfg.SendQueueFrames, ReorderMaxKB: cfg.ReorderMaxKB,
			IMAPIdleRefreshSec: cfg.IMAPIdleRefreshSec, IMAPAppendWorkers: cfg.IMAPAppendWorkers,
			StatsIntervalSec: cfg.StatsIntervalSec, OptimisticOpenMS: cfg.OptimisticOpenMS,
			PingIntervalMS: cfg.PingIntervalMS, PurgeAfterSec: cfg.PurgeAfterSec,
			PurgeEverySec: cfg.PurgeEverySec, PurgeOwner: cfg.PurgeOwner,
		},
	}
	for index, account := range cfg.AccountList() {
		doc.Accounts = append(doc.Accounts, accountDocument{
			ID: strconv.Itoa(index + 1), Enabled: true, Provider: "IMAP",
			Host: account.Host, Port: account.Port, PinIP: account.PinIP,
			DirectInterface: account.DirectInterface, Username: account.Username, Password: account.Password,
		})
	}
	return doc
}

func (c *Controller) Start() error {
	c.mu.Lock()
	if c.phase == "starting" || c.phase == "running" || c.phase == "stopping" {
		c.mu.Unlock()
		return fmt.Errorf("WSIT уже запущен или меняет состояние")
	}
	c.phase = "starting"
	c.stage = "Проверка конфигурации"
	c.lastError = ""
	c.runID++
	runID := c.runID
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.cancel = cancel
	c.done = done
	logger := slog.New(&controllerHandler{controller: c})
	eng, err := engine.New(c.cfg, logger)
	if err != nil {
		c.phase = "error"
		c.stage = "Ошибка запуска"
		c.lastError = err.Error()
		c.cancel = nil
		c.done = nil
		c.mu.Unlock()
		return err
	}
	c.eng = eng
	c.startedAt = time.Now()
	c.addLogLocked("Запуск WSIT")
	c.mu.Unlock()

	go func() {
		err := eng.Run(ctx)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.runID != runID {
			close(done)
			return
		}
		if err != nil && ctx.Err() == nil {
			c.phase = "error"
			c.stage = "Ошибка транспорта"
			c.lastError = err.Error()
			c.addLogLocked("Ошибка: " + err.Error())
		} else {
			c.phase = "stopped"
			c.stage = "Остановлен"
			c.lastError = ""
			c.addLogLocked("WSIT остановлен")
		}
		c.eng = nil
		c.cancel = nil
		c.done = nil
		close(done)
	}()
	return nil
}

func (c *Controller) Stop() error {
	c.mu.Lock()
	if c.phase == "stopped" {
		c.mu.Unlock()
		return nil
	}
	if c.phase == "stopping" {
		done := c.done
		c.mu.Unlock()
		return waitStopped(done)
	}
	c.phase = "stopping"
	c.stage = "Завершение активных потоков"
	c.addLogLocked("Остановка WSIT")
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return waitStopped(done)
}

func (c *Controller) Close() {
	_ = c.Stop()
}

func (c *Controller) ClearLogs() {
	c.mu.Lock()
	c.logs = nil
	c.addLogLocked("Журнал очищен")
	c.mu.Unlock()
}

func (c *Controller) Status() string {
	c.mu.RLock()
	doc := statusDocument{
		Phase:       c.phase,
		Stage:       c.stage,
		Error:       c.lastError,
		Mode:        c.cfg.Mode,
		Listen:      c.cfg.Listen,
		ClientID:    int(c.cfg.ClientID),
		StartedAtMS: c.startedAt.UnixMilli(),
		Logs:        append([]string(nil), c.logs...),
	}
	eng := c.eng
	c.mu.RUnlock()
	if eng != nil {
		stats := eng.Stats()
		doc.TXBytes = stats.TXBytes
		doc.RXBytes = stats.RXBytes
		doc.ActiveStreams = stats.ActiveStreams
		doc.RTTMS = stats.RTTMS
		doc.LiveLanes = stats.LiveLanes
		doc.PendingBytes = stats.PendingBytes
		doc.Appends = stats.Appends
	}
	encoded, _ := json.Marshal(doc)
	return string(encoded)
}

func (c *Controller) onLog(message, line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addLogLocked(line)
	switch message {
	case "lane up":
		if c.phase == "starting" {
			c.stage = "Подключение почтовых аккаунтов"
		}
	case "wsit up":
		if c.cfg.Mode == "server" {
			c.phase = "running"
			c.stage = "Связь с клиентами"
		} else {
			c.stage = "Запуск локального SOCKS5"
		}
	case "socks":
		c.phase = "running"
		c.stage = "Работает"
	}
}

func (c *Controller) addLogLocked(line string) {
	stamp := time.Now().Format("15:04:05")
	c.logs = append(c.logs, stamp+"  "+line)
	if len(c.logs) > 200 {
		copy(c.logs, c.logs[len(c.logs)-200:])
		c.logs = c.logs[:200]
	}
}

type controllerHandler struct {
	controller *Controller
	attrs      []slog.Attr
	group      string
}

func (h *controllerHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *controllerHandler) Handle(_ context.Context, record slog.Record) error {
	parts := []string{record.Message}
	for _, attr := range h.attrs {
		parts = append(parts, attr.Key+"="+attr.Value.String())
	}
	record.Attrs(func(attr slog.Attr) bool {
		parts = append(parts, attr.Key+"="+attr.Value.String())
		return true
	})
	h.controller.onLog(record.Message, strings.Join(parts, " "))
	return nil
}

func (h *controllerHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := *h
	cloned.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &cloned
}

func (h *controllerHandler) WithGroup(name string) slog.Handler {
	cloned := *h
	cloned.group = strings.Trim(strings.Join([]string{h.group, name}, "."), ".")
	return &cloned
}

func parseConfig(configJSON string) (*config.Config, error) {
	var doc configDocument
	if err := json.Unmarshal([]byte(configJSON), &doc); err != nil {
		return nil, fmt.Errorf("конфигурация: %w", err)
	}
	if doc.ClientID < 1 || doc.ClientID > 255 {
		return nil, fmt.Errorf("ID клиента должен быть от 1 до 255")
	}
	cfg := config.Default()
	cfg.Mode = normalizedMode(doc.Mode)
	cfg.Listen = defaultString(doc.Listen, "127.0.0.1:1080")
	cfg.Target = defaultString(doc.Target, "direct")
	cfg.DNSResolver = defaultString(doc.DNSResolver, "1.1.1.1:53")
	cfg.Passphrase = doc.Passphrase
	cfg.ClientID = uint8(doc.ClientID)
	cfg.IMAP.FolderSend = defaultString(doc.FolderSend, "Notes")
	cfg.IMAP.FolderRecv = defaultString(doc.FolderRecv, "Journal")
	cfg.LogLevel = defaultString(doc.LogLevel, "info")
	cfg.IMAP.Username = ""
	cfg.IMAP.Password = ""
	cfg.IMAP.Accounts = make([]config.IMAPAccount, 0, len(doc.Accounts))
	for _, account := range doc.Accounts {
		if !account.Enabled {
			continue
		}
		cfg.IMAP.Accounts = append(cfg.IMAP.Accounts, config.IMAPAccount{
			Host:            defaultString(account.Host, "imap.rambler.ru"),
			Port:            defaultInt(account.Port, 993),
			PinIP:           strings.TrimSpace(account.PinIP),
			DirectInterface: defaultString(account.DirectInterface, "off"),
			Username:        strings.TrimSpace(account.Username),
			Password:        account.Password,
		})
	}
	applyTuning(cfg, doc.Tuning)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyTuning(cfg *config.Config, tuning tuningDocument) {
	if tuning.BatchDelayMS != 0 {
		cfg.BatchDelayMS = tuning.BatchDelayMS
	}
	if tuning.BatchMinKB != 0 {
		cfg.BatchMinKB = tuning.BatchMinKB
	}
	if tuning.BatchMaxKB != 0 {
		cfg.BatchMaxKB = tuning.BatchMaxKB
	}
	if tuning.StripeData != nil {
		cfg.StripeData = *tuning.StripeData
	}
	if tuning.StreamReadKB != 0 {
		cfg.StreamReadKB = tuning.StreamReadKB
	}
	if tuning.StreamWindowKB != 0 {
		cfg.StreamWindowKB = tuning.StreamWindowKB
	}
	if tuning.AckEveryFrames != 0 {
		cfg.AckEveryFrames = tuning.AckEveryFrames
	}
	if tuning.SendQueueFrames != 0 {
		cfg.SendQueueFrames = tuning.SendQueueFrames
	}
	if tuning.ReorderMaxKB != 0 {
		cfg.ReorderMaxKB = tuning.ReorderMaxKB
	}
	if tuning.IMAPIdleRefreshSec != 0 {
		cfg.IMAPIdleRefreshSec = tuning.IMAPIdleRefreshSec
	}
	if tuning.IMAPAppendWorkers != 0 {
		cfg.IMAPAppendWorkers = tuning.IMAPAppendWorkers
	}
	if tuning.StatsIntervalSec != 0 {
		cfg.StatsIntervalSec = tuning.StatsIntervalSec
	}
	if tuning.OptimisticOpenMS != 0 {
		cfg.OptimisticOpenMS = tuning.OptimisticOpenMS
	}
	if tuning.PingIntervalMS != 0 {
		cfg.PingIntervalMS = tuning.PingIntervalMS
	}
	if tuning.PurgeAfterSec != 0 {
		cfg.PurgeAfterSec = tuning.PurgeAfterSec
	}
	if tuning.PurgeEverySec != 0 {
		cfg.PurgeEverySec = tuning.PurgeEverySec
	}
	if tuning.PurgeOwner != "" {
		cfg.PurgeOwner = tuning.PurgeOwner
	}
}

func normalizedMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "server" || mode == "probe" {
		return mode
	}
	return "client"
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func waitStopped(done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("остановка WSIT превысила 15 секунд")
	}
}

type checkAccountResult struct {
	OK        bool   `json:"ok"`
	Detail    string `json:"detail"`
	LatencyMS int64  `json:"latency_ms"`
}

func CheckAccount(accountJSON string) string {
	var account accountDocument
	if err := json.Unmarshal([]byte(accountJSON), &account); err != nil {
		return encodeCheckResult(checkAccountResult{Detail: "Некорректные данные аккаунта: " + err.Error()})
	}
	account.Host = defaultString(account.Host, "imap.rambler.ru")
	account.Port = defaultInt(account.Port, 993)
	if strings.TrimSpace(account.Username) == "" || account.Password == "" {
		return encodeCheckResult(checkAccountResult{Detail: "Нужны адрес почты и пароль"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	selection, err := netroute.Resolve(defaultString(account.DirectInterface, "off"))
	if err != nil {
		return encodeCheckResult(checkAccountResult{Detail: err.Error()})
	}
	started := time.Now()
	dialHost := account.Host
	if account.PinIP != "" {
		dialHost = account.PinIP
	}
	client, err := imapc.Dial(ctx, imapc.DialOpts{
		DialHost: dialHost, SNI: account.Host, Port: account.Port,
		InterfaceIndex: selection.Index, LocalIP: selection.LocalIP,
	})
	if err == nil {
		err = client.Login(ctx, strings.TrimSpace(account.Username), account.Password)
	}
	if err == nil {
		for _, folder := range []string{"Notes", "Journal"} {
			if err = client.Create(ctx, folder); err != nil {
				break
			}
		}
	}
	if client != nil {
		client.Logout(ctx)
		_ = client.Close()
	}
	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		return encodeCheckResult(checkAccountResult{Detail: err.Error(), LatencyMS: elapsed})
	}
	return encodeCheckResult(checkAccountResult{OK: true, Detail: "Аккаунт работает", LatencyMS: elapsed})
}

func encodeCheckResult(result checkAccountResult) string {
	encoded, _ := json.Marshal(result)
	return string(encoded)
}

type speedOptions struct {
	Proxy       string `json:"proxy"`
	DownloadMiB int    `json:"download_mib"`
	UploadMiB   int    `json:"upload_mib"`
	Parallel    int    `json:"parallel"`
	TimeoutSec  int    `json:"timeout_sec"`
}

type speedResult struct {
	OK           bool    `json:"ok"`
	Detail       string  `json:"detail"`
	LatencyMS    int64   `json:"latency_ms"`
	DownloadMbps float64 `json:"download_mbps"`
	UploadMbps   float64 `json:"upload_mbps"`
}

func RunSpeedTest(optionsJSON string) string {
	var options speedOptions
	if err := json.Unmarshal([]byte(optionsJSON), &options); err != nil {
		return encodeSpeedResult(speedResult{Detail: err.Error()})
	}
	options.Proxy = defaultString(options.Proxy, "127.0.0.1:1080")
	options.DownloadMiB = boundedDefault(options.DownloadMiB, 8, 1, 128)
	options.UploadMiB = boundedDefault(options.UploadMiB, 8, 1, 128)
	options.Parallel = boundedDefault(options.Parallel, 4, 1, 8)
	options.TimeoutSec = boundedDefault(options.TimeoutSec, 120, 15, 300)
	if _, _, err := net.SplitHostPort(options.Proxy); err != nil {
		return encodeSpeedResult(speedResult{Detail: "Некорректный SOCKS5: " + err.Error()})
	}
	timeout := time.Duration(options.TimeoutSec) * time.Second
	client := speedHTTPClient(options.Proxy, options.Parallel, timeout)
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	latencyStarted := time.Now()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://speed.cloudflare.com/__down?bytes=1", nil)
	response, err := client.Do(request)
	latency := time.Since(latencyStarted).Milliseconds()
	if err != nil {
		return encodeSpeedResult(speedResult{Detail: "Проверка задержки: " + err.Error()})
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode/100 != 2 {
		return encodeSpeedResult(speedResult{Detail: "Проверка задержки: HTTP " + strconv.Itoa(response.StatusCode)})
	}
	downloadBytes := int64(options.DownloadMiB) * 1024 * 1024
	downloadStarted := time.Now()
	if err := parallelDownload(ctx, client, downloadBytes, options.Parallel); err != nil {
		return encodeSpeedResult(speedResult{LatencyMS: latency, Detail: "Загрузка: " + err.Error()})
	}
	downloadDuration := time.Since(downloadStarted)
	uploadBytes := int64(options.UploadMiB) * 1024 * 1024
	uploadStarted := time.Now()
	if err := upload(ctx, client, uploadBytes); err != nil {
		return encodeSpeedResult(speedResult{LatencyMS: latency, Detail: "Отдача: " + err.Error()})
	}
	uploadDuration := time.Since(uploadStarted)
	return encodeSpeedResult(speedResult{
		OK: true, Detail: "Тест завершён", LatencyMS: latency,
		DownloadMbps: megabitsPerSecond(downloadBytes, downloadDuration),
		UploadMbps:   megabitsPerSecond(uploadBytes, uploadDuration),
	})
}

func speedHTTPClient(proxy string, parallel int, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:               nil,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
		MaxConnsPerHost:     parallel,
		MaxIdleConnsPerHost: parallel,
		DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			host, portText, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			port, err := strconv.ParseUint(portText, 10, 16)
			if err != nil {
				return nil, err
			}
			return socks5.DialThrough(proxy, host, uint16(port), 20*time.Second)
		},
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func parallelDownload(ctx context.Context, client *http.Client, total int64, parallel int) error {
	var firstErr atomic.Pointer[error]
	var group sync.WaitGroup
	base := total / int64(parallel)
	for index := 0; index < parallel; index++ {
		bytesForWorker := base
		if index == parallel-1 {
			bytesForWorker += total - base*int64(parallel)
		}
		group.Add(1)
		go func(size int64) {
			defer group.Done()
			url := "https://speed.cloudflare.com/__down?bytes=" + strconv.FormatInt(size, 10)
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			response, err := client.Do(request)
			if err == nil && response.StatusCode/100 != 2 {
				err = fmt.Errorf("HTTP %d", response.StatusCode)
			}
			if response != nil {
				if err == nil {
					_, err = io.Copy(io.Discard, response.Body)
				}
				_ = response.Body.Close()
			}
			if err != nil {
				errCopy := err
				firstErr.CompareAndSwap(nil, &errCopy)
			}
		}(bytesForWorker)
	}
	group.Wait()
	if ptr := firstErr.Load(); ptr != nil {
		return *ptr
	}
	return nil
}

func upload(ctx context.Context, client *http.Client, size int64) error {
	body := io.NopCloser(io.LimitReader(zeroReader{}, size))
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://speed.cloudflare.com/__up", body)
	request.ContentLength = size
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return nil
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func megabitsPerSecond(bytes int64, duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(bytes) * 8 / duration.Seconds() / 1_000_000
}

func boundedDefault(value, fallback, minimum, maximum int) int {
	if value == 0 {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func encodeSpeedResult(result speedResult) string {
	encoded, _ := json.Marshal(result)
	return string(encoded)
}
