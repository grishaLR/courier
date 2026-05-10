import SwiftUI

struct ActorResult: Codable, Identifiable {
    let did: String
    let handle: String
    let displayName: String?
    let avatar: String?

    var id: String { did }
}

struct OnboardingView: View {
    @EnvironmentObject var authManager: AuthManager
    @EnvironmentObject var pushManager: PushManager

    @State private var handleInput = ""
    @State private var suggestions: [ActorResult] = []
    @State private var isLoading = false
    @State private var isSearching = false
    @State private var errorMessage: String?
    @State private var searchTask: Task<Void, Never>?

    var body: some View {
        NavigationStack {
            ScrollView {
            VStack(spacing: 24) {
                Color.clear.frame(height: 40)

                // Logo
                VStack(spacing: 12) {
                    Image("Logo")
                        .resizable()
                        .scaledToFit()
                        .frame(width: 200, height: 200)

                    Text("Courier")
                        .font(.largeTitle.bold())

                    Text("Push notifications for Open Social")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }

                // Handle input with typeahead
                VStack(spacing: 0) {
                    TextField("handle.bsky.social or DID", text: $handleInput)
                        .textFieldStyle(.roundedBorder)
                        .textContentType(.username)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                        .padding(.horizontal)
                        .onChange(of: handleInput) { _, query in
                            searchTask?.cancel()
                            let trimmed = query.trimmingCharacters(in: .whitespaces)
                            if trimmed.count < 2 || trimmed.starts(with: "did:") {
                                suggestions = []
                                return
                            }
                            searchTask = Task {
                                try? await Task.sleep(for: .milliseconds(250))
                                if !Task.isCancelled {
                                    await searchActors(query: trimmed)
                                }
                            }
                        }

                    // Suggestions dropdown
                    if !suggestions.isEmpty {
                        VStack(spacing: 0) {
                            ForEach(suggestions) { actor in
                                Button {
                                    handleInput = actor.handle
                                    suggestions = []
                                } label: {
                                    HStack(spacing: 10) {
                                        AsyncImage(url: actor.avatar.flatMap { URL(string: $0) }) { image in
                                            image.resizable().scaledToFill()
                                        } placeholder: {
                                            Circle().fill(.quaternary)
                                        }
                                        .frame(width: 32, height: 32)
                                        .clipShape(Circle())

                                        VStack(alignment: .leading, spacing: 1) {
                                            if let displayName = actor.displayName, !displayName.isEmpty {
                                                Text(displayName)
                                                    .font(.subheadline.bold())
                                                    .foregroundStyle(.primary)
                                            }
                                            Text("@\(actor.handle)")
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
                                        }
                                        Spacer()
                                    }
                                    .padding(.horizontal, 12)
                                    .padding(.vertical, 8)
                                }
                                if actor.id != suggestions.last?.id {
                                    Divider().padding(.leading, 54)
                                }
                            }
                        }
                        .background(.ultraThinMaterial)
                        .clipShape(RoundedRectangle(cornerRadius: 10))
                        .padding(.horizontal)
                        .padding(.top, 4)
                    }

                    if let errorMessage {
                        Text(errorMessage)
                            .font(.caption)
                            .foregroundStyle(.red)
                            .padding(.top, 8)
                    }

                    Button {
                        Task { await signIn() }
                    } label: {
                        if isLoading {
                            ProgressView()
                                .frame(maxWidth: .infinity)
                        } else {
                            Text("Login")
                                .frame(maxWidth: .infinity)
                        }
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .disabled(handleInput.isEmpty || isLoading)
                    .padding(.horizontal)
                    .padding(.top, 16)
                }
            }
            }
            .background(Color("BackgroundColor").ignoresSafeArea())
        }
    }

    private func searchActors(query: String) async {
        let encoded = query.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? query
        guard let url = URL(string: "https://typeahead.waow.tech/xrpc/app.bsky.actor.searchActorsTypeahead?q=\(encoded)&limit=6") else { return }

        do {
            var req = URLRequest(url: url)
            req.addValue("courier.social", forHTTPHeaderField: "X-Client")
            let (data, _) = try await URLSession.shared.data(for: req)
            let result = try JSONDecoder().decode(TypeaheadResponse.self, from: data)
            if !Task.isCancelled {
                suggestions = result.actors
            }
        } catch {
            if !Task.isCancelled {
                suggestions = []
            }
        }
    }

    private func signIn() async {
        isLoading = true
        errorMessage = nil
        suggestions = []

        await pushManager.requestPermission()

        do {
            try await authManager.authenticate(handleOrDID: handleInput.trimmingCharacters(in: .whitespaces))
        } catch {
            errorMessage = error.localizedDescription
        }

        isLoading = false
    }
}

private struct TypeaheadResponse: Codable {
    let actors: [ActorResult]
}
