package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	coreconfig "github.com/Nikkkaaws/wsit/internal/config"
	"github.com/Nikkkaaws/wsit/internal/pairing"
	coremobile "github.com/Nikkkaaws/wsit/mobile"
)

const (
	esc       = "\x1b["
	reset     = esc + "0m"
	bold      = esc + "1m"
	dim       = esc + "2m"
	fgWhite   = esc + "38;5;255m"
	fgMuted   = esc + "38;5;245m"
	fgBlue    = esc + "38;5;75m"
	fgGreen   = esc + "38;5;84m"
	fgYellow  = esc + "38;5;221m"
	fgRed     = esc + "38;5;203m"
	bgBlue    = esc + "48;5;25m"
	bgPanel   = esc + "48;5;235m"
	clearHome = esc + "2J" + esc + "H"
)

type screenID int

const (
	screenHome screenID = iota
	screenOverview
	screenAccounts
	screenDoctor
	screenSpeedSettings
	screenSettings
	screenLogs
)

type key int

const (
	keyUnknown key = iota
	keyUp
	keyDown
	keyEnter
	keyEscape
	keyDelete
	keyBackspace
	keySpace
	keyRune
)

type keyEvent struct {
	Kind key
	Rune rune
}

type account struct {
	Email      string
	Provider   string
	Host       string
	Port       uint16
	PinIP      string
	Direct     string
	Enabled    bool
	Health     string
	Latency    time.Duration
	LastCheck  time.Time
	Password   []byte
	LastDetail string
}

type speedResult struct {
	Latency  time.Duration
	Download float64
	Upload   float64
	At       time.Time
}

type doctorState struct {
	ProxyOK      bool
	ProxyDetail  string
	CheckedAt    time.Time
	Speed        speedResult
	SpeedDetail  string
	SpeedRunning bool
}

type application struct {
	reader *bufio.Reader
	fd     int
	raw    *terminalState
	width  int
	height int

	screen            screenID
	homeSelection     int
	accountSelection  int
	doctorSelection   int
	speedSelection    int
	settingsSelection int
	running           bool
	transitioning     bool
	transitionStart   bool
	transitionStage   string
	transitionIndex   int
	transitionTotal   int
	clientID          int
	proxy             string
	passphrase        string
	folderSend        string
	folderRecv        string
	dnsResolver       string
	lanes             int
	autostart         bool
	speedMode         string
	speedDirection    string
	speedStreams      int
	speedTimeout      int
	transitionDelay   time.Duration
	accounts          []account
	doctor            doctorState
	logs              []string
	quitRequested     bool
	realRuntime       bool
	controller        *coremobile.Controller
}

type menuItem struct {
	Label       string
	Description string
	Screen      screenID
	Action      string
}

var homeMenu = []menuItem{
	{Label: "Остановить WSIT", Description: "Остановить транспорт, не меняя внешние приложения", Action: "toggle"},
	{Label: "ID клиента", Description: "Номер этого устройства для подключения · от 1 до 255", Action: "client_id"},
	{Label: "Обзор", Description: "Состояние транспорта и локальная точка подключения", Screen: screenOverview},
	{Label: "Аккаунты", Description: "Добавление, проверка и управление почтовыми линиями", Screen: screenAccounts},
	{Label: "Проверка", Description: "Диагностика соединения и реальный тест скорости", Screen: screenDoctor},
	{Label: "Настройки", Description: "Сеть, производительность и параметры запуска", Screen: screenSettings},
	{Label: "Журнал", Description: "События приложения без паролей и содержимого трафика", Screen: screenLogs},
}

var accountSetupOptions = []string{"Другой IMAP-сервер", "Rambler"}

var startStages = []string{
	"Проверка конфигурации",
	"Подключение почтовых аккаунтов",
	"Запуск почтовых линий",
	"Запуск локального SOCKS5",
	"Проверка связи с сервером",
}

var stopStages = []string{
	"Остановка новых подключений",
	"Завершение активных потоков",
	"Закрытие почтовых линий",
	"Остановка локального SOCKS5",
}

func main() {
	snapshot := flag.Bool("snapshot", false, "print the main screen and exit")
	portable := flag.Bool("portable", false, "run without installing the client")
	uninstall := flag.Bool("uninstall", false, "remove the installed client")
	importConfig := flag.String("import-config", "", "import a local WSIT YAML config before opening the client")
	flag.Parse()
	if *uninstall {
		if err := scheduleClientUninstall(os.Getpid()); err != nil {
			fmt.Fprintln(os.Stderr, "WSIT uninstall:", err)
			os.Exit(1)
		}
		return
	}
	if !*snapshot && !*portable {
		if err := ensureClientInstallation(); err != nil {
			fmt.Fprintln(os.Stderr, "WSIT install:", err)
		}
	}
	prepareConsole()

	a := newApplication()
	if !*snapshot {
		if err := a.loadPersistedState(); err != nil {
			a.log("Не удалось загрузить настройки: " + err.Error())
		}
		if *importConfig != "" {
			if err := a.importConfigFile(*importConfig); err != nil {
				fmt.Fprintln(os.Stderr, "WSIT config import:", err)
				os.Exit(1)
			}
		}
		a.realRuntime = true
		a.running = false
	}
	if *snapshot || !isTerminal(a.fd) {
		fmt.Print(stripANSI(a.render()))
		return
	}
	resizeConsole(96, 30)
	if err := a.enterTerminal(); err != nil {
		fmt.Fprintln(os.Stderr, "WSIT CLI:", err)
		os.Exit(1)
	}
	defer a.leaveTerminal()
	a.loop()
}

func newApplication() *application {
	a := &application{
		reader:          bufio.NewReaderSize(os.Stdin, 4096),
		fd:              int(os.Stdin.Fd()),
		width:           96,
		height:          30,
		screen:          screenHome,
		running:         false,
		clientID:        1,
		proxy:           "127.0.0.1:1080",
		folderSend:      "Notes",
		folderRecv:      "Journal",
		dnsResolver:     "1.1.1.1:53",
		lanes:           0,
		speedMode:       "Стандартный",
		speedDirection:  "Загрузка и отдача",
		speedStreams:    0,
		speedTimeout:    120,
		transitionDelay: 320 * time.Millisecond,
		logs:            make([]string, 0, 100),
	}
	a.log("Интерфейс управления запущен")
	return a
}

func (a *application) enterTerminal() error {
	state, err := makeTerminalRaw(a.fd)
	if err != nil {
		return fmt.Errorf("terminal raw mode: %w", err)
	}
	a.raw = state
	if w, h, err := terminalSize(a.fd); err == nil {
		a.width, a.height = w, h
	}
	fmt.Print("\x1b[?1049h\x1b[?25l\x1b]0;WSIT Control\x07")
	return nil
}

func (a *application) leaveTerminal() {
	if a.controller != nil {
		a.controller.Close()
		a.controller = nil
	}
	for i := range a.accounts {
		zeroBytes(a.accounts[i].Password)
	}
	fmt.Print(reset + "\x1b[?25h\x1b[?1049l")
	if a.raw != nil {
		_ = restoreTerminal(a.fd, a.raw)
	}
}

