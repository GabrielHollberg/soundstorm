package dev.soundstorm.app

import android.net.Uri
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.Inet4Address
import java.net.NetworkInterface
import java.net.URL
import java.util.concurrent.Callable
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/**
 * Finding a SoundStorm server on the network the phone or TV is on, so
 * nobody setting one up has to type an address - as the iPhone and Apple TV
 * apps find one (ios/Shared/ServerDiscovery.swift).
 *
 * Not by mDNS: SoundStorm runs in Docker, which on Windows and macOS keeps a
 * container's announcements off the home network. Every address on the
 * device's own network is asked whether SoundStorm answers on its port
 * (/healthz says so) - a few hundred quick questions at once, a few seconds.
 * One that answers is asked its secure home name (/api/session's
 * secureName), which is kept instead of the bare address when it answers too.
 */
object ServerDiscovery {
    data class Found(val url: Uri) {
        /** What to call it on screen: the code, for a home name. */
        val label: String get() = (url.host ?: url.toString()).removeSuffix(".home.soundstorm.dev")
    }

    /** SoundStorm's own port; a server moved to another is typed in. */
    private const val PORT = 8099

    /** The servers on this device's network, the best address of each. Blocking. */
    fun search(): List<Found> {
        val hosts = neighbours()
        if (hosts.isEmpty()) return emptyList()
        val pool = Executors.newFixedThreadPool(128)
        try {
            val answering = pool.invokeAll(hosts.map { host ->
                Callable { "http://$host:$PORT".takeIf { isSoundStorm(it, 1200) } }
            }, 30, TimeUnit.SECONDS).mapNotNull { runCatching { it.get() }.getOrNull() }.sorted()
            return answering.map { plain -> Found(Uri.parse(secureName(plain) ?: "$plain/")) }
                .distinctBy { it.url.toString() }.sortedBy { it.label }
        } finally {
            pool.shutdownNow()
        }
    }

    private fun get(url: String, timeout: Int): String? = runCatching {
        val conn = URL(url).openConnection() as HttpURLConnection
        conn.connectTimeout = timeout
        conn.readTimeout = timeout
        conn.instanceFollowRedirects = false
        conn.useCaches = false
        try {
            if (conn.responseCode != 200) null else conn.inputStream.bufferedReader().use { it.readText().take(64 * 1024) }
        } finally {
            conn.disconnect()
        }
    }.getOrNull()

    private fun isSoundStorm(base: String, timeout: Int = 2000): Boolean {
        val body = get("$base/healthz", timeout) ?: return false
        return runCatching { JSONObject(body).let { it.optString("status") == "ok" && it.has("sources") } }.getOrDefault(false)
    }

    /** The install's secure home name, if it has one and it answers from here. */
    private fun secureName(plain: String): String? {
        val body = get("$plain/api/session", 3000) ?: return null
        val name = runCatching { JSONObject(body).optString("secureName") }.getOrNull()?.lowercase() ?: return null
        if (!Regex("^[a-z0-9][a-z0-9.-]*\\.soundstorm\\.dev$").matches(name)) return null
        val secure = "https://$name:$PORT"
        return if (isSoundStorm(secure)) "$secure/" else null
    }

    /**
     * Every address on the device's own network (its IPv4 /24), and on
     * 192.168.0.x and 192.168.1.x - the networks routers most often make, for
     * a router inside a router (the owner's projector is on 192.168.86.x
     * inside 192.168.0.x, where the server is). Private networks only.
     */
    private fun neighbours(): List<String> {
        val mine = mutableSetOf<Int>()
        runCatching {
            for (nif in NetworkInterface.getNetworkInterfaces()) {
                if (!nif.isUp || nif.isLoopback) continue
                // Wi-Fi and wired only: mobile data has private addresses
                // too, and a carrier's network is no place to look.
                if (!nif.name.startsWith("wlan") && !nif.name.startsWith("eth")) continue
                for (a in nif.inetAddresses) {
                    if (a !is Inet4Address) continue
                    val b = a.address.map { it.toInt() and 255 }
                    val private = b[0] == 10 || (b[0] == 172 && b[1] in 16..31) || (b[0] == 192 && b[1] == 168)
                    if (private) mine += (b[0] shl 24) or (b[1] shl 16) or (b[2] shl 8)
                }
            }
        }
        if (mine.isEmpty()) return emptyList()
        mine += (192 shl 24) or (168 shl 16)
        mine += (192 shl 24) or (168 shl 16) or (1 shl 8)
        return mine.flatMap { base ->
            (1..254).map { n ->
                val v = base or n
                "${v ushr 24}.${(v shr 16) and 255}.${(v shr 8) and 255}.${v and 255}"
            }
        }.distinct()
    }
}
