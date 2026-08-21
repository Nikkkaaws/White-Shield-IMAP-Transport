package io.whiteshield.wsit

import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import hev.htproxy.TProxyService
import java.io.File

class AndroidVpnTunnel(private val service: VpnService) {
    private var tun: ParcelFileDescriptor? = null
    private var native: TProxyService? = null
    private val configFile = File(service.cacheDir, "tun2socks.yml")

    fun establish() {
        check(tun == null) { "Android VPN уже создан" }
        val builder = service.Builder()
            .setBlocking(false)
            .setSession("WSIT")
            .setMtu(MTU)
            .addAddress(IPV4, 32)
            .addRoute("0.0.0.0", 0)
            .addAddress(IPV6, 128)
            .addRoute("::", 0)
            .addDnsServer(MAPPED_DNS)
            .addDisallowedApplication(service.packageName)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) builder.setMetered(false)
        tun = checkNotNull(builder.establish()) { "Android не создал VPN-интерфейс" }
    }

    fun startSocksForwarder(host: String, port: Int) {
        val descriptor = checkNotNull(tun) { "VPN-интерфейс не создан" }
        require(host == "127.0.0.1" || host == "localhost") {
            "Встроенный Android VPN поддерживает локальный SOCKS5"
        }
        configFile.writeText(config(host, port))
        val next = TProxyService()
        check(next.TProxyStartService(configFile.absolutePath, descriptor.fd)) {
            "Не удалось запустить встроенный SOCKS-переходник"
        }
        Thread.sleep(120)
        check(next.TProxyIsRunning()) { "Встроенный SOCKS-переходник остановился при запуске" }
        native = next
    }

    fun stop() {
        runCatching { native?.TProxyStopService() }
        native = null
        runCatching { tun?.close() }
        tun = null
        runCatching { configFile.delete() }
    }

    private fun config(host: String, port: Int) = """
        misc:
          task-stack-size: 81920
        tunnel:
          mtu: $MTU
          icmp: 'reply'
        socks5:
          port: $port
          address: '$host'
          udp: 'udp'
        mapdns:
          address: $MAPPED_DNS
          port: 53
          network: 240.0.0.0
          netmask: 240.0.0.0
          cache-size: 10000
        """.trimIndent() + "\n"

    companion object {
        private const val MTU = 8500
        private const val IPV4 = "198.18.0.1"
        private const val IPV6 = "fc00::1"
        private const val MAPPED_DNS = "198.18.0.2"
    }
}
