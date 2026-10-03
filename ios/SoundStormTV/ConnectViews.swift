import SwiftUI

/// First launch, or Change server: the servers this TV knows, to pick one
/// (held for Rename and Remove), and the address of another to add, checked
/// the same way the iPhone app checks it (ServerAddress, shared).
struct ConnectView: View {
    @Environment(AppModel.self) private var model
    @State private var address = ""
    @State private var checking = false
    @State private var message: String?
    @State private var list = ServerAddress.all
    @State private var renaming: ServerAddress.Server?
    @State private var newName = ""
    /// Servers found on this TV's network that it does not know yet.
    @State private var nearby: [ServerDiscovery.Found] = []
    @State private var searched = false

    var body: some View {
        VStack(spacing: 40) {
            Logo()
            // Found on the network: one press, nothing to type - the way
            // somebody who just plugged the server in gets started.
            if !nearby.isEmpty {
                VStack(spacing: 16) {
                    Text(nearby.count == 1 ? "We found \(nearby[0].label) on your network" : "We found SoundStorm on your network - which one?")
                        .font(.title3.bold())
                    ForEach(nearby) { found in
                        Button {
                            if found.setUp {
                                ServerAddress.serverCalls(found.url, found.name)
                                model.use(found.url)
                            } else {
                                message = "That SoundStorm isn't set up yet. Set it up with the SoundStorm app on your phone first - then choose it here."
                            }
                        } label: {
                            VStack {
                                Text(nearby.count == 1 ? (found.setUp ? "Use it" : "Not set up yet") : found.label)
                                if nearby.count > 1 && !found.setUp {
                                    Text("Not set up yet").font(.caption).foregroundStyle(.secondary)
                                }
                            }
                            .frame(width: 600)
                        }
                    }
                    if nearby.count == 1 && !nearby[0].setUp {
                        Text("Set it up with the SoundStorm app on your phone first.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            } else if searched && list.isEmpty {
                // Why nothing was found, and that it keeps looking.
                Text(ServerDiscovery.nothingFound(tv: true))
                    .foregroundStyle(.secondary)
                    .frame(width: 1100)
            } else if !searched && list.isEmpty {
                HStack(spacing: 16) {
                    ProgressView()
                    Text("Looking for SoundStorm on your network…").foregroundStyle(.secondary)
                }
            }
            if !list.isEmpty {
                VStack(spacing: 16) {
                    Text("Your servers").font(.headline)
                    ForEach(list) { server in
                        Button {
                            model.use(server.url)
                        } label: {
                            HStack {
                                VStack(alignment: .leading) {
                                    Text(server.name)
                                    if server.name != (server.url.host() ?? "") {
                                        Text(server.url.host() ?? server.url.absoluteString)
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                                Spacer()
                                if server.url == ServerAddress.saved {
                                    Image(systemName: "checkmark")
                                }
                            }
                            .frame(width: 800)
                        }
                        .contextMenu {
                            Button("Rename") {
                                newName = server.name
                                renaming = server
                            }
                            Button("Remove", role: .destructive) {
                                ServerAddress.forget(server.url)
                                list = ServerAddress.all
                            }
                        }
                    }
                }
            }
            Text(list.isEmpty && nearby.isEmpty
                 ? "Enter your server's address, like abc123.home.soundstorm.dev at home or abc123.net.soundstorm.dev away - or just the abc123 at its start. It is in SoundStorm's Settings, under Use on your phone or TV."
                 : list.isEmpty ? "Or type its address, or just the code at its start." : "Or add another - its address, or just the code at its start.")
                .font(.headline)
                .foregroundStyle(.secondary)
            TextField("abc123.home.soundstorm.dev", text: $address)
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
        // Looked for at once, and again while nothing is found: the server
        // may still be starting, or the TV just joined the network.
        .task {
            while !Task.isCancelled {
                let known = Set(list.map { $0.url.host() ?? "" })
                let all = await ServerDiscovery.search()
                nearby = all.filter { !known.contains($0.url.host() ?? "") }
                searched = true
                try? await Task.sleep(for: .seconds(nearby.isEmpty ? 10 : 30))
            }
        }
        .alert("Rename", isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
            TextField("Name", text: $newName)
            Button("Save") {
                if let renaming { ServerAddress.rename(renaming.url, to: newName) }
                list = ServerAddress.all
                renaming = nil
            }
            Button("Cancel", role: .cancel) { renaming = nil }
        }
    }

    private func found(_ f: ServerDiscovery.Found) -> String {
        f.url.host() ?? f.url.absoluteString
    }

    private func connect() {
        guard !checking else { return }
        message = nil
        checking = true
        Task {
            do {
                // A TV stays put: its home name first.
                model.use(try await ServerAddress.find(address, preferAway: false))
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
                if let name = ServerAddress.all.first(where: { $0.url == model.api?.server })?.name {
                    Text("Sign in to \(name)").font(.title3.bold())
                }
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
                Text("This SoundStorm isn't set up yet. Set it up with the SoundStorm app on your phone, then sign in here.")
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
        .onAppear {
            if model.phoneFirst {
                model.phoneFirst = false
                linking = true
            }
        }
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

/// Who's listening? The people kept on this TV: a circle each, and switching
/// to one asks for their PIN if they set one, and the owner's PIN or
/// password always. Someone else signs in, and is kept here too.
struct ProfilesView: View {
    let people: [API.Profile]
    /// Opened from Settings: Back keeps the person signed in. Never when the
    /// TV opens - that is the question being asked, and Back would skip the
    /// PIN of whoever was here last.
    var canGoBack = false
    @Environment(AppModel.self) private var model
    @State private var picked: API.Profile?
    @State private var secret = ""
    @State private var busy = false
    @State private var message: String?

    var body: some View {
        VStack(spacing: 50) {
            Logo()
            Text("Who's listening?").font(.title2.bold())
            if let picked {
                VStack(spacing: 30) {
                    tile(picked).disabled(true)
                    SecureField(picked.needs == "pin" ? "\(picked.name)'s PIN" : "\(picked.name)'s password", text: $secret)
                        .keyboardType(picked.needs == "pin" ? .numberPad : .default)
                        .frame(width: 600)
                        .onSubmit { go(picked) }
                    if let message {
                        Text(message).foregroundStyle(.red)
                    }
                    Button(busy ? "Switching…" : "Continue") { go(picked) }
                        .disabled(busy || secret.isEmpty)
                    // Typing with the remote is the chore: a phone signed in
                    // can sign the TV in instead (the page's #profiles-phone).
                    Button("Use your phone instead") { Task { await model.signInWithPhone() } }
                    Button("Back") { self.picked = nil; secret = ""; message = nil }
                }
            } else {
                HStack(spacing: 60) {
                    ForEach(people) { person in
                        Button { pick(person) } label: { tile(person) }
                            .buttonStyle(.borderless)
                    }
                }
                if let message {
                    Text(message).foregroundStyle(.red)
                }
                Button("Someone else") { Task { await model.signInSomeoneElse() } }
                // Opened from Settings: Back keeps the person signed in.
                if canGoBack {
                    Button("Back") { model.cancelSwitch() }
                }
            }
        }
        .multilineTextAlignment(.center)
        #if DEBUG
        // For the simulator, which cannot press: -profile <name> -profileSecret <pin or password>
        .task {
            if let name = UserDefaults.standard.string(forKey: "profile"),
               let person = people.first(where: { $0.name == name }) {
                pick(person)
                if let s = UserDefaults.standard.string(forKey: "profileSecret") {
                    secret = s
                    go(person)
                }
            }
        }
        #endif
    }

    private func tile(_ person: API.Profile) -> some View {
        VStack(spacing: 14) {
            Avatar(name: person.name, picture: person.picture, size: 180)
            Text(person.name)
            Text(person.needs == "pin" ? "PIN" : person.needs == "password" ? "Password" : " ")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    private func pick(_ person: API.Profile) {
        message = nil
        if person.needs.isEmpty {
            go(person)
        } else {
            secret = ""
            picked = person
        }
    }

    private func go(_ person: API.Profile) {
        guard !busy, let api = model.api else { return }
        busy = true
        Task {
            do {
                // The book playing keeps its place as the person listening,
                // saved before the account changes.
                if api.user?.id != person.id { await model.beforeSwitch() }
                try await api.switchProfile(person, secret: secret)
                model.switched()
            } catch API.Failure.status(403, _) {
                message = person.needs == "pin" ? "That PIN is not right." : "That password is not right."
                secret = ""
            } catch {
                message = error.localizedDescription
            }
            busy = false
        }
    }
}

/// A person's circle: their own picture, or their initial on a colour of
/// their own - the same colour the page gives them (app.js `paintAvatar`).
struct Avatar: View {
    let name: String
    let picture: String?
    var size: CGFloat = 60
    @Environment(AppModel.self) private var model

    private static let hues: [Double] = [210, 340, 28, 140, 265, 190, 5, 95]

    var body: some View {
        let hash = name.unicodeScalars.reduce(UInt32(0)) { $0 &* 31 &+ $1.value }
        let initial = Text(name.prefix(1).uppercased())
            .font(.system(size: size * 0.44, weight: .heavy))
            .foregroundStyle(.white)
            .frame(width: size, height: size)
        ZStack {
            Circle().fill(Color(hue: Self.hues[Int(hash % UInt32(Self.hues.count))] / 360, saturation: 0.55, brightness: 0.62))
            if let picture, let url = try? model.api?.absolute(picture) {
                SafeImage(url: url, maxPixels: Int(size * 2)) { image in
                    image.resizable().scaledToFill()
                } placeholder: { initial }
            } else {
                initial
            }
        }
        .frame(width: size, height: size)
        .clipShape(Circle())
    }
}

/// The signed-in person's circle as an image, for a place that takes only
/// an image (the side bar's entry): their picture cropped round, or their
/// initial on their colour.
enum AvatarImage {
    static let hues: [Double] = [210, 340, 28, 140, 265, 190, 5, 95]

    static func make(api: API, size: CGFloat) async -> UIImage? {
        guard let user = api.user else { return nil }
        var picture: UIImage?
        if let path = user.picture, let url = try? api.absolute(path),
           let (data, _) = try? await SafeLoad.data(from: url, limit: SafeLoad.picture) {
            picture = await Task.detached(priority: .utility) { SafeLoad.image(data, maxPixels: Int(size * 3)) }.value
        }
        let hash = user.name.unicodeScalars.reduce(UInt32(0)) { $0 &* 31 &+ $1.value }
        let colour = UIColor(hue: hues[Int(hash % UInt32(hues.count))] / 360, saturation: 0.55, brightness: 0.62, alpha: 1)
        let rect = CGRect(x: 0, y: 0, width: size, height: size)
        return UIGraphicsImageRenderer(size: rect.size).image { _ in
            UIBezierPath(ovalIn: rect).addClip()
            colour.setFill()
            UIRectFill(rect)
            if let picture {
                let scale = max(size / picture.size.width, size / picture.size.height)
                let w = picture.size.width * scale, h = picture.size.height * scale
                picture.draw(in: CGRect(x: (size - w) / 2, y: (size - h) / 2, width: w, height: h))
            } else {
                let text = NSAttributedString(string: user.name.prefix(1).uppercased(), attributes: [
                    .font: UIFont.systemFont(ofSize: size * 0.46, weight: .heavy), .foregroundColor: UIColor.white,
                ])
                let t = text.size()
                text.draw(at: CGPoint(x: (size - t.width) / 2, y: (size - t.height) / 2))
            }
        }
    }
}

/// The server did not answer: said, and tried again every ten seconds, with
/// a way to another server - not dropped to the address as if it were wrong.
struct UnreachableView: View {
    let host: String
    @Environment(AppModel.self) private var model

    var body: some View {
        VStack(spacing: 40) {
            Logo()
            Text("Can't reach \(host)")
                .font(.title2.bold())
            Text("Is the computer SoundStorm runs on switched on, and this TV on a network that reaches it? Trying again by itself.")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .frame(width: 1100)
            Button("Try again") { Task { await model.refreshSession() } }
            Button("Change server") { model.changeServer() }
        }
        .multilineTextAlignment(.center)
        .task {
            while true {
                try? await Task.sleep(for: .seconds(10))
                if Task.isCancelled { return } // left for the library or the picker
                await model.refreshSession()
            }
        }
    }
}
