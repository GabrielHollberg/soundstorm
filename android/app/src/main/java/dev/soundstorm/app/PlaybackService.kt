package dev.soundstorm.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.media.AudioAttributes
import android.media.AudioManager
import android.os.Handler
import android.os.Looper
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.os.Build
import android.os.IBinder
import android.os.SystemClock
import android.support.v4.media.MediaMetadataCompat
import android.support.v4.media.session.MediaSessionCompat
import android.support.v4.media.session.PlaybackStateCompat
import android.webkit.CookieManager
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.media.app.NotificationCompat.MediaStyle
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.Executors

/**
 * Keeps SoundStorm playing with the screen off, and gives it its lock-screen,
 * notification, headphone and Bluetooth controls.
 *
 * A foreground service of the media-playback kind is what Android requires
 * before it lets a backgrounded app go on playing, and its notification is
 * the media controls. The audio itself stays in the page: this only shows
 * what the page says is playing (MediaBridge) and hands the buttons back.
 */
class PlaybackService : Service() {
    private lateinit var session: MediaSessionCompat
    private val artLoader = Executors.newSingleThreadExecutor()
    private var artUrl: String? = null
    private var art: Bitmap? = null
    private var foreground = false
    private val main = Handler(Looper.getMainLooper())
    private var watchStarted = 0L
    private var interrupterSeen = false

    override fun onCreate() {
        super.onCreate()
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(
            CHANNEL, getString(R.string.channel_playback), NotificationManager.IMPORTANCE_LOW,
        ).apply { setShowBadge(false) })

