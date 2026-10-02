package dev.soundstorm.app

import android.annotation.SuppressLint
import android.app.Activity
import android.app.AlertDialog
import android.app.UiModeManager
import android.content.res.Configuration
import android.content.ActivityNotFoundException
import android.content.Intent
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Message
import android.text.InputType
import android.util.TypedValue
import android.view.Gravity
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputMethodManager
import android.window.OnBackInvokedDispatcher
import android.webkit.CookieManager
import android.webkit.JsPromptResult
import android.webkit.JsResult
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import org.json.JSONObject
import java.util.concurrent.Executors

/**
 * Shows the connect screen until a server is known, then the server's own web
 * app, full screen. The page is the app: this only gives it what a browser
 * tab would (dialogs, file pickers, full-screen video, links out, recovering
 * from a failed load) plus what a tab cannot - playing on with the screen off,
 * with lock-screen controls (PlaybackService). The Android counterpart of the
 * iPhone app's RootViewController, ConnectViewController and WebViewController.
 */
class MainActivity : Activity() {
    private lateinit var root: FrameLayout
    private lateinit var content: FrameLayout
    private lateinit var statusScrim: View
    private lateinit var navScrim: View
    private var webView: WebView? = null
    private var server: Uri? = null
    private var failure: View? = null
    private var fullscreen: View? = null
    private var fullscreenCallback: WebChromeClient.CustomViewCallback? = null
    private var fileCallback: ValueCallback<Array<Uri>>? = null
    private var safeCss = ""
    private var safeScript: androidx.webkit.ScriptHandler? = null
    /** Set once the saved home name failed and its away twin was tried. */
    private var triedAway = false
    private val background = Executors.newSingleThreadExecutor()

