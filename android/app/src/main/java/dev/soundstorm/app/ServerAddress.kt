package dev.soundstorm.app

import android.content.Context
import android.net.Uri
import org.json.JSONObject
import java.net.ConnectException
import java.net.HttpURLConnection
import java.net.SocketTimeoutException
import java.net.URL
import java.net.UnknownHostException
import javax.net.ssl.SSLException

/**
 * The one SoundStorm server this app talks to. Every install is someone's
 * own, so the address is asked for on first launch rather than built in -
 * as the iPhone app does (ios/SoundStorm/ServerAddress.swift).
 */
object ServerAddress {
    private const val PREFS = "soundstorm"
    private const val KEY = "serverURL"

    fun saved(context: Context): Uri? =
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY, null)?.let(::parse)

    fun save(context: Context, server: Uri?) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(KEY, server?.toString()).apply()
    }

    // Several saved servers, as the iPhone and Apple TV apps keep them
    // (ios/Shared/ServerAddress.swift): the latest used first. Sign-ins stay
    // with each, since cookies belong to an address, so switching signs
    // nobody out.
    private const val LIST_KEY = "servers"

    data class Server(val url: Uri, val name: String)

    fun all(context: Context): List<Server> {
        val raw = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(LIST_KEY, null)
        if (raw == null) {
            // From before the list: the one address in use seeds it.
            return saved(context)?.let { listOf(Server(it, defaultName(it))) } ?: emptyList()
        }
        val array = runCatching { org.json.JSONArray(raw) }.getOrNull() ?: return emptyList()
        val out = mutableListOf<Server>()
        for (i in 0 until array.length()) {
            val o = array.optJSONObject(i) ?: continue
            val url = parse(o.optString("url")) ?: continue
            if (out.any { origin(it.url) == origin(url) }) continue
            out += Server(url, o.optString("name").ifBlank { defaultName(url) })
        }
        return out
    }

    private fun store(context: Context, list: List<Server>) {
        val array = org.json.JSONArray()
        list.take(20).forEach { array.put(JSONObject().put("url", it.url.toString()).put("name", it.name)) }
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(LIST_KEY, array.toString()).apply()
    }

    /** The server now in use: to the top of the list, added if new. */
    fun remember(context: Context, url: Uri) {
        val list = all(context)
        val name = list.firstOrNull { origin(it.url) == origin(url) }?.name ?: defaultName(url)
        store(context, listOf(Server(url, name)) + list.filter { origin(it.url) != origin(url) })
        save(context, url)
    }

    fun forget(context: Context, url: Uri) {
        store(context, all(context).filter { origin(it.url) != origin(url) })
        if (saved(context)?.let { origin(it) == origin(url) } == true) save(context, null)
    }

    fun rename(context: Context, url: Uri, name: String) {
        store(context, all(context).map {
            if (origin(it.url) == origin(url)) it.copy(name = name.trim().ifBlank { defaultName(url) }) else it
        })
    }

    /** "SoundStorm abc123" for an install's own name, else the host. */
    fun defaultName(url: Uri): String {
        val host = url.host ?: return url.toString()
        for (suffix in listOf(".home.soundstorm.dev", ".net.soundstorm.dev")) {
            if (host.endsWith(suffix)) return "SoundStorm " + host.removeSuffix(suffix)
        }
        return host
    }

    /**
     * Turns what somebody typed into the server's root URL. Anything without a
     * scheme is https: the default install has a real certificate on its
     * soundstorm.dev name. A path is dropped - the app lives at the root.
     */
    fun parse(typed: String): Uri? {
        var text = typed.trim()
        if (text.isEmpty()) return null
        if (!text.contains("://")) text = "https://$text"
        val uri = Uri.parse(text)
        val scheme = uri.scheme?.lowercase() ?: return null
        if (scheme != "https" && scheme != "http") return null
        // Lowercase, as an origin is compared: a typed capital left the
        // page's messages unrecognised.
        val host = uri.host?.lowercase()
        if (host.isNullOrEmpty()) return null
        val authority = if (uri.port != -1) "$host:${uri.port}" else host
        return Uri.Builder().scheme(scheme).encodedAuthority(authority).path("/").build()
    }

    /** "scheme://host[:port]", the form an origin rule and an origin check use. */
    fun origin(server: Uri): String =
        "${server.scheme}://${server.host}" + if (server.port != -1) ":${server.port}" else ""

    /** The origin of the page showing now (set by MainActivity), which may
     *  be the install's secure name rather than the address typed. */
    @Volatile var current: String? = null

    /**
     * Whether an address the page hands the app is the server's own: the
     * native player, its covers and the notification fetch nothing else. A
     * page is somebody else's text as far as the app is concerned - the
     * review found any address, any scheme, was loaded with the session.
     */
    fun isServer(context: Context, url: String?): Boolean {
        if (url.isNullOrEmpty()) return false
        val u = runCatching { Uri.parse(url) }.getOrNull() ?: return false
        val scheme = u.scheme?.lowercase()
        if (scheme != "http" && scheme != "https") return false
        val o = origin(u).lowercase()
        if (current?.lowercase() == o) return true
        return saved(context)?.let { origin(it).lowercase() == o } == true
    }

    class CheckFailed(message: String) : Exception(message)

    /**
     * Asks the server's /healthz, which every SoundStorm answers with
     * {"status":"ok","sources":n}, so a typo that lands on some other web
     * server is caught here rather than as a strange page later. Blocking:
     * call it off the main thread.
     */
    fun check(server: Uri) {
        val host = server.host ?: server.toString()
        val conn = try {
            (URL(origin(server) + "/healthz").openConnection() as HttpURLConnection).apply {
                connectTimeout = 10_000
                readTimeout = 10_000
                useCaches = false
            }
        } catch (e: Exception) {
            throw CheckFailed("That doesn't look like a web address.")
        }
        try {
            val code = conn.responseCode
            val body = (if (code in 200..299) conn.inputStream else conn.errorStream)
                ?.bufferedReader()?.use { it.readText() }.orEmpty()
            val ok = code == 200 && runCatching {
                val json = JSONObject(body)
                json.optString("status") == "ok" && json.has("sources")
            }.getOrDefault(false)
            if (!ok) throw CheckFailed("Something answered at that address, but it isn't SoundStorm.")
        } catch (e: CheckFailed) {
            throw e
        } catch (e: SSLException) {
            throw CheckFailed("$host has a certificate this phone doesn't trust. " +
                "Use the soundstorm.dev address SoundStorm gave you.")
        } catch (e: UnknownHostException) {
            throw CheckFailed("Couldn't find $host. Check the address.")
        } catch (e: SocketTimeoutException) {
            throw CheckFailed("Couldn't reach $host. Is SoundStorm running, and is this phone on a network that can reach it?")
        } catch (e: ConnectException) {
            throw CheckFailed("Couldn't reach $host. Is SoundStorm running, and is this phone on a network that can reach it?")
        } catch (e: Exception) {
            throw CheckFailed(e.message ?: "Couldn't reach $host.")
        } finally {
            conn.disconnect()
        }
    }

    /**
     * The addresses worth trying for what was typed, best first - as the
     * iPhone and Apple TV apps try them (ServerAddress.candidates). Just the
     * install's code ("abc123", or "abc123.soundstorm.dev") is its home and
     * away names, with and without :8099; a soundstorm.dev name typed without
     * its port is tried on SoundStorm's own port first (the away name is
     * "<id>.net.soundstorm.dev:8099", and typed without the port it found
     * nothing). A phone prefers the away name, which works anywhere (the page
     * moves itself to the home name when it can); a TV, which stays put, the
     * home name. Null when it is not an address at all.
     */
    fun candidates(typed: String, preferAway: Boolean): List<Uri>? {
        val text = typed.trim().lowercase()
        val bare = text.removeSuffix(".soundstorm.dev")
        if (!text.contains("://") && Regex("^[a-z0-9][a-z0-9-]{2,62}$").matches(bare) &&
            bare != "localhost" && !bare.all { it.isDigit() }) {
            val levels = if (preferAway) listOf("net", "home") else listOf("home", "net")
            return levels.flatMap { level ->
                listOf("https://$bare.$level.soundstorm.dev:8099", "https://$bare.$level.soundstorm.dev").mapNotNull(::parse)
            }
        }
        val url = parse(typed) ?: return null
        if (url.port != -1 || url.scheme != "https" || url.host?.endsWith(".soundstorm.dev") != true) return listOf(url)
        val withPort = parse("https://${url.host}:8099") ?: return listOf(url)
        return listOf(withPort, url)
    }

    /**
     * The best of the candidates that answers like SoundStorm. All are asked
     * at once, so a home name that cannot be reached from here costs no wait
     * beyond the slowest. Blocking: call it off the main thread.
     */
    fun find(typed: String, preferAway: Boolean): Uri {
        val list = candidates(typed, preferAway) ?: throw CheckFailed("That doesn't look like a web address.")
        if (list.size == 1) {
            check(list[0])
            return list[0]
        }
        val pool = java.util.concurrent.Executors.newFixedThreadPool(list.size)
        try {
            val results = list.map { url -> pool.submit<String?> { try { check(url); null } catch (e: CheckFailed) { e.message } } }
            // The best that answered, once nothing better can.
            for ((i, f) in results.withIndex()) {
                if (runCatching { f.get() }.getOrDefault("") == null) return list[i]
            }
            if (list.size > 2 && !typed.contains('.')) {
                throw CheckFailed("Couldn't reach a server with the code ${typed.trim()}, at home or away. Check the code - it is in SoundStorm's Settings, Use on your phone or TV - and, away from home, that remote access is on.")
            }
            throw CheckFailed(runCatching { results.last().get() }.getOrNull() ?: "Something answered at that address, but it isn't SoundStorm.")
        } finally {
            pool.shutdownNow()
        }
    }
}
