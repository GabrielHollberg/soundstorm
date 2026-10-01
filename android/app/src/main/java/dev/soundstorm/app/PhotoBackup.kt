package dev.soundstorm.app

import android.Manifest
import android.content.ContentUris
import android.content.Context
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.webkit.CookieManager
import androidx.core.content.ContextCompat
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
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
 * What was sent is remembered on the phone (by its MediaStore id), and before
 * sending anything the server is asked which it already has - by name, when
 * taken and size - so reinstalling the app or a new phone sends nothing twice.
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
            .put("lastRun", p.getLong("lastRun", 0))
    }

    /** What the page asked for: on or off, and its options. */
    fun configure(c: Context, options: JSONObject) {
        val e = prefs(c).edit()
        if (options.has("enabled")) e.putBoolean("enabled", options.optBoolean("enabled"))
        if (options.has("wifiOnly")) e.putBoolean("wifiOnly", options.optBoolean("wifiOnly"))
        if (options.has("videos")) e.putBoolean("videos", options.optBoolean("videos"))
        if (options.has("charging")) e.putBoolean("charging", options.optBoolean("charging"))
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
            wm.enqueueUniqueWork(NOW, ExistingWorkPolicy.REPLACE,
                OneTimeWorkRequestBuilder<BackupWorker>().setConstraints(cons).build())
        }
        watchForNewPhotos(c)
    }

    /** A job that runs when a photo or video is added to the phone. */
    fun watchForNewPhotos(c: Context) {
        val cons = Constraints.Builder()
            .setRequiredNetworkType(if (prefs(c).getBoolean("wifiOnly", true)) NetworkType.UNMETERED else NetworkType.CONNECTED)
            .setRequiresCharging(prefs(c).getBoolean("charging", false))
            .addContentUriTrigger(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, true)
            .addContentUriTrigger(MediaStore.Video.Media.EXTERNAL_CONTENT_URI, true)
            .setTriggerContentUpdateDelay(30, TimeUnit.SECONDS)
            .build()
        WorkManager.getInstance(c).enqueueUniqueWork(NEW_PHOTOS, ExistingWorkPolicy.KEEP,
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

    // ------------------------------------------------------------------ the server

    class Refused(val code: Int, message: String) : Exception(message)

    private fun open(server: Uri, path: String, method: String): HttpURLConnection {
        val conn = URL(server.toString().trimEnd('/') + path).openConnection() as HttpURLConnection
        conn.requestMethod = method
        conn.connectTimeout = 15_000
        conn.readTimeout = 120_000
        CookieManager.getInstance().getCookie(server.toString())?.let { conn.setRequestProperty("Cookie", it) }
        return conn
    }

    /** Which of these the server already has. */
    fun check(server: Uri, items: List<Pair<Item, Long>>): BooleanArray {
        val conn = open(server, "/api/photos/backup/check", "POST")
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
    fun send(c: Context, server: Uri, item: Item, uri: Uri, size: Long) {
        val q = "?name=" + URLEncoder.encode(item.name, "UTF-8") + "&taken=" + item.taken
        val conn = open(server, "/api/photos/backup$q", "PUT")
        conn.doOutput = true
        conn.setRequestProperty("Content-Type", if (item.video) "video/*" else "image/*")
        if (size > 0) conn.setFixedLengthStreamingMode(size) else conn.setChunkedStreamingMode(256 * 1024)
        c.contentResolver.openInputStream(uri)?.use { input ->
            conn.outputStream.use { out -> input.copyTo(out, 256 * 1024) }
        } ?: throw Refused(0, "could not read the file")
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
    override fun doWork(): Result {
        val c = applicationContext
        // The job that waits for new photos is used up by running; set it up
        // again for the next one.
        PhotoBackup.watchForNewPhotos(c)
        if (!PhotoBackup.enabled(c) || !PhotoBackup.hasPermission(c)) return Result.success()
        val server = ServerAddress.saved(c) ?: return Result.success()
        val started = System.currentTimeMillis()
        val roll = PhotoBackup.cameraRoll(c)
        val sent = PhotoBackup.sent(c)
        val waiting = roll.filter { it.key !in sent }
        PhotoBackup.record(c, done = roll.size - waiting.size, total = roll.size, running = true, problem = "")
        var done = roll.size - waiting.size
        try {
            for (batch in waiting.chunked(100)) {
                if (isStopped) return Result.retry()
                val sized = batch.map { it to PhotoBackup.length(c, PhotoBackup.original(c, it.uri)) }
                val have = PhotoBackup.check(server, sized)
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
                    PhotoBackup.send(c, server, item, PhotoBackup.original(c, item.uri), size)
                    PhotoBackup.markSent(c, listOf(item.key))
                    done++
                    PhotoBackup.record(c, done = done)
                }
            }
            return Result.success()
        } catch (e: PhotoBackup.Refused) {
            // Said plainly in Settings. A full photo space or disk, or a
            // sign-in that has ended, will not mend itself by trying again in
            // a minute: the next scheduled job tries.
            val message = when (e.code) {
                401 -> "Sign in to SoundStorm again to carry on backing up."
                403 -> "This account does not have Pictures."
                507 -> e.message ?: "There is no room left for photos."
                else -> e.message ?: "The server refused a photo."
            }
            PhotoBackup.record(c, problem = message)
            return if (e.code in listOf(401, 403, 507)) Result.success() else Result.retry()
        } catch (e: Exception) {
            // The network: WorkManager tries again later by itself.
            PhotoBackup.record(c, problem = "Could not reach the server; it will try again.")
            return Result.retry()
        } finally {
            PhotoBackup.record(c, running = false)
        }
    }
}
