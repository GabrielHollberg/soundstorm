package dev.soundstorm.app

import android.webkit.CookieManager
import java.net.CookieHandler
import java.net.URI

/**
 * The web view's cookies for every HttpURLConnection the app opens: the
 * native player's songs and covers, the notification's cover, photo backup.
 *
 * Each of those used to copy the session cookie into a "Cookie" header by
 * hand, and a header goes wherever the request is redirected - so a redirect
 * to another host would have carried the session there (a security review).
 * A cookie handler is asked afresh for each address, redirects included, and
 * answers with that address's own cookies only.
 */
object WebCookies : CookieHandler() {
    fun install() {
        if (getDefault() !== this) setDefault(this)
    }

    override fun get(uri: URI, requestHeaders: MutableMap<String, MutableList<String>>): MutableMap<String, MutableList<String>> {
        val scheme = uri.scheme?.lowercase()
        if (scheme != "http" && scheme != "https") return mutableMapOf()
        val cookie = runCatching { CookieManager.getInstance().getCookie(uri.toString()) }.getOrNull()
        return if (cookie.isNullOrEmpty()) mutableMapOf() else mutableMapOf("Cookie" to mutableListOf(cookie))
    }

    // The server sets its cookies through the page; nothing here keeps any.
    override fun put(uri: URI, responseHeaders: MutableMap<String, MutableList<String>>) {}
}
