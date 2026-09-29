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
        field.typeText("localhost:9\n")
        let why = app.staticTexts.containing(NSPredicate(format: "label CONTAINS[c] %@", "Couldn't reach localhost")).firstMatch
        XCTAssertTrue(why.waitForExistence(timeout: 15), "no explanation shown for an address nothing answers at")
    }

    func testConnectSignInAndChangeServer() {
        let field = app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
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

        let back = app.textFields.firstMatch
        XCTAssertTrue(back.waitForExistence(timeout: 5), "Change server did not return to the connect screen")
        XCTAssertEqual(back.value as? String, "localhost", "the connect screen should start from the current server")
    }

    private func signIn(_ web: XCUIElement) {
        let username = web.textFields.firstMatch
        username.tap()
        username.typeText("owner")
        let password = web.secureTextFields.firstMatch
        password.tap()
        password.typeText("correct horse battery")
        // Return submits, as it would for a person; the keyboard's own bar
        // sits over the form's button.
        if web.textFields.count > 1 {
            let code = web.textFields.element(boundBy: 1)
            code.tap()
            code.typeText("test-setup-code\n")
        } else {
            password.typeText("\n")
        }
    }
}
