package dev.soundstorm.app

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.OpenableColumns
import android.util.Base64
import android.webkit.MimeTypeMap
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.RandomAccessFile
import java.util.UUID

/**
 * Files shared to SoundStorm from another app (the system's Share sheet): a
 * song from Files, a film from the gallery, a book from an email - added to
 * the library through the same review as Add media (the owner's asking).
 *
 * A share only lends the app the files while it is open, and the files go up
 * in the background (Uploads) after it may have been left, so each is first
 * copied into the app's own storage. The page is then handed the copies as
 * AppFiles (name, size, date) and reads the little it needs - a sample to
 * spot a copy, the ends to name a different one - through readFile, as the
 * iPhone app's picked files are read. Once a copy has gone up, it is deleted;
 * any left over a week later go too.
 */
object Shared {
    data class Item(val id: String, val name: String, val size: Long, val modified: Long, val file: File?, val uri: Uri)

    private val items = mutableMapOf<String, Item>()
    private const val KEEP_DAYS = 7L

    private fun dir(c: Context) = File(c.filesDir, "shared")

    /** The files a Share sent, or none: ACTION_SEND or ACTION_SEND_MULTIPLE. */
    fun urisOf(intent: Intent?): List<Uri> {
        intent ?: return emptyList()
        if (intent.action != Intent.ACTION_SEND && intent.action != Intent.ACTION_SEND_MULTIPLE) return emptyList()
        val out = linkedSetOf<Uri>()
        @Suppress("DEPRECATION")
        when (intent.action) {
            Intent.ACTION_SEND -> (if (Build.VERSION.SDK_INT >= 33) intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
                else intent.getParcelableExtra(Intent.EXTRA_STREAM) as? Uri)?.let { out += it }
            Intent.ACTION_SEND_MULTIPLE -> (if (Build.VERSION.SDK_INT >= 33) intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java)
                else intent.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM))?.let { out += it }
        }
        intent.clipData?.let { clip -> for (i in 0 until clip.itemCount) clip.getItemAt(i).uri?.let { out += it } }
        // Only content the sharing app lends, never a file path of this app's.
        return out.filter { it.scheme == "content" }
    }

    /**
     * Copies each shared file into the app's storage and remembers it; slow
     * for a film, so it runs off the main thread. Answers the page's list.
     * A file that cannot be copied (no room) is read from where it was shared,
     * which holds while the app is open.
     */
    fun receive(c: Context, uris: List<Uri>, sharedType: String? = null): JSONArray {
        prune(c)
        val list = JSONArray()
        for (uri in uris) {
            var name = uri.lastPathSegment ?: "shared"
            var size = -1L
            var modified = System.currentTimeMillis()
            // The two columns every provider must answer, asked by name: asked
            // for everything, some providers (the media store's downloads)
            // refuse, and the file was named "24", its row number.
            runCatching {
                c.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { cur ->
                    if (cur.moveToFirst()) {
                        cur.getString(0)?.let { name = it }
                        if (!cur.isNull(1)) size = cur.getLong(1)
                    }
                }
            }
            runCatching {
                c.contentResolver.query(uri, arrayOf("last_modified"), null, null, null)?.use { cur ->
                    if (cur.moveToFirst() && !cur.isNull(0)) cur.getLong(0).takeIf { it > 0 }?.let { modified = it }
                }
            }
            // A name is the sharing app's text: only its last part, nothing
            // that climbs out of the folder.
            name = name.substringAfterLast('/').substringAfterLast('\\').trim().ifEmpty { "shared" }.take(200)
            // SoundStorm files by extension: one missing is taken from the
            // file's type, which a share always carries.
            if (!name.contains('.')) {
                val type = runCatching { c.contentResolver.getType(uri) }.getOrNull() ?: sharedType?.takeUnless { it.endsWith("/*") }
                val ext = type?.let { MimeTypeMap.getSingleton().getExtensionFromMimeType(it) }
                if (ext != null) name = "$name.$ext"
            }
            val id = UUID.randomUUID().toString()
            var file: File? = File(dir(c), "$id/$name")
            val room = dir(c).apply { mkdirs() }.usableSpace
            if (size < 0 || size * 11 / 10 + (200L shl 20) > room) file = null
            file?.let { f ->
                val copied = runCatching {
                    f.parentFile?.mkdirs()
                    c.contentResolver.openInputStream(uri)?.use { input -> f.outputStream().use { input.copyTo(it, 1 shl 20) } }
                        ?: error("unreadable")
                    if (size < 0) size = f.length()
                    f.setLastModified(modified)
                }.isSuccess
                if (!copied) { f.parentFile?.deleteRecursively(); file = null }
            }
            val readUri = file?.let(Uri::fromFile) ?: uri
            synchronized(items) { items[id] = Item(id, name, size, modified, file, readUri) }
            Uploads.rememberFile(name, size, readUri)
            list.put(JSONObject().put("id", id).put("name", name).put("size", size).put("lastModified", modified))
        }
        return list
    }

    /** Bytes from..to of a shared file, base64, for the page; null if not ours. */
    fun read(c: Context, id: String, from: Long, to: Long): String? {
        val item = synchronized(items) { items[id] } ?: return null
        val n = (to - from).coerceIn(0, 8L shl 20).toInt()
        if (from < 0) return null
        val buf = ByteArray(n)
        var got = 0
        runCatching {
            val f = item.file
            if (f != null) {
                RandomAccessFile(f, "r").use { r ->
                    r.seek(from)
                    while (got < n) { val k = r.read(buf, got, n - got); if (k < 0) break; got += k }
                }
            } else {
                c.contentResolver.openInputStream(item.uri)?.use { input ->
                    var skip = from
                    while (skip > 0) { val k = input.skip(skip); if (k <= 0) break; skip -= k }
                    while (got < n) { val k = input.read(buf, got, n - got); if (k < 0) break; got += k }
                }
            }
        }.onFailure { return null }
        return Base64.encodeToString(buf, 0, got, Base64.NO_WRAP)
    }

    /** A copy that has gone up (or was declined as already there) is deleted. */
    fun forget(c: Context, uri: String) {
        val path = Uri.parse(uri).takeIf { it.scheme == "file" }?.path ?: return
        val f = File(path)
        if (f.canonicalPath.startsWith(dir(c).canonicalPath + File.separator)) f.parentFile?.deleteRecursively()
    }

    /** Copies left over a week (never sent) are deleted. */
    private fun prune(c: Context) {
        val cutoff = System.currentTimeMillis() - KEEP_DAYS * 24 * 3600 * 1000
        dir(c).listFiles()?.forEach { if (it.lastModified() < cutoff) it.deleteRecursively() }
    }
}