func (a *application) loop() {
	for {
		fmt.Print(a.render())
		ev, err := a.readKey()
		if err != nil {
			return
		}
		if a.handleGlobal(ev) {
			return
		}
		switch a.screen {
		case screenOverview:
			a.handleOverview(ev)
		case screenHome:
			a.handleHome(ev)
		case screenAccounts:
			a.handleAccounts(ev)
		case screenDoctor:
			a.handleDoctor(ev)
		case screenSpeedSettings:
			a.handleSpeedSettings(ev)
		case screenSettings:
			a.handleSettings(ev)
		case screenLogs:
			a.handleLogs(ev)
		}
		if a.quitRequested {
			return
		}
	}
}

func (a *application) handleOverview(ev keyEvent) {
	if ev.Kind == keyEscape {
		a.screen = screenHome
	}
}

func (a *application) handleGlobal(ev keyEvent) bool {
	if ev.Kind != keyRune {
		return false
	}
	switch lowerRune(ev.Rune) {
	case 'q':
		return true
	case 'r':
		return false
	}
	return false
}

func (a *application) handleHome(ev keyEvent) {
	switch ev.Kind {
	case keyUp:
		a.homeSelection = wrap(a.homeSelection-1, len(homeMenu))
	case keyDown:
		a.homeSelection = wrap(a.homeSelection+1, len(homeMenu))
	case keyEnter:
		item := homeMenu[a.homeSelection]
		switch item.Action {
		case "toggle":
			a.toggleTransport()
			return
		case "client_id":
			a.editClientID()
			return
		}
		if item.Screen != screenHome {
			a.screen = item.Screen
		}
	}
}

func (a *application) handleAccounts(ev keyEvent) {
	if ev.Kind == keyEscape {
		a.screen = screenHome
		return
	}
	if len(a.accounts) > 0 {
		switch ev.Kind {
		case keyUp:
			a.accountSelection = wrap(a.accountSelection-1, len(a.accounts))
		case keyDown:
			a.accountSelection = wrap(a.accountSelection+1, len(a.accounts))
		case keySpace:
			a.toggleSelectedAccount()
		case keyDelete:
			a.removeSelectedAccount()
		case keyEnter:
			a.showAccountDetails()
		}
	}
	if ev.Kind == keyRune {
		switch lowerRune(ev.Rune) {
		case 'a':
			a.addAccountWizard()
		case 't':
			a.testSelectedAccount()
		}
	}
}

func (a *application) handleDoctor(ev keyEvent) {
	if ev.Kind == keyEscape {
		a.screen = screenHome
		return
	}
	switch ev.Kind {
	case keyUp:
		a.doctorSelection = wrap(a.doctorSelection-1, 4)
	case keyDown:
		a.doctorSelection = wrap(a.doctorSelection+1, 4)
	case keyEnter:
		switch a.doctorSelection {
		case 0:
			a.runSystemCheck()
		case 1:
			a.runAccountChecks()
		case 2:
			a.screen = screenSpeedSettings
		case 3:
			a.runSpeedTest()
		}
	}
}

func (a *application) handleSpeedSettings(ev keyEvent) {
	if ev.Kind == keyEscape {
		a.screen = screenDoctor
		return
	}
	switch ev.Kind {
	case keyUp:
		a.speedSelection = wrap(a.speedSelection-1, 4)
	case keyDown:
		a.speedSelection = wrap(a.speedSelection+1, 4)
	case keyEnter:
		a.editSpeedSetting()
	}
}

func (a *application) handleSettings(ev keyEvent) {
	if ev.Kind == keyEscape {
		a.screen = screenHome
		return
	}
	switch ev.Kind {
	case keyUp:
		a.settingsSelection = wrap(a.settingsSelection-1, len(a.settingsItems()))
	case keyDown:
		a.settingsSelection = wrap(a.settingsSelection+1, len(a.settingsItems()))
	case keyEnter:
		a.editSetting()
	}
}

func (a *application) handleLogs(ev keyEvent) {
	if ev.Kind == keyEscape {
		a.screen = screenHome
		return
	}
	if ev.Kind == keyRune && lowerRune(ev.Rune) == 'c' {
		a.logs = nil
		a.log("Журнал очищен")
	}
}

func (a *application) render() string {
	if w, h, err := terminalSize(a.fd); err == nil {
		a.width, a.height = w, h
	}
	w := a.width
	if w < 72 {
		w = 72
	}
	if w > 118 {
		w = 118
	}
	c := newCanvas(w)
	switch a.screen {
	case screenOverview:
		a.renderOverview(c)
	case screenAccounts:
		a.renderAccounts(c)
	case screenDoctor:
		a.renderDoctor(c)
	case screenSpeedSettings:
		a.renderSpeedSettings(c)
	case screenSettings:
		a.renderSettings(c)
	case screenLogs:
		a.renderLogs(c)
	default:
		a.renderHome(c)
	}
	return clearHome + c.String()
}

func (a *application) renderHome(c *canvas) {
	c.top("WSIT CONTROL", "")
	c.blank()
	healthy, enabled := a.accountCounts()
	if a.transitioning {
		stateColor := transitionStatusColor(a.transitionStart, a.transitionIndex, a.transitionTotal)
		status := fmt.Sprintf("  %s● %-34s%s   SOCKS5  %-21s   ЛИНИИ  %d/%d", stateColor, a.transitionStage, reset, a.proxy, healthy, enabled)
		c.row(status)
		c.blank()
	} else {
		state, stateColor := "РАБОТАЕТ", fgGreen
		if !a.running {
			state, stateColor = "ОСТАНОВЛЕН", fgRed
		}
		status := fmt.Sprintf("  %s● %-34s%s   SOCKS5  %-21s   ЛИНИИ  %d/%d", stateColor, state, reset, a.proxy, healthy, enabled)
		c.row(status)
		c.blank()
	}
	c.rule()
	c.row(fgMuted + "  НАВИГАЦИЯ" + reset)
	c.blank()
	for i, item := range homeMenu {
		label := item.Label
		if item.Action == "toggle" {
			if a.transitioning && a.transitionStart {
				label = "Запуск WSIT…"
			} else if a.transitioning {
				label = "Остановка WSIT…"
			} else if a.running {
				label = "Остановить WSIT"
			} else {
				label = "Включить WSIT"
			}
		} else if item.Action == "client_id" {
			label = fmt.Sprintf("ID клиента: %d", a.clientID)
		}
		left := fmt.Sprintf("  %-20s", label)
		right := item.Description
		if item.Action == "toggle" && !a.running {
			right = "Запустить транспорт и открыть локальную точку подключения"
		}
		if i == a.homeSelection {
			c.selected("  › " + pad(label, 18) + "  " + right)
		} else {
			c.row(left + "    " + fgMuted + right + reset)
		}
	}
	c.blank()
	c.rule()
	c.row("  " + fgMuted + "ПОДКЛЮЧЕНИЕ" + reset)
	c.row("  Укажите в любом внешнем приложении SOCKS5 " + bold + a.proxy + reset)
	c.blank()
	c.footer("↑↓ Выбор", "Enter Открыть", "R Обновить", "Q Выход")
}

