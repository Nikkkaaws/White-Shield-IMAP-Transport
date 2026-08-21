package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHomeContainsRequestedNavigation(t *testing.T) {
	a := newApplication()
	rendered := stripANSI(a.render())
	for _, want := range []string{"ID клиента: 1", "Аккаунты", "Проверка", "Настройки", "Журнал", "Включить WSIT"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("home screen does not contain %q", want)
		}
	}
	for _, unwanted := range []string{"DNS", "Питание", "прототип", "ПРОТОТИП"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("home screen unexpectedly contains %q", unwanted)
		}
	}
}

func TestTransportHasNoGlobalHotkey(t *testing.T) {
	a := newApplication()
	before := a.running
	a.handleGlobal(keyEvent{Kind: keyRune, Rune: 's'})
	if a.running != before {
		t.Fatal("S still toggles the transport")
	}
	for _, screen := range []screenID{screenHome, screenOverview, screenAccounts, screenDoctor, screenSpeedSettings, screenSettings, screenLogs} {
		a.screen = screen
		rendered := stripANSI(a.render())
		for _, unwanted := range []string{"S Включить", "S Остановить", "S Вкл/выкл", "S Выполняется"} {
			if strings.Contains(rendered, unwanted) {
				t.Fatalf("screen %d still contains transport hotkey %q", screen, unwanted)
			}
		}
	}
}

func TestPasswordNeverAppearsInRenderedAccount(t *testing.T) {
	a := newApplication()
	a.screen = screenAccounts
	a.accounts = []account{{
		Email: "relay@example.com", Provider: "Другой IMAP", Host: "imap.example.com",
		Port: 993, Enabled: true, Health: "Работает", Password: []byte("never-print-this"),
	}}
	rendered := a.render()
	if strings.Contains(rendered, "never-print-this") {
		t.Fatal("account password leaked into rendered interface")
	}
}

func TestHomeStartsWithPowerAndOverviewOpens(t *testing.T) {
	if homeMenu[0].Action != "toggle" {
		t.Fatal("power action is not first")
	}
	a := newApplication()
	a.homeSelection = 2
	a.handleHome(keyEvent{Kind: keyEnter})
	if a.screen != screenOverview {
		t.Fatal("overview did not open")
	}
}

func TestAccountSetupChoicesAreManualThenRambler(t *testing.T) {
	want := []string{"Другой IMAP-сервер", "Rambler"}
	if len(accountSetupOptions) != len(want) {
		t.Fatalf("account setup choices = %v", accountSetupOptions)
	}
	for i := range want {
		if accountSetupOptions[i] != want[i] {
			t.Fatalf("account setup choices = %v, want %v", accountSetupOptions, want)
		}
	}
}

func TestSettingsDoNotContainInternetChecksOrLogModes(t *testing.T) {
	a := newApplication()
	for _, item := range a.settingsItems() {
		text := item.Group + " " + item.Label + " " + item.Description
		for _, unwanted := range []string{"тест", "скорост", "журнал", "тихий", "подробный", "распредел"} {
			if strings.Contains(strings.ToLower(text), unwanted) {
				t.Fatalf("settings unexpectedly contain %q in %q", unwanted, text)
			}
		}
	}
}

func TestMailLanesFollowEnabledAccounts(t *testing.T) {
	a := newApplication()
	a.accounts = []account{
		{Email: "one@example.com", Enabled: true},
		{Email: "two@example.com", Enabled: false},
		{Email: "three@example.com", Enabled: true},
	}
	a.syncLanes()
	if a.lanes != 2 {
		t.Fatalf("lanes = %d, want 2", a.lanes)
	}
	a.accountSelection = 1
	a.toggleSelectedAccount()
	if a.lanes != 3 {
		t.Fatalf("lanes after enabling account = %d, want 3", a.lanes)
	}
}

func TestDoctorHasSpeedSettingsWithoutSpeedHotkey(t *testing.T) {
	a := newApplication()
	a.screen = screenDoctor
	rendered := stripANSI(a.render())
	for _, want := range []string{"Связь с сервером", "СКОРОСТЬ ИНТЕРНЕТА", "Настройки теста скорости"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("doctor screen does not contain %q", want)
		}
	}
	for _, unwanted := range []string{"T Скорость", "Локальный SOCKS5"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("doctor screen unexpectedly contains %q", unwanted)
		}
	}
}

func TestSettingsUseEnterOnly(t *testing.T) {
	a := newApplication()
	a.screen = screenSettings
	rendered := stripANSI(a.render())
	if strings.ContainsAny(rendered, "←→") {
		t.Fatal("settings still advertise left/right controls")
	}
	if !strings.Contains(rendered, "Enter Настроить") {
		t.Fatal("settings do not advertise Enter interaction")
	}
}

