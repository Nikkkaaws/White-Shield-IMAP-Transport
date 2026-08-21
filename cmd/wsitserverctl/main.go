package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Nikkkaaws/wsit/internal/config"
	"github.com/Nikkkaaws/wsit/internal/engine"
	"github.com/Nikkkaaws/wsit/internal/pairing"
	"github.com/Nikkkaaws/wsit/mobile"
)

const (
	esc       = "\x1b["
	reset     = esc + "0m"
	bold      = esc + "1m"
	fgWhite   = esc + "38;5;255m"
	fgMuted   = esc + "38;5;245m"
	fgBlue    = esc + "38;5;75m"
	fgGreen   = esc + "38;5;84m"
	fgYellow  = esc + "38;5;221m"
	fgRed     = esc + "38;5;203m"
	bgBlue    = esc + "48;5;25m"
	clearHome = esc + "2J" + esc + "H"
)

type key int

const (
	keyUnknown key = iota
	keyUp
	keyDown
	keyEnter
	keyEscape
	keyRune
)

type keyEvent struct {
	kind key
	r    rune
}

type screenID int

const (
	screenHome screenID = iota
	screenOverview
	screenAccounts
	screenCheck
	screenSettings
	screenLogs
	screenPair
	screenUninstall
)

type transportStatus struct {
	Phase         string   `json:"phase"`
	Stage         string   `json:"stage"`
	Error         string   `json:"error"`
	Target        string   `json:"target"`
	LiveLanes     int      `json:"live_lanes"`
	TXBytes       uint64   `json:"tx_bytes"`
	RXBytes       uint64   `json:"rx_bytes"`
	ActiveStreams int64    `json:"active_streams"`
	PendingBytes  int64    `json:"pending_bytes"`
	Appends       uint64   `json:"appends"`
	Logs          []string `json:"logs"`
}

type application struct {
	mu             sync.RWMutex
	reader         *bufio.Reader
	fd             int
	raw            *terminalState
	configPath     string
	config         *config.Config
	controller     *mobile.Controller
	screen         screenID
	selection      int
	checking       bool
	checkText      string
	checkOK        bool
	serviceMode    bool
	uninstallError string
}

var menu = []struct {
	label       string
	description string
	screen      screenID
	action      string
}{
	{label: "Запустить сервер", description: "Запустить серверный транспорт", action: "toggle"},
	{label: "Обзор", description: "Трафик, потоки и состояние сервера", screen: screenOverview},
	{label: "Аккаунты", description: "Почтовые линии без отображения паролей", screen: screenAccounts},
	{label: "Проверка", description: "Конфигурация и вход во все IMAP-аккаунты", screen: screenCheck},
	{label: "Код подключения", description: "Подключить ПК или Android без ручного ввода ключа", screen: screenPair},
	{label: "Настройки", description: "Цель, очистка почты и производительность", screen: screenSettings},
	{label: "Журнал", description: "События серверного транспорта", screen: screenLogs},
	{label: "Удалить WSIT", description: "Удалить клиент и сервис, сохранив config.yaml", screen: screenUninstall},
	{label: "Выход", description: "Закрыть менеджер; сервер продолжит работу", action: "quit"},
}