func (a *application) renderOverview(c *canvas) {
	c.top("ОБЗОР", "")
	c.blank()
	state := boolState(a.running, "работает", "остановлен")
	healthy, enabled := a.accountCounts()
	c.row("  " + fgMuted + "ТРАНСПОРТ" + reset)
	c.row(fmt.Sprintf("  %-28s %s", "Состояние", state))
	c.row(fmt.Sprintf("  %-28s %d", "ID клиента", a.clientID))
	c.row(fmt.Sprintf("  %-28s %s", "Локальный SOCKS5", a.proxy))
	c.blank()
	c.rule()
	c.row("  " + fgMuted + "ПОЧТОВЫЕ ЛИНИИ" + reset)
	c.row(fmt.Sprintf("  %-28s %d", "Добавлено аккаунтов", len(a.accounts)))
	c.row(fmt.Sprintf("  %-28s %d/%d", "Работают", healthy, enabled))
	c.row(fmt.Sprintf("  %-28s %d", "Параллельных линий", a.lanes))
	c.blank()
	c.rule()
	c.row("  " + fgMuted + "ПОДКЛЮЧЕНИЕ" + reset)
	c.row("  Укажите в нужном приложении SOCKS5 " + bold + a.proxy + reset)
	c.blank()
	c.footer("R Обновить", "Esc Назад", "Q Выход")
}

func (a *application) renderAccounts(c *canvas) {
	c.top("АККАУНТЫ", "IMAP-ЛИНИИ")
	c.row("  Добавьте любой IMAP вручную или выберите Rambler.")
	c.blank()
	if len(a.accounts) == 0 {
		c.rule()
		c.blank()
		c.row(fgMuted + "  Аккаунтов пока нет." + reset)
		c.row("  Нажмите " + bold + "A" + reset + ", введите адрес и скрытый пароль — проверка запустится сразу.")
		c.blank()
		c.rule()
	} else {
		c.row(fgMuted + fmt.Sprintf("  %-3s %-30s %-24s %-18s", "", "АДРЕС", "ПРОВАЙДЕР", "СОСТОЯНИЕ") + reset)
		c.rule()
		for i, acc := range a.accounts {
			state := accountState(acc)
			line := fmt.Sprintf("  %-3s %-30s %-24s %-18s", enabledMark(acc.Enabled), truncate(acc.Email, 29), truncate(acc.Provider, 23), truncate(state, 17))
			if i == a.accountSelection {
				c.selected("  › " + strings.TrimLeft(line, " "))
			} else {
				c.row(line)
			}
		}
		c.rule()
		c.row(fgMuted + "  Выбранный аккаунт проверяется без APPEND и без изменения почтового ящика." + reset)
	}
	c.blank()
	c.footer("A Добавить", "T Проверить", "Space Вкл/выкл", "Del Удалить", "Esc Назад")
}

func (a *application) renderDoctor(c *canvas) {
	c.top("ПРОВЕРКА", "СИСТЕМА И СКОРОСТЬ")
	c.blank()
	healthyAccounts, enabledAccounts := a.accountCounts()
	routeState := fgMuted + "не проверялась" + reset
	if !a.doctor.CheckedAt.IsZero() {
		if a.doctor.ProxyOK {
			routeState = fgGreen + "OK · " + a.doctor.ProxyDetail + reset
		} else {
			routeState = fgRed + "ошибка · " + a.doctor.ProxyDetail + reset
		}
	}
	c.row(fmt.Sprintf("  WSIT                          %s", boolState(a.running, "работает", "остановлен")))
	c.row("  Связь с сервером             " + routeState)
	c.row(fmt.Sprintf("  Почтовые аккаунты             %d/%d работают", healthyAccounts, enabledAccounts))
	if !a.doctor.CheckedAt.IsZero() {
		c.row("  Последняя проверка           " + a.doctor.CheckedAt.Format("15:04:05"))
	}
	c.blank()
	c.rule()
	c.row(fgMuted + "  СКОРОСТЬ ИНТЕРНЕТА" + reset)
	if a.doctor.Speed.At.IsZero() {
		c.row("  Задержка —        Загрузка —        Отдача —")
	} else {
		download := "—"
		upload := "—"
		if a.doctor.Speed.Download > 0 {
			download = fmt.Sprintf("%.1f Мбит/с", a.doctor.Speed.Download)
		}
		if a.doctor.Speed.Upload > 0 {
			upload = fmt.Sprintf("%.1f Мбит/с", a.doctor.Speed.Upload)
		}
		c.row(fmt.Sprintf("  Задержка %s%-8s%s  Загрузка %s%-14s%s  Отдача %s%-14s%s",
			bold, a.doctor.Speed.Latency.Round(time.Millisecond), reset,
			fgGreen, download, reset, fgBlue, upload, reset))
		c.row(fgMuted + "  Последнее измерение " + a.doctor.Speed.At.Format("15:04:05") + reset)
	}
	if a.doctor.SpeedDetail != "" {
		c.row("  " + fgMuted + truncate(a.doctor.SpeedDetail, c.inner-4) + reset)
	} else {
		c.row("  " + fgMuted + a.speedSettingsSummary() + reset)
	}
	c.blank()
	actions := []string{"Проверить соединение WSIT", "Проверить почтовые аккаунты", "Настройки теста скорости", "Запустить тест скорости"}
	for i, action := range actions {
		if i == a.doctorSelection {
			c.selected("  › " + action)
		} else {
			c.row("    " + action)
		}
	}
	c.blank()
	c.footer("↑↓ Выбор", "Enter Открыть", "Esc Назад")
}

func (a *application) renderSpeedSettings(c *canvas) {
	c.top("НАСТРОЙКИ ТЕСТА СКОРОСТИ", "")
	c.row(fgMuted + "  Все параметры меняются через Enter." + reset)
	c.blank()
	items := []settingItem{
		{Label: "Профиль проверки", Value: a.speedMode, Description: "Быстрый, стандартный или точный замер"},
		{Label: "Проверять", Value: a.speedDirection, Description: "Оба направления или только одно"},
		{Label: "Параллельные потоки", Value: speedStreamsLabel(a.speedStreams), Description: "Автоматический выбор либо фиксированное число"},
		{Label: "Лимит времени", Value: fmt.Sprintf("%d секунд", a.speedTimeout), Description: "Максимальное время полного теста"},
	}
	for i, item := range items {
		line := fmt.Sprintf("    %-29s %s", item.Label, item.Value)
		if i == a.speedSelection {
			c.selected("  › " + strings.TrimLeft(line, " "))
			c.row("      " + fgMuted + item.Description + reset)
		} else {
			c.row(line)
		}
		c.blank()
	}
	c.footer("↑↓ Выбор", "Enter Изменить", "Esc Назад")
}

