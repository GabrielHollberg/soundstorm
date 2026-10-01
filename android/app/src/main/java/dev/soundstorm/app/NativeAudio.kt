package dev.soundstorm.app

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.webkit.WebView
import androidx.annotation.OptIn
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.PlaybackException
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import org.json.JSONObject
import java.lang.ref.WeakReference

/**
 * The page's audio element, played natively (AudioService).
 *
 * PageScript gives the page's `<audio id="audio-player">` a stand-in: setting
 * its src to a song on the server, play(), pause(), seeking, volume and speed
 * arrive here as "audio" messages, and what the player does goes back as
 * events the stand-in turns into the element's own (playing, pause, ended,
 * timeupdate...). The page cannot tell. A song kept on the device (a blob:
 * address, from the downloads) is not the server's and plays in the page as
 * before.
 *
 * The next song is handed over ahead ("queue"), so the player moves into it
 * itself - gapless, and never stopping between songs, which is what kept the
 * music going with the screen off. The page is told the song ended, sets the
 * next one as it always does, and finds it already playing.
 */
@OptIn(UnstableApi::class)
object NativeAudio {
    private val main = Handler(Looper.getMainLooper())
    private var player: ExoPlayer? = null
    private var webView = WeakReference<WebView>(null)
    /** Messages that arrived before the service had its player. */
    private val waiting = mutableListOf<JSONObject>()
    private var app: Context? = null
    private var seeking = false

    fun attachView(view: WebView) {
        webView = WeakReference(view)
    }

    fun detachView(view: WebView) {
        if (webView.get() === view) webView = WeakReference(null)
    }

    fun attach(p: ExoPlayer) {
        player = p
        p.addListener(listener)
        val pending = waiting.toList()
        waiting.clear()
        pending.forEach { run(it) }
    }

    fun detach() {
        player?.removeListener(listener)
        player = null
        main.removeCallbacks(tick)
    }

    /** The player's side of a playback report (PlayerLog). */
    fun describe(): String {
        val p = player ?: return " player=none"
        return " player: volume=${p.volume} state=${p.playbackState} playing=${p.isPlaying}" +
            " playWhenReady=${p.playWhenReady} suppressed=${p.playbackSuppressionReason}" +
            " at=${p.currentPosition}ms of ${p.duration}ms song=${PlayerLog.song(p.currentMediaItem?.localConfiguration?.uri?.toString())}" +
            " queued=${p.mediaItemCount}"
    }

    /** Whether the player holds a song (the page's audio is native now). */
    val active: Boolean get() = (player?.mediaItemCount ?: 0) > 0 || waiting.isNotEmpty()

    /** An "audio" message from the page. */
    fun handle(context: Context, message: JSONObject) {
        app = context.applicationContext
        if (player == null) {
            // The service makes the player when it starts; until then, kept.
            if (message.optString("cmd") == "stop") {
                waiting.clear()
                return
            }
            waiting.add(message)
            // Refused from the background (Android 8 and later); kept, and
            // tried again with the next message rather than crashing.
            runCatching { context.startService(Intent(context, AudioService::class.java)) }
            return
        }
        run(message)
    }

