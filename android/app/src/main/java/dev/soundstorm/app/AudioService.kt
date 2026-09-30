package dev.soundstorm.app

import android.app.PendingIntent
import android.content.Intent
import android.webkit.CookieManager
import androidx.annotation.OptIn
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSourceBitmapLoader
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.datasource.ResolvingDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.session.DefaultMediaNotificationProvider
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import com.google.common.util.concurrent.MoreExecutors
import java.util.concurrent.Executors

/**
 * Music played by Android's own media player (Media3's ExoPlayer, in a media
 * session service) rather than by the web page's audio element.
 *
 * A page in a web view is something Android pauses, slows and ends in the
 * background: music stopped after an alarm, and after one song with the screen
 * off. A media session service is what Android expects to be playing music -
 * it stays in the foreground, pauses for an alarm or a call and plays on
 * after, pauses when headphones come out, and moves into the next song
 * without a gap. The page still decides everything - what plays, the queue,
 * Now Playing, the looks, the lyrics - and drives this through NativeAudio.
 */
@OptIn(UnstableApi::class)
class AudioService : MediaSessionService() {
    private var session: MediaSession? = null

    override fun onCreate() {
        super.onCreate()
        // The songs are the server's, behind the page's sign-in: every request
        // carries the web view's own cookies for that address.
        val http = DefaultHttpDataSource.Factory()
            .setUserAgent("SoundStormApp/1")
            .setAllowCrossProtocolRedirects(false)
        val signedIn: DataSource.Factory = ResolvingDataSource.Factory(http) { spec ->
            val cookie = CookieManager.getInstance().getCookie(spec.uri.toString())
            if (cookie.isNullOrEmpty()) spec else spec.withAdditionalHeaders(mapOf("Cookie" to cookie))
        }
        val player = ExoPlayer.Builder(this)
            .setMediaSourceFactory(DefaultMediaSourceFactory(this).setDataSourceFactory(signedIn))
            // Takes the sound properly: pauses for an alarm or a call and
            // plays on once it is over; pauses when headphones come out.
            .setAudioAttributes(
                AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MUSIC).build(),
                true,
            )
            .setHandleAudioBecomingNoisy(true)
            .setWakeMode(C.WAKE_MODE_NETWORK)
            .build()
        NativeAudio.attach(player)

        // Next and previous on the lock screen, in the notification and from
        // a car are the page's to answer: it holds the queue.
        val skips = setOf(
            Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM,
            Player.COMMAND_SEEK_TO_PREVIOUS, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
        )
        val forwarding = object : ForwardingPlayer(player) {
            override fun getAvailableCommands(): Player.Commands =
                super.getAvailableCommands().buildUpon().addAll(*skips.toIntArray()).build()

            override fun isCommandAvailable(command: Int) = command in skips || super.isCommandAvailable(command)
            override fun seekToNext() = NativeAudio.skip(true)
            override fun seekToNextMediaItem() = NativeAudio.skip(true)
            override fun seekToPrevious() = NativeAudio.skip(false)
            override fun seekToPreviousMediaItem() = NativeAudio.skip(false)
        }
        val open = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        session = MediaSession.Builder(this, forwarding)
            .setSessionActivity(open)
            .setBitmapLoader(DataSourceBitmapLoader(
                MoreExecutors.listeningDecorator(Executors.newSingleThreadExecutor()), signedIn))
            .build()
        setMediaNotificationProvider(DefaultMediaNotificationProvider.Builder(this).build().apply {
            setSmallIcon(R.drawable.ic_stat_soundstorm)
        })
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = session

    // The app swiped away: the music goes on while it plays, as a music app's
    // does; paused, the service goes with the app.
    override fun onTaskRemoved(rootIntent: Intent?) {
        val player = session?.player
        if (player == null || !player.playWhenReady || player.mediaItemCount == 0) stopSelf()
    }

    override fun onDestroy() {
        NativeAudio.detach()
        session?.run {
            player.release()
            release()
        }
        session = null
        super.onDestroy()
    }
}
