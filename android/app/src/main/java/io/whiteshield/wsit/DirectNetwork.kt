package io.whiteshield.wsit

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.SystemClock
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

object DirectNetwork {
    private val lock = Any()

    @Volatile
    private var active: Network? = null

    @Volatile
    private var error: String = ""

    private var started = false
    private var ready = CountDownLatch(1)

    fun ensure(context: Context) {
        synchronized(lock) {
            if (started) return
            started = true
        }
        val manager = context.applicationContext
            .getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            .build()
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                synchronized(lock) {
                    if (active != null) return
                    if (!manager.bindProcessToNetwork(network)) {
                        error = "Android не разрешил привязать WSIT к прямой сети"
                        ready.countDown()
                        return
                    }
                    active = network
                    error = ""
                    ready.countDown()
                }
            }

            override fun onLost(network: Network) {
                synchronized(lock) {
                    if (active != network) return
                    manager.bindProcessToNetwork(null)
                    active = null
                    error = "Прямая сеть потеряна; ожидается переподключение"
                    ready = CountDownLatch(1)
                }
            }

            override fun onUnavailable() {
                synchronized(lock) {
                    error = "Физическая сеть без VPN недоступна"
                    ready.countDown()
                }
            }
        }
        runCatching { manager.requestNetwork(request, callback) }
            .onFailure { failure ->
                synchronized(lock) {
                    error = failure.message ?: "Не удалось запросить прямую сеть"
                    ready.countDown()
                }
            }
    }

    fun await(context: Context, timeoutMs: Long = 8_000): String {
        ensure(context)
        val deadline = SystemClock.elapsedRealtime() + timeoutMs
        while (active == null) {
            val remaining = deadline - SystemClock.elapsedRealtime()
            if (remaining <= 0) return error.ifBlank { "Не найдена физическая сеть без VPN" }
            val currentLatch = synchronized(lock) { ready }
            currentLatch.await(remaining, TimeUnit.MILLISECONDS)
            if (active == null && error.isNotBlank()) return error
        }
        return ""
    }

    fun isBound(): Boolean = active != null
}