    private fun run(m: JSONObject) {
        val p = player ?: return
        val cmd = m.optString("cmd")
        when (cmd) {
            "volume" -> PlayerLog.add("page: volume ${m.optDouble("v", 1.0)}")
            "rate" -> {}
            "load", "queue" -> PlayerLog.add("page: $cmd ${PlayerLog.song(m.optString("url"))}")
            "seek" -> PlayerLog.add("page: seek ${m.optDouble("s", 0.0)}s")
            "upcoming" -> PlayerLog.add("page: upcoming ${m.optJSONArray("items")?.length() ?: 0} songs")
            else -> PlayerLog.add("page: $cmd")
        }
        when (cmd) {
            "load" -> {
                val url = m.optString("url")
                if (!allowed(url)) return
                val index = p.currentMediaItemIndex
                val current = p.currentMediaItem?.localConfiguration?.uri?.toString()
                if (current == url && p.playbackState != Player.STATE_IDLE) {
                    // Already this song: the player moved into it by itself -
                    // or it played to its end and is asked for again, which
                    // starts it over (a finished player plays nothing until it
                    // is sent back to the start: songs would not play again).
                    if (p.playbackState == Player.STATE_ENDED) p.seekToDefaultPosition()
                    report()
                    return
                }
                // The one queued next, reached early (a skip): move to it.
                val next = if (index + 1 < p.mediaItemCount) p.getMediaItemAt(index + 1) else null
                if (next?.localConfiguration?.uri?.toString() == url) {
                    p.seekToDefaultPosition(index + 1)
                    p.removeMediaItems(0, index + 1)
                } else {
                    p.setMediaItem(item(url, null))
                    p.prepare()
                }
                report()
            }
            "queue" -> {
                val url = m.optString("url")
                if (!allowed(url) || p.mediaItemCount == 0) return
                val index = p.currentMediaItemIndex
                // Only ever the one song after this one.
                if (index + 1 < p.mediaItemCount) p.removeMediaItems(index + 1, p.mediaItemCount)
                p.addMediaItem(item(url, null))
            }
            // The songs after this one, several of them, each with what it
            // is: if Android ends the page while the app is in the
            // background, the player carries on through them by itself, the
            // lock screen still naming each (it stopped at the first song
            // the page was not there to hand over).
            "upcoming" -> {
                if (p.mediaItemCount == 0) return
                val index = p.currentMediaItemIndex
                if (index + 1 < p.mediaItemCount) p.removeMediaItems(index + 1, p.mediaItemCount)
                val list = m.optJSONArray("items") ?: return
                val items = (0 until minOf(list.length(), 50)).mapNotNull { i ->
                    val o = list.optJSONObject(i) ?: return@mapNotNull null
                    val url = o.optString("url")
                    if (!allowed(url)) return@mapNotNull null
                    MediaItem.Builder().setUri(url).setMediaId(url)
                        .setMediaMetadata(MediaMetadata.Builder()
                            .setTitle(o.optString("title").ifEmpty { null })
                            .setArtist(o.optString("artist").ifEmpty { null })
                            .setAlbumTitle(o.optString("album").ifEmpty { null })
                            .setArtworkUri(o.optString("art").takeIf(::allowed)?.let(Uri::parse))
                            .build())
                        .build()
                }
                if (items.isNotEmpty()) p.addMediaItems(items)
            }
            "unqueue" -> {
                val index = p.currentMediaItemIndex
                if (index + 1 < p.mediaItemCount) p.removeMediaItems(index + 1, p.mediaItemCount)
            }
            "play" -> {
                if (p.playbackState == Player.STATE_IDLE) p.prepare()
                // Played to its end: play from the start, as the page's own
                // audio element does.
                if (p.playbackState == Player.STATE_ENDED) p.seekToDefaultPosition()
                p.play()
            }
            "pause" -> p.pause()
            // What is playing, asked by a page made again after Android ended
            // the last one, to take the song over rather than show nothing.
            "state" -> report()
            "seek" -> {
                seeking = true
                p.seekTo((m.optDouble("s", 0.0) * 1000).toLong().coerceAtLeast(0))
            }
            "volume" -> p.volume = m.optDouble("v", 1.0).toFloat().coerceIn(0f, 1f)
            "rate" -> p.playbackParameters = PlaybackParameters(m.optDouble("r", 1.0).toFloat().coerceIn(0.25f, 4f))
            "stop" -> {
                // Paused too, or the next song loaded played before the page
                // asked it to (a review).
                p.pause()
                p.stop()
                p.clearMediaItems()
                report()
            }
        }
    }

    /** What the page says is playing, for the lock screen and notification. */
    fun metadata(now: MediaBridge.NowPlaying) {
        main.post {
            val p = player ?: return@post
            val current = p.currentMediaItem ?: return@post
            val meta = meta(now)
            if (current.mediaMetadata == meta) return@post
            p.replaceMediaItem(p.currentMediaItemIndex, current.buildUpon().setMediaMetadata(meta).build())
        }
    }