func main() {
	configPath := flag.String("config", serverDefaultConfigPath(), "path to server config")
	snapshot := flag.Bool("snapshot", false, "print a server UI preview and exit")
	pair := flag.Bool("pair", false, "print an Android connection code and exit")
	daemon := flag.Bool("daemon", false, "run the installed server service")
	portable := flag.Bool("portable", false, "run without installation or systemd")
	uninstall := flag.Bool("uninstall", false, "remove the installed server client and service")
	elevated := flag.Bool("elevated", false, "internal elevation marker")
	flag.Parse()
	if *daemon {
		if err := runServerDaemon(*configPath); err != nil {
			fmt.Fprintln(os.Stderr, "WSIT server:", err)
			os.Exit(1)
		}
		return
	}
	if *pair {
		code, err := pairingCode(*configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "WSIT Server Control:", err)
			os.Exit(1)
		}
		fmt.Println(code)
		return
	}
	if *uninstall {
		if err := requestServerUninstall(*elevated); err != nil {
			fmt.Fprintln(os.Stderr, "WSIT uninstall:", err)
			os.Exit(1)
		}
		return
	}
	serviceMode := false
	if !*snapshot && !*portable {
		resolvedConfig, installedMode, childHandled, err := prepareServerInstallation(*configPath, *elevated)
		if err != nil {
			fmt.Fprintln(os.Stderr, "WSIT install:", err)
			os.Exit(1)
		}
		if childHandled {
			return
		}
		*configPath = resolvedConfig
		serviceMode = installedMode
	}
	app := newApplicationMode(*configPath, serviceMode)
	if *snapshot {
		fmt.Print(stripANSI(app.render()))
		return
	}
	prepareConsole()
	resizeConsole(96, 30)
	if err := app.enterTerminal(); err != nil {
		fmt.Fprintln(os.Stderr, "WSIT Server Control:", err)
		os.Exit(1)
	}
	defer app.leaveTerminal()
	app.loop()
}

func pairingCode(configPath string) (string, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", err
	}
	return pairing.Encode(pairing.Profile{
		Passphrase: cfg.Passphrase,
		FolderSend: cfg.IMAP.FolderSend, FolderRecv: cfg.IMAP.FolderRecv,
		DNSResolver: cfg.DNSResolver,
	})
}

func newApplication(configPath string) *application {
	return newApplicationMode(configPath, false)
}

func newApplicationMode(configPath string, serviceMode bool) *application {
	app := &application{
		reader: bufio.NewReaderSize(os.Stdin, 4096), fd: int(os.Stdin.Fd()),
		configPath: configPath, screen: screenHome, serviceMode: serviceMode,
	}
	app.reloadConfig()
	return app
}

func (a *application) enterTerminal() error {
	state, err := makeTerminalRaw(a.fd)
	if err != nil {
		return err
	}
	a.raw = state
	fmt.Print("\x1b[?1049h\x1b[?25l\x1b]0;WSIT Server Control\x07")
	return nil
}

func (a *application) leaveTerminal() {
	if !a.serviceMode && a.controller != nil {
		a.controller.Close()
	}
	fmt.Print(reset + "\x1b[?25h\x1b[?1049l")
	_ = restoreTerminal(a.fd, a.raw)
}

func (a *application) loop() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	events := make(chan keyEvent, 1)
	go func() {
		for {
			event, err := a.readKey()
			if err != nil {
				close(events)
				return
			}
			events <- event
		}
	}()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	fmt.Print(a.render())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Print(a.render())
		case event, ok := <-events:
			if !ok || a.handle(event) {
				return
			}
			fmt.Print(a.render())
		}
	}
}

func (a *application) handle(event keyEvent) bool {
	if event.kind == keyRune && (event.r == 'q' || event.r == 'Q') {
		return true
	}
	if a.screen != screenHome {
		if a.screen == screenUninstall && event.kind == keyEnter {
			if err := uninstallServerInstallation(); err != nil {
				a.uninstallError = err.Error()
				return false
			}
			return true
		}
		if event.kind == keyEscape {
			a.screen = screenHome
		}
		if a.screen == screenLogs && event.kind == keyRune && (event.r == 'c' || event.r == 'C') && a.controller != nil {
			a.controller.ClearLogs()
		}
		return false
	}
	switch event.kind {
	case keyUp:
		a.selection = wrap(a.selection-1, len(menu))
	case keyDown:
		a.selection = wrap(a.selection+1, len(menu))
	case keyEnter:
		item := menu[a.selection]
		switch item.action {
		case "toggle":
			a.toggle()
		case "quit":
			return true
		default:
			a.screen = item.screen
		}
	}
	return false
}

