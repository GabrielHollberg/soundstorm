package dev.soundstorm.app

import android.annotation.SuppressLint
import android.app.Activity
import android.app.AlertDialog
import android.content.ActivityNotFoundException
import android.content.Intent
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Bundle
import android.os.Message
import android.text.InputType
import android.util.TypedValue
import android.view.Gravity
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputMethodManager
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
    private val background = Executors.newSingleThreadExecutor()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Edge to edge, as Android now requires: the page's area sits between
        // the bars, and the bars' own space is painted by two scrims - the
        // status bar in the page's theme colour, the navigation bar black.
        WindowCompat.setDecorFitsSystemWindows(window, false)
        WindowInsetsControllerCompat(window, window.decorView).apply {
            isAppearanceLightStatusBars = false
            isAppearanceLightNavigationBars = false
        }
        root = FrameLayout(this).apply { setBackgroundColor(Color.BLACK) }
        content = FrameLayout(this)
        statusScrim = View(this).apply { setBackgroundColor(Color.BLACK) }
        navScrim = View(this).apply { setBackgroundColor(Color.BLACK) }
        root.addView(content, FrameLayout.LayoutParams(MATCH, MATCH))
        root.addView(statusScrim, FrameLayout.LayoutParams(MATCH, 0, Gravity.TOP))
        root.addView(navScrim, FrameLayout.LayoutParams(MATCH, 0, Gravity.BOTTOM))
        ViewCompat.setOnApplyWindowInsetsListener(root) { _, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
            val ime = insets.getInsets(WindowInsetsCompat.Type.ime())
            content.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, ime.bottom))
            statusScrim.layoutParams = (statusScrim.layoutParams as FrameLayout.LayoutParams).apply { height = bars.top }
            navScrim.layoutParams = (navScrim.layoutParams as FrameLayout.LayoutParams).apply { height = bars.bottom }
            WindowInsetsCompat.CONSUMED
        }
        setContentView(root)

        // A server given at launch, for testing:
        // adb shell am start -n dev.soundstorm.app/.MainActivity --es serverURL http://10.0.2.2:8099
        intent?.getStringExtra("serverURL")?.let(ServerAddress::parse)?.let { ServerAddress.save(this, it) }
        val saved = ServerAddress.saved(this)
        if (saved != null) showWeb(saved) else showConnect(null)
    }

    override fun onDestroy() {
        webView?.let {
            MediaBridge.detach(it)
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
        column.addView(TextView(this).apply {
            text = "Enter your server's address - the one you open in a browser."
            setTextColor(Color.argb(0x99, 0xeb, 0xeb, 0xf5))
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 15f)
            gravity = Gravity.CENTER
        }, fill(bottom = 28))
        val field = EditText(this).apply {
            hint = "yourname.home.soundstorm.dev"
            // Without the scheme for https (the default when none is typed),
            // with it for plain http, which would otherwise be read as https.
            setText(prefill?.let {
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
        val holder = FrameLayout(this)
        holder.addView(column, FrameLayout.LayoutParams(minOf(dp(420), resources.displayMetrics.widthPixels), FrameLayout.LayoutParams.WRAP_CONTENT, Gravity.CENTER))
        content.removeAllViews()
        content.addView(holder, FrameLayout.LayoutParams(MATCH, MATCH))

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
                        ServerAddress.save(this, parsed)
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
        field.requestFocus()
        field.post { getSystemService(InputMethodManager::class.java).showSoftInput(field, 0) }
    }

    // -------------------------------------------------------------------- web

    @SuppressLint("SetJavaScriptEnabled")
    private fun showWeb(target: Uri) {
        tearDownWeb()
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
            // Chrome's own user agent, with a name the page can test for.
            userAgentString = "$userAgentString SoundStormApp/1"
        }
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG) // chrome://inspect

        val origin = ServerAddress.origin(target)
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
        load()
    }

    private fun load() {
        failure?.let { content.removeView(it) }
        failure = null
        server?.let { webView?.loadUrl(it.toString()) }
    }

    private fun tearDownWeb() {
        webView?.let {
            MediaBridge.detach(it)
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
            "themeColor" -> setStatusColor(runCatching { Color.parseColor(message.optString("color")) }.getOrDefault(Color.BLACK))
        }
    }

    private fun setStatusColor(color: Int) {
        statusScrim.setBackgroundColor(color)
    }

    private fun isServer(url: Uri): Boolean {
        val s = server ?: return false
        return url.scheme == s.scheme && url.host == s.host && url.port == s.port
    }

    private fun openOutside(url: Uri) {
        try {
            startActivity(Intent(Intent.ACTION_VIEW, url).addCategory(Intent.CATEGORY_BROWSABLE))
        } catch (_: ActivityNotFoundException) {
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
            setOnClickListener { load() }
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
            if (request.isForMainFrame && scheme in setOf("http", "https") && !isServer(url)) {
                // A link off the server leaves for the browser, as one out of
                // an installed web app does.
                openOutside(url)
                return true
            }
            if (scheme !in setOf("http", "https", "blob", "data", "about")) {
                // mailto:, tel: and the like belong to other apps.
                openOutside(url)
                return true
            }
            return false
        }

        override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
            if (request.isForMainFrame) showFailure(error.description?.toString() ?: "")
        }

        override fun onRenderProcessGone(view: WebView, detail: android.webkit.RenderProcessGoneDetail): Boolean {
            // Android reclaims a background web view's memory by killing its
            // page. Start it afresh, as a browser does on coming back to a tab.
            server?.let { showWeb(it) }
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
            val catcher = WebView(this@MainActivity)
            catcher.webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(v: WebView, request: WebResourceRequest): Boolean {
                    openOutside(request.url)
                    v.destroy()
                    return true
                }
            }
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
            WindowInsetsControllerCompat(window, window.decorView).apply {
                hide(WindowInsetsCompat.Type.systemBars())
                systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            }
        }

        override fun onHideCustomView() {
            fullscreen?.let { root.removeView(it) }
            fullscreen = null
            fullscreenCallback = null
            WindowInsetsControllerCompat(window, window.decorView).show(WindowInsetsCompat.Type.systemBars())
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
    @Deprecated("The platform Activity's back handling, which is all this needs.")
    override fun onBackPressed() {
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

    private fun fill(height: Int = 0, bottom: Int = 0) =
        LinearLayout.LayoutParams(MATCH, if (height > 0) dp(height) else ViewGroup.LayoutParams.WRAP_CONTENT)
            .apply { bottomMargin = dp(bottom) }

    private fun hideKeyboard(view: View) {
        getSystemService(InputMethodManager::class.java).hideSoftInputFromWindow(view.windowToken, 0)
    }

    companion object {
        private const val MATCH = ViewGroup.LayoutParams.MATCH_PARENT
        private const val PICK_FILES = 1
    }
}
