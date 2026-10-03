package dev.soundstorm.app

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.pm.ServiceInfo
import android.content.ContentUris
import android.content.Context
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.webkit.CookieManager
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.Worker
import androidx.work.WorkerParameters
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Backing up this phone's photos and videos to the person's own folder on
 * their server (pictures/Personal/<name>/<year>/<month>/), the owner's design:
 * the server's half is httpapi/personalphotos.go.
 *
 * It runs as WorkManager jobs, so Android decides when - on Wi-Fi only and
 * while charging if asked - and it carries on with the app closed: a job when
 * a new photo appears (a content trigger), one every few hours as a safety
 * net, and each job chains another while there is more to send, since one
 * job may run only minutes. Newest first, so today's photos are safe before
 * last year's.
 *
 * The phone keeps no list of what it sent (0.37, the owner's asking): each run
 * asks the server which it already has - by name, when taken and size - and
 * sends the rest, so the server is the one record. A photo deleted there is
 * one it says it has, so it is not sent back; one that went missing there is
 * sent again by itself.
 * It signs in with the web view's own cookie: nothing to set up.
 */
object PhotoBackup {
    private const val PREFS = "photo-backup"
    private const val SENT = "backup-sent.txt"
    private const val PERIODIC = "photo-backup-periodic"
    private const val NOW = "photo-backup-now"
    private const val NEW_PHOTOS = "photo-backup-new"

    /** Each job sends for at most this long, then hands over to the next. */
    const val BUDGET_MS = 8 * 60 * 1000L

    private fun prefs(c: Context) = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun enabled(c: Context) = prefs(c).getBoolean("enabled", false)

    /**
     * Where to send: the secure address the page was on when it asked about
     * backup, rather than the address typed - which is often a plain http
     * one, and the page's own move to the secure name is not saved. Over
     * plain http every photo, and the cookie, crossed the Wi-Fi readable (a
     * security review); and the session lives with the secure name, so a
     * plain address had no cookie to send either.
     */
    fun rememberServer(c: Context, origin: String?) {
        val o = origin ?: return
        if (!o.startsWith("https://")) return
        prefs(c).edit().putString("server", o).apply()
    }

    /** The addresses to try, best first: the page's secure one, its away
     *  twin (a home name is unreachable away from home), the typed one. */
    fun servers(c: Context): List<Uri> {
        val out = mutableListOf<Uri>()
        prefs(c).getString("server", null)?.let { s ->
            val u = Uri.parse(s)
            out += u
            val host = u.host ?: ""
            if (host.endsWith(".home.soundstorm.dev")) {
                out += u.buildUpon().encodedAuthority(
                    host.removeSuffix(".home.soundstorm.dev") + ".net.soundstorm.dev" + if (u.port != -1) ":${u.port}" else ""
                ).build()
            }
        }
        ServerAddress.saved(c)?.let { s -> if (out.none { ServerAddress.origin(it) == ServerAddress.origin(s) }) out += s }
        return out
    }

    /** The settings and how it is going, for the page's Settings. */
    fun status(c: Context): JSONObject {
        val p = prefs(c)
        return JSONObject()
            .put("enabled", p.getBoolean("enabled", false))
            .put("decided", p.contains("enabled"))
            .put("wifiOnly", p.getBoolean("wifiOnly", true))
            .put("videos", p.getBoolean("videos", true))
            .put("charging", p.getBoolean("charging", false))
            .put("permission", hasPermission(c))
            .put("done", p.getInt("done", 0))
            .put("total", p.getInt("total", 0))
            .put("running", p.getBoolean("running", false))
            .put("problem", p.getString("problem", "") ?: "")
            .put("sendingSize", p.getLong("sendingSize", 0))
            .put("sendingSent", p.getLong("sendingSent", 0))
            .put("sendingVideo", p.getBoolean("sendingVideo", false))
            .put("lastRun", p.getLong("lastRun", 0))
    }

    /** What the page asked for: on or off, and its options. */
    fun configure(c: Context, options: JSONObject) {
        val e = prefs(c).edit()
        if (options.has("enabled")) e.putBoolean("enabled", options.optBoolean("enabled"))
        if (options.has("wifiOnly")) e.putBoolean("wifiOnly", options.optBoolean("wifiOnly"))
        if (options.has("videos")) e.putBoolean("videos", options.optBoolean("videos"))
        if (options.has("charging")) e.putBoolean("charging", options.optBoolean("charging"))
        // Whose backup it is: sent with every photo, so the server can refuse
        // one meant for another account when somebody else signs in here.
        if (options.has("account")) e.putString("account", options.optString("account"))
        e.putString("problem", "")
        e.apply()
        schedule(c, now = true)
    }

    /** The permissions to ask for: photos and videos, and their places. */
    fun permissions(): Array<String> = when {
        Build.VERSION.SDK_INT >= 33 -> arrayOf(
            Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO,
            Manifest.permission.ACCESS_MEDIA_LOCATION)
        Build.VERSION.SDK_INT >= 29 -> arrayOf(
            Manifest.permission.READ_EXTERNAL_STORAGE, Manifest.permission.ACCESS_MEDIA_LOCATION)
        else -> arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE)
    }

    fun hasPermission(c: Context): Boolean {
        fun has(p: String) = ContextCompat.checkSelfPermission(c, p) == PackageManager.PERMISSION_GRANTED
        return when {
            Build.VERSION.SDK_INT >= 34 -> has(Manifest.permission.READ_MEDIA_IMAGES) ||
                has(Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED)
            Build.VERSION.SDK_INT >= 33 -> has(Manifest.permission.READ_MEDIA_IMAGES)
            else -> has(Manifest.permission.READ_EXTERNAL_STORAGE)
        }
    }

    private fun constraints(c: Context): Constraints {
        val p = prefs(c)
        return Constraints.Builder()
            .setRequiredNetworkType(if (p.getBoolean("wifiOnly", true)) NetworkType.UNMETERED else NetworkType.CONNECTED)
            .setRequiresCharging(p.getBoolean("charging", false))
            .build()
    }

    /** Sets up the jobs, or cancels them when backup is off. */
    fun schedule(c: Context, now: Boolean = false) {
        val wm = WorkManager.getInstance(c)
        if (!enabled(c) || !hasPermission(c)) {
            wm.cancelUniqueWork(PERIODIC)
            wm.cancelUniqueWork(NOW)
            wm.cancelUniqueWork(NEW_PHOTOS)
            return
        }
        val cons = constraints(c)
        wm.enqueueUniquePeriodicWork(PERIODIC, ExistingPeriodicWorkPolicy.UPDATE,
            PeriodicWorkRequestBuilder<BackupWorker>(6, TimeUnit.HOURS).setConstraints(cons).build())
        if (now) {
            wm.enqueueUniqueWork(NOW, ExistingWorkPolicy.KEEP,
                OneTimeWorkRequestBuilder<BackupWorker>().setConstraints(cons).build())
        }
        // Kept as it is on an ordinary opening (appended, it grew a job on
        // every launch - a review); replaced when the options changed.
        watchForNewPhotos(c, if (now) ExistingWorkPolicy.REPLACE else ExistingWorkPolicy.KEEP)
    }

    /** A job that runs when a photo or video is added to the phone. */
    fun watchForNewPhotos(c: Context, policy: ExistingWorkPolicy = ExistingWorkPolicy.APPEND_OR_REPLACE) {
        val cons = Constraints.Builder()
            .setRequiredNetworkType(if (prefs(c).getBoolean("wifiOnly", true)) NetworkType.UNMETERED else NetworkType.CONNECTED)
            .setRequiresCharging(prefs(c).getBoolean("charging", false))
            .addContentUriTrigger(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, true)
            .addContentUriTrigger(MediaStore.Video.Media.EXTERNAL_CONTENT_URI, true)
            .setTriggerContentUpdateDelay(30, TimeUnit.SECONDS)
            .build()
        // Replaced, not kept: it is set up again from inside the job it
        // replaces, and KEEP found that running job and did nothing, so the
        // trigger fired once and no more (a review).
        WorkManager.getInstance(c).enqueueUniqueWork(NEW_PHOTOS, policy,
            OneTimeWorkRequestBuilder<BackupWorker>().setConstraints(cons).build())
    }

    /** More to send than one job had time for: the next one, straight after. */
    fun continueLater(c: Context) {
        WorkManager.getInstance(c).enqueueUniqueWork(NOW, ExistingWorkPolicy.APPEND_OR_REPLACE,
            OneTimeWorkRequestBuilder<BackupWorker>().setConstraints(constraints(c)).build())
    }

    // ------------------------------------------------------------- the camera roll

    data class Item(val key: String, val uri: Uri, val name: String, val taken: Long, val video: Boolean)

    /** Every photo, and video if asked for, newest first. */
    fun cameraRoll(c: Context): List<Item> {
        val out = ArrayList<Item>()
        val sources = mutableListOf(MediaStore.Images.Media.EXTERNAL_CONTENT_URI to false)
        if (prefs(c).getBoolean("videos", true)) sources.add(MediaStore.Video.Media.EXTERNAL_CONTENT_URI to true)
        for ((collection, video) in sources) {
            val projection = arrayOf(
                MediaStore.MediaColumns._ID, MediaStore.MediaColumns.DISPLAY_NAME,
                MediaStore.MediaColumns.DATE_TAKEN, MediaStore.MediaColumns.DATE_ADDED)
            c.contentResolver.query(collection, projection, null, null, null)?.use { cur ->
                val id = cur.getColumnIndexOrThrow(MediaStore.MediaColumns._ID)
                val name = cur.getColumnIndexOrThrow(MediaStore.MediaColumns.DISPLAY_NAME)
                val taken = cur.getColumnIndexOrThrow(MediaStore.MediaColumns.DATE_TAKEN)
                val added = cur.getColumnIndexOrThrow(MediaStore.MediaColumns.DATE_ADDED)
                while (cur.moveToNext()) {
                    val n = cur.getString(name) ?: continue
                    val t = cur.getLong(taken).takeIf { it > 0 } ?: (cur.getLong(added) * 1000)
                    val rowId = cur.getLong(id)
                    out.add(Item((if (video) "v" else "i") + rowId, ContentUris.withAppendedId(collection, rowId), n, t, video))
                }
            }
        }
        out.sortByDescending { it.taken }
        return out
    }

    /**
     * The file as it is, location included where allowed: without the
     * original, Android hands over a copy with the place taken out, which
     * would leave Places empty and differ in size from the original.
     */
    fun original(c: Context, uri: Uri): Uri =
        if (Build.VERSION.SDK_INT >= 29 && ContextCompat.checkSelfPermission(c, Manifest.permission.ACCESS_MEDIA_LOCATION) == PackageManager.PERMISSION_GRANTED)
            MediaStore.setRequireOriginal(uri) else uri

    /** How long the file is, as it will be sent. */
    fun length(c: Context, uri: Uri): Long =
        runCatching { c.contentResolver.openAssetFileDescriptor(uri, "r")?.use { it.length } }.getOrNull() ?: -1L

    fun sent(c: Context): MutableSet<String> {
        val f = File(c.filesDir, SENT)
        return if (f.exists()) f.readLines().filter { it.isNotBlank() }.toMutableSet() else mutableSetOf()
    }

    fun markSent(c: Context, keys: Collection<String>) {
        if (keys.isEmpty()) return
        File(c.filesDir, SENT).appendText(keys.joinToString("\n", postfix = "\n"))
    }

    fun record(c: Context, done: Int? = null, total: Int? = null, running: Boolean? = null, problem: String? = null) {
        val e = prefs(c).edit()
        done?.let { e.putInt("done", it) }
        total?.let { e.putInt("total", it) }
        running?.let { e.putBoolean("running", it); if (!it) e.putLong("lastRun", System.currentTimeMillis()) }
        problem?.let { e.putString("problem", it) }
        e.apply()
    }

    // ------------------------------------------------------------- the notification

    private const val CHANNEL = "photo-backup"
    const val NOTIFICATION_ID = 7302

    /** While sending, the job shows a quiet notification: that is what lets
     *  Android run it past ten minutes, which one long video can take. */
    fun foregroundInfo(c: Context, done: Int, total: Int, sent: Long = 0, size: Long = 0): ForegroundInfo {
        val nm = c.getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= 26 && nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, "Photo backup", NotificationManager.IMPORTANCE_LOW)
                .apply { setShowBadge(false) })
        }
        val text = if (size > 0) "$done of $total - ${sent / 1_000_000} of ${size / 1_000_000} MB of this one"
            else "$done of $total backed up"
        val n = NotificationCompat.Builder(c, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_soundstorm)
            .setContentTitle("Backing up photos")
            .setContentText(text)
            .setOngoing(true)
            .setSilent(true)
            .setProgress(if (size > 0) 1000 else total, if (size > 0) (sent * 1000 / size).toInt() else done, false)
            .build()
        return if (Build.VERSION.SDK_INT >= 29) ForegroundInfo(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
            else ForegroundInfo(NOTIFICATION_ID, n)
    }

    // ------------------------------------------------------------------ the server

    class Refused(val code: Int, message: String) : Exception(message)

    private fun open(server: Uri, path: String, method: String): HttpURLConnection {
        val conn = URL(server.toString().trimEnd('/') + path).openConnection() as HttpURLConnection
        conn.requestMethod = method
        conn.connectTimeout = 15_000
        conn.readTimeout = 120_000
        conn.instanceFollowRedirects = false
        WebCookies.install()
        return conn
    }

    /** Which of these the server already has. */
    private fun accountQuery(c: Context, sep: String): String {
        val a = prefs(c).getString("account", "") ?: ""
        return if (a.isEmpty()) "" else sep + "account=" + URLEncoder.encode(a, "UTF-8")
    }

    fun check(c: Context, server: Uri, items: List<Pair<Item, Long>>): BooleanArray {
        val conn = open(server, "/api/photos/backup/check" + accountQuery(c, "?"), "POST")
        conn.doOutput = true
        conn.setRequestProperty("Content-Type", "application/json")
        val list = JSONArray()
        for ((it, size) in items) list.put(JSONObject().put("name", it.name).put("taken", it.taken).put("size", size))
        conn.outputStream.use { it.write(JSONObject().put("items", list).toString().toByteArray()) }
        val code = conn.responseCode
        if (code !in 200..299) throw Refused(code, errorOf(conn))
        val have = JSONObject(conn.inputStream.bufferedReader().readText()).getJSONArray("have")
        return BooleanArray(items.size) { have.optBoolean(it) }
    }

    /** Sends one photo or video, the whole file as the body. */
    fun send(c: Context, server: Uri, item: Item, uri: Uri, size: Long, onProgress: (Long) -> Unit = {}) {
        val q = "?name=" + URLEncoder.encode(item.name, "UTF-8") + "&taken=" + item.taken + accountQuery(c, "&")
        val conn = open(server, "/api/photos/backup$q", "PUT")
        conn.doOutput = true
        conn.setRequestProperty("Content-Type", if (item.video) "video/*" else "image/*")
        if (size > 0) conn.setFixedLengthStreamingMode(size) else conn.setChunkedStreamingMode(256 * 1024)
        // How far through this file, for Settings: a long video otherwise
        // looks like nothing happening until all of it is there.
        val p = prefs(c)
        p.edit().putLong("sendingSize", size).putLong("sendingSent", 0).putBoolean("sendingVideo", item.video).apply()
        try {
            c.contentResolver.openInputStream(uri)?.use { input ->
                conn.outputStream.use { out ->
                    val buf = ByteArray(256 * 1024)
                    var sent = 0L
                    var shown = 0L
                    while (true) {
                        val n = input.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        sent += n
                        val now = System.currentTimeMillis()
                        if (now - shown > 1000) {
                            shown = now
                            p.edit().putLong("sendingSent", sent).apply()
                            onProgress(sent)
                        }
                    }
                }
            } ?: throw Refused(0, "could not read the file")
        } finally {
            p.edit().putLong("sendingSize", 0).putLong("sendingSent", 0).apply()
        }
        val code = conn.responseCode
        if (code !in 200..299 && code != 409) throw Refused(code, errorOf(conn))
        conn.inputStream.close()
    }

    private fun errorOf(conn: HttpURLConnection): String =
        runCatching { JSONObject(conn.errorStream.bufferedReader().readText()).optString("error") }.getOrNull()
            ?.takeIf { it.isNotBlank() } ?: "the server answered ${conn.responseCode}"
}