func (a *application) toggle() {
	status := a.status()
	if a.serviceMode {
		a.mu.Lock()
		a.checkText = "Выполняется…"
		a.mu.Unlock()
		go func() {
			action := "start"
			if status.Phase == "running" || status.Phase == "starting" {
				action = "stop"
			}
			err := serverServiceAction(action)
			a.mu.Lock()
			defer a.mu.Unlock()
			if err != nil {
				a.checkText, a.checkOK = err.Error(), false
			} else {
				a.checkText, a.checkOK = "Команда сервиса выполнена", true
			}
		}()
		return
	}
	if status.Phase == "running" || status.Phase == "starting" {
		controller := a.controller
		if controller != nil {
			go func() { _ = controller.Stop() }()
		}
		return
	}
	a.reloadConfig()
	controller, err := mobile.NewServerController(a.configPath)
	if err != nil {
		a.mu.Lock()
		a.checkText, a.checkOK = err.Error(), false
		a.mu.Unlock()
		return
	}
	a.controller = controller
	if err := controller.Start(); err != nil {
		a.mu.Lock()
		a.checkText, a.checkOK = err.Error(), false
		a.mu.Unlock()
	}
}

func (a *application) reloadConfig() {
	cfg, err := config.Load(a.configPath)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.config = config.Default()
		a.config.Mode = "server"
		a.checkText = "Конфигурация не загружена: " + err.Error()
		a.checkOK = false
		return
	}
	cfg.Mode = "server"
	if err := cfg.Validate(); err != nil {
		a.config = cfg
		a.checkText = "Ошибка конфигурации: " + err.Error()
		a.checkOK = false
		return
	}
	a.config = cfg
	a.checkText = "Конфигурация готова"
	a.checkOK = true
}

func (a *application) runProbe() {
	a.mu.Lock()
	if a.checking {
		a.mu.Unlock()
		return
	}
	a.checking = true
	a.checkText = "Проверка IMAP-аккаунтов…"
	a.mu.Unlock()
	go func() {
		cfg, err := config.Load(a.configPath)
		if err == nil {
			cfg.Mode = "probe"
			err = cfg.Validate()
		}
		if err == nil {
			probe, createErr := engine.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if createErr != nil {
				err = createErr
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				err = probe.Probe(ctx)
				cancel()
			}
		}
		a.mu.Lock()
		a.checking = false
		a.checkOK = err == nil
		if err == nil {
			a.checkText = "Все доступные IMAP-линии проверены"
		} else {
			a.checkText = err.Error()
		}
		a.mu.Unlock()
	}()
}

func (a *application) status() transportStatus {
	if a.serviceMode {
		return serverServiceStatus()
	}
	if a.controller == nil {
		return transportStatus{Phase: "stopped", Stage: "Остановлен"}
	}
	var status transportStatus
	if err := json.Unmarshal([]byte(a.controller.Status()), &status); err != nil {
		status.Phase, status.Stage, status.Error = "error", "Ошибка состояния", err.Error()
	}
	return status
}

func (a *application) render() string {
	canvas := newCanvas(96)
	switch a.screen {
	case screenOverview:
		a.renderOverview(canvas)
	case screenAccounts:
		a.renderAccounts(canvas)
	case screenCheck:
		a.renderCheck(canvas)
	case screenSettings:
		a.renderSettings(canvas)
	case screenLogs:
		a.renderLogs(canvas)
	case screenPair:
		a.renderPair(canvas)
	case screenUninstall:
		a.renderUninstall(canvas)
	default:
		a.renderHome(canvas)
	}
	return clearHome + canvas.String()
}