func (a *application) renderSettings(c *canvas) {
	c.top("НАСТРОЙКИ", "СГРУППИРОВАНО ПО ЗАДАЧАМ")
	c.row(fgMuted + "  Выберите пункт стрелками и нажмите Enter." + reset)
	c.blank()
	items := a.settingsItems()
	lastGroup := ""
	for i, item := range items {
		if item.Group != lastGroup {
			if lastGroup != "" {
				c.blank()
			}
			c.row("  " + fgBlue + strings.ToUpper(item.Group) + reset)
			lastGroup = item.Group
		}
		line := fmt.Sprintf("    %-27s %s", item.Label, item.Value)
		if i == a.settingsSelection {
			c.selected("  › " + strings.TrimLeft(line, " "))
			c.row("      " + fgMuted + item.Description + reset)
		} else {
			c.row(line)
		}
	}
	c.blank()
	c.footer("↑↓ Выбор", "Enter Настроить", "Esc Назад")
}

func (a *application) renderLogs(c *canvas) {
	c.top("ЖУРНАЛ", "БЕЗ СЕКРЕТОВ")
	c.row(fgMuted + "  Пароли, содержимое трафика и почтовых сообщений сюда не записываются." + reset)
	c.blank()
	start := 0
	maxRows := 15
	if len(a.logs) > maxRows {
		start = len(a.logs) - maxRows
	}
	if len(a.logs) == 0 {
		c.row("  Журнал пуст")
	} else {
		for _, line := range a.logs[start:] {
			c.row("  " + fgMuted + truncate(line, c.inner-4) + reset)
		}
	}
	c.blank()
	c.footer("C Очистить", "R Обновить", "Esc Назад", "Q Выход")
}

func (a *application) toggleTransport() {
	if a.realRuntime {
		a.toggleRealTransport()
		return
	}
	if a.running {
		a.runTransportTransition(false)
		a.log("Транспорт остановлен")
	} else {
		a.runTransportTransition(true)
		a.log("Транспорт включён")
	}
}

type clientRuntimeStatus struct {
	Phase        string `json:"phase"`
	Stage        string `json:"stage"`
	Error        string `json:"error"`
	LiveLanes    int    `json:"live_lanes"`
	RTTMS        int64  `json:"rtt_ms"`
	PendingBytes int64  `json:"pending_bytes"`
}

func (a *application) toggleRealTransport() {
	if a.running || a.controller != nil {
		a.stopRealTransport()
		return
	}
	if a.passphrase == "" {
		a.notice("Нужен код подключения", "Импортируйте код из меню WSIT на VPS в настройках.", true)
		return
	}
	if _, enabled := a.accountCounts(); enabled == 0 {
		a.notice("Нет аккаунтов", "Добавьте и включите хотя бы один IMAP-аккаунт.", true)
		return
	}
	configJSON, err := a.coreConfigJSON()
	if err != nil {
		a.notice("Ошибка конфигурации", err.Error(), true)
		return
	}
	controller, err := coremobile.NewController(configJSON)
	if err != nil {
		a.notice("Ошибка конфигурации", err.Error(), true)
		return
	}
	a.controller = controller
	if err := controller.Start(); err != nil {
		controller.Close()
		a.controller = nil
		a.notice("Не удалось запустить WSIT", err.Error(), true)
		return
	}
	a.transitioning, a.transitionStart = true, true
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		status := decodeClientStatus(controller.Status())
		a.transitionStage = status.Stage
		a.lanes = status.LiveLanes
		fmt.Print(a.render())
		switch status.Phase {
		case "running":
			a.running, a.transitioning = true, false
			a.doctor.ProxyDetail = durationLabel(status.RTTMS)
			a.log("Транспорт включён")
			return
		case "error":
			controller.Close()
			a.controller = nil
			a.transitioning = false
			a.notice("Не удалось запустить WSIT", status.Error, true)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	controller.Close()
	a.controller = nil
	a.transitioning = false
	a.notice("Не удалось запустить WSIT", "Запуск превысил 45 секунд", true)
}

func (a *application) stopRealTransport() {
	controller := a.controller
	if controller == nil {
		a.running = false
		return
	}
	a.transitioning, a.transitionStart, a.transitionStage = true, false, "Остановка новых подключений"
	done := make(chan error, 1)
	go func() { done <- controller.Stop() }()
	for {
		select {
		case err := <-done:
			controller.Close()
			a.controller = nil
			a.running, a.transitioning, a.lanes = false, false, 0
			if err != nil {
				a.notice("Ошибка остановки WSIT", err.Error(), true)
			} else {
				a.log("Транспорт остановлен")
			}
			return
		case <-time.After(100 * time.Millisecond):
			status := decodeClientStatus(controller.Status())
			a.transitionStage = status.Stage
			fmt.Print(a.render())
		}
	}
}

func (a *application) coreConfigJSON() (string, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(coremobile.DefaultConfig("client")), &doc); err != nil {
		return "", err
	}
	doc["listen"] = a.proxy
	doc["passphrase"] = a.passphrase
	doc["client_id"] = a.clientID
	doc["folder_send"] = a.folderSend
	doc["folder_recv"] = a.folderRecv
	doc["dns_resolver"] = a.dnsResolver
	accounts := make([]map[string]any, 0, len(a.accounts))
	for _, account := range a.accounts {
		if !account.Enabled {
			continue
		}
		accounts = append(accounts, map[string]any{
			"enabled": true, "provider": account.Provider,
			"host": account.Host, "port": account.Port,
			"pin_ip": account.PinIP, "direct_interface": defaultString(account.Direct, "auto"), "username": account.Email,
			"password": string(account.Password),
		})
	}
	doc["accounts"] = accounts
	raw, err := json.Marshal(doc)
	return string(raw), err
}

func decodeClientStatus(raw string) clientRuntimeStatus {
	var status clientRuntimeStatus
	_ = json.Unmarshal([]byte(raw), &status)
	return status
}

func durationLabel(milliseconds int64) string {
	if milliseconds <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d мс", milliseconds)
}

func (a *application) runTransportTransition(start bool) {
	stages := stopStages
	if start {
		stages = startStages
	}
	a.screen = screenHome
	a.transitioning = true
	a.transitionStart = start
	a.transitionTotal = len(stages)
	for index, stage := range stages {
		a.transitionStage = stage
		a.transitionIndex = index + 1
		fmt.Print(a.render())
		time.Sleep(a.transitionDelay)
	}
	a.running = start
	a.transitioning = false
	a.transitionStage = ""
	a.transitionIndex = 0
	a.transitionTotal = 0
	fmt.Print(a.render())
	a.reader.Reset(os.Stdin)
	flushTerminalInput(a.fd)
}

func transitionStatusColor(start bool, index, total int) string {
	red := [3]int{255, 95, 95}
	green := [3]int{95, 215, 135}
	from, to := red, green
	if !start {
		from, to = green, red
	}
	progress := 1.0
	if total > 1 {
		progress = float64(index-1) / float64(total-1)
	}
	r := from[0] + int(float64(to[0]-from[0])*progress)
	g := from[1] + int(float64(to[1]-from[1])*progress)
	b := from[2] + int(float64(to[2]-from[2])*progress)
	return fmt.Sprintf("%s38;2;%d;%d;%dm", esc, r, g, b)
}

func (a *application) editClientID() {
	value, ok := a.textDialog("ID КЛИЕНТА", "Номер устройства", "Допустимые значения: 1–255", false, strconv.Itoa(a.clientID))
	if !ok {
		return
	}
	clientID, err := parseClientID(value)
	if err != nil {
		a.notice("Некорректный ID клиента", err.Error(), true)
		return
	}
	a.clientID = clientID
	a.persist()
	a.log(fmt.Sprintf("ID клиента изменён на %d", clientID))
}

