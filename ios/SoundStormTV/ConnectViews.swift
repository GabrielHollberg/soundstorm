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

    var body: some View {
        VStack(spacing: 40) {
            Logo()
            if hasAccount {
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
    }

    private func signIn() {
        guard !busy, let api = model.api else { return }
        busy = true
        message = nil
        Task {
            do {
                try await api.signIn(username: username, password: password)
                model.signedIn()
            } catch {
                message = error.localizedDescription
            }
            busy = false
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