/** One backup job: as much of the camera roll as fits in its time. */
class BackupWorker(context: Context, params: WorkerParameters) : Worker(context, params) {
    companion object {
        // One backup at a time, whichever job started it (a new photo, the
        // six-hourly one, the switch turned on): two at once sent the same
        // video twice side by side.
        private val busy = AtomicBoolean(false)
    }

    override fun doWork(): Result {
        if (!busy.compareAndSet(false, true)) return Result.success()
        try {
            return backUp()
        } finally {
            busy.set(false)
        }
    }

    private var foreground = false

    private fun showProgress(done: Int, total: Int, sent: Long = 0, size: Long = 0) {
        if (!foreground) return
        runCatching { setForegroundAsync(PhotoBackup.foregroundInfo(applicationContext, done, total, sent, size)) }
    }

    private fun backUp(): Result {
        val c = applicationContext
        // The job that waits for new photos is used up by running; set it up
        // again for the next one.
        PhotoBackup.watchForNewPhotos(c)
        if (!PhotoBackup.enabled(c) || !PhotoBackup.hasPermission(c)) return Result.success()
        val candidates = PhotoBackup.servers(c)
        if (candidates.isEmpty()) return Result.success()
        var server = candidates.first()
        val started = System.currentTimeMillis()
        val roll = PhotoBackup.cameraRoll(c)
        // Every photo is asked about: the server is the record (see above).
        val waiting = roll
        PhotoBackup.record(c, total = roll.size, running = true, problem = "")
        var done = 0
        var foregroundAsked = false
        try {
            for (batch in waiting.chunked(100)) {
                if (isStopped) return Result.retry()
                val sized = batch.map { it to PhotoBackup.length(c, PhotoBackup.original(c, it.uri)) }
                // The first address that answers, for the rest of this job.
                var have: BooleanArray? = null
                var lastError: java.io.IOException? = null
                for (s in candidates.dropWhile { it != server }) {
                    try {
                        have = PhotoBackup.check(c, s, sized)
                        server = s
                        break
                    } catch (e: java.io.IOException) {
                        lastError = e
                    }
                }
                if (have == null) throw lastError ?: java.io.IOException("no server")
                val already = batch.filterIndexed { i, _ -> have[i] }.map { it.key }
                PhotoBackup.markSent(c, already)
                done += already.size
                PhotoBackup.record(c, done = done)
                for ((i, pair) in sized.withIndex()) {
                    if (have[i]) continue
                    if (isStopped) return Result.retry()
                    if (System.currentTimeMillis() - started > PhotoBackup.BUDGET_MS) {
                        PhotoBackup.continueLater(c)
                        return Result.success()
                    }
                    val (item, size) = pair
                    val sentSoFar = done
                    if (!foregroundAsked) {
                        // Only once something is to be sent. Android may
                        // refuse this from the background (12 and later); then
                        // it carries on as an ordinary job, ten minutes at a time.
                        foregroundAsked = true
                        foreground = runCatching {
                            setForegroundAsync(PhotoBackup.foregroundInfo(c, done, roll.size)).get(); true
                        }.getOrDefault(false)
                    }
                    try {
                        PhotoBackup.send(c, server, item, PhotoBackup.original(c, item.uri), size) { sent ->
                            showProgress(sentSoFar, roll.size, sent, size)
                        }
                    } catch (e: PhotoBackup.Refused) {
                        // One photo the server will not take (a type it
                        // refuses, a file that cannot be read) is passed
                        // over: newest first, every retry met it first and
                        // nothing older was ever sent (a review). Space, the
                        // sign-in and access still stop the job.
                        if (e.code in listOf(401, 403, 429, 507)) throw e
                        android.util.Log.w("PhotoBackup", "passed over ${item.name}: ${e.message}")
                        PhotoBackup.record(c, problem = "Passed over ${item.name}: ${e.message}")
                    }
                    PhotoBackup.markSent(c, listOf(item.key))
                    done++
                    PhotoBackup.record(c, done = done)
                    showProgress(done, roll.size)
                }
            }
            return Result.success()
        } catch (e: PhotoBackup.Refused) {
            // Said plainly in Settings. A full photo space or disk, or a
            // sign-in that has ended, will not mend itself by trying again in
            // a minute: the next scheduled job tries.
            val message = when (e.code) {
                401 -> "Sign in to SoundStorm again to carry on backing up."
                403 -> e.message?.takeIf { it.isNotBlank() } ?: "This account does not have Pictures."
                507 -> e.message ?: "There is no room left for photos."
                else -> e.message ?: "The server refused a photo."
            }
            PhotoBackup.record(c, problem = message)
            return if (e.code in listOf(401, 403, 507)) Result.success() else Result.retry()
        } catch (e: java.io.IOException) {
            // The network: WorkManager tries again later by itself.
            android.util.Log.w("PhotoBackup", "send failed", e)
            PhotoBackup.record(c, problem = "Could not reach the server; it will try again.")
            return Result.retry()
        } catch (e: Exception) {
            android.util.Log.w("PhotoBackup", "backup failed", e)
            PhotoBackup.record(c, problem = "Backup stopped (${e.javaClass.simpleName}); it will try again.")
            return Result.retry()
        } finally {
            PhotoBackup.record(c, running = false)
        }
    }
}
