package io.whiteshield.wsit

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Spinner
import android.widget.ArrayAdapter
import android.widget.Switch
import android.widget.TextView
import android.widget.Toast
import wsit.mobile.Mobile
import org.json.JSONObject
import java.util.Locale
import java.util.concurrent.Executors
import kotlin.math.max

class MainActivity : Activity() {
    private lateinit var store: SecureConfigStore
    private lateinit var config: AppConfig
    private lateinit var content: LinearLayout
    private lateinit var statusTitle: TextView
    private lateinit var statusDetail: TextView
    private lateinit var navButtons: List<Button>
    private val handler = Handler(Looper.getMainLooper())
    private val worker = Executors.newSingleThreadExecutor()
    private var screen = 0
    private var renderedPhase = ""
    private var renderedStage = ""
    private var lastSpeedResult: JSONObject? = null

    private val refreshTask = object : Runnable {
        override fun run() {
            refreshStatus()
            handler.postDelayed(this, 400)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        store = SecureConfigStore(this)
        config = store.load()
        window.statusBarColor = BG
        window.navigationBarColor = BG
        buildShell()
        showScreen(0)
        requestNotificationPermission()
        handler.post(refreshTask)
    }

    override fun onDestroy() {
        handler.removeCallbacks(refreshTask)
        worker.shutdownNow()
        super.onDestroy()
    }

    private fun buildShell() {
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(BG)
            setPadding(dp(16), dp(14), dp(16), dp(8))
            setOnApplyWindowInsetsListener { view, insets ->
                val top: Int
                val bottom: Int
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                    val bars = insets.getInsets(WindowInsets.Type.systemBars())
                    top = bars.top
                    bottom = bars.bottom
                } else {
                    @Suppress("DEPRECATION")
                    top = insets.systemWindowInsetTop
                    @Suppress("DEPRECATION")
                    bottom = insets.systemWindowInsetBottom
                }
                view.setPadding(dp(16), top + dp(14), dp(16), bottom + dp(8))
                insets
            }
        }
        val header = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        header.addView(text("WSIT", 22f, WHITE, Typeface.BOLD), weighted(1))
        header.addView(text("IMAP TRANSPORT", 11f, MUTED, Typeface.BOLD))
        root.addView(header, matchWrap())
        root.addView(space(12))