func (a *application) renderHome(c *canvas) {
	status := a.status()
	c.top("WSIT SERVER CONTROL", "VPS")
	c.blank()
	color := phaseColor(status.Phase)
	a.mu.RLock()
	cfg := a.config
	a.mu.RUnlock()
	target := "direct"
	if cfg != nil {
		target = cfg.Target
	}
	c.row(fmt.Sprintf("  %s● %-34s%s   ЦЕЛЬ  %-20s   ЛИНИИ  %d", color, status.Stage, reset, target, status.LiveLanes))
	c.blank()
	c.rule()
	c.row(fgMuted + "  УПРАВЛЕНИЕ" + reset)
	c.blank()
	for index, item := range menu {
		label := item.label
		description := item.description
		if item.action == "toggle" {
			if status.Phase == "running" || status.Phase == "starting" {
				label = "Остановить сервер"
				description = "Корректно завершить потоки и почтовые линии"
			}
		}
		line := "  › " + pad(label, 22) + "  " + description
		if index == a.selection {
			c.selected(line)
		} else {
			c.row("  " + pad(label, 24) + fgMuted + description + reset)
		}
	}
	c.blank()
	c.footer("↑↓ Выбор", "Enter Открыть", "R Обновить", "Q Выход")
}

func (a *application) renderOverview(c *canvas) {
	status := a.status()
	c.top("ОБЗОР СЕРВЕРА", "VPS")
	c.blank()
	c.row(fmt.Sprintf("  %-28s %s%s%s", "Состояние", phaseColor(status.Phase), status.Stage, reset))
	c.row(fmt.Sprintf("  %-28s %d", "Почтовых линий", status.LiveLanes))
	c.row(fmt.Sprintf("  %-28s %d", "Активных потоков", status.ActiveStreams))
	c.row(fmt.Sprintf("  %-28s %s", "Принято", formatBytes(status.RXBytes)))
	c.row(fmt.Sprintf("  %-28s %s", "Отправлено", formatBytes(status.TXBytes)))
	c.row(fmt.Sprintf("  %-28s %s", "Очередь", formatBytes(uint64(max(status.PendingBytes, 0)))))
	c.row(fmt.Sprintf("  %-28s %d", "IMAP APPEND", status.Appends))
	if status.Error != "" {
		c.blank()
		c.row("  " + fgRed + status.Error + reset)
	}
	c.blank()
	c.footer("Esc Назад", "Q Выход")
}

func (a *application) renderAccounts(c *canvas) {
	c.top("АККАУНТЫ СЕРВЕРА", "БЕЗ ПАРОЛЕЙ")
	a.mu.RLock()
	cfg := a.config
	a.mu.RUnlock()
	c.blank()
	if cfg == nil || len(cfg.AccountList()) == 0 {
		c.row("  " + fgRed + "Аккаунты не загружены" + reset)
	} else {
		for index, account := range cfg.AccountList() {
			c.row(fmt.Sprintf("  %s●%s  %02d  %-31s  %s:%d", fgGreen, reset, index+1, redactEmail(account.Username), account.Host, account.Port))
		}
	}
	c.blank()
	c.footer("Esc Назад", "Q Выход")
}

func (a *application) renderCheck(c *canvas) {
	c.top("ПРОВЕРКА СЕРВЕРА", "IMAP")
	a.mu.RLock()
	checking, text, ok := a.checking, a.checkText, a.checkOK
	a.mu.RUnlock()
	c.blank()
	color := fgRed
	if ok {
		color = fgGreen
	}
	if checking {
		color = fgYellow
	}
	c.row("  " + color + "● " + text + reset)
	c.blank()
	c.row("  Нажмите Enter, чтобы проверить конфигурацию, вход и рабочие папки всех аккаунтов.")
	c.blank()
	c.footer("Enter Проверить", "Esc Назад", "Q Выход")
}

func (a *application) renderSettings(c *canvas) {
	c.top("НАСТРОЙКИ СЕРВЕРА", "READ ONLY")
	a.mu.RLock()
	cfg := a.config
	a.mu.RUnlock()
	c.blank()
	if cfg != nil {
		c.row(fmt.Sprintf("  %-28s %s", "Файл", a.configPath))
		c.row(fmt.Sprintf("  %-28s %s", "Цель", cfg.Target))
		c.row(fmt.Sprintf("  %-28s %s", "DNS-резолвер", cfg.DNSResolver))
		c.row(fmt.Sprintf("  %-28s %d", "Почтовых линий", len(cfg.AccountList())))
		c.row(fmt.Sprintf("  %-28s %d с", "Очистка старше", cfg.PurgeAfterSec))
		c.row(fmt.Sprintf("  %-28s %s", "Владелец очистки", cfg.PurgeOwner))
		c.row(fmt.Sprintf("  %-28s %d", "APPEND-воркеров", cfg.IMAPAppendWorkers))
		c.row(fmt.Sprintf("  %-28s %d КиБ", "Окно потока", cfg.StreamWindowKB))
	}
	c.blank()
	c.footer("R Перечитать файл", "Esc Назад", "Q Выход")
}

