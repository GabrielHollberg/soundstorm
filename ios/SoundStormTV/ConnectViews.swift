import SwiftUI

/// First launch: the address of somebody's own SoundStorm, checked the same
/// way the iPhone app checks it (ServerAddress, shared).
struct ConnectView: View {
    @Environment(AppModel.self) private var model
    @State private var address = ServerAddress.saved?.host() ?? ""
    @State private var checking = false
    @State private var message: String?

    var body: some View {
        VStack(spacing: 40) {
            Logo()
            Text("Enter your server's address - the one you open in a browser.")
                .font(.headline)
                .foregroundStyle(.secondary)
            TextField("yourname.home.soundstorm.dev", text: $address)
                .keyboardType(.URL)
                .textContentType(.URL)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .frame(width: 900)
                .onSubmit(connect)
            Button(checking ? "Connecting…" : "Connect", action: connect)
                .disabled(checking)
            if let message {
                Text(message).foregroundStyle(.red).frame(width: 900)
            }
        }
        .multilineTextAlignment(.center)
    }

    private func connect() {
        guard !checking else { return }
        guard let server = ServerAddress.parse(address) else {
            message = "That doesn't look like a web address."
            return
        }
        message = nil
        checking = true
        Task {
            do {
                try await ServerAddress.check(server)
                model.use(server)
            } catch {
                message = error.localizedDescription
            }
            checking = false
        }
    }
}

struct SignInView: View {
    let hasAccount: Bool
    @Environment(AppModel.self) private var model
    @State private var username = ""
    @State private var password = ""
    @State private var busy = false
    @State private var message: String?
    /// A new device waiting for approval: its id, and whether the server's
    /// setup code may approve it.
    @State private var waiting: (id: String, codeAllowed: Bool)?
    @State private var setupCode = ""
    @State private var linking = false

    var body: some View {
        VStack(spacing: 40) {
            Logo()
            if let waiting {
                waitingView(waiting)
            } else if linking {
                LinkView(done: { linking = false })
            } else if hasAccount {
                // The easy way first: no typing with a remote.
                Button("Sign in with your phone") { linking = true }
                TextField("Username", text: $username)
                    .textContentType(.username)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    .frame(width: 700)
                SecureField("Password", text: $password)
                    .textContentType(.password)
                    .frame(width: 700)
                    .onSubmit(signIn)
                Button(busy ? "Signing in…" : "Sign in", action: signIn)
                    .disabled(busy || username.isEmpty || password.isEmpty)
            } else {
                // Creating the owner needs the setup code, which is on the
                // computer SoundStorm runs on: easier there than on a TV.
                Text("This SoundStorm has no account yet. Open it in a browser to create one, then sign in here.")
                    .frame(width: 900)
                Button("Try again") { Task { await model.refreshSession() } }
            }
            if let message {
                Text(message).foregroundStyle(.red).frame(width: 900)
            }
            Button("Change server") { model.changeServer() }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
        }
        .multilineTextAlignment(.center)
        #if DEBUG
        // For the simulator, which cannot type: -username <u> -password <p>
        .task {
            // -phoneSignIn YES opens the phone sign-in at once.
            if UserDefaults.standard.bool(forKey: "phoneSignIn") { linking = true }
            if let u = UserDefaults.standard.string(forKey: "username"),
               let p = UserDefaults.standard.string(forKey: "password") {
                username = u
                password = p
                signIn()
            }
        }
        #endif
    }

    private func signIn() {
        guard !busy, let api = model.api else { return }
        busy = true
        message = nil
        Task {
            do {
                switch try await api.signIn(username: username, password: password) {
                case .signedIn:
                    model.signedIn()
                case .waiting(let id, let codeAllowed):
                    password = ""
                    waiting = (id, codeAllowed)
                }
            } catch {
                message = error.localizedDescription
            }
            busy = false
        }
    }

    /// The right password on a TV the account has not signed in on, with
    /// approval of new devices turned on: it waits, asking every three
    /// seconds, as the page does, until a phone or computer already signed in
    /// approves it - or the setup code from the server's .env does.
    @ViewBuilder
    private func waitingView(_ w: (id: String, codeAllowed: Bool)) -> some View {
        Text("Waiting for approval")
            .font(.title2.bold())
        Text("This Apple TV hasn't signed in to this account before. Approve it on a phone or computer that is already signed in, or ask whoever looks after the server.")
            .fixedSize(horizontal: false, vertical: true)
            .frame(width: 1100)
        if w.codeAllowed {
            Text("Nothing else signed in? The setup code from the server's .env file approves it too.")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .frame(width: 1100)
            TextField("Setup code", text: $setupCode)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .frame(width: 700)
            Button("Approve with the code") {
                Task { await ask(w.id, code: setupCode) }
            }
            .disabled(setupCode.isEmpty)
        }
        Button("Cancel") { waiting = nil }
            .task(id: w.id) {
                while !Task.isCancelled, waiting?.id == w.id {
                    try? await Task.sleep(for: .seconds(3))
                    guard waiting?.id == w.id else { return }
                    await ask(w.id, code: nil)
                }
            }
    }

    private func ask(_ id: String, code: String?) async {
        guard let api = model.api else { return }
        do {
            if try await api.waitingSignIn(id, setupCode: code) {
                waiting = nil
                model.signedIn()
            }
        } catch API.Failure.status(let status, let text) where code != nil && status == 403 && (text ?? "").contains("setup code") {
            // A mistyped code: still waiting, and saying so.
            message = text
        } catch {
            // Refused, or no longer waiting: back to signing in, saying why.
            waiting = nil
            message = error.localizedDescription
        }
    }
}