func parseClientID(value string) (int, error) {
	clientID, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || clientID < 1 || clientID > 255 {
		return 0, fmt.Errorf("Введите целое число от 1 до 255")
	}
	return clientID, nil
}

func (a *application) addAccountWizard() {
	providerIndex, ok := a.selectDialog("ДОБАВЛЕНИЕ АККАУНТА", "Выберите способ настройки", accountSetupOptions)
	if !ok {
		return
	}
	provider := "Другой IMAP"
	host := "imap.rambler.ru"
	port := uint16(993)
	pinIP := ""
	if providerIndex == 0 {
		host, ok = a.textDialog("НАСТРОЙКА IMAP", "Адрес сервера", "imap.example.com", false, "")
		host = strings.TrimSpace(host)
		if !ok {
			return
		}
		portText, portOK := a.textDialog("НАСТРОЙКА IMAP", "Порт", "обычно 993", false, "993")
		if !portOK {
			return
		}
		parsed, err := strconv.ParseUint(strings.TrimSpace(portText), 10, 16)
		if err != nil || parsed == 0 {
			a.notice("Не удалось добавить", "Порт должен быть числом от 1 до 65535", true)
			return
		}
		port = uint16(parsed)
	} else {
		provider = "Rambler"
		pinIP = "81.19.77.168"
	}
	if net.ParseIP(host) == nil && !validHostname(host) {
		a.notice("Не удалось добавить", "Некорректный адрес IMAP-сервера", true)
		return
	}
	email, ok := a.textDialog("ДОБАВЛЕНИЕ АККАУНТА", "Почтовый адрес", "name@example.com", false, "")
	email = strings.TrimSpace(email)
	if !ok {
		return
	}
	if !validEmail(email) {
		a.notice("Не удалось добавить", "Введите полный адрес вида name@example.com", true)
		return
	}
	password, ok := a.textDialog("ДОБАВЛЕНИЕ АККАУНТА", "Пароль или пароль приложения", "ввод скрыт", true, "")
	if !ok {
		return
	}
	if password == "" {
		a.notice("Не удалось добавить", "Пароль не может быть пустым", true)
		return
	}
	acc := account{
		Email: email, Provider: provider, Host: host, Port: port,
		PinIP: pinIP, Direct: "auto",
		Enabled: true, Health: "Проверяется", Password: []byte(password),
	}
	password = ""
	a.progress("ПРОВЕРКА АККАУНТА", "TLS → LOGIN → рабочие папки", host+":"+strconv.Itoa(int(port)))
	a.checkAccountNow(&acc)
	a.accounts = append(a.accounts, acc)
	a.accountSelection = len(a.accounts) - 1
	a.syncLanes()
	a.persist()
	a.log("Аккаунт добавлен: " + redactEmail(acc.Email))
}

func (a *application) testSelectedAccount() {
	if len(a.accounts) == 0 {
		a.notice("НЕТ АККАУНТОВ", "Сначала добавьте аккаунт клавишей A", true)
		return
	}
	acc := &a.accounts[a.accountSelection]
	a.progress("ПРОВЕРКА АККАУНТА", "TLS → LOGIN → рабочие папки", acc.Host+":"+strconv.Itoa(int(acc.Port)))
	a.checkAccountNow(acc)
	a.persist()
	a.log("Аккаунт проверен: " + redactEmail(acc.Email))
}

func (a *application) toggleSelectedAccount() {
	acc := &a.accounts[a.accountSelection]
	acc.Enabled = !acc.Enabled
	if acc.Enabled {
		a.log("Аккаунт включён: " + redactEmail(acc.Email))
	} else {
		a.log("Аккаунт отключён: " + redactEmail(acc.Email))
	}
	a.syncLanes()
	a.persist()
}

func (a *application) removeSelectedAccount() {
	if len(a.accounts) == 0 {
		return
	}
	acc := a.accounts[a.accountSelection]
	choice, ok := a.selectDialog("УДАЛЕНИЕ АККАУНТА", "Удалить "+acc.Email+"?", []string{"Удалить", "Отмена"})
	if !ok || choice != 0 {
		return
	}
	zeroBytes(a.accounts[a.accountSelection].Password)
	a.accounts = append(a.accounts[:a.accountSelection], a.accounts[a.accountSelection+1:]...)
	if a.accountSelection >= len(a.accounts) && a.accountSelection > 0 {
		a.accountSelection--
	}
	a.syncLanes()
	a.persist()
	a.log("Аккаунт удалён: " + redactEmail(acc.Email))
}

func (a *application) showAccountDetails() {
	if len(a.accounts) == 0 {
		return
	}
	acc := a.accounts[a.accountSelection]
	lines := []string{
		"Адрес:      " + acc.Email,
		"Провайдер:  " + acc.Provider,
		"IMAP:       " + net.JoinHostPort(acc.Host, strconv.Itoa(int(acc.Port))),
		"Состояние:  " + accountState(acc),
		"Проверка:   " + timeOrNever(acc.LastCheck),
		"Пароль:     скрыт, хранится в Windows DPAPI",
	}
	a.notice("АККАУНТ", strings.Join(lines, "\n"), false)
}

func (a *application) runSystemCheck() {
	a.progress("ПРОВЕРКА СИСТЕМЫ", "WSIT → сервер → почтовые линии", a.proxy)
	status := clientRuntimeStatus{}
	if a.controller != nil {
		status = decodeClientStatus(a.controller.Status())
	}
	a.doctor.ProxyOK = status.Phase == "running" && status.LiveLanes > 0
	a.doctor.ProxyDetail = durationLabel(status.RTTMS)
	if !a.doctor.ProxyOK {
		a.doctor.ProxyDetail = "транспорт не подключён"
	}
	a.doctor.CheckedAt = time.Now()
	a.log("Проверка системы завершена")
}

func (a *application) runAccountChecks() {
	if len(a.accounts) == 0 {
		a.notice("НЕТ АККАУНТОВ", "Добавьте хотя бы один почтовый аккаунт.", true)
		return
	}
	a.progress("ПРОВЕРКА АККАУНТОВ", "TLS → LOGIN → рабочие папки", fmt.Sprintf("Аккаунтов: %d", len(a.accounts)))
	for i := range a.accounts {
		if !a.accounts[i].Enabled {
			continue
		}
		a.checkAccountNow(&a.accounts[i])
	}
	a.doctor.CheckedAt = time.Now()
	a.persist()
	a.log("Проверка аккаунтов завершена")
}