    /** On a TV (Google TV, Android TV, Fire TV): the page lays itself out for
     *  the room and the remote, told by its user agent. */
    private val isTv by lazy {
        getSystemService(UiModeManager::class.java)?.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION ||
            packageManager.hasSystemFeature("android.software.leanback")
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Both bars hidden, everywhere (the owner's asking - the one thing
        // the installed web app could never do, as Chrome owns its bars): a
        // swipe in from an edge shows them for a moment, as in a game or a
        // video. The app draws into the camera cutout too, so there is no
        // black strip across the top; the page is kept clear of the cutout
        // itself, and that sliver is painted in the page's theme colour.
        WindowCompat.setDecorFitsSystemWindows(window, false)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            window.attributes.layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS
        } else if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            window.attributes.layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES
        }
        WindowInsetsControllerCompat(window, window.decorView).apply {
            isAppearanceLightStatusBars = false
            isAppearanceLightNavigationBars = false
        }
        hideBars()
        root = FrameLayout(this).apply { setBackgroundColor(Color.BLACK) }
        content = FrameLayout(this)
        statusScrim = View(this).apply { setBackgroundColor(Color.BLACK) }
        navScrim = View(this).apply { setBackgroundColor(Color.BLACK) }
        root.addView(content, FrameLayout.LayoutParams(MATCH, MATCH))
        root.addView(statusScrim, FrameLayout.LayoutParams(MATCH, 0, Gravity.TOP))
        root.addView(navScrim, FrameLayout.LayoutParams(MATCH, 0, Gravity.BOTTOM))
        ViewCompat.setOnApplyWindowInsetsListener(root) { _, insets ->
            // The bars' own space only (none while they are hidden). The
            // camera cutout is not padded: the page draws up into it, and
            // the web view tells the page where it is (env(safe-area-inset-*)),
            // which the page already keeps its words clear of.
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars())
            val ime = insets.getInsets(WindowInsetsCompat.Type.ime())
            val cut = insets.getInsets(WindowInsetsCompat.Type.displayCutout())
            setSafeArea(cut.top, cut.right, cut.bottom, cut.left)
            content.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, ime.bottom))
            statusScrim.layoutParams = (statusScrim.layoutParams as FrameLayout.LayoutParams).apply { height = bars.top }
            navScrim.layoutParams = (navScrim.layoutParams as FrameLayout.LayoutParams).apply { height = bars.bottom }
            WindowInsetsCompat.CONSUMED
        }
        setContentView(root)
        // Back, the new way. From Android 16 an app built for it no longer
        // gets onBackPressed: without this the system closed the app on
        // Back, from inside a menu or Now Playing - found on the TV.
        if (Build.VERSION.SDK_INT >= 33) {
            onBackInvokedDispatcher.registerOnBackInvokedCallback(OnBackInvokedDispatcher.PRIORITY_DEFAULT) { goBack() }
        }

        // (A server address given in the launching intent, once used for
        // testing, is gone: the activity is exported, so any app on the device
        // could have pointed SoundStorm at its own server - found by a
        // security review.)
        val saved = ServerAddress.saved(this)
        // Opened from a TV's sign-in code: the server's page asks "Sign in a
        // TV?" for it.
        val link = tvLink(intent)
        if (link != null) pendingLink = link.second
        val start = link?.first ?: saved
        if (start != null) showWeb(start) else showConnect(null)
        // Photo backup's jobs, if it is on: kept up to date with its options.
        if (!isTv) PhotoBackup.schedule(applicationContext)
    }

    /**
     * Tells the page how much room the camera cutout needs, in CSS pixels, as
     * the --safe-* values style.css spaces its edges by. The web view itself
     * reports 0 for a cutout the app draws into, which would put the page's
     * header under the camera. Set before each page's own scripts run (a
     * document-start script, replaced when the cutout changes, as on turning
     * the phone) and on the page already showing.
     */
    private fun setSafeArea(top: Int, right: Int, bottom: Int, left: Int) {
        // A TV has no cutout; its margins are the page's own (html.tv).
        if (isTv) return
        val d = resources.displayMetrics.density
        val css = "t=${(top / d).toInt()};r=${(right / d).toInt()};b=${(bottom / d).toInt()};l=${(left / d).toInt()}"
        if (css == safeCss) return
        safeCss = css
        applySafeArea()
    }

    private fun safeAreaScript(): String {
        val v = safeCss.split(';').associate { it.substringBefore('=') to it.substringAfter('=') }
        return "(() => { const s = document.documentElement.style;" +
            " s.setProperty('--safe-top', '${v["t"] ?: 0}px'); s.setProperty('--safe-right', '${v["r"] ?: 0}px');" +
            " s.setProperty('--safe-bottom', '${v["b"] ?: 0}px'); s.setProperty('--safe-left', '${v["l"] ?: 0}px'); })();"
    }

    private fun applySafeArea() {
        val view = webView ?: return
        val origin = server?.let(ServerAddress::origin) ?: return
        if (safeCss.isEmpty()) return
        val script = safeAreaScript()
        if (WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)) {
            safeScript?.remove()
            safeScript = WebViewCompat.addDocumentStartJavaScript(view, script, setOf(origin))
        }
        view.evaluateJavascript(script, null)
    }

    /** Hides the status and navigation bars; a swipe from an edge shows them briefly. */
    private fun hideBars() {
        WindowInsetsControllerCompat(window, window.decorView).apply {
            systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            hide(WindowInsetsCompat.Type.systemBars())
        }
    }

    // Hidden again whenever the window comes back - after a dialog, the file
    // picker, another app, or the screen waking.
    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus) hideBars()
    }

    override fun onResume() {
        super.onResume()
        resumed = true
        hideBars()
        // The page Android ended while the app was in the background, made
        // again now it is looked at.
        if (pageLost) {
            pageLost = false
            server?.let { showWeb(it, keepMusic = true) }
        }
    }

    // The sign-in is a cookie, which the web view writes to storage in its own
    // time; Android often ends a backgrounded app before it has, and the next
    // open asked for the password again (the owner's report). Written the
    // moment the app is left.
    override fun onPause() {
        resumed = false
        CookieManager.getInstance().flush()
        super.onPause()
    }

    private var resumed = false
    private var pageLost = false

    override fun onDestroy() {
        webView?.let {
            MediaBridge.detach(it)
            NativeAudio.detachView(it)
            (it.parent as? android.view.ViewGroup)?.removeView(it)
            it.destroy()
        }
        background.shutdownNow()
        super.onDestroy()
    }

    // ---------------------------------------------------------------- connect

    private fun showConnect(prefill: Uri?) {
        tearDownWeb()
        setStatusColor(Color.BLACK)
        val surface = Color.rgb(0x17, 0x1b, 0x22)
        val accent = Color.rgb(0x6a, 0xa8, 0xff)

        val column = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_HORIZONTAL
            setPadding(dp(24), dp(24), dp(24), dp(24))
        }
        val logo = ImageView(this).apply {
            setImageResource(R.drawable.logo)
            clipToOutline = true
            background = GradientDrawable().apply { cornerRadius = dp(18).toFloat(); setColor(Color.BLACK) }
        }
        column.addView(logo, LinearLayout.LayoutParams(dp(80), dp(80)).apply { bottomMargin = dp(16) })
        column.addView(TextView(this).apply {
            text = getString(R.string.app_name)
            setTextColor(Color.WHITE)
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 32f)
            setTypeface(Typeface.DEFAULT, Typeface.BOLD_ITALIC)
        }, wrap(bottom = 16))
        // Your servers: the latest used first, the one in use ticked; a tap
        // switches, a hold offers Rename and Remove.
        val servers = ServerAddress.all(this)
        if (servers.isNotEmpty()) {
            column.addView(TextView(this).apply {
                text = "Your servers"
                setTextColor(Color.argb(0x99, 0xeb, 0xeb, 0xf5))
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 13f)
                setTypeface(Typeface.DEFAULT, Typeface.BOLD)
            }, fill(bottom = 8))
            val inUse = ServerAddress.saved(this)?.let(ServerAddress::origin)
            for (s in servers) column.addView(serverRow(s, ServerAddress.origin(s.url) == inUse, surface, accent), fill(bottom = 8))
        }
        column.addView(TextView(this).apply {
            text = if (servers.isEmpty()) "Enter your server's address - the one you open in a browser."
                else "Or add another - the address you open in a browser."
            setTextColor(Color.argb(0x99, 0xeb, 0xeb, 0xf5))
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 15f)
            gravity = Gravity.CENTER
        }, fill(top = if (servers.isEmpty()) 0 else 16, bottom = 28))
        val known = prefill != null && servers.any { ServerAddress.origin(it.url) == ServerAddress.origin(prefill) }
        val field = EditText(this).apply {
            hint = "yourname.home.soundstorm.dev"
            // Without the scheme for https (the default when none is typed),
            // with it for plain http, which would otherwise be read as https.
            // A server already in the list is not typed out again.
            setText(prefill?.takeUnless { known }?.let {
                val hostPort = it.host + if (it.port != -1) ":${it.port}" else ""
                if (it.scheme == "https") hostPort else "${it.scheme}://$hostPort"
            } ?: "")
            setTextColor(Color.WHITE)
            setHintTextColor(Color.argb(0x66, 0xff, 0xff, 0xff))
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            imeOptions = EditorInfo.IME_ACTION_GO
            isSingleLine = true
            setPadding(dp(14), 0, dp(14), 0)
            background = GradientDrawable().apply { cornerRadius = dp(12).toFloat(); setColor(surface) }
        }
        column.addView(field, fill(height = 50, bottom = 16))
        val button = Button(this).apply {
            text = "Connect"
            isAllCaps = false
            setTextColor(Color.BLACK)
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 17f)
            setTypeface(typeface, Typeface.BOLD)
            background = GradientDrawable().apply { cornerRadius = dp(14).toFloat(); setColor(accent) }
            stateListAnimator = null
        }
        column.addView(button, fill(height = 52, bottom = 16))
        val busy = ProgressBar(this).apply { visibility = View.GONE; isIndeterminate = true }
        column.addView(busy, wrap(bottom = 8))
        val message = TextView(this).apply {
            setTextColor(Color.rgb(0xff, 0x45, 0x3a))
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 13f)
            gravity = Gravity.CENTER
        }
        column.addView(message, fill())

        // Centred in whatever the keyboard leaves, and no wider than 420dp so
        // it stays a column on a tablet.
        // Scrolls once the list is longer than the screen.
        val holder = FrameLayout(this)
        holder.addView(column, FrameLayout.LayoutParams(minOf(dp(420), resources.displayMetrics.widthPixels), FrameLayout.LayoutParams.WRAP_CONTENT, Gravity.CENTER))
        val scroller = android.widget.ScrollView(this).apply { isFillViewport = true }
        scroller.addView(holder, FrameLayout.LayoutParams(MATCH, ViewGroup.LayoutParams.WRAP_CONTENT))
        content.removeAllViews()
        content.addView(scroller, FrameLayout.LayoutParams(MATCH, MATCH))

        var checking = false
        fun connect() {
            if (checking) return
            val parsed = ServerAddress.parse(field.text.toString())
            if (parsed == null) {
                message.text = "That doesn't look like a web address."
                return
            }
            message.text = ""
            checking = true
            field.isEnabled = false
            button.text = "Connecting"
            busy.visibility = View.VISIBLE
            background.execute {
                val problem = try {
                    ServerAddress.check(parsed)
                    null
                } catch (e: ServerAddress.CheckFailed) {
                    e.message
                }
                runOnUiThread {
                    checking = false
                    field.isEnabled = true
                    button.text = "Connect"
                    busy.visibility = View.GONE
                    if (problem == null) {
                        ServerAddress.remember(this, parsed)
                        hideKeyboard(field)
                        showWeb(parsed)
                    } else {
                        message.text = problem
                    }
                }
            }
        }
        button.setOnClickListener { connect() }
        field.setOnEditorActionListener { _, action, event ->
            if (action == EditorInfo.IME_ACTION_GO || event?.keyCode == KeyEvent.KEYCODE_ENTER) {
                connect(); true
            } else false
        }
        // The keyboard only when there is nothing to pick from.
        if (servers.isEmpty()) {
            field.requestFocus()
            field.post { getSystemService(InputMethodManager::class.java).showSoftInput(field, 0) }
        }
    }

    /** One saved server on the connect screen. */
    private fun serverRow(s: ServerAddress.Server, inUse: Boolean, surface: Int, accent: Int): View {
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(14), dp(10), dp(14), dp(10))
            isClickable = true
            isFocusable = true
            // A ring for the TV remote's focus, the fill otherwise.
            background = android.graphics.drawable.StateListDrawable().apply {
                addState(intArrayOf(android.R.attr.state_focused), GradientDrawable().apply {
                    cornerRadius = dp(12).toFloat(); setColor(surface); setStroke(dp(2), Color.WHITE)
                })
                addState(intArrayOf(android.R.attr.state_pressed), GradientDrawable().apply {
                    cornerRadius = dp(12).toFloat(); setColor(Color.rgb(0x24, 0x2a, 0x34))
                })
                addState(intArrayOf(), GradientDrawable().apply { cornerRadius = dp(12).toFloat(); setColor(surface) })
            }
        }
        val texts = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        texts.addView(TextView(this).apply {
            text = s.name
            setTextColor(Color.WHITE)
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 16f)
            setTypeface(Typeface.DEFAULT, Typeface.BOLD)
            isSingleLine = true
            ellipsize = android.text.TextUtils.TruncateAt.END
        })
        texts.addView(TextView(this).apply {
            text = ServerAddress.origin(s.url).removePrefix("https://")
            setTextColor(Color.argb(0x99, 0xeb, 0xeb, 0xf5))
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 13f)
            isSingleLine = true
            ellipsize = android.text.TextUtils.TruncateAt.MIDDLE
        })
        row.addView(texts, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        if (inUse) row.addView(TextView(this).apply {
            text = "✓"
            contentDescription = "In use"
            setTextColor(accent)
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 20f)
        }, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply { marginStart = dp(10) })
        row.setOnClickListener {
            ServerAddress.remember(this, s.url)
            hideKeyboard(row)
            showWeb(s.url)
        }
        row.setOnLongClickListener {
            AlertDialog.Builder(this)
                .setTitle(s.name)
                .setItems(arrayOf("Rename", "Remove")) { _, which ->
                    if (which == 0) renameServer(s) else {
                        AlertDialog.Builder(this)
                            .setTitle("Remove ${s.name}?")
                            .setMessage("It comes off this list. Nothing on the server changes, and you can add it again with its address.")
                            .setPositiveButton("Remove") { _, _ ->
                                ServerAddress.forget(this, s.url)
                                showConnect(null)
                            }
                            .setNegativeButton("Cancel", null)
                            .show()
                    }
                }
                .show()
            true
        }
        return row
    }

    private fun renameServer(s: ServerAddress.Server) {
        val box = EditText(this).apply {
            setText(s.name)
            setSelectAllOnFocus(true)
            isSingleLine = true
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_WORDS
        }
        val frame = FrameLayout(this).apply { setPadding(dp(20), dp(8), dp(20), 0); addView(box) }
        AlertDialog.Builder(this)
            .setTitle("Rename")
            .setView(frame)
            .setPositiveButton("Save") { _, _ ->
                ServerAddress.rename(this, s.url, box.text.toString().take(60))
                showConnect(null)
            }
            .setNegativeButton("Cancel", null)
            .show()
    }

    // -------------------------------------------------------------------- web

    @SuppressLint("SetJavaScriptEnabled")
    private fun showWeb(target: Uri, keepMusic: Boolean = false) {
        tearDownWeb(keepMusic)
        server = target
        content.removeAllViews()
        val view = WebView(this)
        view.setBackgroundColor(Color.BLACK)
        view.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            // The queue moving on to the next song is not a tap.
            mediaPlaybackRequiresUserGesture = false
            setSupportMultipleWindows(true)
            javaScriptCanOpenWindowsAutomatically = false
            // Nothing on the phone is the page's to read (a security review).
            allowContentAccess = false
            allowFileAccess = false
            // Chrome's own user agent, with a name the page can test for.
            userAgentString = "$userAgentString SoundStormApp/1" + if (isTv) " SoundStormTV/1" else ""
        }
        // chrome://inspect only in a build marked debuggable, which the
        // published one is not (build.gradle.kts): with it, anyone with adb
        // access had a console inside the signed-in page.
        WebView.setWebContentsDebuggingEnabled((applicationInfo.flags and android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE) != 0)

        val origin = ServerAddress.origin(target)
        ServerAddress.current = origin
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            WebViewCompat.addWebMessageListener(view, "SoundStormNative", setOf(origin)) { _, message, source, isMainFrame, _ ->
                if (isMainFrame && source.toString().trimEnd('/') == origin) received(message.data)
            }
        }
        if (WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)) {
            WebViewCompat.addDocumentStartJavaScript(view, PageScript.SOURCE, setOf(origin))
        }
        view.webViewClient = Client()
        view.webChromeClient = Chrome()
        content.addView(view, FrameLayout.LayoutParams(MATCH, MATCH))
        webView = view
        MediaBridge.attach(view)
        NativeAudio.attachView(view)
        safeScript = null
        applySafeArea()
        load()
    }

    /**
     * Scans a TV's sign-in QR code with Google's own scanner (Play services:
     * its screen, no camera permission for the app), and hands the code to
     * the page, which asks "Sign in a TV?". The QR holds the TV's address
     * with ?link=<code>; a code alone, or a soundstorm://link, also does.
     */
    private fun scanTvCode() {
        val options = com.google.mlkit.vision.codescanner.GmsBarcodeScannerOptions.Builder()
            .setBarcodeFormats(com.google.mlkit.vision.barcode.common.Barcode.FORMAT_QR_CODE)
            .build()
        val tell = { what: String ->
            webView?.evaluateJavascript("window.__soundstormScanned && window.__soundstormScanned(" + JSONObject.quote(what) + ")", null)
        }
        com.google.mlkit.vision.codescanner.GmsBarcodeScanning.getClient(this, options).startScan()
            .addOnSuccessListener { barcode ->
                val code = scannedCode(barcode.rawValue)
                if (code == null) {
                    tell("not-ours")
                    return@addOnSuccessListener
                }
                webView?.evaluateJavascript("window.__soundstormLink && window.__soundstormLink(" + JSONObject.quote(code) + ")", null)
            }
            .addOnFailureListener { tell("failed") }
    }

    private fun scannedCode(raw: String?): String? {
        if (raw.isNullOrBlank()) return null
        val uri = runCatching { Uri.parse(raw.trim()) }.getOrNull()
        val fromLink = uri?.let { u ->
            if (u.isHierarchical) u.getQueryParameter("link") ?: u.getQueryParameter("code") else null
        }
        val code = (fromLink ?: raw).uppercase().filter { it.isLetterOrDigit() }
        return code.takeIf { it.length == 6 && (fromLink != null || raw.trim().length <= 9) }
    }

    /** A TV's sign-in code waiting for the page to load (soundstorm://link). */
    private var pendingLink: String? = null

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        val link = tvLink(intent) ?: return
        val view = webView
        if (view != null && server?.let(ServerAddress::origin) == ServerAddress.origin(link.first)) {
            // The page is there: it asks at once, nothing reloaded (music
            // playing in it carries on). A page that cannot - not signed in,
            // or loaded before it knew how (the owner's first try just
            // brought the app forward) - is loaded again with the code.
            val code = link.second
            view.evaluateJavascript(
                "(typeof window.__soundstormLink === 'function' && window.__soundstormLink(" + JSONObject.quote(code) + ")) === true",
            ) { took ->
                if (took != "true") {
                    pendingLink = code
                    load()
                }
            }
        } else {
            pendingLink = link.second
            showWeb(link.first)
        }
    }

    /**
     * A soundstorm://link?server=&code= link: the server it is for and the
     * code. Only a server this app already knows - its saved ones, or the
     * install's secure name it moved to - is opened: a link must never point
     * the app somewhere new. Unknown, the app's own server is used, where a
     * code from elsewhere simply finds no TV.
     */
    private fun tvLink(intent: Intent?): Pair<Uri, String>? {
        val data = intent?.data ?: return null
        if (data.scheme != "soundstorm" || data.host != "link") return null
        val code = (data.getQueryParameter("code") ?: return null).uppercase().filter { it.isLetterOrDigit() }
        if (code.length != 6) return null
        val wanted = data.getQueryParameter("server")?.let(ServerAddress::parse)
        val known = ServerAddress.all(this).map { it.url } + listOfNotNull(ServerAddress.saved(this))
        val installId = { u: Uri -> u.host?.substringBefore('.')?.takeIf { u.host?.endsWith(".soundstorm.dev") == true } }
        val match = wanted?.let { w ->
            known.firstOrNull { ServerAddress.origin(it) == ServerAddress.origin(w) }
                ?: known.firstOrNull { installId(it) != null && installId(it) == installId(w) }
                ?: ServerAddress.current?.let(ServerAddress::parse)?.takeIf { ServerAddress.origin(it) == ServerAddress.origin(w) }
        }
        val server = match ?: ServerAddress.saved(this) ?: return null
        return server to code
    }

    private fun load() {
        failure?.let { content.removeView(it) }
        failure = null
        server?.let { s ->
            val code = pendingLink
            pendingLink = null
            webView?.loadUrl(if (code != null) s.buildUpon().appendQueryParameter("link", code).build().toString() else s.toString())
        }
    }

    private fun tearDownWeb(keepMusic: Boolean = false) {
        webView?.let {
            MediaBridge.detach(it)
            NativeAudio.detachView(it)
            // A page Android ended is not the music ending: the native player
            // plays on through the songs it was handed.
            if (!keepMusic) NativeAudio.handle(applicationContext, org.json.JSONObject().put("cmd", "stop"))
            it.stopLoading()
            it.destroy()
        }
        webView = null
        // Nothing can play without the page.
        PlaybackService.stop(this)
    }

    private fun received(data: String?) {
        val message = runCatching { JSONObject(data ?: "") }.getOrNull() ?: return
        when (message.optString("type")) {
            // Posted, so the web view is not torn down inside its own callback.
            "changeServer" -> content.post { showConnect(server) }
            "media" -> MediaBridge.update(applicationContext, message)
            "audio" -> {
                // Songs from the server now play natively: whatever the page
                // was playing the old way (a download) is let go.
                if (message.optString("cmd") == "load") PlaybackService.stop(this)
                NativeAudio.handle(applicationContext, message)
            }
            "interrupted" -> MediaBridge.interruption(true)
            "resumed" -> MediaBridge.interruption(false)
            "themeColor" -> setStatusColor(runCatching { Color.parseColor(message.optString("color")) }.getOrDefault(Color.BLACK))
            "scanCode" -> scanTvCode()
            // A playback report: what the native player saw (PlayerLog).
            "playerLog" -> webView?.evaluateJavascript(
                "window.__soundstormPlayerLog && window.__soundstormPlayerLog(" +
                    JSONObject.quote(PlayerLog.text(applicationContext)) + ")", null)
            "backup" -> {
                PhotoBackup.rememberServer(applicationContext, ServerAddress.current)
                backup(message.optString("cmd"), message.optJSONObject("options") ?: JSONObject())
            }
        }
    }

    /**
     * Phone photo backup, as the page's Settings asks: "status" answers how
     * it is going; "set" turns it on or off and changes its options, asking
     * for the camera roll first when it is turned on.
     */
    private fun backup(cmd: String, options: JSONObject) {
        if (isTv) return
        when (cmd) {
            "set" -> {
                if (options.optBoolean("enabled") && !PhotoBackup.hasPermission(this)) {
                    pendingBackup = options
                    requestPermissions(PhotoBackup.permissions(), BACKUP_PERMISSION)
                    return
                }
                PhotoBackup.configure(applicationContext, options)
            }
        }
        reportBackup()
    }

    private var pendingBackup: JSONObject? = null

    private fun reportBackup() {
        val view = webView ?: return
        view.evaluateJavascript(
            "window.__soundstormBackup && window.__soundstormBackup(" + PhotoBackup.status(this) + ")", null)
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != BACKUP_PERMISSION) return
        val options = pendingBackup ?: return
        pendingBackup = null
        // Refused, backup stays off and the page says why.
        if (!PhotoBackup.hasPermission(this)) options.put("enabled", false)
        PhotoBackup.configure(applicationContext, options)
        reportBackup()
    }

    private fun setStatusColor(color: Int) {
        statusScrim.setBackgroundColor(color)
    }

    private fun isServer(url: Uri): Boolean {
        val s = server ?: return false
        return url.scheme == s.scheme && url.host == s.host && url.port == s.port
    }

    /**
     * https on one of this install's own names, on the port in use now. When
     * the server is known by one of its names, only its twin (the same id,
     * home and away) - any install can get a name under soundstorm.dev, and a
     * security review found a page could send the app to another's. From a
     * plain address (a LAN IP) the name cannot be checked, so the move is only
     * followed, never saved (see shouldOverrideUrlLoading).
     */
    private fun isOwnSecureName(url: Uri): Boolean {
        val s = server ?: return false
        val host = url.host?.lowercase() ?: return false
        if (url.scheme != "https" || isServer(url)) return false
        if (!host.endsWith(".home.soundstorm.dev") && !host.endsWith(".net.soundstorm.dev")) return false
        if (url.port != s.port) return false
        val current = s.host?.lowercase() ?: return false
        if (current.endsWith(".soundstorm.dev")) return host.substringBefore('.') == current.substringBefore('.')
        return true
    }

    private fun openOutside(url: Uri) {
        // Never a file: - Android throws on handing one to another app, which
        // crashed the app.
        if (url.scheme == "file" || url.scheme == "content") return
        try {
            startActivity(Intent(Intent.ACTION_VIEW, url).addCategory(Intent.CATEGORY_BROWSABLE))
        } catch (_: Exception) {
        }
    }

    private fun showFailure(detail: String) {
        failure?.let { content.removeView(it) }
        val host = server?.host ?: ""
        val accent = Color.rgb(0x6a, 0xa8, 0xff)
        val column = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setPadding(dp(32), dp(32), dp(32), dp(32))
            setBackgroundColor(Color.BLACK)
            isClickable = true
        }
        column.addView(TextView(this).apply {
            text = "Can't reach SoundStorm at $host"
            setTextColor(Color.WHITE)
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 20f)
            gravity = Gravity.CENTER
        }, fill(bottom = 12))
        column.addView(TextView(this).apply {
            text = detail
            setTextColor(Color.argb(0x99, 0xeb, 0xeb, 0xf5))
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 13f)
            gravity = Gravity.CENTER
        }, fill(bottom = 24))
        column.addView(Button(this).apply {
            text = "Try again"
            isAllCaps = false
            setTextColor(Color.BLACK)
            background = GradientDrawable().apply { cornerRadius = dp(14).toFloat(); setColor(accent) }
            stateListAnimator = null
            // From the address the app starts from: after one failure on the
            // home name it had moved to the away name, and Try again kept
            // loading that one for as long as the app ran (a review).
            setOnClickListener {
                triedAway = false
                val saved = ServerAddress.saved(this@MainActivity)
                if (saved != null && saved != server) showWeb(saved) else load()
            }
        }, LinearLayout.LayoutParams(dp(200), dp(48)).apply { bottomMargin = dp(8) })
        column.addView(Button(this).apply {
            text = "Change server"
            isAllCaps = false
            setTextColor(Color.argb(0x99, 0xeb, 0xeb, 0xf5))
            setBackgroundColor(Color.TRANSPARENT)
            stateListAnimator = null
            setOnClickListener { showConnect(server) }
        }, LinearLayout.LayoutParams(dp(200), dp(48)))
        content.addView(column, FrameLayout.LayoutParams(MATCH, MATCH))
        failure = column
    }

    private inner class Client : WebViewClient() {
        override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
            val url = request.url
            val scheme = url.scheme ?: return true
            // The page moving itself to the install's secure name - which it
            // does, after checking it can reach it, when opened by a home
            // address like http://192.168.0.19:8099. That is the same server,
            // not a link out: it is kept as the address from now on, and the
            // page reopened there (the page's script and messages are allowed
            // for one origin). Found on a TV set up by its LAN address, which
            // sat on the loading spinner, the move refused as a link out.
            if (request.isForMainFrame && isOwnSecureName(url)) {
                val target = ServerAddress.parse(url.toString())
                val current = server
                // A TV's code rides along: the page is reopened at the name's
                // root, which dropped it, so "Sign in a TV?" never came (the
                // owner's report).
                url.getQueryParameter("link")?.uppercase()?.filter { it.isLetterOrDigit() }
                    ?.takeIf { it.length == 6 }?.let { pendingLink = it }
                if (target != null && current != null && !(current.host ?: "").endsWith(".soundstorm.dev")) {
                    // From a plain address the name cannot be told apart from
                    // another install's by its text, and the page that asked
                    // came over plain http. So it is followed only if it
                    // names the very address in use: an attacker's name would
                    // point at their own machine (a security review).
                    Thread {
                        val same = runCatching {
                            java.net.InetAddress.getAllByName(target.host).any { it.hostAddress == current.host }
                        }.getOrDefault(false)
                        if (same) content.post { if (server == current) showWeb(target) }
                    }.start()
                    return true
                }
                if (target != null) {
                    // Followed for now, never saved: the address typed stays
                    // the one the app starts from. Saving the home name made a
                    // phone that had come home unable to reach the server once
                    // out again, and saving any name from a plain-http page let
                    // someone on the same Wi-Fi pin the app to their own server
                    // for good (a security review).
                    content.post { showWeb(target) }
                    return true
                }
            }
            if (request.isForMainFrame && scheme in setOf("http", "https") && !isServer(url)) {
                // A link off the server leaves for the browser, as one out of
                // an installed web app does - one somebody tapped, not a page
                // sending people away on its own.
                if (request.hasGesture() || request.isRedirect) openOutside(url)
                return true
            }
            if (scheme !in setOf("http", "https", "blob", "data", "about")) {
                // mailto:, tel: and the like belong to other apps - but only
                // from a link somebody followed in the page itself, never from
                // a frame or a script on its own (a security review).
                if (request.isForMainFrame && request.hasGesture()) openOutside(url)
                return true
            }
            return false
        }

        override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
            if (!request.isForMainFrame) return
            // A home name (<id>.home.soundstorm.dev) cannot be reached away
            // from home; its away twin (<id>.net...) can, when remote access
            // is on. Tried once, without being saved.
            val s = server
            val host = s?.host?.lowercase()
            if (s != null && host != null && host.endsWith(".home.soundstorm.dev") && !triedAway) {
                triedAway = true
                val away = s.buildUpon().encodedAuthority(
                    host.removeSuffix(".home.soundstorm.dev") + ".net.soundstorm.dev" + if (s.port != -1) ":${s.port}" else ""
                ).build()
                content.post { showWeb(away) }
                return
            }
            showFailure(error.description?.toString() ?: "")
        }

        // A form posted elsewhere, or a data: or blob: page, does not pass
        // through shouldOverrideUrlLoading: anything that is not the server
        // starting in the app's own window is stopped, and the server shown
        // again (a security review).
        override fun onPageStarted(view: WebView, url: String?, favicon: android.graphics.Bitmap?) {
            val u = url?.let { runCatching { Uri.parse(it) }.getOrNull() }
            if (u == null || url == "about:blank" || isServer(u)) return
            view.stopLoading()
            content.post { load() }
        }

        override fun onPageFinished(view: WebView, url: String?) {
            // Loaded: the next failure may try the away name again.
            triedAway = false
            CookieManager.getInstance().flush()
        }

        override fun onRenderProcessGone(view: WebView, detail: android.webkit.RenderProcessGoneDetail): Boolean {
            // Android reclaims a background web view's memory by killing its
            // page. Start it afresh, as a browser does on coming back to a tab -
            // without stopping the music, which this used to do: tearing the
            // old page down told the native player to stop, so music stopped
            // whenever Android ended the page while somebody was in another
            // app. In the background the page is made again on coming back.
            if (resumed) server?.let { showWeb(it, keepMusic = true) }
            else {
                tearDownWeb(keepMusic = true)
                pageLost = true
            }
            return true
        }
    }

    private inner class Chrome : WebChromeClient() {
        // A web view shows alert(), confirm() and prompt() in its own plain
        // way; these match the system's dialogs. SoundStorm asks before
        // removing downloads and big files.
        override fun onJsAlert(view: WebView, url: String?, message: String?, result: JsResult): Boolean {
            AlertDialog.Builder(this@MainActivity).setMessage(message)
                .setPositiveButton("OK") { _, _ -> result.confirm() }
                .setOnCancelListener { result.cancel() }.show()
            return true
        }

        override fun onJsConfirm(view: WebView, url: String?, message: String?, result: JsResult): Boolean {
            AlertDialog.Builder(this@MainActivity).setMessage(message)
                .setPositiveButton("OK") { _, _ -> result.confirm() }
                .setNegativeButton("Cancel") { _, _ -> result.cancel() }
                .setOnCancelListener { result.cancel() }.show()
            return true
        }

        override fun onJsPrompt(view: WebView, url: String?, message: String?, defaultValue: String?, result: JsPromptResult): Boolean {
            val input = EditText(this@MainActivity).apply { setText(defaultValue ?: "") }
            AlertDialog.Builder(this@MainActivity).setMessage(message).setView(input)
                .setPositiveButton("OK") { _, _ -> result.confirm(input.text.toString()) }
                .setNegativeButton("Cancel") { _, _ -> result.cancel() }
                .setOnCancelListener { result.cancel() }.show()
            return true
        }

        /** target="_blank" (the ListenBrainz and LRCLIB links in Account): the browser. */
        override fun onCreateWindow(view: WebView, isDialog: Boolean, isUserGesture: Boolean, resultMsg: Message): Boolean {
            // Only for a link somebody tapped, and only to a web address.
            if (!isUserGesture) return false
            val catcher = WebView(this@MainActivity)
            var done = false
            fun finish(v: WebView, url: Uri?) {
                if (done) return
                done = true
                if (url != null && url.scheme in setOf("http", "https")) openOutside(url)
                v.stopLoading()
                v.post { v.destroy() }
            }
            catcher.webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(v: WebView, request: WebResourceRequest): Boolean {
                    finish(v, request.url)
                    return true
                }

                // A posted form never reaches shouldOverrideUrlLoading: it is
                // caught as it starts, and nothing loads in the hidden view.
                override fun onPageStarted(v: WebView, url: String?, favicon: android.graphics.Bitmap?) {
                    if (url == null || url == "about:blank") return
                    finish(v, Uri.parse(url))
                }
            }
            // A window nothing ever navigates is let go.
            catcher.postDelayed({ if (!done) { done = true; catcher.destroy() } }, 10_000)
            (resultMsg.obj as WebView.WebViewTransport).webView = catcher
            resultMsg.sendToTarget()
            return true
        }

        /** Add media, Import playlist, Change cover: the system's file picker. */
        override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
            fileCallback?.onReceiveValue(null)
            fileCallback = callback
            return try {
                startActivityForResult(params.createIntent(), PICK_FILES)
                true
            } catch (_: ActivityNotFoundException) {
                fileCallback = null
                false
            }
        }

        /** A film's full screen. */
        override fun onShowCustomView(view: View, callback: CustomViewCallback) {
            if (fullscreen != null) {
                callback.onCustomViewHidden()
                return
            }
            fullscreen = view
            fullscreenCallback = callback
            root.addView(view, FrameLayout.LayoutParams(MATCH, MATCH))
            hideBars()
        }

        override fun onHideCustomView() {
            fullscreen?.let { root.removeView(it) }
            fullscreen = null
            fullscreenCallback = null
            hideBars()
        }
    }

    @Deprecated("The platform Activity's result API, which is all this needs.")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == PICK_FILES) {
            fileCallback?.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(resultCode, data))
            fileCallback = null
            return
        }
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
    }

    /**
     * Back steps back through the page first - the page keeps a history entry
     * armed while a menu, a book or Now Playing is open, and closes the top
     * one on back. With nothing left, the app goes to the background rather
     * than closing, which would stop the music.
     */
    @Deprecated("The platform Activity's back handling, for Android before 13.")
    override fun onBackPressed() {
        goBack()
    }

    private fun goBack() {
        val view = webView
        when {
            fullscreen != null -> fullscreenCallback?.onCustomViewHidden()
            failure == null && view != null && view.canGoBack() -> view.goBack()
            else -> moveTaskToBack(true)
        }
    }

    // ---------------------------------------------------------------- helpers

    private fun dp(value: Int) = (value * resources.displayMetrics.density).toInt()

    private fun wrap(bottom: Int = 0) =
        LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT)
            .apply { bottomMargin = dp(bottom) }

    private fun fill(height: Int = 0, bottom: Int = 0, top: Int = 0) =
        LinearLayout.LayoutParams(MATCH, if (height > 0) dp(height) else ViewGroup.LayoutParams.WRAP_CONTENT)
            .apply { bottomMargin = dp(bottom); topMargin = dp(top) }

    private fun hideKeyboard(view: View) {
        getSystemService(InputMethodManager::class.java).hideSoftInputFromWindow(view.windowToken, 0)
    }

    companion object {
        private const val MATCH = ViewGroup.LayoutParams.MATCH_PARENT
        private const val PICK_FILES = 1
        private const val BACKUP_PERMISSION = 2
    }
}