        val status = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(12), dp(16), dp(12))
            background = rounded(PANEL, 14)
        }
        statusTitle = text("ОСТАНОВЛЕН", 15f, RED, Typeface.BOLD)
        statusDetail = text("SOCKS5 ${config.listen}", 12f, MUTED)
        status.addView(statusTitle)
        status.addView(statusDetail)
        root.addView(status, matchWrap())
        root.addView(space(10))

        val scroll = ScrollView(this).apply {
            isFillViewport = true
            overScrollMode = View.OVER_SCROLL_NEVER
        }
        content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(0, 0, 0, dp(12))
        }
        scroll.addView(content, matchWrap())
        root.addView(scroll, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))

        val nav = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER
        }
        val names = listOf("Главная", "Аккаунты", "Проверка", "Настройки", "Журнал")
        navButtons = names.mapIndexed { index, name ->
            Button(this).apply {
                text = name
                isAllCaps = false
                textSize = 10f
                minHeight = 0
                minimumHeight = 0
                minimumWidth = 0
                setPadding(dp(2), dp(9), dp(2), dp(9))
                setOnClickListener { showScreen(index) }
                nav.addView(this, LinearLayout.LayoutParams(0, dp(44), 1f).apply {
                    if (index < names.lastIndex) marginEnd = dp(4)
                })
            }
        }
        root.addView(nav, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        setContentView(root)
        root.requestApplyInsets()
    }

    private fun showScreen(index: Int) {
        screen = index
        navButtons.forEachIndexed { item, button ->
            button.setTextColor(if (item == index) WHITE else MUTED)
            button.background = rounded(if (item == index) BLUE_DARK else PANEL, 12)
        }
        renderCurrent()
    }

    private fun renderCurrent() {
        content.removeAllViews()
        when (screen) {
            0 -> renderHome()
            1 -> renderAccounts()
            2 -> renderChecks()
            3 -> renderSettings()
            else -> renderLogs()
        }
    }

    private fun refreshStatus() {
        val state = TransportService.snapshot
        statusTitle.text = state.stage.uppercase(Locale.getDefault())
        statusTitle.setTextColor(statusColor(state))
        statusDetail.text = "SOCKS5 ${state.listen}   ·   линии ${state.lanes}   ·   ID ${state.clientId}"
        if (state.phase != renderedPhase || state.stage != renderedStage) {
            renderedPhase = state.phase
            renderedStage = state.stage
            if (screen == 0 || screen == 2 || screen == 4) renderCurrent()
        }
    }

    private fun renderHome() {
        val state = TransportService.snapshot
        section("УПРАВЛЕНИЕ")
        card {
            val changing = state.phase == "starting" || state.phase == "stopping"
            val running = state.phase == "running" || state.phase == "starting"
            addView(text(state.stage, 19f, statusColor(state), Typeface.BOLD))
            addView(text(if (running) "Транспорт активен в фоновом сервисе" else "Транспорт сейчас не запущен", 13f, MUTED))
            addView(space(12))
            addView(actionButton(if (running) "Остановить" else "Включить", !running) {
                if (!changing) {
                    if (running) stopTransport() else startTransport()
                }
            })
        }
        section("ОБЗОР")
        card {
            addView(valueRow("ID клиента", config.clientId.toString()) { editClientID() })
            addView(divider())
            addView(valueRow("Локальный SOCKS5", config.listen) { copy(config.listen, "Адрес SOCKS5 скопирован") })
            addView(divider())
            addView(valueRow("Почтовые линии", "${state.lanes}/${config.accounts.count { it.enabled }}"))
            addView(divider())
            addView(valueRow("Активные потоки", state.streams.toString()))
            addView(divider())
            addView(valueRow("Задержка транспорта", if (state.rttMs > 0) "${state.rttMs} мс" else "—"))
        }
        section("ТРАФИК")
        card {
            addView(valueRow("Принято", formatBytes(state.rxBytes)))
            addView(divider())
            addView(valueRow("Отправлено", formatBytes(state.txBytes)))
            addView(divider())
            addView(valueRow("Очередь", formatBytes(state.pendingBytes)))
        }
        if (state.error.isNotBlank()) {
            section("ОШИБКА")
            card(RED_DARK) { addView(text(state.error, 13f, RED)) }
        }
    }

    private fun renderAccounts() {
        section("ПОЧТОВЫЕ АККАУНТЫ")
        addView(actionButton("Добавить аккаунт", true) { showAccountDialog(null) })
        addView(space(8))
        if (config.accounts.isEmpty()) {
            card { addView(text("Аккаунтов пока нет. Добавьте Rambler или любой другой IMAP.", 14f, MUTED)) }
            return
        }
        config.accounts.forEach { account ->
            card {
                val top = LinearLayout(this@MainActivity).apply {
                    orientation = LinearLayout.HORIZONTAL
                    gravity = Gravity.CENTER_VERTICAL
                }
                top.addView(text(account.username, 15f, WHITE, Typeface.BOLD), weighted(1))
                top.addView(text(if (account.enabled) "ВКЛ" else "ВЫКЛ", 11f, if (account.enabled) GREEN else MUTED, Typeface.BOLD))
                addView(top)
                addView(text("${account.provider} · ${account.host}:${account.port}", 12f, MUTED))
                addView(text(account.health + if (account.latencyMs > 0) " · ${account.latencyMs} мс" else "", 12f, healthColor(account.health), Typeface.BOLD))
                addView(space(10))
                val actions = LinearLayout(this@MainActivity).apply { orientation = LinearLayout.HORIZONTAL }
                actions.addView(smallButton("Проверить") { checkAccount(account) }, weighted(1))
                actions.addView(space(6))
                actions.addView(smallButton(if (account.enabled) "Отключить" else "Включить") {
                    account.enabled = !account.enabled
                    saveAndRender()
                }, weighted(1))
                actions.addView(space(6))
                actions.addView(smallButton("Изменить") { showAccountDialog(account) }, weighted(1))
                addView(actions)
                addView(space(6))
                addView(smallButton("Удалить") { confirmDelete(account) })
            }
        }
    }

    private fun renderChecks() {
        val state = TransportService.snapshot
        section("СВЯЗЬ С СЕРВЕРОМ")
        card {
            val ok = state.phase == "running" && state.lanes > 0
            addView(text(if (ok) "Связь работает" else "Связь не установлена", 17f, if (ok) GREEN else RED, Typeface.BOLD))
            addView(text("Линии: ${state.lanes} · RTT: ${if (state.rttMs > 0) "${state.rttMs} мс" else "—"}", 13f, MUTED))
            addView(space(10))
            addView(actionButton("Обновить состояние", false) { refreshStatus(); renderCurrent() })
        }
        section("АККАУНТЫ")
        card {
            val healthy = config.accounts.count { it.enabled && it.health == "Работает" }
            addView(text("Работают $healthy/${config.accounts.count { it.enabled }}", 16f, WHITE, Typeface.BOLD))
            addView(space(10))
            addView(actionButton("Проверить все аккаунты", false) { checkAllAccounts() })
        }
        section("СКОРОСТЬ ИНТЕРНЕТА")
        card {
            val result = lastSpeedResult
            if (result == null) {
                addView(text("Замер ещё не запускался", 14f, MUTED))
            } else if (result.optBoolean("ok")) {
                addView(text(String.format(Locale.US, "↓ %.2f Мбит/с", result.optDouble("download_mbps")), 19f, GREEN, Typeface.BOLD))
                addView(text(String.format(Locale.US, "↑ %.2f Мбит/с", result.optDouble("upload_mbps")), 19f, BLUE, Typeface.BOLD))
                addView(text("Задержка ${result.optLong("latency_ms")} мс", 13f, MUTED))
            } else {
                addView(text(result.optString("detail"), 13f, RED))
            }
            addView(space(10))
            addView(actionButton("Запустить тест через WSIT", true) { runSpeedTest() })
        }
        section("НАСТРОЙКИ ТЕСТА")
        card {
            addView(valueRow("Загрузка", "${config.speedDownloadMiB} МиБ") { editNumber("Объём загрузки", config.speedDownloadMiB, 1, 128) { config.speedDownloadMiB = it } })
            addView(divider())
            addView(valueRow("Отдача", "${config.speedUploadMiB} МиБ") { editNumber("Объём отдачи", config.speedUploadMiB, 1, 128) { config.speedUploadMiB = it } })
            addView(divider())
            addView(valueRow("Параллельные потоки", config.speedParallel.toString()) { editNumber("Параллельные потоки", config.speedParallel, 1, 8) { config.speedParallel = it } })
            addView(divider())
            addView(valueRow("Лимит времени", "${config.speedTimeoutSec} с") { editNumber("Лимит времени", config.speedTimeoutSec, 15, 300) { config.speedTimeoutSec = it } })
        }
    }

    private fun renderSettings() {
        section("ПОДКЛЮЧЕНИЕ")
        card {
            addView(valueRow("Локальный SOCKS5", config.listen) { editText("Локальный SOCKS5", config.listen, false) { config.listen = it } })
            addView(divider())
            addView(valueRow("ID клиента", config.clientId.toString()) { editClientID() })
            addView(divider())
            addView(valueRow("Код подключения", if (config.passphrase.isBlank()) "Не импортирован" else "Импортирован") { importConnectionCode() })
        }
        section("ПРОИЗВОДИТЕЛЬНОСТЬ")
        card {
            addView(valueRow("Профиль", config.profile) { chooseProfile() })
            addView(divider())
            addView(valueRow("Почтовые линии", config.accounts.count { it.enabled }.toString()))
        }
        section("ПОЧТА")
        card {
            addView(valueRow("Папка отправки", config.folderSend) { editText("Папка отправки", config.folderSend, false) { config.folderSend = it } })
            addView(divider())
            addView(valueRow("Папка приёма", config.folderRecv) { editText("Папка приёма", config.folderRecv, false) { config.folderRecv = it } })
        }
        section("ЗАПУСК")
        card {
            val toggle = Switch(this@MainActivity).apply {
                text = "Запускать WSIT после включения телефона"
                setTextColor(WHITE)
                textSize = 14f
                isChecked = config.autostart
                setOnCheckedChangeListener { _, checked ->
                    config.autostart = checked
                    store.save(config)
                }
            }
            addView(toggle, matchWrap())
        }
        section("КОНФИГУРАЦИЯ")
        addView(actionButton("Проверить конфигурацию", false) {
            val detail = configurationProblem()
            message(if (detail.isBlank()) "Конфигурация готова" else detail)
        })
    }

    private fun renderLogs() {
        val logs = TransportService.snapshot.logs
        section("ЖУРНАЛ")
        val controls = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        controls.addView(actionButton("Обновить", false) { renderCurrent() }, weighted(1))
        controls.addView(space(8))
        controls.addView(actionButton("Копировать", false) { copy(logs.joinToString("\n"), "Журнал скопирован") }, weighted(1))
        content.addView(controls)
        content.addView(space(8))
        content.addView(actionButton("Очистить журнал", false) {
            startService(Intent(this, TransportService::class.java).setAction(TransportService.ACTION_CLEAR_LOGS))
        })
        content.addView(space(8))
        card {
            if (logs.isEmpty()) addView(text("Журнал пуст", 13f, MUTED))
            logs.takeLast(150).forEach { addView(text(it, 12f, MUTED, Typeface.NORMAL, true)) }
        }
    }

    private fun startTransport() {
        val detail = configurationProblem()
        if (detail.isNotBlank()) {
            message(detail)
            return
        }
        store.save(config)
        val intent = Intent(this, TransportService::class.java).setAction(TransportService.ACTION_START)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) startForegroundService(intent) else startService(intent)
        renderedPhase = ""
        refreshStatus()
    }

    private fun configurationProblem(): String {
        if (config.passphrase.isBlank()) return "Сначала импортируйте код подключения"
        if (config.accounts.none { it.enabled }) return "Добавьте хотя бы один почтовый аккаунт"
        return Mobile.validateConfig(config.toCoreJSON().toString())
    }

    private fun stopTransport() {
        startService(Intent(this, TransportService::class.java).setAction(TransportService.ACTION_STOP))
    }

    private fun checkAccount(account: MailAccount) {
        account.health = "Проверяется"
        account.latencyMs = 0
        store.save(config)
        renderCurrent()
        worker.execute {
            val result = JSONObject(Mobile.checkAccount(account.toJSON(false).toString()))
            runOnUiThread {
                account.health = if (result.optBoolean("ok")) "Работает" else "Ошибка: ${result.optString("detail")}"
                account.latencyMs = result.optLong("latency_ms")
                store.save(config)
                if (screen == 1 || screen == 2) renderCurrent()
            }
        }
    }

    private fun checkAllAccounts() {
        val enabled = config.accounts.filter { it.enabled }
        if (enabled.isEmpty()) {
            message("Нет включённых аккаунтов")
            return
        }
        enabled.forEach { it.health = "Проверяется" }
        renderCurrent()
        worker.execute {
            enabled.forEach { account ->
                val result = JSONObject(Mobile.checkAccount(account.toJSON(false).toString()))
                account.health = if (result.optBoolean("ok")) "Работает" else "Ошибка: ${result.optString("detail")}"
                account.latencyMs = result.optLong("latency_ms")
                runOnUiThread { store.save(config); if (screen == 1 || screen == 2) renderCurrent() }
            }
        }
    }

    private fun runSpeedTest() {
        if (TransportService.snapshot.phase != "running") {
            message("Сначала включите WSIT")
            return
        }
        lastSpeedResult = JSONObject().put("detail", "Идёт измерение…")
        renderCurrent()
        worker.execute {
            val result = JSONObject(Mobile.runSpeedTest(config.speedOptionsJSON().toString()))
            runOnUiThread { lastSpeedResult = result; if (screen == 2) renderCurrent() }
        }
    }

    private fun showAccountDialog(existing: MailAccount?) {
        val original = existing ?: MailAccount()
        val body = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(20), dp(8), dp(20), 0)
        }
        val provider = Spinner(this)
        val providers = listOf("Другой IMAP", "Rambler")
        provider.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, providers)
        provider.setSelection(if (original.provider == "Rambler") 1 else 0)
        val host = dialogInput("Адрес IMAP", original.host)
        val port = dialogInput("Порт", original.port.toString(), InputType.TYPE_CLASS_NUMBER)
        val pin = dialogInput("Закреплённый IP · необязательно", original.pinIp)
        val email = dialogInput("Почта", original.username, InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_EMAIL_ADDRESS)
        val password = dialogInput(if (existing == null) "Пароль" else "Новый пароль · пусто = оставить", "", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD)
        body.addView(provider)
        listOf(host, port, pin, email, password).forEach { body.addView(it, matchWrap()) }
        provider.onItemSelectedListener = SimpleItemSelectedListener { position ->
            if (position == 1) {
                host.setText("imap.rambler.ru")
                port.setText("993")
                pin.setText("81.19.77.168")
            }
        }
        val scroll = ScrollView(this).apply { addView(body) }
        val dialog = AlertDialog.Builder(this)
            .setTitle(if (existing == null) "Добавление аккаунта" else "Изменение аккаунта")
            .setView(scroll)
            .setNegativeButton("Отмена", null)
            .setPositiveButton("Сохранить", null)
            .create()
        dialog.show()
        dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
            val parsedPort = port.text.toString().toIntOrNull()
            if (host.text.isBlank() || email.text.isBlank() || parsedPort == null || parsedPort !in 1..65535 || (existing == null && password.text.isBlank())) {
                Toast.makeText(this, "Проверьте адрес, порт, почту и пароль", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }
            original.provider = providers[provider.selectedItemPosition]
            original.host = host.text.toString().trim()
            original.port = parsedPort
            original.pinIp = pin.text.toString().trim()
            original.username = email.text.toString().trim()
            if (password.text.isNotBlank()) original.password = password.text.toString()
            original.enabled = true
            original.health = "Не проверен"
            if (existing == null) config.accounts += original
            store.save(config)
            dialog.dismiss()
            renderCurrent()
            checkAccount(original)
        }
    }

    private fun confirmDelete(account: MailAccount) {
        AlertDialog.Builder(this)
            .setTitle("Удалить аккаунт?")
            .setMessage(account.username)
            .setNegativeButton("Отмена", null)
            .setPositiveButton("Удалить") { _, _ ->
                account.password = ""
                config.accounts.remove(account)
                saveAndRender()
            }
            .show()
    }

    private fun editClientID() = editNumber("ID клиента", config.clientId, 1, 255) { config.clientId = it }

    private fun chooseProfile() {
        val options = arrayOf("Стандартный", "Максимальная скорость", "Минимальная задержка")
        AlertDialog.Builder(this)
            .setTitle("Профиль производительности")
            .setSingleChoiceItems(options, max(0, options.indexOf(config.profile))) { dialog, index ->
                config.profile = options[index]
                saveAndRender()
                dialog.dismiss()
            }
            .show()
    }

    private fun importConnectionCode() {
        val input = dialogInput("WSIT1.…", "")
        val dialog = AlertDialog.Builder(this)
            .setTitle("Код подключения")
            .setMessage("Скопируйте код из меню WSIT на VPS")
            .setView(input)
            .setNegativeButton("Отмена", null)
            .setPositiveButton("Импортировать", null)
            .create()
        dialog.setOnShowListener {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val result = JSONObject(Mobile.decodePairingCode(input.text.toString()))
                if (!result.optBoolean("ok")) {
                    Toast.makeText(this, result.optString("detail", "Некорректный код"), Toast.LENGTH_LONG).show()
                    return@setOnClickListener
                }
                config.passphrase = result.getString("passphrase")
                result.optString("folder_send").takeIf { it.isNotBlank() }?.let { config.folderSend = it }
                result.optString("folder_recv").takeIf { it.isNotBlank() }?.let { config.folderRecv = it }
                result.optString("dns_resolver").takeIf { it.isNotBlank() }?.let { config.dnsResolver = it }
                store.save(config)
                dialog.dismiss()
                renderCurrent()
                Toast.makeText(this, "Код подключения импортирован", Toast.LENGTH_SHORT).show()
            }
        }
        dialog.show()
    }

    private fun editText(title: String, initial: String, password: Boolean, apply: (String) -> Unit) {
        val input = dialogInput(title, initial, if (password) InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD else InputType.TYPE_CLASS_TEXT)
        AlertDialog.Builder(this)
            .setTitle(title)
            .setView(input)
            .setNegativeButton("Отмена", null)
            .setPositiveButton("Сохранить") { _, _ ->
                if (input.text.isNotBlank()) {
                    apply(input.text.toString().trim())
                    saveAndRender()
                }
            }
            .show()
    }

    private fun editNumber(title: String, initial: Int, minimum: Int, maximum: Int, apply: (Int) -> Unit) {
        val input = dialogInput("$minimum–$maximum", initial.toString(), InputType.TYPE_CLASS_NUMBER)
        val dialog = AlertDialog.Builder(this)
            .setTitle(title)
            .setView(input)
            .setNegativeButton("Отмена", null)
            .setPositiveButton("Сохранить", null)
            .create()
        dialog.setOnShowListener {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val value = input.text.toString().toIntOrNull()
                if (value == null || value !in minimum..maximum) {
                    Toast.makeText(this, "Введите число от $minimum до $maximum", Toast.LENGTH_SHORT).show()
                } else {
                    apply(value)
                    saveAndRender()
                    dialog.dismiss()
                }
            }
        }
        dialog.show()
    }

    private fun saveAndRender() {
        store.save(config)
        renderCurrent()
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 100)
        }
    }

    private fun section(label: String) {
        content.addView(space(14))
        content.addView(text(label, 11f, MUTED, Typeface.BOLD))
        content.addView(space(7))
    }

    private fun card(color: Int = PANEL, block: LinearLayout.() -> Unit) {
        val card = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(15), dp(13), dp(15), dp(13))
            background = rounded(color, 14)
            block()
        }
        content.addView(card, matchWrap().apply { bottomMargin = dp(8) })
    }

    private fun actionButton(label: String, primary: Boolean, click: () -> Unit) = Button(this).apply {
        text = label
        isAllCaps = false
        textSize = 14f
        setTextColor(WHITE)
        background = rounded(if (primary) BLUE_DARK else PANEL_LIGHT, 11)
        setOnClickListener { click() }
    }

    private fun smallButton(label: String, click: () -> Unit) = actionButton(label, false, click).apply {
        textSize = 12f
        minHeight = 0
        minimumHeight = 0
        setPadding(dp(8), dp(8), dp(8), dp(8))
    }

    private fun valueRow(label: String, value: String, click: (() -> Unit)? = null): View {
        return LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(0, dp(7), 0, dp(7))
            addView(text(label, 14f, WHITE), weighted(1))
            addView(text(value, 13f, if (click == null) MUTED else BLUE, Typeface.BOLD))
            if (click != null) setOnClickListener { click() }
        }
    }

    private fun divider() = View(this).apply { setBackgroundColor(DIVIDER) }.also {
        it.layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(1))
    }

    private fun dialogInput(hint: String, value: String, type: Int = InputType.TYPE_CLASS_TEXT) = EditText(this).apply {
        this.hint = hint
        setText(value)
        inputType = type
        setSelectAllOnFocus(true)
        setPadding(dp(14), dp(12), dp(14), dp(12))
    }

    private fun text(value: String, size: Float, color: Int, style: Int = Typeface.NORMAL, mono: Boolean = false) = TextView(this).apply {
        text = value
        textSize = size
        setTextColor(color)
        typeface = if (mono) Typeface.MONOSPACE else Typeface.create(Typeface.DEFAULT, style)
        setLineSpacing(0f, 1.12f)
    }

    private fun statusColor(state: TransportSnapshot): Int = when {
        state.phase == "running" -> GREEN
        state.phase == "error" -> RED
        state.phase == "stopping" -> RED
        state.stage.contains("Проверка конфигурации") -> RED
        state.stage.contains("аккаунтов") -> ORANGE
        state.stage.contains("линий") -> YELLOW
        state.stage.contains("SOCKS5") -> GREEN
        else -> RED
    }

    private fun healthColor(health: String): Int = when {
        health == "Работает" -> GREEN
        health == "Проверяется" -> YELLOW
        health.startsWith("Ошибка") -> RED
        else -> MUTED
    }

    private fun message(value: String) = AlertDialog.Builder(this).setMessage(value).setPositiveButton("Готово", null).show()

    private fun copy(value: String, confirmation: String) {
        (getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager).setPrimaryClip(ClipData.newPlainText("WSIT", value))
        Toast.makeText(this, confirmation, Toast.LENGTH_SHORT).show()
    }

    private fun formatBytes(value: Long): String = when {
        value >= 1_000_000_000 -> String.format(Locale.US, "%.2f ГБ", value / 1_000_000_000.0)
        value >= 1_000_000 -> String.format(Locale.US, "%.2f МБ", value / 1_000_000.0)
        value >= 1_000 -> String.format(Locale.US, "%.1f КБ", value / 1_000.0)
        else -> "$value Б"
    }

    private fun rounded(color: Int, radius: Int) = GradientDrawable().apply {
        setColor(color)
        cornerRadius = dp(radius).toFloat()
    }

    private fun addView(view: View) = content.addView(view, matchWrap())
    private fun space(height: Int) = View(this).apply { layoutParams = LinearLayout.LayoutParams(1, dp(height)) }
    private fun dp(value: Int) = (value * resources.displayMetrics.density + 0.5f).toInt()
    private fun matchWrap() = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
    private fun weighted(weight: Int) = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, weight.toFloat())

    companion object {
        private val BG = Color.rgb(11, 13, 16)
        private val PANEL = Color.rgb(22, 26, 32)
        private val PANEL_LIGHT = Color.rgb(38, 44, 53)
        private val BLUE_DARK = Color.rgb(27, 82, 132)
        private val RED_DARK = Color.rgb(58, 25, 28)
        private val WHITE = Color.rgb(245, 247, 250)
        private val MUTED = Color.rgb(153, 161, 173)
        private val BLUE = Color.rgb(74, 163, 255)
        private val GREEN = Color.rgb(85, 217, 138)
        private val RED = Color.rgb(255, 95, 95)
        private val ORANGE = Color.rgb(240, 151, 74)
        private val YELLOW = Color.rgb(230, 203, 83)
        private val DIVIDER = Color.rgb(45, 50, 58)
    }
}

private class SimpleItemSelectedListener(private val selected: (Int) -> Unit) : android.widget.AdapterView.OnItemSelectedListener {
    override fun onItemSelected(parent: android.widget.AdapterView<*>?, view: View?, position: Int, id: Long) = selected(position)
    override fun onNothingSelected(parent: android.widget.AdapterView<*>?) = Unit
}
