package dev.soundstorm.app

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.provider.OpenableColumns
import androidx.core.app.NotificationCompat
import androidx.work.Constraints
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.Worker
import androidx.work.WorkerParameters
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/**
 * Adding files from this phone, sent by the app rather than the page, so
 * they carry on after the app is left or swiped away (the owner's asking):
 * a WorkManager job in the foreground with a notification and a Stop button,
 * on Wi-Fi only or while charging if asked, as photo backup is.
 *
 * The page still plans everything - which shelf, which path - and hands the
 * app the list; the files themselves are the ones picked with Add media,
 * which the app remembers by name and size as the picker returns them, with
 * a lasting permission to read them. One at a time, each the whole file as
 * the body of PUT /api/upload, as the page sends them. A file the
 * connection drops is sent again, from its start, on the next try.
 */
object Uploads {
    private const val PREFS = "uploads"
    private const val QUEUE = "uploads.json"
    private const val WORK = "uploads"
    private const val CHANNEL = "uploads"
    const val NOTIFICATION_ID = 7303

    /** What the picker returned, newest last: what the page's files are. */
    private data class Picked(val uri: Uri, val name: String, val size: Long)
    private val picked = mutableListOf<Picked>()

    /** The connection sending now, so Stop can let it go at once. */
    @Volatile var current: HttpURLConnection? = null

    private fun prefs(c: Context) = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    /** Files the picker handed over: kept readable after the app is left. */
    fun rememberPicked(c: Context, uris: Array<Uri>?) {
        for (uri in uris ?: return) {
            runCatching {
                c.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }
            var name = uri.lastPathSegment ?: ""
            var size = -1L
            runCatching {
                c.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { cur ->
                    if (cur.moveToFirst()) {
                        cur.getString(0)?.let { name = it }
                        if (!cur.isNull(1)) size = cur.getLong(1)
                    }
                }
            }
            synchronized(picked) {
                picked += Picked(uri, name, size)
                while (picked.size > 2000) picked.removeAt(0)
            }
        }
    }

    // ------------------------------------------------------------- the queue

    private fun queueFile(c: Context) = File(c.filesDir, QUEUE)

    @Synchronized
    fun load(c: Context): JSONObject =
        runCatching { JSONObject(queueFile(c).readText()) }.getOrNull() ?: JSONObject().put("jobs", JSONArray())

    @Synchronized
    fun save(c: Context, q: JSONObject) {
        val f = queueFile(c)
        val tmp = File(f.path + ".tmp")
        tmp.writeText(q.toString())
        tmp.renameTo(f)
    }

    @Synchronized
    fun update(c: Context, change: (JSONObject) -> Unit) {
        val q = load(c)
        change(q)
        save(c, q)
    }

    private fun jobs(q: JSONObject): List<JSONObject> {
        val a = q.optJSONArray("jobs") ?: return emptyList()
        return (0 until a.length()).map { a.getJSONObject(it) }
    }

    /**
     * The page's list: [{name, size, path, kind, taken?}], to `server`. Each
     * is matched to a picked file by name and size; any that cannot be is
     * reported back, and nothing is queued (the page then sends them itself).
     */
    fun add(c: Context, server: String, list: JSONArray): JSONObject {
        if (!ServerAddress.isServer(c, server)) return JSONObject().put("added", false).put("why", "server")
        val used = mutableSetOf<Uri>()
        val found = mutableListOf<JSONObject>()
        var missing = 0
        synchronized(picked) {
            for (i in 0 until list.length()) {
                val j = list.getJSONObject(i)
                val name = j.optString("name")
                val size = j.optLong("size", -1)
                val p = picked.lastOrNull { it.uri !in used && it.name == name && (it.size < 0 || size < 0 || it.size == size) }
                if (p == null) { missing++; continue }
                used += p.uri
                found += JSONObject()
                    .put("uri", p.uri.toString()).put("name", name).put("size", size)
                    .put("path", j.optString("path")).put("kind", j.optString("kind"))
                    .put("group", j.optString("group"))
                    .put("conflict", j.optString("conflict"))
                    .put("taken", j.optLong("taken", 0)).put("state", "waiting")
            }
        }
        if (missing > 0) return JSONObject().put("added", false).put("missing", missing)
        update(c) { q ->
            // A batch that has finished is replaced; one still going is added to.
            val keep = jobs(q).filter { it.optString("state") == "waiting" }
            val all = JSONArray()
            keep.forEach { all.put(it) }
            found.forEach { all.put(it) }
            q.put("jobs", all).put("server", server).put("stop", false).put("seen", false)
        }
        schedule(c)
        return JSONObject().put("added", true).put("count", found.size)
    }

    // ------------------------------------------------------------- settings

    fun options(c: Context): JSONObject = JSONObject()
        .put("wifiOnly", prefs(c).getBoolean("wifiOnly", false))
        .put("charging", prefs(c).getBoolean("charging", false))

