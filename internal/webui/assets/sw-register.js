// Register the service worker.
//
// A separate file rather than three lines inline in the shell, and that is
// forced rather than stylistic: the shell is served with script-src 'self',
// which blocks inline scripts outright. Inline, this registration was silently
// refused - the console said so, nothing else did, and "Add to Home Screen"
// simply produced a bookmark. Weakening the policy to 'unsafe-inline' was
// never an option; it is the thing stopping an EPUB running its own
// JavaScript against the session cookie.
//
// Loaded from the shell rather than from app.js so that a worker failing to
// register can never be the reason the app does not start.
//
// navigator.serviceWorker is undefined outside a secure context, so over plain
// http on a LAN address this quietly does nothing and SoundStorm works exactly
// as it did before. That is also why installing to a home screen from a phone
// needs the installer's --https. localhost counts as secure either way, which
// is why it works in development without it.
if ('serviceWorker' in navigator) {
  window.addEventListener('load', function () {
    navigator.serviceWorker.register('/sw.js').catch(function () {
      // Offline start-up is a nicety. Nothing else depends on it.
    });
  });
}