    /** Next or previous from the lock screen, a notification or a car. */
    fun skip(next: Boolean) {
        val actions = MediaBridge.current?.actions.orEmpty()
        when {
            next && "nexttrack" in actions -> MediaBridge.dispatch("nexttrack")
            next && "seekforward" in actions -> MediaBridge.dispatch("seekforward")
            !next && "previoustrack" in actions -> MediaBridge.dispatch("previoustrack")
            !next && "seekbackward" in actions -> MediaBridge.dispatch("seekbackward")
            !next -> player?.seekTo(0)
        }
    }

    /** Only the server's own addresses: a page could otherwise have the
     *  player fetch anything, anywhere, with the session. */
    private fun allowed(url: String?): Boolean {
        val c = app ?: return false
        return ServerAddress.isServer(c, url)
    }

    private fun item(url: String, now: MediaBridge.NowPlaying?): MediaItem =
        MediaItem.Builder().setUri(url).setMediaId(url)
            .apply { if (now != null) setMediaMetadata(meta(now)) }
            .build()

    private fun meta(now: MediaBridge.NowPlaying): MediaMetadata =
        MediaMetadata.Builder()
            .setTitle(now.title.ifEmpty { null })
            .setArtist(now.artist.ifEmpty { null })
            .setAlbumTitle(now.album.ifEmpty { null })
            .setArtworkUri(now.artwork?.takeIf(::allowed)?.let(Uri::parse))
            .build()

    private val listener = object : Player.Listener {
        override fun onEvents(player: Player, events: Player.Events) {
            report()
        }

        override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
            if (reason == Player.MEDIA_ITEM_TRANSITION_REASON_AUTO) {
                // Into the queued song by itself: the page hears the last one
                // end, and sets this one, which it finds already playing.
                player?.let { p -> if (p.currentMediaItemIndex > 0) p.removeMediaItems(0, p.currentMediaItemIndex) }
                send(JSONObject().put("ev", "ended").put("next", mediaItem?.localConfiguration?.uri?.toString()))
            }
        }

        override fun onPlayerError(error: PlaybackException) {
            send(state().put("error", 4))
        }
    }

    private val tick = object : Runnable {
        override fun run() {
            report()
        }
    }

    private fun state(): JSONObject {
        val p = player
        val o = JSONObject().put("ev", "state")
        if (p == null || p.mediaItemCount == 0) return o.put("state", "idle").put("playing", false).put("pwr", false)
        val duration = p.duration
        return o.put("state", when (p.playbackState) {
            Player.STATE_BUFFERING -> "buffering"
            Player.STATE_READY -> "ready"
            Player.STATE_ENDED -> "ended"
            else -> "idle"
        })
            .put("playing", p.isPlaying)
            .put("pwr", p.playWhenReady)
            .put("url", p.currentMediaItem?.localConfiguration?.uri?.toString())
            .put("position", p.currentPosition / 1000.0)
            .put("duration", if (duration > 0) duration / 1000.0 else JSONObject.NULL)
            .put("buffered", p.bufferedPosition / 1000.0)
            .put("rate", p.playbackParameters.speed.toDouble())
            .put("volume", p.volume.toDouble())
            .put("seeked", seeking && p.playbackState != Player.STATE_BUFFERING)
    }

    private fun report() {
        main.removeCallbacks(tick)
        val s = state()
        if (s.optBoolean("seeked")) seeking = false
        send(s)
        // While playing, the position every half second: the page runs its
        // own clock between, from the moment each report was made.
        if (player?.isPlaying == true) main.postDelayed(tick, 500)
    }

    private fun send(event: JSONObject) {
        main.post {
            val view = webView.get() ?: return@post
            view.evaluateJavascript("window.__soundstormAudio && window.__soundstormAudio($event)", null)
        }
    }
}
