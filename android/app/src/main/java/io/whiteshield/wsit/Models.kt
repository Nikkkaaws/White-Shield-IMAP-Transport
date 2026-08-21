package io.whiteshield.wsit

import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

data class MailAccount(
    var id: String = UUID.randomUUID().toString(),
    var enabled: Boolean = true,
    var provider: String = "Другой IMAP",
    var host: String = "",
    var port: Int = 993,
    var pinIp: String = "",
    var username: String = "",
    var password: String = "",
    var health: String = "Не проверен",
    var latencyMs: Long = 0,
) {
    fun toJSON(includeHealth: Boolean = true) = JSONObject().apply {
        put("id", id)
        put("enabled", enabled)
        put("provider", provider)
        put("host", host)
        put("port", port)
        put("pin_ip", pinIp)
        put("direct_interface", "off")
        put("username", username)
        put("password", password)
        if (includeHealth) {
            put("health", health)
            put("latency_ms", latencyMs)
        }
    }

    companion object {
        fun fromJSON(json: JSONObject) = MailAccount(
            id = json.optString("id").ifBlank { UUID.randomUUID().toString() },
            enabled = json.optBoolean("enabled", true),
            provider = json.optString("provider", "Другой IMAP"),
            host = json.optString("host"),
            port = json.optInt("port", 993),
            pinIp = json.optString("pin_ip"),
            username = json.optString("username"),
            password = json.optString("password"),
            health = json.optString("health", "Не проверен"),
            latencyMs = json.optLong("latency_ms"),
        )
    }
}

data class AppConfig(
    var listen: String = "127.0.0.1:1080",
    var target: String = "direct",
    var dnsResolver: String = "1.1.1.1:53",
    var passphrase: String = "",
    var clientId: Int = 2,
    var folderSend: String = "Notes",
    var folderRecv: String = "Journal",
    var logLevel: String = "info",
    var autostart: Boolean = false,
    var profile: String = "Стандартный",
    var speedDownloadMiB: Int = 8,
    var speedUploadMiB: Int = 8,
    var speedParallel: Int = 4,
    var speedTimeoutSec: Int = 120,
    val accounts: MutableList<MailAccount> = mutableListOf(),
) {
    fun toCoreJSON(mode: String = "client") = JSONObject().apply {
        put("mode", mode)
        put("listen", listen)
        put("target", target)
        put("dns_resolver", dnsResolver)
        put("passphrase", passphrase)
        put("client_id", clientId)
        put("folder_send", folderSend)
        put("folder_recv", folderRecv)
        put("log_level", logLevel)
        put("accounts", JSONArray().apply { accounts.forEach { put(it.toJSON(false)) } })
        put("tuning", tuningJSON())
    }

    fun toStorageJSON() = toCoreJSON().apply {
        put("autostart", autostart)
        put("profile", profile)
        put("speed_download_mib", speedDownloadMiB)
        put("speed_upload_mib", speedUploadMiB)
        put("speed_parallel", speedParallel)
        put("speed_timeout_sec", speedTimeoutSec)
        put("accounts", JSONArray().apply { accounts.forEach { put(it.toJSON(true)) } })
    }

    fun speedOptionsJSON() = JSONObject().apply {
        put("proxy", listen)
        put("download_mib", speedDownloadMiB)
        put("upload_mib", speedUploadMiB)
        put("parallel", speedParallel)
        put("timeout_sec", speedTimeoutSec)
    }

    private fun tuningJSON(): JSONObject {
        val values = when (profile) {
            "Минимальная задержка" -> intArrayOf(1, 64, 128, 32, 4096, 16, 128, 8192, 1, 5)
            "Максимальная скорость" -> intArrayOf(3, 256, 512, 96, 16384, 48, 512, 32768, 2, 15)
            else -> intArrayOf(5, 192, 384, 64, 8192, 32, 256, 16384, 1, 10)
        }
        return JSONObject().apply {
            put("batch_delay_ms", values[0])
            put("batch_min_kb", values[1])
            put("batch_max_kb", values[2])
            put("stripe_data", true)
            put("stream_read_kb", values[3])
            put("stream_window_kb", values[4])
            put("ack_every_frames", values[5])
            put("send_queue_frames", values[6])
            put("reorder_max_kb", values[7])
            put("imap_append_workers", values[8])
            put("ping_interval_ms", values[9] * 1000)
            put("imap_idle_refresh_sec", 45)
            put("stats_interval_sec", 5)
            put("optimistic_open_ms", 20)
            put("purge_after_sec", 90)
            put("purge_every_sec", 30)
            put("purge_owner", "server")
        }
    }

    companion object {
        fun fromJSON(json: JSONObject): AppConfig {
            val config = AppConfig(
                listen = json.optString("listen", "127.0.0.1:1080"),
                target = json.optString("target", "direct"),
                dnsResolver = json.optString("dns_resolver", "1.1.1.1:53"),
                passphrase = json.optString("passphrase"),
                clientId = json.optInt("client_id", 2),
                folderSend = json.optString("folder_send", "Notes"),
                folderRecv = json.optString("folder_recv", "Journal"),
                logLevel = json.optString("log_level", "info"),
                autostart = json.optBoolean("autostart"),
                profile = json.optString("profile", "Стандартный"),
                speedDownloadMiB = json.optInt("speed_download_mib", 8),
                speedUploadMiB = json.optInt("speed_upload_mib", 8),
                speedParallel = json.optInt("speed_parallel", 4),
                speedTimeoutSec = json.optInt("speed_timeout_sec", 120),
            )
            val accounts = json.optJSONArray("accounts") ?: JSONArray()
            repeat(accounts.length()) { config.accounts += MailAccount.fromJSON(accounts.getJSONObject(it)) }
            return config
        }
    }
}

data class TransportSnapshot(
    val phase: String = "stopped",
    val stage: String = "Остановлен",
    val error: String = "",
    val listen: String = "127.0.0.1:1080",
    val clientId: Int = 2,
    val txBytes: Long = 0,
    val rxBytes: Long = 0,
    val streams: Long = 0,
    val rttMs: Long = 0,
    val lanes: Int = 0,
    val pendingBytes: Long = 0,
    val appends: Long = 0,
    val logs: List<String> = emptyList(),
) {
    companion object {
        fun fromJSON(raw: String): TransportSnapshot = runCatching {
            val json = JSONObject(raw)
            val logs = json.optJSONArray("logs") ?: JSONArray()
            TransportSnapshot(
                phase = json.optString("phase", "stopped"),
                stage = json.optString("stage", "Остановлен"),
                error = json.optString("error"),
                listen = json.optString("listen", "127.0.0.1:1080"),
                clientId = json.optInt("client_id", 2),
                txBytes = json.optLong("tx_bytes"),
                rxBytes = json.optLong("rx_bytes"),
                streams = json.optLong("active_streams"),
                rttMs = json.optLong("rtt_ms"),
                lanes = json.optInt("live_lanes"),
                pendingBytes = json.optLong("pending_bytes"),
                appends = json.optLong("appends"),
                logs = List(logs.length()) { logs.optString(it) },
            )
        }.getOrElse { TransportSnapshot(error = it.message.orEmpty()) }
    }
}
