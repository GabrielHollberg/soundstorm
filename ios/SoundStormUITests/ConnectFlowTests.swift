import XCTest

/// Drives the app like a person would, against a real SoundStorm.
///
/// Needs a throwaway server - the first run creates its owner account:
///
///   SOUNDSTORM_LISTEN=127.0.0.1:8080 SOUNDSTORM_STATE_DIR=/tmp/ss-state \
///   SOUNDSTORM_LIBRARY_DIR=/tmp/ss-library SOUNDSTORM_STARTER_LIBRARY=false \
///   SOUNDSTORM_SETUP_CODE=test-setup-code go run ./cmd/soundstorm
///
/// then Product → Test in Xcode, or xcodebuild test. Skipped when no server
/// answers, so it never fails for want of one.
@MainActor
final class ConnectFlowTests: XCTestCase {
    private let server = "http://localhost:8080"
    private var app: XCUIApplication!

    override func setUp() async throws {
        continueAfterFailure = false
        var request = URLRequest(url: URL(string: server + "/healthz")!)
        request.timeoutInterval = 3
        do {
            _ = try await URLSession.shared.data(for: request)
        } catch {
            throw XCTSkip("No SoundStorm at \(server); see the comment on ConnectFlowTests.")
        }
        app = XCUIApplication()
        // An empty saved address: every launch starts at the connect screen.
        app.launchArguments = ["-serverURL", ""]
        app.launch()
    }

    func testWrongAddressSaysWhy() {
        let field = app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        focus(field)
        field.typeText("localhost:9\n")
        let why = app.staticTexts.containing(NSPredicate(format: "label CONTAINS[c] %@", "Couldn't reach localhost")).firstMatch
        XCTAssertTrue(why.waitForExistence(timeout: 15), "no explanation shown for an address nothing answers at")
    }