        session = MediaSessionCompat(this, "SoundStorm").apply {
            setCallback(object : MediaSessionCompat.Callback() {
                override fun onPlay() = MediaBridge.dispatch("play")
                override fun onPause() = MediaBridge.dispatch("pause")
                override fun onSkipToNext() = MediaBridge.dispatch("nexttrack")
                override fun onSkipToPrevious() = MediaBridge.dispatch("previoustrack")
                override fun onStop() = MediaBridge.dispatch("stop")
                override fun onFastForward() = MediaBridge.dispatch("seekforward")
                override fun onRewind() = MediaBridge.dispatch("seekbackward")
                override fun onSeekTo(pos: Long) =
                    MediaBridge.dispatch("seekto", JSONObject().put("seekTime", pos / 1000.0))
            })
            setSessionActivity(openApp())
            isActive = true
        }
        MediaBridge.listener = { show(it) }
        MediaBridge.onInterruption = { on -> if (on) startWatching() else stopWatching() }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val action = intent?.action
        if (action != null) {
            // A notification button, handed to the page as the lock screen's
            // are; the page's answer (playing, paused, closed) updates the
            // notification. Not shown again from here, or swiping a paused
            // one away would put it straight back.
            MediaBridge.dispatch(action)
        } else {
            // Started by MediaBridge because something began playing: to the
            // foreground straight away, as Android requires.
            show(MediaBridge.current)
        }
        return START_NOT_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        main.removeCallbacks(detach)
        stopWatching()
        MediaBridge.onInterruption = null
        MediaBridge.listener = null
        session.release()
        artLoader.shutdownNow()
        super.onDestroy()
    }

    private fun show(now: MediaBridge.NowPlaying?) {
        if (now == null || now.state == "none") {
            // Nothing playing any more (the player was closed): go away.
            ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
            foreground = false
            stopSelf()
            return
        }
        val playing = now.state == "playing"
        loadArt(now.artwork)
        session.setMetadata(MediaMetadataCompat.Builder()
            .putString(MediaMetadataCompat.METADATA_KEY_TITLE, now.title)
            .putString(MediaMetadataCompat.METADATA_KEY_ARTIST, now.artist)
            .putString(MediaMetadataCompat.METADATA_KEY_ALBUM, now.album)
            .putLong(MediaMetadataCompat.METADATA_KEY_DURATION, if (now.durationMs > 0) now.durationMs else -1)
            .apply { art?.let { putBitmap(MediaMetadataCompat.METADATA_KEY_ALBUM_ART, it) } }
            .build())

        var actions = PlaybackStateCompat.ACTION_PLAY_PAUSE or PlaybackStateCompat.ACTION_PLAY or
            PlaybackStateCompat.ACTION_PAUSE or PlaybackStateCompat.ACTION_STOP
        if ("nexttrack" in now.actions) actions = actions or PlaybackStateCompat.ACTION_SKIP_TO_NEXT
        if ("previoustrack" in now.actions) actions = actions or PlaybackStateCompat.ACTION_SKIP_TO_PREVIOUS
        if ("seekto" in now.actions) actions = actions or PlaybackStateCompat.ACTION_SEEK_TO
        if ("seekforward" in now.actions) actions = actions or PlaybackStateCompat.ACTION_FAST_FORWARD
        if ("seekbackward" in now.actions) actions = actions or PlaybackStateCompat.ACTION_REWIND
        // The position as of when the page said it, which Android carries
        // forward at the playing rate itself.
        val elapsed = if (playing) ((SystemClock.elapsedRealtime() - now.at) * now.rate).toLong() else 0L
        session.setPlaybackState(PlaybackStateCompat.Builder()
            .setActions(actions)
            .setState(
                if (playing) PlaybackStateCompat.STATE_PLAYING else PlaybackStateCompat.STATE_PAUSED,
                now.positionMs + elapsed,
                if (playing) now.rate else 0f,
                SystemClock.elapsedRealtime(),
            )
            .build())

        val notification = build(now, playing)
        if (playing) {
            main.removeCallbacks(detach)
            startInForeground(notification)
            return
        }
        // Paused by an alarm or a call: stay in the foreground, so Android
        // does not end the app before it can play again (startWatching).
        if (MediaBridge.interrupted) {
            main.removeCallbacks(detach)
            startInForeground(notification)
            return
        }
        // Started straight into a pause (a button pressed as the page
        // paused): Android still needs the foreground call first.
        if (!foreground) startInForeground(notification)
        getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification)
        // Still in the foreground for a while. Between songs the page is
        // paused for the moment the next one takes to arrive, and leaving the
        // foreground then meant the next song, starting with the screen off,
        // had to come back into it from the background - which Android
        // refuses, and the music stopped after one song (the owner's report).
        // A real pause lets go after twenty seconds: then the notification can
        // be swiped away and Android may stop the service, as with any paused
        // player.
        main.removeCallbacks(detach)
        main.postDelayed(detach, 20_000)
    }

    private val detach = Runnable {
        if (MediaBridge.current?.state == "playing" || MediaBridge.interrupted) return@Runnable
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_DETACH)
        foreground = false
        MediaBridge.current?.let { now ->
            if (now.state != "none") getSystemService(NotificationManager::class.java).notify(NOTIFICATION, build(now, false))
        }
    }

    // Into the foreground, as a media player. Android can refuse this from the
    // background; refused, the notification is shown anyway rather than the
    // service - and with it the app - falling over.
    private fun startInForeground(notification: Notification) {
        try {
            ServiceCompat.startForeground(this, NOTIFICATION, notification,
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK else 0)
            foreground = true
        } catch (_: IllegalStateException) {
            getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification)
        }
    }

    private fun build(now: MediaBridge.NowPlaying, playing: Boolean): Notification {
        val builder = NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_soundstorm)
            .setContentTitle(now.title.ifEmpty { getString(R.string.app_name) })
            .setContentText(now.artist)
            .setSubText(now.album.ifEmpty { null })
            .setLargeIcon(art)
            .setContentIntent(openApp())
            .setDeleteIntent(button("stop", 9))
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            .setOnlyAlertOnce(true)
            .setOngoing(playing)
        val compact = mutableListOf<Int>()
        if ("previoustrack" in now.actions) {
            builder.addAction(android.R.drawable.ic_media_previous, "Previous", button("previoustrack", 1))
            compact += compact.size
        }
        builder.addAction(
            if (playing) android.R.drawable.ic_media_pause else android.R.drawable.ic_media_play,
            if (playing) "Pause" else "Play",
            button(if (playing) "pause" else "play", 2))
        compact += compact.size
        if ("nexttrack" in now.actions) {
            builder.addAction(android.R.drawable.ic_media_next, "Next", button("nexttrack", 3))
            compact += compact.size
        }
        builder.setStyle(MediaStyle()
            .setMediaSession(session.sessionToken)
            .setShowActionsInCompactView(*compact.toIntArray()))
        return builder.build()
    }

    private fun button(action: String, code: Int): PendingIntent = PendingIntent.getService(
        this, code, Intent(this, PlaybackService::class.java).setAction(action),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)

    private fun openApp(): PendingIntent = PendingIntent.getActivity(
        this, 0, Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)

    /**
     * The cover, fetched with the page's own session cookie - covers are
     * served only to someone signed in - and shown once it arrives.
     */
    private fun loadArt(url: String?) {
        if (url == artUrl) return
        artUrl = url
        art = null
        if (url == null) return
        artLoader.execute {
            val bitmap = runCatching {
                val conn = URL(url).openConnection() as HttpURLConnection
                conn.connectTimeout = 10_000
                conn.readTimeout = 10_000
                CookieManager.getInstance().getCookie(url)?.let { conn.setRequestProperty("Cookie", it) }
                conn.inputStream.use { BitmapFactory.decodeStream(it) }
            }.getOrNull()
            if (bitmap != null) {
                android.os.Handler(mainLooper).post {
                    if (artUrl == url) {
                        art = scaled(bitmap)
                        MediaBridge.current?.let { show(it) }
                    }
                }
            }
        }
    }

    private fun scaled(bitmap: Bitmap): Bitmap {
        val side = 512
        if (bitmap.width <= side && bitmap.height <= side) return bitmap
        val k = side.toFloat() / maxOf(bitmap.width, bitmap.height)
        return Bitmap.createScaledBitmap(bitmap, (bitmap.width * k).toInt(), (bitmap.height * k).toInt(), true)
    }

    // After an interruption: every two seconds, is an alarm ringing or a call
    // going on? Once one has been, and is over, the music plays again. If none
    // shows within ten seconds it was something else - headphones pulled out,
    // another music app - and the music stays paused, as it should. Given up
    // after thirty minutes either way.
    private fun startWatching() {
        watchStarted = SystemClock.elapsedRealtime()
        interrupterSeen = false
        main.removeCallbacks(watch)
        main.post(watch)
    }

    private fun stopWatching() {
        main.removeCallbacks(watch)
        if (MediaBridge.interrupted) MediaBridge.interruption(false)
    }

    private val watch = object : Runnable {
        override fun run() {
            val waited = SystemClock.elapsedRealtime() - watchStarted
            val busy = interrupterActive()
            if (busy) interrupterSeen = true
            when {
                interrupterSeen && !busy -> {
                    MediaBridge.interruption(false)
                    MediaBridge.dispatch("play")
                    MediaBridge.current?.let { show(it) }
                }
                (!interrupterSeen && waited > 10_000) || waited > 30 * 60_000 -> {
                    MediaBridge.interruption(false)
                    MediaBridge.current?.let { show(it) }
                }
                else -> main.postDelayed(this, 2000)
            }
        }
    }

    private fun interrupterActive(): Boolean {
        val audio = getSystemService(AudioManager::class.java) ?: return false
        if (audio.mode == AudioManager.MODE_IN_CALL || audio.mode == AudioManager.MODE_IN_COMMUNICATION ||
            audio.mode == AudioManager.MODE_RINGTONE) return true
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return false
        val interrupting = setOf(
            AudioAttributes.USAGE_ALARM,
            AudioAttributes.USAGE_NOTIFICATION_RINGTONE,
            AudioAttributes.USAGE_VOICE_COMMUNICATION,
            AudioAttributes.USAGE_ASSISTANT,
            AudioAttributes.USAGE_ASSISTANCE_NAVIGATION_GUIDANCE,
        )
        return audio.activePlaybackConfigurations.any { it.audioAttributes.usage in interrupting }
    }

    companion object {
        private const val CHANNEL = "playback"
        private const val NOTIFICATION = 1

        fun stop(context: Context) {
            context.stopService(Intent(context, PlaybackService::class.java))
        }
    }
}