    fun configure(c: Context, o: JSONObject) {
        val e = prefs(c).edit()
        if (o.has("wifiOnly")) e.putBoolean("wifiOnly", o.optBoolean("wifiOnly"))
        if (o.has("charging")) e.putBoolean("charging", o.optBoolean("charging"))
        e.apply()
        // Waiting files wait for the new conditions.
        if (jobs(load(c)).any { it.optString("state") == "waiting" }) schedule(c, ExistingWorkPolicy.REPLACE)
    }

    private fun schedule(c: Context, policy: ExistingWorkPolicy = ExistingWorkPolicy.KEEP) {
        val cons = Constraints.Builder()
            .setRequiredNetworkType(if (prefs(c).getBoolean("wifiOnly", false)) NetworkType.UNMETERED else NetworkType.CONNECTED)
            .setRequiresCharging(prefs(c).getBoolean("charging", false))
            .build()
        WorkManager.getInstance(c).enqueueUniqueWork(WORK, policy,
            OneTimeWorkRequestBuilder<UploadWorker>().setConstraints(cons).build())
    }

    /** Stop: the file going up let go, the rest not sent. */
    fun stop(c: Context) {
        update(c) { q ->
            q.put("stop", true)
            jobs(q).filter { it.optString("state") == "waiting" }.forEach { it.put("state", "stopped") }
        }
        runCatching { current?.disconnect() }
        WorkManager.getInstance(c).cancelUniqueWork(WORK)
        c.getSystemService(NotificationManager::class.java)?.cancel(NOTIFICATION_ID)
    }

    /** How it is going, for the page. */
    fun status(c: Context): JSONObject {
        val q = load(c)
        val all = jobs(q)
        val waiting = all.count { it.optString("state") == "waiting" }
        val p = prefs(c)
        return JSONObject()
            .put("total", all.size)
            .put("done", all.size - waiting)
            .put("waiting", waiting)
            .put("running", p.getBoolean("running", false) && waiting > 0)
            .put("current", p.getString("currentName", "") ?: "")
            .put("currentSent", p.getLong("currentSent", 0))
            .put("currentSize", p.getLong("currentSize", 0))
            .put("totalBytes", all.sumOf { maxOf(0L, it.optLong("size", 0)) })
            .put("doneBytes", all.filter { it.optString("state") != "waiting" }.sumOf { maxOf(0L, it.optLong("size", 0)) })
            .put("seen", q.optBoolean("seen", true))
            .put("problem", p.getString("problem", "") ?: "")
            .put("options", options(c))
            .put("jobs", JSONArray().apply { all.forEach { put(JSONObject(it.toString()).apply { remove("uri") }) } })
    }

    fun markSeen(c: Context) = update(c) { it.put("seen", true) }

    fun record(c: Context, running: Boolean? = null, name: String? = null, sent: Long? = null, size: Long? = null, problem: String? = null) {
        val e = prefs(c).edit()
        running?.let { e.putBoolean("running", it) }
        name?.let { e.putString("currentName", it) }
        sent?.let { e.putLong("currentSent", it) }
        size?.let { e.putLong("currentSize", it) }
        problem?.let { e.putString("problem", it) }
        e.apply()
    }

    // ------------------------------------------------------------- the notification