func (a *application) renderLogs(c *canvas) {
	c.top("ЖУРНАЛ СЕРВЕРА", "БЕЗ СЕКРЕТОВ")
	logs := a.status().Logs
	if a.serviceMode {
		logs = serverServiceLogs(18)
	}
	c.blank()
	start := max(0, len(logs)-18)
	for _, line := range logs[start:] {
		c.row("  " + fgMuted + line + reset)
	}
	if len(logs) == 0 {
		c.row("  " + fgMuted + "Журнал пуст" + reset)
	}
	c.blank()
	if a.serviceMode {
		c.footer("Esc Назад", "Q Выход")
	} else {
		c.footer("C Очистить", "Esc Назад", "Q Выход")
	}
}

func (a *application) renderUninstall(c *canvas) {
	c.top("УДАЛЕНИЕ WSIT", "VPS")
	c.blank()
	c.row("  Будут остановлены и удалены сервис, команда wsit и этот клиент.")
	c.row("  " + fgMuted + "Файл /etc/wsit/config.yaml останется на сервере." + reset)
	if a.uninstallError != "" {
		c.blank()
		c.row("  " + fgRed + a.uninstallError + reset)
	}
	c.blank()
	c.row("  " + fgRed + bold + "Нажмите Enter для удаления" + reset)
	c.blank()
	c.footer("Enter Удалить", "Esc Отмена", "Q Выход")
}

func (a *application) renderPair(c *canvas) {
	c.top("КОД ПОДКЛЮЧЕНИЯ", "ПК И ANDROID")
	c.blank()
	code, err := pairingCode(a.configPath)
	if err != nil {
		c.row("  " + fgRed + err.Error() + reset)
	} else {
		c.row("  Скопируйте этот код в клиент WSIT:")
		c.blank()
		for _, line := range chunkRunes(code, 82) {
			c.row("  " + fgWhite + line + reset)
		}
	}
	c.blank()
	c.footer("Esc Назад", "Q Выход")
}

func chunkRunes(value string, width int) []string {
	if width < 1 {
		return nil
	}
	runes := []rune(value)
	lines := make([]string, 0, (len(runes)+width-1)/width)
	for len(runes) > 0 {
		count := min(width, len(runes))
		lines = append(lines, string(runes[:count]))
		runes = runes[count:]
	}
	return lines
}

func (a *application) readKey() (keyEvent, error) {
	b, err := a.reader.ReadByte()
	if err != nil {
		return keyEvent{}, err
	}
	switch b {
	case '\r', '\n':
		if a.screen == screenCheck {
			a.runProbe()
		}
		if a.screen == screenSettings {
			a.reloadConfig()
		}
		return keyEvent{kind: keyEnter}, nil
	case 0, 224:
		next, err := a.reader.ReadByte()
		if err != nil {
			return keyEvent{}, err
		}
		if next == 72 {
			return keyEvent{kind: keyUp}, nil
		}
		if next == 80 {
			return keyEvent{kind: keyDown}, nil
		}
	case 27:
		if a.reader.Buffered() == 0 {
			return keyEvent{kind: keyEscape}, nil
		}
		next, _ := a.reader.ReadByte()
		if next != '[' {
			return keyEvent{kind: keyEscape}, nil
		}
		code, err := a.reader.ReadByte()
		if err != nil {
			return keyEvent{}, err
		}
		if code == 'A' {
			return keyEvent{kind: keyUp}, nil
		}
		if code == 'B' {
			return keyEvent{kind: keyDown}, nil
		}
	}
	if b < utf8.RuneSelf {
		return keyEvent{kind: keyRune, r: rune(b)}, nil
	}
	buffer := []byte{b}
	for len(buffer) < utf8.UTFMax && !utf8.FullRune(buffer) {
		next, readErr := a.reader.ReadByte()
		if readErr != nil {
			return keyEvent{}, readErr
		}
		buffer = append(buffer, next)
	}
	r, _ := utf8.DecodeRune(buffer)
	return keyEvent{kind: keyRune, r: r}, nil
}