func (a *application) runSpeedTest() {
	if !a.running || a.controller == nil {
		a.notice("WSIT остановлен", "Сначала включите транспорт.", true)
		return
	}
	a.doctor.SpeedRunning = true
	defer func() { a.doctor.SpeedRunning = false }()
	a.progress("ТЕСТ СКОРОСТИ", a.speedDirection, a.speedSettingsSummary())
	size := 8
	if a.speedMode == "Быстрый" {
		size = 4
	} else if a.speedMode == "Точный" {
		size = 32
	}
	streams := a.speedStreams
	if streams == 0 {
		streams = 4
	}
	options, _ := json.Marshal(map[string]any{
		"proxy": a.proxy, "download_mib": size, "upload_mib": size,
		"parallel": streams, "timeout_sec": a.speedTimeout,
	})
	var result struct {
		OK           bool    `json:"ok"`
		Detail       string  `json:"detail"`
		LatencyMS    int64   `json:"latency_ms"`
		DownloadMbps float64 `json:"download_mbps"`
		UploadMbps   float64 `json:"upload_mbps"`
	}
	_ = json.Unmarshal([]byte(coremobile.RunSpeedTest(string(options))), &result)
	if !result.OK {
		a.doctor.SpeedDetail = result.Detail
		a.notice("Тест скорости не завершён", result.Detail, true)
		return
	}
	download, upload := result.DownloadMbps, result.UploadMbps
	if a.speedDirection == "Только загрузка" {
		upload = 0
	}
	if a.speedDirection == "Только отдача" {
		download = 0
	}
	a.doctor.Speed = speedResult{Latency: time.Duration(result.LatencyMS) * time.Millisecond, Download: download, Upload: upload, At: time.Now()}
	a.doctor.SpeedDetail = a.speedSettingsSummary()
	a.log("Тест скорости завершён")
}

func (a *application) checkAccountNow(account *account) {
	document, _ := json.Marshal(map[string]any{
		"enabled": true, "provider": account.Provider,
		"host": account.Host, "port": account.Port,
		"pin_ip": account.PinIP, "direct_interface": defaultString(account.Direct, "auto"), "username": account.Email,
		"password": string(account.Password),
	})
	var result struct {
		OK        bool   `json:"ok"`
		Detail    string `json:"detail"`
		LatencyMS int64  `json:"latency_ms"`
	}
	_ = json.Unmarshal([]byte(coremobile.CheckAccount(string(document))), &result)
	account.LastCheck = time.Now()
	account.Latency = time.Duration(result.LatencyMS) * time.Millisecond
	account.LastDetail = result.Detail
	if result.OK {
		account.Health = "Работает"
	} else {
		account.Health = "Ошибка"
	}
}