    fun foregroundInfo(c: Context, done: Int, total: Int, name: String, sent: Long, size: Long): ForegroundInfo {
        val nm = c.getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= 26 && nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, "Adding files", NotificationManager.IMPORTANCE_LOW)
                .apply { setShowBadge(false) })
        }
        val stop = PendingIntent.getBroadcast(c, 0, Intent(c, UploadStopReceiver::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val open = PendingIntent.getActivity(c, 0,
            Intent(c, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val n = NotificationCompat.Builder(c, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_soundstorm)
            .setContentTitle("Adding ${done + 1} of $total to SoundStorm")
            .setContentText(if (size > 0) "$name - ${sent / 1_000_000} of ${size / 1_000_000} MB" else name)
            .setOngoing(true)
            .setSilent(true)
            .setContentIntent(open)
            .addAction(0, "Stop", stop)
            .setProgress(1000, if (size > 0) (sent * 1000 / size).toInt() else 0, size <= 0)
            .build()
        return if (Build.VERSION.SDK_INT >= 29) ForegroundInfo(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
            else ForegroundInfo(NOTIFICATION_ID, n)
    }

    // ------------------------------------------------------------- sending

    class Refused(val code: Int, message: String) : Exception(message)

    /** One file, the whole of it as the body. The server's answer, or a Refused. */
    fun send(c: Context, server: String, job: JSONObject, onProgress: (Long) -> Unit): String {
        val q = StringBuilder("?path=").append(URLEncoder.encode(job.optString("path"), "UTF-8"))
            .append("&kind=").append(URLEncoder.encode(job.optString("kind"), "UTF-8"))
        val taken = job.optLong("taken", 0)
        if (taken > 0) q.append("&taken=").append(taken)
        // A taken name, as the person chose before sending: keep both or replace.
        val conflict = job.optString("conflict")
        if (conflict == "keep" || conflict == "replace") q.append("&conflict=").append(conflict)
        WebCookies.install()
        val conn = URL(server.trimEnd('/') + "/api/upload" + q).openConnection() as HttpURLConnection
        conn.requestMethod = "PUT"
        conn.connectTimeout = 15_000
        conn.readTimeout = 120_000
        conn.instanceFollowRedirects = false
        conn.doOutput = true
        conn.setRequestProperty("Content-Type", "application/octet-stream")
        val size = job.optLong("size", -1)
        if (size > 0) conn.setFixedLengthStreamingMode(size) else conn.setChunkedStreamingMode(256 * 1024)
        current = conn
        try {
            c.contentResolver.openInputStream(Uri.parse(job.optString("uri")))?.use { input ->
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
                        if (now - shown > 1000) { shown = now; onProgress(sent) }
                    }
                }
            } ?: throw Refused(0, "could not read the file")
            val code = conn.responseCode
            val body = runCatching { (if (code in 200..299) conn.inputStream else conn.errorStream).bufferedReader().readText() }.getOrNull() ?: ""
            if (code in 200..299) return runCatching { JSONObject(body).optString("dest") }.getOrNull() ?: ""
            val msg = runCatching { JSONObject(body).optString("error") }.getOrNull()?.takeIf { it.isNotBlank() } ?: "the server answered $code"
            throw Refused(code, msg)
        } finally {
            current = null
            conn.disconnect()
        }
    }
}

/** The notification's Stop. */
class UploadStopReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        Uploads.stop(context.applicationContext)
    }
}

/** Sends what is waiting, one file at a time, in the foreground. */
class UploadWorker(context: Context, params: WorkerParameters) : Worker(context, params) {
    override fun doWork(): Result {
        val c = applicationContext
        Uploads.record(c, running = true, problem = "")
        try {
            while (!isStopped) {
                val q = Uploads.load(c)
                if (q.optBoolean("stop")) return Result.success()
                val all = q.optJSONArray("jobs") ?: return Result.success()
                val list = (0 until all.length()).map { all.getJSONObject(it) }
                val i = list.indexOfFirst { it.optString("state") == "waiting" }
                if (i < 0) return Result.success()
                val job = list[i]
                val total = list.size
                val name = job.optString("name")
                val size = job.optLong("size", 0)
                Uploads.record(c, name = name, sent = 0, size = size)
                runCatching { setForegroundAsync(Uploads.foregroundInfo(c, i, total, name, 0, size)).get() }
                val server = q.optString("server")
                val outcome: Pair<String, String> = try {
                    val dest = Uploads.send(c, server, job) { sent ->
                        Uploads.record(c, sent = sent)
                        runCatching { setForegroundAsync(Uploads.foregroundInfo(c, i, total, name, sent, size)) }
                    }
                    "done" to dest
                } catch (e: Uploads.Refused) {
                    when (e.code) {
                        409 -> "skipped" to (e.message ?: "already there")
                        401, 403 -> {
                            Uploads.record(c, problem = "Signed out - open SoundStorm and sign in again.")
                            return Result.failure()
                        }
                        0 -> "failed" to (e.message ?: "could not read the file")
                        else -> if (e.code >= 500 || e.code == 429) {
                            // The server busy or the disk full: tried again later.
                            Uploads.record(c, problem = e.message)
                            return Result.retry()
                        } else "failed" to (e.message ?: "refused")
                    }
                } catch (e: IOException) {
                    if (Uploads.load(c).optBoolean("stop")) return Result.success()
                    // The connection dropped: this file again, from its start, later.
                    Uploads.record(c, problem = "Could not reach the server - trying again when it can.")
                    return Result.retry()
                } catch (e: SecurityException) {
                    "failed" to "the file can no longer be read"
                }
                Uploads.update(c) { cur ->
                    val a = cur.optJSONArray("jobs") ?: return@update
                    for (k in 0 until a.length()) {
                        val j = a.getJSONObject(k)
                        if (j.optString("uri") == job.optString("uri") && j.optString("state") == "waiting") {
                            j.put("state", outcome.first)
                            if (outcome.first == "done") j.put("dest", outcome.second) else j.put("error", outcome.second)
                            break
                        }
                    }
                }
                // Done with this file: its read permission is not needed.
                runCatching {
                    c.contentResolver.releasePersistableUriPermission(Uri.parse(job.optString("uri")), Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }
            }
            return Result.retry()
        } finally {
            Uploads.record(c, running = false, name = "", sent = 0, size = 0)
        }
    }
}