func TestSettingsContainUninstallAction(t *testing.T) {
	a := newApplication()
	a.screen = screenSettings
	rendered := stripANSI(a.render())
	if !strings.Contains(rendered, "Удалить WSIT") {
		t.Fatal("settings do not contain the uninstall action")
	}
}

func TestSettingsUseConnectionCodeInsteadOfSharedKey(t *testing.T) {
	a := newApplication()
	a.screen = screenSettings
	rendered := stripANSI(a.render())
	if !strings.Contains(rendered, "Код подключения") || strings.Contains(rendered, "Общий ключ") {
		t.Fatalf("unexpected connection settings: %q", rendered)
	}
}

func TestStoppedStateAppearsOnlyInTopStatus(t *testing.T) {
	a := newApplication()
	a.transitionDelay = 0
	if a.running {
		t.Fatal("transport starts in the running state")
	}
	a.toggleTransport()
	if !a.running {
		t.Fatal("transport remained stopped after start transition")
	}
	a.toggleTransport()
	if a.running {
		t.Fatal("transport remained running after stop transition")
	}
	rendered := stripANSI(a.render())
	if count := strings.Count(rendered, "ОСТАНОВЛЕН"); count != 1 {
		t.Fatalf("stopped status appears %d times, want exactly once", count)
	}
	if !strings.Contains(rendered, "Запустить транспорт") || strings.Contains(rendered, "Остановить транспорт") {
		t.Fatal("stopped home screen has the wrong power-action description")
	}
}

func TestTransportTransitionsExposeExpectedStages(t *testing.T) {
	for _, stages := range [][]string{startStages, stopStages} {
		if len(stages) < 4 {
			t.Fatalf("too few transition stages: %v", stages)
		}
		for _, stage := range stages {
			if strings.TrimSpace(stage) == "" {
				t.Fatalf("empty transition stage in %v", stages)
			}
		}
	}
}

func TestTransitionStageReplacesFinalStateOnHome(t *testing.T) {
	a := newApplication()
	normal := stripANSI(a.render())
	a.transitioning = true
	a.transitionStart = false
	a.transitionStage = stopStages[0]
	a.transitionIndex = 1
	a.transitionTotal = len(stopStages)
	rendered := stripANSI(a.render())
	if !strings.Contains(rendered, stopStages[0]) {
		t.Fatal("current transition stage is absent from home status")
	}
	if strings.Contains(rendered, "РАБОТАЕТ") || strings.Contains(rendered, "ОСТАНОВЛЕН") {
		t.Fatal("final transport state is visible during transition")
	}
	if !strings.Contains(rendered, "Обзор") || !strings.Contains(rendered, "Аккаунты") {
		t.Fatal("main menu disappeared during transition")
	}
	if strings.Contains(rendered, "1/4") {
		t.Fatal("transition progress counter is still visible")
	}
	if statusColumn(normal, "SOCKS5") != statusColumn(rendered, "SOCKS5") || statusColumn(normal, "ЛИНИИ") != statusColumn(rendered, "ЛИНИИ") {
		t.Fatal("SOCKS5 or mail-line status moved during transition")
	}
}

func statusColumn(rendered, marker string) int {
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, marker) && strings.Contains(line, "●") {
			return utf8.RuneCountInString(line[:strings.Index(line, marker)])
		}
	}
	return -1
}

func TestTransitionColorRunsBothDirections(t *testing.T) {
	startFirst := transitionStatusColor(true, 1, 5)
	startLast := transitionStatusColor(true, 5, 5)
	stopFirst := transitionStatusColor(false, 1, 5)
	stopLast := transitionStatusColor(false, 5, 5)
	if startFirst != stopLast || startLast != stopFirst || startFirst == startLast {
		t.Fatal("transition colors do not reverse between start and stop")
	}
}

func TestClientIDRange(t *testing.T) {
	for _, value := range []string{"1", "42", "255", " 7 "} {
		if _, err := parseClientID(value); err != nil {
			t.Fatalf("valid client ID %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", "0", "256", "1.5", "abc"} {
		if _, err := parseClientID(value); err == nil {
			t.Fatalf("invalid client ID %q accepted", value)
		}
	}
}

func TestCurrentChoiceIndexIsPreserved(t *testing.T) {
	if got := stringIndex([]string{"Быстрый", "Стандартный", "Точный"}, "Стандартный"); got != 1 {
		t.Fatalf("stringIndex = %d, want 1", got)
	}
	if got := intIndex([]int{60, 120, 180}, 120); got != 1 {
		t.Fatalf("intIndex = %d, want 1", got)
	}
}

func TestInputValidation(t *testing.T) {
	if !validEmail("relay@example.com") {
		t.Fatal("valid email rejected")
	}
	for _, value := range []string{"", "relay", "@example.com", "relay@bad host"} {
		if validEmail(value) {
			t.Fatalf("invalid email accepted: %q", value)
		}
	}
}
