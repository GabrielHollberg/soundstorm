package dev.soundstorm.app

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.webkit.WebView
import androidx.core.content.ContextCompat
import org.json.JSONObject
import java.lang.ref.WeakReference

/**
 * What the page says is playing, and the way back to it.
 *
 * The page describes its audio through navigator.mediaSession (see PageScript);
 * that arrives here as [NowPlaying]. The playback service shows it on the lock
 * screen and in the notification, and a button pressed there comes back
 * through [dispatch] to the page's own handler - the same one a browser's
 * media controls would call. The page keeps doing all the playing.
 */
object MediaBridge {
    data class NowPlaying(
        val state: String,
        val title: String,
        val artist: String,
        val album: String,
        val artwork: String?,
        val actions: Set<String>,
        val durationMs: Long,
        val positionMs: Long,
        val rate: Float,
        val at: Long,
    )

    private val main = Handler(Looper.getMainLooper())
    private var webView = WeakReference<WebView>(null)

    @Volatile
    var current: NowPlaying? = null
        private set

    /** The service, while it runs, is told of every change. */
    var listener: ((NowPlaying) -> Unit)? = null

    /** Something other than the page paused the music (an alarm, a call). */
    @Volatile
    var interrupted = false
        private set

    /** The service, while it runs, is told when that starts and ends. */
    var onInterruption: ((Boolean) -> Unit)? = null

    fun interruption(on: Boolean) {
        // Only while the service runs to watch it through: otherwise nothing
        // would ever clear it.
        if (on && listener == null) return
        interrupted = on
        main.post { onInterruption?.invoke(on) }
    }

    fun attach(view: WebView) {
        webView = WeakReference(view)
    }

    fun detach(view: WebView) {
        if (webView.get() === view) webView = WeakReference(null)
    }

    /** A "media" message from the page. */
    fun update(context: Context, message: JSONObject) {
        val meta = message.optJSONObject("metadata")
        val position = message.optJSONObject("position")
        val actions = buildSet {
            val list = message.optJSONArray("actions")
            if (list != null) for (i in 0 until list.length()) add(list.optString(i))
        }
        val artwork = meta?.optJSONArray("artwork")?.let { list ->
            (0 until list.length()).map { list.optString(it) }.lastOrNull { it.isNotEmpty() }
        }
        val now = NowPlaying(
            state = message.optString("state", "none"),
            title = meta?.optString("title").orEmpty(),
            artist = meta?.optString("artist").orEmpty(),
            album = meta?.optString("album").orEmpty(),
            artwork = artwork,
            actions = actions,
            durationMs = ((position?.optDouble("duration", 0.0) ?: 0.0) * 1000).toLong(),
            positionMs = ((position?.optDouble("position", 0.0) ?: 0.0) * 1000).toLong(),
            rate = (position?.optDouble("playbackRate", 1.0) ?: 1.0).toFloat(),
            at = android.os.SystemClock.elapsedRealtime(),
        )
        current = now
        main.post {
            val playing = now.state == "playing"
            val showing = listener != null
            when {
                // Playing starts the service, which goes to the foreground
                // straight away: that is what keeps the page playing with
                // the screen off.
                playing && !showing -> ContextCompat.startForegroundService(
                    context, Intent(context, PlaybackService::class.java))
                showing -> listener?.invoke(now)
            }
        }
    }

    /** A button pressed on the lock screen, the notification or headphones. */
    fun dispatch(action: String, details: JSONObject? = null) {
        main.post {
            val view = webView.get() ?: return@post
            val args = details?.toString() ?: "{}"
            view.evaluateJavascript(
                "window.__soundstormMediaAction && window.__soundstormMediaAction(" +
                    JSONObject.quote(action) + ", " + args + ")", null)
        }
    }
}
