package io.whiteshield.wsit

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.IBinder
import android.os.PowerManager
import wsit.mobile.Controller
import wsit.mobile.Mobile
import org.json.JSONObject
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

class TransportService : VpnService() {
    private val worker = Executors.newSingleThreadExecutor()
    private var poller: ScheduledExecutorService? = null
    private var controller: Controller? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private var vpnTunnel: AndroidVpnTunnel? = null
    @Volatile private var starting = false
    private lateinit var store: SecureConfigStore

    override fun onCreate() {
        super.onCreate()
        store = SecureConfigStore(this)
        createChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> stopTransport()
            ACTION_START -> startTransport()
            ACTION_CLEAR_LOGS -> controller?.clearLogs()
            else -> if (store.transportRequested()) startTransport() else stopSelf()
        }
        return START_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = super.onBind(intent)

    override fun onDestroy() {
        poller?.shutdownNow()
        vpnTunnel?.stop()
        vpnTunnel = null
        runCatching { controller?.stop() }
        runCatching { controller?.close() }
        controller = null
        releaseWakeLock()
        worker.shutdownNow()
        super.onDestroy()
    }

    private fun startTransport() {
        if (starting || (controller != null && snapshot.phase != "error")) return
        starting = true
        store.setTransportRequested(true)
        startForeground(NOTIFICATION_ID, notification(snapshot, true))
        acquireWakeLock()
        worker.execute {
            runCatching {
                snapshot = TransportSnapshot(phase = "starting", stage = "Выбор прямой сети")
                updateNotification()
                val networkError = DirectNetwork.await(this)
                check(networkError.isBlank()) { networkError }
                val config = store.load()
                val validation = Mobile.validateConfig(config.toCoreJSON().toString())
                check(validation.isBlank()) { validation }

                cleanupRuntime()
                snapshot = TransportSnapshot(phase = "starting", stage = "Подключение Android VPN")
                updateNotification()
                vpnTunnel = AndroidVpnTunnel(this).also { it.establish() }

                val next = Mobile.newController(config.toCoreJSON().toString())
                controller = next
                next.start()
                waitForSocks(next)
                snapshot = snapshot.copy(phase = "starting", stage = "Подключение к SOCKS5")
                updateNotification()
                val (host, port) = splitListen(config.listen)
                checkNotNull(vpnTunnel).startSocksForwarder(host, port)
                startPolling()
            }.onFailure {
                cleanupRuntime()
                snapshot = TransportSnapshot(phase = "error", stage = "Ошибка запуска", error = it.message.orEmpty())
                store.setTransportRequested(false)
                updateNotification()
                releaseWakeLock()
            }.also {
                starting = false
            }
        }
    }

    private fun stopTransport() {
        store.setTransportRequested(false)
        val active = controller
        if (active == null) {
            snapshot = TransportSnapshot()
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
            return
        }
        worker.execute {
            snapshot = snapshot.copy(phase = "stopping", stage = "Завершение активных потоков")
            updateNotification()
            cleanupRuntime()
            snapshot = TransportSnapshot()
            releaseWakeLock()
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
    }

    private fun waitForSocks(active: Controller) {
        val deadline = android.os.SystemClock.elapsedRealtime() + 35_000
        while (android.os.SystemClock.elapsedRealtime() < deadline) {
            val state = TransportSnapshot.fromJSON(active.status())
            snapshot = state
            updateNotification()
            when (state.phase) {
                "running" -> return
                "error" -> error(state.error.ifBlank { "WSIT не запустил локальный SOCKS5" })
            }
            Thread.sleep(150)
        }
        error("Истекло время запуска локального SOCKS5")
    }

    private fun splitListen(value: String): Pair<String, Int> {
        val separator = value.lastIndexOf(':')
        require(separator > 0) { "Неверный адрес SOCKS5: $value" }
        return value.substring(0, separator) to value.substring(separator + 1).toInt()
    }

    private fun cleanupRuntime() {
        poller?.shutdownNow()
        poller = null
        vpnTunnel?.stop()
        vpnTunnel = null
        val active = controller
        controller = null
        runCatching { active?.stop() }
        runCatching { active?.close() }
    }

    private fun startPolling() {
        poller?.shutdownNow()
        poller = Executors.newSingleThreadScheduledExecutor().also { scheduler ->
            scheduler.scheduleAtFixedRate({
                val active = controller ?: return@scheduleAtFixedRate
                snapshot = TransportSnapshot.fromJSON(active.status())
                updateNotification()
                if (snapshot.phase == "error") store.setTransportRequested(false)
            }, 0, 500, TimeUnit.MILLISECONDS)
        }
    }

    private fun updateNotification() {
        (getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager)
            .notify(NOTIFICATION_ID, notification(snapshot, controller != null))
    }

    private fun notification(state: TransportSnapshot, active: Boolean): Notification {
        val openIntent = PendingIntent.getActivity(
            this,
            1,
            Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val builder = Notification.Builder(this, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_sys_upload_done)
            .setContentTitle("WSIT · ${state.stage}")
            .setContentText("SOCKS5 ${state.listen} · линии ${state.lanes}")
            .setContentIntent(openIntent)
            .setOngoing(active)
            .setOnlyAlertOnce(true)
            .setCategory(Notification.CATEGORY_SERVICE)
        if (active) {
            val stopIntent = PendingIntent.getService(
                this,
                2,
                Intent(this, TransportService::class.java).setAction(ACTION_STOP),
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
            )
            builder.addAction(Notification.Action.Builder(null, "Остановить", stopIntent).build())
        }
        return builder.build()
    }

    private fun createChannel() {
        val channel = NotificationChannel(CHANNEL_ID, "Работа WSIT", NotificationManager.IMPORTANCE_LOW).apply {
            description = "Состояние транспорта и локального SOCKS5"
            setShowBadge(false)
        }
        (getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager).createNotificationChannel(channel)
    }

    private fun acquireWakeLock() {
        if (wakeLock?.isHeld == true) return
        wakeLock = (getSystemService(Context.POWER_SERVICE) as PowerManager)
            .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "WSIT::Transport")
            .apply { acquire() }
    }

    private fun releaseWakeLock() {
        wakeLock?.takeIf { it.isHeld }?.release()
        wakeLock = null
    }

    override fun onRevoke() {
        stopTransport()
        super.onRevoke()
    }

    companion object {
        const val ACTION_START = "io.whiteshield.wsit.START"
        const val ACTION_STOP = "io.whiteshield.wsit.STOP"
        const val ACTION_CLEAR_LOGS = "io.whiteshield.wsit.CLEAR_LOGS"
        private const val CHANNEL_ID = "wsit_transport"
        private const val NOTIFICATION_ID = 4815

        @Volatile
        var snapshot: TransportSnapshot = TransportSnapshot()
            private set

        fun speedResult(raw: String): JSONObject = JSONObject(raw)
    }
}