func (a *application) importConfigFile(path string) error {
	cfg, err := coreconfig.Load(path)
	if err != nil {
		return err
	}
	accounts := cfg.AccountList()
	if len(accounts) == 0 {
		return fmt.Errorf("в конфигурации нет IMAP-аккаунтов")
	}
	fmt.Printf("[1/%d] Импорт кода подключения и сетевых настроек\r\n", len(accounts)+2)
	time.Sleep(250 * time.Millisecond)
	for index := range a.accounts {
		zeroBytes(a.accounts[index].Password)
	}
	a.proxy = cfg.Listen
	a.passphrase = cfg.Passphrase
	a.clientID = int(cfg.ClientID)
	a.folderSend = cfg.IMAP.FolderSend
	a.folderRecv = cfg.IMAP.FolderRecv
	a.dnsResolver = cfg.DNSResolver
	a.accounts = make([]account, 0, len(accounts))
	for index, source := range accounts {
		provider := "Другой IMAP"
		if strings.EqualFold(source.Host, "imap.rambler.ru") {
			provider = "Rambler"
		}
		fmt.Printf("[%d/%d] Проверка аккаунта %s\r\n", index+2, len(accounts)+2, redactEmail(source.Username))
		item := account{
			Email: source.Username, Provider: provider, Host: source.Host, Port: uint16(source.Port),
			PinIP: source.PinIP, Direct: defaultString(source.DirectInterface, "auto"),
			Enabled: true, Health: "Проверяется", Password: []byte(source.Password),
		}
		a.checkAccountNow(&item)
		a.accounts = append(a.accounts, item)
	}
	a.syncLanes()
	a.persist()
	fmt.Printf("[%d/%d] Конфигурация импортирована · линий %d\r\n", len(accounts)+2, len(accounts)+2, a.lanes)
	time.Sleep(500 * time.Millisecond)
	return nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (a *application) syncLanes() {
	_, enabled := a.accountCounts()
	a.lanes = enabled
}

type settingItem struct {
	Group       string
	Label       string
	Value       string
	Description string
}

func (a *application) settingsItems() []settingItem {
	return []settingItem{
		{"Сеть", "Локальный SOCKS5", a.proxy, "Адрес, который указывается во внешнем клиенте"},
		{"Сеть", "Код подключения", connectedLabel(a.passphrase), "Импортируется из меню WSIT на VPS"},
		{Group: "Аккаунты", Label: "Почтовых линий", Value: fmt.Sprintf("%d · автоматически", a.lanes), Description: "Равно числу включённых аккаунтов"},
		{Group: "Запуск", Label: "Автозапуск", Value: onOff(a.autostart), Description: "Запускать WSIT при входе в систему"},
		{Group: "Приложение", Label: "Удалить WSIT", Value: "", Description: "Удалить клиент, ярлык и настройки автозапуска"},
	}
}

func (a *application) editSetting() {
	switch a.settingsSelection {
	case 1:
		a.importConnectionCode()
		return
	case 2:
		a.notice("ПОЧТОВЫЕ ЛИНИИ", "Количество меняется автоматически при включении, отключении и удалении аккаунтов.", false)
		return
	case 3:
		initial := 1
		if a.autostart {
			initial = 0
		}
		choice, ok := a.selectDialogAt("АВТОЗАПУСК", "Запускать WSIT при входе в систему", []string{"Включён", "Выключен"}, initial)
		if ok {
			a.autostart = choice == 0
			if err := configureClientAutostart(a.autostart); err != nil {
				a.notice("Не удалось изменить автозапуск", err.Error(), true)
				return
			}
			a.log("Настройка автозапуска обновлена")
			a.persist()
		}
		return
	case 4:
		choice, ok := a.selectDialogAt("УДАЛЕНИЕ WSIT", "Клиент, ярлык и автозапуск будут удалены.", []string{"Отмена", "Удалить WSIT"}, 0)
		if !ok || choice == 0 {
			return
		}
		if err := scheduleClientUninstall(os.Getpid()); err != nil {
			a.notice("Не удалось удалить WSIT", err.Error(), true)
			return
		}
		a.notice("WSIT будет удалён", "Удаление завершится сразу после закрытия этого окна.", false)
		a.quitRequested = true
		return
	}
	value, ok := a.textDialog("НАСТРОЙКА SOCKS5", "Локальный адрес", "", false, a.proxy)
	if !ok {
		return
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil || port == "" {
		a.notice("Некорректный адрес", "Используйте формат 127.0.0.1:1080", true)
		return
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		a.notice("Некорректный адрес", "SOCKS5 должен слушать локальный loopback-адрес", true)
		return
	}
	if p, err := strconv.ParseUint(port, 10, 16); err != nil || p == 0 {
		a.notice("Некорректный адрес", "Порт должен быть числом от 1 до 65535", true)
		return
	}
	a.proxy = value
	a.persist()
	a.log("Настройка SOCKS5 обновлена")
}

func (a *application) importConnectionCode() {
	value, ok := a.textDialog("КОД ПОДКЛЮЧЕНИЯ", "Код из меню WSIT на VPS", "Начинается с WSIT1.", false, "")
	if !ok {
		return
	}
	profile, err := pairing.Decode(value)
	if err != nil {
		a.notice("Некорректный код подключения", err.Error(), true)
		return
	}
	a.passphrase = profile.Passphrase
	if profile.FolderSend != "" {
		a.folderSend = profile.FolderSend
	}
	if profile.FolderRecv != "" {
		a.folderRecv = profile.FolderRecv
	}
	if profile.DNSResolver != "" {
		a.dnsResolver = profile.DNSResolver
	}
	a.log("Код подключения импортирован")
	a.persist()
	a.notice("Код подключения", "Настройки сервера импортированы.", false)
}

func connectedLabel(passphrase string) string {
	if passphrase == "" {
		return "Не импортирован"
	}
	return "Импортирован"
}

func (a *application) editSpeedSetting() {
	switch a.speedSelection {
	case 0:
		options := []string{"Быстрый", "Стандартный", "Точный"}
		choice, ok := a.selectDialogAt("ПРОФИЛЬ ПРОВЕРКИ", "Выберите точность и длительность", options, stringIndex(options, a.speedMode))
		if ok {
			a.speedMode = options[choice]
			a.persist()
		}
	case 1:
		options := []string{"Загрузка и отдача", "Только загрузка", "Только отдача"}
		choice, ok := a.selectDialogAt("НАПРАВЛЕНИЕ", "Какие скорости измерять", options, stringIndex(options, a.speedDirection))
		if ok {
			a.speedDirection = options[choice]
			a.persist()
		}
	case 2:
		options := []string{"Автоматически", "1 поток", "2 потока", "4 потока", "8 потоков"}
		values := []int{0, 1, 2, 4, 8}
		choice, ok := a.selectDialogAt("ПАРАЛЛЕЛЬНЫЕ ПОТОКИ", "Количество соединений для замера", options, intIndex(values, a.speedStreams))
		if ok {
			a.speedStreams = values[choice]
			a.persist()
		}
	case 3:
		options := []string{"60 секунд", "120 секунд", "180 секунд"}
		values := []int{60, 120, 180}
		choice, ok := a.selectDialogAt("ЛИМИТ ВРЕМЕНИ", "Максимальная длительность теста", options, intIndex(values, a.speedTimeout))
		if ok {
			a.speedTimeout = values[choice]
			a.persist()
		}
	}
}

func (a *application) speedSettingsSummary() string {
	return fmt.Sprintf("%s · %s · %s · до %d с", a.speedMode, a.speedDirection, speedStreamsLabel(a.speedStreams), a.speedTimeout)
}

func speedStreamsLabel(streams int) string {
	if streams == 0 {
		return "потоки автоматически"
	}
	if streams == 1 {
		return "1 поток"
	}
	if streams < 5 {
		return fmt.Sprintf("%d потока", streams)
	}
	return fmt.Sprintf("%d потоков", streams)
}

func (a *application) selectDialog(title, subtitle string, options []string) (int, bool) {
	return a.selectDialogAt(title, subtitle, options, 0)
}

func (a *application) selectDialogAt(title, subtitle string, options []string, initial int) (int, bool) {
	selected := wrap(initial, len(options))
	for {
		c := newCanvas(a.dialogWidth())
		c.top(title, "")
		for _, line := range strings.Split(subtitle, "\n") {
			c.row("  " + truncate(line, c.inner-4))
		}
		c.blank()
		for i, option := range options {
			if i == selected {
				c.selected("  › " + option)
			} else {
				c.row("    " + option)
			}
		}
		c.blank()
		c.footer("↑↓ Выбор", "Enter Продолжить", "Esc Отмена")
		fmt.Print(clearHome + c.String())
		ev, err := a.readKey()
		if err != nil {
			return 0, false
		}
		switch ev.Kind {
		case keyUp:
			selected = wrap(selected-1, len(options))
		case keyDown:
			selected = wrap(selected+1, len(options))
		case keyEnter:
			return selected, true
		case keyEscape:
			return 0, false
		}
	}
}

func stringIndex(values []string, current string) int {
	for i, value := range values {
		if value == current {
			return i
		}
	}
	return 0
}

func intIndex(values []int, current int) int {
	for i, value := range values {
		if value == current {
			return i
		}
	}
	return 0
}

func (a *application) textDialog(title, label, hint string, secret bool, initial string) (string, bool) {
	value := []rune(initial)
	for {
		shown := string(value)
		if secret {
			shown = strings.Repeat("•", len(value))
		}
		c := newCanvas(a.dialogWidth())
		c.top(title, "")
		c.blank()
		c.row("  " + label)
		c.row("  " + bgPanel + fgWhite + " " + pad(shown, c.inner-7) + " " + reset)
		if hint != "" {
			c.row("  " + fgMuted + hint + reset)
		}
		if secret {
			c.row("  " + fgMuted + "Символы скрыты; значение не попадает в журнал." + reset)
		}
		c.blank()
		c.footer("Enter Готово", "Backspace Стереть", "Ctrl+U Очистить", "Esc Отмена")
		fmt.Print(clearHome + c.String())
		ev, err := a.readKey()
		if err != nil {
			return "", false
		}
		switch ev.Kind {
		case keyEnter:
			return string(value), true
		case keyEscape:
			return "", false
		case keyBackspace, keyDelete:
			if len(value) > 0 {
				value = value[:len(value)-1]
			}
		case keySpace:
			if len(value) < 255 {
				value = append(value, ' ')
			}
		case keyRune:
			if ev.Rune == 0x15 {
				value = value[:0]
			} else if ev.Rune >= 0x20 && len(value) < 255 {
				value = append(value, ev.Rune)
			}
		}
	}
}

func (a *application) notice(title, body string, isError bool) {
	for {
		c := newCanvas(a.dialogWidth())
		c.top(title, "")
		c.blank()
		color := fgWhite
		if isError {
			color = fgRed
		}
		for _, line := range strings.Split(body, "\n") {
			c.row("  " + color + truncate(line, c.inner-4) + reset)
		}
		c.blank()
		c.footer("Enter Закрыть", "Esc Закрыть")
		fmt.Print(clearHome + c.String())
		ev, err := a.readKey()
		if err != nil || ev.Kind == keyEnter || ev.Kind == keyEscape {
			return
		}
	}
}

func (a *application) progress(title, stage, detail string) {
	c := newCanvas(a.dialogWidth())
	c.top(title, "")
	c.blank()
	c.row("  " + fgBlue + "●" + reset + "  " + bold + stage + reset)
	c.row("     " + fgMuted + detail + reset)
	c.blank()
	c.row("  Выполняется…")
	c.blank()
	c.footer("Пожалуйста, подождите")
	fmt.Print(clearHome + c.String())
}

func (a *application) dialogWidth() int {
	w := a.width
	if w < 72 {
		return 72
	}
	if w > 96 {
		return 96
	}
	return w
}

func (a *application) readKey() (keyEvent, error) {
	b, err := a.reader.ReadByte()
	if err != nil {
		return keyEvent{}, err
	}
	switch b {
	case '\r', '\n':
		return keyEvent{Kind: keyEnter}, nil
	case 8, 127:
		return keyEvent{Kind: keyBackspace}, nil
	case ' ':
		return keyEvent{Kind: keySpace}, nil
	case 0, 224:
		next, err := a.reader.ReadByte()
		if err != nil {
			return keyEvent{}, err
		}
		switch next {
		case 72:
			return keyEvent{Kind: keyUp}, nil
		case 80:
			return keyEvent{Kind: keyDown}, nil
		case 75, 77:
			return keyEvent{Kind: keyUnknown}, nil
		case 83:
			return keyEvent{Kind: keyDelete}, nil
		}
	case 27:
		if a.reader.Buffered() == 0 {
			return keyEvent{Kind: keyEscape}, nil
		}
		next, err := a.reader.ReadByte()
		if err != nil || next != '[' {
			return keyEvent{Kind: keyEscape}, nil
		}
		code, err := a.reader.ReadByte()
		if err != nil {
			return keyEvent{}, err
		}
		switch code {
		case 'A':
			return keyEvent{Kind: keyUp}, nil
		case 'B':
			return keyEvent{Kind: keyDown}, nil
		case 'C', 'D':
			return keyEvent{Kind: keyUnknown}, nil
		case '3':
			if a.reader.Buffered() > 0 {
				_, _ = a.reader.ReadByte()
			}
			return keyEvent{Kind: keyDelete}, nil
		}
		return keyEvent{Kind: keyUnknown}, nil
	}
	if b < utf8.RuneSelf {
		return keyEvent{Kind: keyRune, Rune: rune(b)}, nil
	}
	buf := []byte{b}
	for len(buf) < utf8.UTFMax && !utf8.FullRune(buf) {
		next, readErr := a.reader.ReadByte()
		if readErr != nil {
			return keyEvent{}, readErr
		}
		buf = append(buf, next)
	}
	r, _ := utf8.DecodeRune(buf)
	return keyEvent{Kind: keyRune, Rune: r}, nil
}

type canvas struct {
	b     strings.Builder
	width int
	inner int
}

func newCanvas(width int) *canvas {
	if width < 60 {
		width = 60
	}
	return &canvas{width: width, inner: width - 2}
}

func (c *canvas) String() string { return c.b.String() }

func (c *canvas) top(title, badge string) {
	label := "─ " + bold + fgWhite + title + reset + " "
	if badge != "" {
		space := c.inner - visibleLen(label) - len([]rune(badge)) - 2
		if space < 1 {
			space = 1
		}
		label += strings.Repeat("─", space) + " " + fgBlue + badge + reset + " "
	}
	remaining := c.inner - visibleLen(label)
	if remaining < 0 {
		remaining = 0
	}
	c.b.WriteString(fgBlue + "╭" + reset + label + strings.Repeat("─", remaining) + fgBlue + "╮" + reset + "\r\n")
}

func (c *canvas) row(content string) {
	c.b.WriteString(fgBlue + "│" + reset + c.fit(content) + fgBlue + "│" + reset + "\r\n")
}

func (c *canvas) selected(content string) {
	c.b.WriteString(fgBlue + "│" + reset + bgBlue + fgWhite + bold + c.fit(content) + reset + fgBlue + "│" + reset + "\r\n")
}

func (c *canvas) fit(content string) string {
	if visibleLen(content) > c.inner-1 {
		content = truncateANSI(content, c.inner-1)
	}
	return pad(content, c.inner)
}

func (c *canvas) blank() { c.row("") }

func (c *canvas) rule() {
	c.b.WriteString(fgBlue + "├" + strings.Repeat("─", c.inner) + "┤" + reset + "\r\n")
}

func (c *canvas) footer(items ...string) {
	c.rule()
	var parts []string
	for _, item := range items {
		parts = append(parts, fgWhite+item+reset)
	}
	c.row("  " + strings.Join(parts, fgMuted+"  ·  "+reset))
	c.close()
}

func (c *canvas) close() {
	c.b.WriteString(fgBlue + "╰" + strings.Repeat("─", c.inner) + "╯" + reset + "\r\n")
}

func (a *application) accountCounts() (healthy, enabled int) {
	for _, acc := range a.accounts {
		if !acc.Enabled {
			continue
		}
		enabled++
		if acc.Health == "Работает" {
			healthy++
		}
	}
	return healthy, enabled
}

func (a *application) log(message string) {
	a.logs = append(a.logs, time.Now().Format("15:04:05")+"  "+message)
	if len(a.logs) > 100 {
		a.logs = append([]string(nil), a.logs[len(a.logs)-100:]...)
	}
}

func accountState(acc account) string {
	if !acc.Enabled {
		return "Отключён"
	}
	if acc.Health == "Работает" && acc.Latency > 0 {
		return fmt.Sprintf("OK · %s", acc.Latency.Round(time.Millisecond))
	}
	if acc.Health == "" {
		return "Не проверен"
	}
	return acc.Health
}

func enabledMark(enabled bool) string {
	if enabled {
		return "●"
	}
	return "○"
}

func boolState(value bool, yes, no string) string {
	if value {
		return fgGreen + yes + reset
	}
	return fgRed + no + reset
}

func onOff(value bool) string {
	if value {
		return "Включён"
	}
	return "Выключен"
}

func validEmail(value string) bool {
	parts := strings.Split(value, "@")
	return len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && validHostname(parts[1])
}

func validHostname(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 253 || strings.ContainsAny(value, " /\\:@") {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func redactEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	name := []rune(parts[0])
	if len(name) == 0 {
		return "***@" + parts[1]
	}
	return string(name[0]) + "***@" + parts[1]
}

func timeOrNever(value time.Time) string {
	if value.IsZero() {
		return "ещё не выполнялась"
	}
	return value.Format("02.01.2006 15:04:05")
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func lowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

func wrap(value, count int) int {
	if count <= 0 {
		return 0
	}
	for value < 0 {
		value += count
	}
	return value % count
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func pad(value string, width int) string {
	visible := visibleLen(value)
	if visible > width {
		return truncateANSI(value, width)
	}
	if visible == width {
		return value
	}
	return value + strings.Repeat(" ", width-visible)
}

func truncateANSI(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if visibleLen(value) <= width {
		return value
	}
	limit := width - 1
	var out strings.Builder
	visible := 0
	for i := 0; i < len(value) && visible < limit; {
		if value[i] == 0x1b {
			start := i
			i++
			for i < len(value) {
				b := value[i]
				i++
				if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') {
					break
				}
			}
			out.WriteString(value[start:i])
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		out.WriteRune(r)
		i += size
		visible++
	}
	out.WriteRune('…')
	out.WriteString(reset)
	return out.String()
}

func visibleLen(value string) int {
	plain := stripANSI(value)
	return utf8.RuneCountInString(plain)
}

func stripANSI(value string) string {
	var out strings.Builder
	inEscape := false
	for i := 0; i < len(value); i++ {
		b := value[i]
		if inEscape {
			if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') {
				inEscape = false
			}
			continue
		}
		if b == 0x1b {
			inEscape = true
			continue
		}
		out.WriteByte(b)
	}
	return out.String()
}
