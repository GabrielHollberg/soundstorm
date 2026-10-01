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
}