    func testConnectSignInAndChangeServer() {
        let field = app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        focus(field)
        field.typeText(server + "\n")

        // The server's own page, inside the app: signed in already if an
        // earlier run was, otherwise its sign-in form. The first run against a
        // new server creates the owner; later ones sign in as it.
        let web = app.webViews.firstMatch
        let settings = web.buttons["Settings"]
        let username = web.textFields.firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 15) || username.exists, "the server's page never appeared")
        if !settings.exists {
            signIn(web)
        }

        XCTAssertTrue(settings.waitForExistence(timeout: 15), "never got past signing in")
        notNowToBackup(web)
        settings.tap()
        // Settings opens on its first category; Change server sits with
        // Sign out under Account.
        let account = web.buttons["Account"]
        XCTAssertTrue(account.waitForExistence(timeout: 5))
        account.tap()

        // Only shown inside the app (window.soundstormApp).
        let change = web.buttons["Change server"]
        for _ in 0..<8 where !change.isHittable { web.swipeUp() }
        XCTAssertTrue(change.isHittable, "Change server is missing from Settings")
        change.tap()

        // Back at the connect screen, the server just used is in the list,
        // and choosing it opens it again without typing.
        let saved = app.buttons.containing(NSPredicate(format: "label CONTAINS %@", "localhost")).firstMatch
        XCTAssertTrue(saved.waitForExistence(timeout: 5), "the server used should be in the list")
        saved.tap()
        XCTAssertTrue(app.webViews.firstMatch.buttons["Settings"].waitForExistence(timeout: 15),
                      "choosing a saved server should open it, still signed in")
    }

    /// Photo backup end to end: turned on from the page (its question, or
    /// the switch in Settings once it has been decided on this phone), photo
    /// access allowed on the system's prompt, the simulator's sample photos
    /// sent to the account's folder, and Settings saying so.
    func testPhotoBackupSendsTheCameraRoll() {
        let field = app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        focus(field)
        field.typeText(server + "\n")
        let web = app.webViews.firstMatch
        let settings = web.buttons["Settings"]
        XCTAssertTrue(settings.waitForExistence(timeout: 15) || web.textFields.firstMatch.exists, "the server's page never appeared")
        if !settings.exists { signIn(web) }
        XCTAssertTrue(settings.waitForExistence(timeout: 15), "never got past signing in")

        let turnOn = web.buttons["Turn on"]
        if turnOn.waitForExistence(timeout: 8) {
            turnOn.tap()
            settings.tap()
        } else {
            settings.tap()
            let toggle = web.descendants(matching: .any)["Back up this phone's photos and videos"].firstMatch
            for _ in 0..<8 where !toggle.isHittable { web.swipeUp() }
            XCTAssertTrue(toggle.isHittable, "no backup switch in Settings")
            // On already from an earlier run, it stays on.
            let on = (toggle.value as? String) == "1" || (toggle.value as? NSNumber)?.boolValue == true
            if !on { toggle.tap() }
        }
        // The system asks once for the photo library.
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Allow Full Access"]
        if allow.waitForExistence(timeout: 10) { allow.tap() }

        let done = web.staticTexts.containing(NSPredicate(format: "label BEGINSWITH %@ AND label CONTAINS %@", "All ", "backed up")).firstMatch
        let deadline = Date().addingTimeInterval(120)
        while !done.exists && Date() < deadline {
            _ = done.waitForExistence(timeout: 5)
        }
        XCTAssertTrue(done.exists, "Settings never said the photos were backed up")
    }

    /// Signing out turns this phone's backup off, as it must - the next
    /// person's cookie would otherwise send the last person's photos - but it
    /// also marked backup "decided", so whoever signed in next was never
    /// asked. Now they are.
    func testBackupIsAskedOfWhoeverSignsInNext() {
        let field = app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        focus(field)
        field.typeText(server + "\n")
        let web = app.webViews.firstMatch
        let settings = web.buttons["Settings"]
        XCTAssertTrue(settings.waitForExistence(timeout: 15) || web.textFields.firstMatch.exists, "the server's page never appeared")
        if settings.exists {
            settings.tap()
            let account = web.buttons["Account"]
            XCTAssertTrue(account.waitForExistence(timeout: 5))
            account.tap()
            let signOut = web.buttons["Sign out"]
            for _ in 0..<8 where !signOut.isHittable { web.swipeUp() }
            XCTAssertTrue(signOut.isHittable, "no Sign out in Settings")
            signOut.tap()
            XCTAssertTrue(web.textFields.firstMatch.waitForExistence(timeout: 15), "signing out did not return to the sign-in form")
            // Opened again, as the next person would: after signing out the
            // page puts the keyboard up over the password field at once.
            app.terminate()
            app.launch()
            let again = app.textFields.firstMatch
            XCTAssertTrue(again.waitForExistence(timeout: 5))
            focus(again)
            again.typeText(server + "\n")
            XCTAssertTrue(web.secureTextFields.firstMatch.waitForExistence(timeout: 15), "the sign-in form did not come back")
        }
        signIn(web)
        XCTAssertTrue(web.buttons["Turn on"].waitForExistence(timeout: 15),
                      "the person who signed in was not asked about backing up photos")
    }

    /// A TV's sign-in code reaching the app by link (soundstorm://link, what
    /// the page offers in Safari): the app opens its server's page, which
    /// asks "Sign in a TV?" for that code.
    func testATVCodeLinkAsksToSignInTheTV() async throws {
        // A TV asks the server for a code, as the TV app does.
        var ask = URLRequest(url: URL(string: server + "/api/link")!)
        ask.httpMethod = "POST"
        ask.setValue("application/json", forHTTPHeaderField: "Content-Type")
        ask.httpBody = Data("{}".utf8)
        let (data, _) = try await URLSession.shared.data(for: ask)
        let code = ((try JSONSerialization.jsonObject(with: data) as? [String: Any])?["code"] as? String ?? "")
            .replacingOccurrences(of: "-", with: "")
        XCTAssertEqual(code.count, 6)

        let field = app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        focus(field)
        field.typeText(server + "\n")
        let web = app.webViews.firstMatch
        let settings = web.buttons["Settings"]
        XCTAssertTrue(settings.waitForExistence(timeout: 15) || web.textFields.firstMatch.exists)
        if !settings.exists { signIn(web) }
        XCTAssertTrue(settings.waitForExistence(timeout: 15), "never got past signing in")
        notNowToBackup(web)

        let link = "soundstorm://link?server=" + server.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&code=" + code
        app.open(URL(string: link)!)
        let open = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Open"]
        if open.waitForExistence(timeout: 5) { open.tap() }
        XCTAssertTrue(web.staticTexts["Sign in a TV?"].waitForExistence(timeout: 15),
                      "the page did not ask about the TV's code")
        web.buttons["Don't allow"].tap()
    }

    /// The page asks about photo backup after signing in, over everything,
    /// on a phone where this person has not answered; a test about something
    /// else says not now.
    private func notNowToBackup(_ web: XCUIElement) {
        let later = web.buttons["Not now"]
        if later.waitForExistence(timeout: 4) { later.tap() }
    }

    /// Taps a field until the keyboard is up for it: just after a page loads
    /// (or reloads, as signing out does) a tap can land before it takes focus.
    private func focus(_ field: XCUIElement) {
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        for _ in 0..<5 {
            field.tap()
            for _ in 0..<10 {
                if (field.value(forKey: "hasKeyboardFocus") as? Bool) == true { return }
                Thread.sleep(forTimeInterval: 0.2)
            }
        }
    }

    private func signIn(_ web: XCUIElement) {
        let username = web.textFields.firstMatch
        focus(username)
        username.typeText("owner")
        let password = web.secureTextFields.firstMatch
        focus(password)
        password.typeText("correct horse battery")
        // Return submits, as it would for a person; the keyboard's own bar
        // sits over the form's button.
        if web.textFields.count > 1 {
            let code = web.textFields.element(boundBy: 1)
            focus(code)
            code.typeText("test-setup-code\n")
        } else {
            password.typeText("\n")
        }
    }
}
