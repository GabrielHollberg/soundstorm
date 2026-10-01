package dev.soundstorm.app

import android.content.Context
import android.media.AudioDeviceCallback
import android.media.AudioDeviceInfo
import android.media.AudioManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import androidx.annotation.OptIn
import androidx.media3.common.PlaybackException
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.analytics.AnalyticsListener
import androidx.media3.exoplayer.audio.AudioSink
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * What the native player saw, the last few hundred things, kept in memory:
 * volume, audio focus, the audio output and its errors, the page's commands.
 * Asked for after "the sound goes out and the song keeps going", which no code
 * explained - Settings sends it to the server the moment it happens (on the
 * developer's own install). Never a cookie or a whole address: songs are
 * named by their id.
 */
object PlayerLog {
    private const val KEEP = 600
    private val lines = ArrayDeque<String>()
    private val clock = SimpleDateFormat("HH:mm:ss.SSS", Locale.US)

    fun add(what: String) {
        val line = clock.format(Date()) + "  " + what
        synchronized(lines) {
            lines.addLast(line)
            while (lines.size > KEEP) lines.removeFirst()
        }
    }

    fun text(context: Context): String {
        val now = snapshot(context)
        return synchronized(lines) { lines.joinToString("\n") } + "\n" + clock.format(Date()) + "  NOW " + now
    }

    /** A song's address cut to what names it. */
    fun song(url: String?): String {
        if (url.isNullOrEmpty()) return "-"
        val id = Regex("[?&]id=([^&]+)").find(url)?.groupValues?.get(1)
        return id ?: url.substringAfterLast('/').take(40)
    }

    /** The phone's own side: media volume, what it plays through, the call mode. */
    fun snapshot(context: Context): String {
        val am = context.getSystemService(AudioManager::class.java) ?: return ""
        val outs = am.getDevices(AudioManager.GET_DEVICES_OUTPUTS).joinToString(",") { kind(it.type) }
        return "media volume ${am.getStreamVolume(AudioManager.STREAM_MUSIC)}/${am.getStreamMaxVolume(AudioManager.STREAM_MUSIC)}" +
            " muted=${am.isStreamMute(AudioManager.STREAM_MUSIC)} music-active=${am.isMusicActive}" +
            " mode=${am.mode} outputs=[$outs]" + NativeAudio.describe()
    }

    private fun kind(type: Int) = when (type) {
        AudioDeviceInfo.TYPE_BUILTIN_SPEAKER -> "speaker"
        AudioDeviceInfo.TYPE_BUILTIN_EARPIECE -> "earpiece"
        AudioDeviceInfo.TYPE_WIRED_HEADPHONES, AudioDeviceInfo.TYPE_WIRED_HEADSET -> "wired"
        AudioDeviceInfo.TYPE_BLUETOOTH_A2DP -> "bt-a2dp"
        AudioDeviceInfo.TYPE_BLUETOOTH_SCO -> "bt-sco"
        AudioDeviceInfo.TYPE_USB_DEVICE, AudioDeviceInfo.TYPE_USB_HEADSET -> "usb"
        AudioDeviceInfo.TYPE_HDMI -> "hdmi"
        AudioDeviceInfo.TYPE_TELEPHONY -> "telephony"
        26, 27 -> "ble" // BLE headset and speaker (API 31)
        else -> "type$type"
    }

    private var watching = false

    /** Outputs coming and going: headphones, Bluetooth, the car. */
    fun watchDevices(context: Context) {
        if (watching) return
        watching = true
        val am = context.getSystemService(AudioManager::class.java) ?: return
        am.registerAudioDeviceCallback(object : AudioDeviceCallback() {
            override fun onAudioDevicesAdded(added: Array<out AudioDeviceInfo>) {
                added.filter { it.isSink }.forEach { add("output added ${kind(it.type)} ${it.productName}") }
            }
            override fun onAudioDevicesRemoved(removed: Array<out AudioDeviceInfo>) {
                removed.filter { it.isSink }.forEach { add("output removed ${kind(it.type)} ${it.productName}") }
            }
        }, Handler(Looper.getMainLooper()))
        // What the phone itself is playing, and where to: a mute or a change
        // of route on the system's side never shows in the player's volume.
        var lastPlaying = ""
        am.registerAudioPlaybackCallback(object : AudioManager.AudioPlaybackCallback() {
            override fun onPlaybackConfigChanged(configs: MutableList<android.media.AudioPlaybackConfiguration>) {
                val now = configs.joinToString(",") { cfg ->
                    val usage = cfg.audioAttributes.usage
                    val to = if (Build.VERSION.SDK_INT >= 33) cfg.audioDeviceInfo?.let { kind(it.type) } ?: "?" else "?"
                    "usage$usage->$to"
                }
                if (now != lastPlaying) {
                    lastPlaying = now
                    add("phone playing [$now]")
                }
            }
        }, Handler(Looper.getMainLooper()))
    }

    @OptIn(UnstableApi::class)
    val analytics = object : AnalyticsListener {
        override fun onIsPlayingChanged(t: AnalyticsListener.EventTime, isPlaying: Boolean) = add("playing=$isPlaying at ${t.currentPlaybackPositionMs}ms")
        override fun onPlaybackStateChanged(t: AnalyticsListener.EventTime, state: Int) = add("state=$state (1 idle 2 buffering 3 ready 4 ended)")
        override fun onPlayWhenReadyChanged(t: AnalyticsListener.EventTime, playWhenReady: Boolean, reason: Int) = add("playWhenReady=$playWhenReady reason=$reason (1 asked 2 audio focus lost 3 noisy 4 remote 5 end)")
        override fun onPlaybackSuppressionReasonChanged(t: AnalyticsListener.EventTime, reason: Int) = add("suppressed=$reason (0 none 1 transient focus loss 2 unsuitable output)")
        override fun onVolumeChanged(t: AnalyticsListener.EventTime, volume: Float) = add("player volume=$volume")
        override fun onAudioUnderrun(t: AnalyticsListener.EventTime, bufferSize: Int, bufferSizeMs: Long, elapsedSinceLastFeedMs: Long) = add("audio underrun, ${elapsedSinceLastFeedMs}ms since fed")
        override fun onAudioSinkError(t: AnalyticsListener.EventTime, audioSinkError: Exception) = add("AUDIO SINK ERROR $audioSinkError")
        override fun onAudioCodecError(t: AnalyticsListener.EventTime, audioCodecError: Exception) = add("AUDIO CODEC ERROR $audioCodecError")
        override fun onAudioTrackInitialized(t: AnalyticsListener.EventTime, c: AudioSink.AudioTrackConfig) = add("audio track opened ${c.sampleRate}Hz ${c.encoding} offload=${c.offload}")
        override fun onAudioTrackReleased(t: AnalyticsListener.EventTime, c: AudioSink.AudioTrackConfig) = add("audio track closed")
        override fun onAudioPositionAdvancing(t: AnalyticsListener.EventTime, playoutStartSystemTimeMs: Long) = add("audio position advancing")
        override fun onAudioSessionIdChanged(t: AnalyticsListener.EventTime, audioSessionId: Int) = add("audio session $audioSessionId")
        override fun onSkipSilenceEnabledChanged(t: AnalyticsListener.EventTime, skipSilenceEnabled: Boolean) = add("skip silence=$skipSilenceEnabled")
        override fun onPlayerError(t: AnalyticsListener.EventTime, error: PlaybackException) = add("PLAYER ERROR ${error.errorCodeName} ${error.message}")
        override fun onMediaItemTransition(t: AnalyticsListener.EventTime, mediaItem: androidx.media3.common.MediaItem?, reason: Int) =
            add("now ${song(mediaItem?.localConfiguration?.uri?.toString())} reason=$reason (0 repeat 1 auto 2 seek 3 playlist changed)")
    }
}