type canvas struct {
	b     strings.Builder
	inner int
}

func newCanvas(width int) *canvas { return &canvas{inner: max(width, 60) - 2} }
func (c *canvas) String() string  { return c.b.String() }

func (c *canvas) top(title, badge string) {
	label := "─ " + bold + fgWhite + title + reset + " "
	if badge != "" {
		space := max(1, c.inner-visibleLen(label)-len([]rune(badge))-2)
		label += strings.Repeat("─", space) + " " + fgBlue + badge + reset + " "
	}
	c.b.WriteString(fgBlue + "╭" + reset + label + strings.Repeat("─", max(0, c.inner-visibleLen(label))) + fgBlue + "╮" + reset + "\r\n")
}

func (c *canvas) row(content string) {
	c.b.WriteString(fgBlue + "│" + reset + pad(truncateANSI(content, c.inner-1), c.inner) + fgBlue + "│" + reset + "\r\n")
}

func (c *canvas) selected(content string) {
	c.b.WriteString(fgBlue + "│" + reset + bgBlue + fgWhite + bold + pad(truncateANSI(content, c.inner-1), c.inner) + reset + fgBlue + "│" + reset + "\r\n")
}

func (c *canvas) blank() { c.row("") }
func (c *canvas) rule() {
	c.b.WriteString(fgBlue + "├" + strings.Repeat("─", c.inner) + "┤" + reset + "\r\n")
}
func (c *canvas) footer(items ...string) {
	c.rule()
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fgWhite+item+reset)
	}
	c.row("  " + strings.Join(parts, fgMuted+"  ·  "+reset))
	c.b.WriteString(fgBlue + "╰" + strings.Repeat("─", c.inner) + "╯" + reset + "\r\n")
}

func phaseColor(phase string) string {
	switch phase {
	case "running":
		return fgGreen
	case "starting", "stopping":
		return fgYellow
	default:
		return fgRed
	}
}

func formatBytes(value uint64) string {
	switch {
	case value >= 1_000_000_000:
		return fmt.Sprintf("%.2f ГБ", float64(value)/1_000_000_000)
	case value >= 1_000_000:
		return fmt.Sprintf("%.2f МБ", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1f КБ", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d Б", value)
	}
}

func redactEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "***"
	}
	return string([]rune(parts[0])[0]) + "***@" + parts[1]
}

func wrap(value, count int) int {
	for value < 0 {
		value += count
	}
	return value % count
}

func pad(value string, width int) string {
	return value + strings.Repeat(" ", max(0, width-visibleLen(value)))
}

func visibleLen(value string) int {
	return len([]rune(stripANSI(value)))
}

func truncateANSI(value string, width int) string {
	if visibleLen(value) <= width {
		return value
	}
	var result strings.Builder
	visible := 0
	for index := 0; index < len(value) && visible < width; {
		if value[index] == 0x1b {
			end := index + 1
			for end < len(value) && value[end] != 'm' {
				end++
			}
			if end < len(value) {
				end++
			}
			result.WriteString(value[index:end])
			index = end
			continue
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		result.WriteRune(r)
		index += size
		visible++
	}
	return result.String() + reset
}

func stripANSI(value string) string {
	var result strings.Builder
	for index := 0; index < len(value); {
		if value[index] == 0x1b {
			index++
			if index < len(value) && value[index] == '[' {
				index++
				for index < len(value) && (value[index] < '@' || value[index] > '~') {
					index++
				}
				if index < len(value) {
					index++
				}
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		result.WriteRune(r)
		index += size
	}
	return result.String()
}