struct Logo: View {
    var body: some View {
        VStack(spacing: 16) {
            Image("Logo")
            Text("SoundStorm")
                .font(.system(size: 64, weight: .heavy).italic())
        }
    }
}

/// Choosing a new password before anything else: the owner asked everybody,
/// or this one no longer meets the rules. The server answers nothing else
/// until it is done, so this stands in for the whole app, as the page's
/// screen for it does.
struct RenewPasswordView: View {
    @Environment(AppModel.self) private var model
    @State private var current = ""
    @State private var new = ""
    @State private var busy = false
    @State private var message: String?

    var body: some View {
        VStack(spacing: 36) {
            Logo()
            Text("Please choose a new password to carry on.")
                .font(.title3)
            SecureField("Your current password", text: $current)
                .textContentType(.password)
                .frame(width: 700)
            SecureField("New password", text: $new)
                .textContentType(.newPassword)
                .frame(width: 700)
                .onSubmit(save)
            Text("At least 12 characters, and not a common one. A few unrelated words make a good password.")
                .foregroundStyle(.secondary)
                .frame(width: 1000)
            if let message {
                Text(message).foregroundStyle(.red).frame(width: 1000)
            }
            Button(busy ? "Saving…" : "Save and carry on", action: save)
                .disabled(busy || current.isEmpty || new.count < 12)
            Button("Sign out") { Task { await model.signOut() } }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
        }
        .multilineTextAlignment(.center)
        #if DEBUG
        // For the simulator, which cannot type: -password <old> -newPassword <new>
        .task {
            if let old = UserDefaults.standard.string(forKey: "password"),
               let fresh = UserDefaults.standard.string(forKey: "newPassword") {
                current = old
                new = fresh
                save()
            }
        }
        #endif
    }

    private func save() {
        guard !busy, let api = model.api else { return }
        busy = true
        message = nil
        Task {
            do {
                try await api.changePassword(current: current, new: new)
                current = ""
                new = ""
            } catch {
                message = error.localizedDescription
            }
            busy = false
        }
    }
}

/// On a TV already signed in: a new device asking to sign in to this account
/// (any account, for the owner) is offered for approval, as on the page -
/// at a home where the TV is the only thing signed in, it is the one to ask.
struct DeviceRequests: ViewModifier {
    @Environment(API.self) private var api
    @State private var asking: API.PendingDevice?
    /// Put off with Not now (or Back): not asked again on this TV.
    @State private var putOff: Set<String> = []

    func body(content: Content) -> some View {
        content
            .task {
                while !Task.isCancelled {
                    if asking == nil, let next = await api.pendingDevices().first(where: { !putOff.contains($0.id) }) {
                        asking = next
                    }
                    try? await Task.sleep(for: .seconds(8))
                }
            }
            .alert("Allow this sign-in?", isPresented: Binding(get: { asking != nil }, set: { if !$0 { asking = nil } }),
                   presenting: asking) { device in
                Button("Allow") { Task { await api.answer(device, approve: true); asking = nil } }
                Button("Don't allow", role: .destructive) { Task { await api.answer(device, approve: false); asking = nil } }
                // Back is a cancel: it must not refuse somebody's real sign-in.
                Button("Not now", role: .cancel) { putOff.insert(device.id); asking = nil }
            } message: { device in
                Text("\(device.device) wants to sign in to \(device.user == api.user?.name ? "your account" : "\(device.user)'s account"). Allow it only if you, or they, are signing in on it right now.")
            }
    }
}

/// Signing this TV in from a phone: a code and a QR code of the address that
/// allows it; a phone signed in scans it (or has the code typed into
/// Settings, Sign in a TV) and allows it, and this TV, asking every three
/// seconds, is signed in as that phone's person. A code that runs out after
/// its ten minutes is replaced.
struct LinkView: View {
    let done: () -> Void
    @Environment(AppModel.self) private var model
    @State private var link: API.Link?
    @State private var message: String?

    var body: some View {
        VStack(spacing: 30) {
            Text("On a phone signed in to SoundStorm, scan this - or open SoundStorm on it and enter the code under Settings, Sign in a TV.")
                .fixedSize(horizontal: false, vertical: true)
                .frame(width: 1100)
            if let link, let api = model.api {
                SafeImage(url: api.linkQR(link.id), maxPixels: 1000) { image in
                    image.interpolation(.none).resizable().scaledToFit()
                } placeholder: {
                    Color.white.opacity(0.06)
                }
                .frame(width: 360, height: 360)
                .clipShape(RoundedRectangle(cornerRadius: 12))
                Text(link.code)
                    .font(.system(size: 72, weight: .heavy))
                    .tracking(8)
            } else {
                ProgressView()
            }
            if let message {
                Text(message).foregroundStyle(.red).frame(width: 900)
            }
            Button("Use a password instead", action: done)
        }
        .multilineTextAlignment(.center)
        .task { await run() }
    }

    private func run() async {
        guard let api = model.api else { return }
        while !Task.isCancelled {
            do {
                let fresh = try await api.newLink()
                link = fresh
                message = nil
                while !Task.isCancelled {
                    try await Task.sleep(for: .seconds(3))
                    if try await api.linkSignedIn(fresh.id) {
                        model.signedIn()
                        return
                    }
                }
            } catch API.Failure.status(404, _) {
                continue // run out: a new code
            } catch is CancellationError {
                return
            } catch {
                // Not allowed, or the server would not give a code: said, and
                // tried again in a moment.
                link = nil
                message = error.localizedDescription
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }
}
