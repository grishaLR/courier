import SwiftUI

struct AppInfo: Codable, Identifiable {
    let collectionPrefix: String
    let appName: String
    let appUrl: String
    let category: String
    let description: String?
    let faviconUrl: String?
    let alternativeFor: String?

    var id: String { collectionPrefix + appUrl }
}

struct AppGroup: Codable, Identifiable {
    let category: String
    let apps: [AppInfo]

    var id: String { category }
}

struct UserAppsResponse: Codable {
    let yourApps: [AppGroup]
    let discoverApps: [AppGroup]
}

struct PreferencesView: View {
    @EnvironmentObject var authManager: AuthManager

    @State private var yourApps: [AppGroup] = []
    @State private var discoverApps: [AppGroup] = []
    @State private var appPrefs: [String: Bool] = [:]
    @State private var typePrefs = Preferences.default
    @State private var expandedCategories: Set<String> = []
    @State private var userAppPrefixes: Set<String> = []
    @State private var yourAppsExpanded = true
    @State private var discoverExpanded = false
    @State private var preferredApps: [String: String] = [:] // prefix → appURL
    @State private var pickerPrefix: String?
    private let sharedLexicons: Set<String> = ["app.bsky", "community.lexicon.calendar"]
    @State private var isLoading = true
    @State private var isSaving = false
    @State private var saved = false

    var body: some View {
        NavigationStack {
            Group {
                if isLoading {
                    ProgressView()
                } else {
                    List {
                        // Account + notification types in one compact section
                        Section {
                            DisclosureGroup("Notification Types") {
                                Toggle("Likes", isOn: $typePrefs.likes)
                                Toggle("Replies", isOn: $typePrefs.replies)
                                Toggle("Reposts", isOn: $typePrefs.reposts)
                                Toggle("Follows", isOn: $typePrefs.follows)
                                Toggle("Mentions", isOn: $typePrefs.mentions)
                                Toggle("Quotes", isOn: $typePrefs.quotes)
                                Toggle("Other", isOn: $typePrefs.generic)
                            }
                        } header: {
                            if let handle = authManager.handle {
                                Text(handle).font(.caption)
                            }
                        }

                        // Apps you're on
                        if !yourApps.isEmpty {
                            Section {
                                if yourAppsExpanded {
                                    ForEach(yourApps) { group in
                                        appCategoryRow(group: group, showRemove: true)
                                    }
                                }
                            } header: {
                                Button {
                                    withAnimation { yourAppsExpanded.toggle() }
                                } label: {
                                    HStack {
                                        Text("Apps You're On")
                                        Spacer()
                                        Image(systemName: yourAppsExpanded ? "chevron.down" : "chevron.right")
                                            .font(.caption)
                                    }
                                    .foregroundStyle(.primary)
                                }
                            }
                        }

                        // Discover
                        if !discoverApps.isEmpty {
                            Section {
                                if discoverExpanded {
                                    ForEach(discoverApps) { group in
                                        appCategoryRow(group: group, showAdd: true)
                                    }
                                }
                            } header: {
                                Button {
                                    withAnimation { discoverExpanded.toggle() }
                                } label: {
                                    HStack {
                                        Text("Apps You Could Be On")
                                        Spacer()
                                        Image(systemName: discoverExpanded ? "chevron.down" : "chevron.right")
                                            .font(.caption)
                                    }
                                    .foregroundStyle(.primary)
                                }
                            }
                        }

                        // Save button
                        Section {
                            Button {
                                Task { await save() }
                            } label: {
                                HStack {
                                    Text("Save All")
                                        .fontWeight(.medium)
                                    Spacer()
                                    if isSaving {
                                        ProgressView()
                                    } else if saved {
                                        Image(systemName: "checkmark.circle.fill")
                                            .foregroundStyle(.green)
                                    }
                                }
                            }
                            .disabled(isSaving)
                        }
                    }
                }
            }
            .listStyle(.plain)
            .navigationTitle("Preferences")
            .sheet(isPresented: Binding(
                get: { pickerPrefix != nil },
                set: { if !$0 { pickerPrefix = nil } }
            )) {
                if let prefix = pickerPrefix {
                    PreferredAppSheet(
                        collectionPrefix: prefix,
                        currentAppURL: preferredApps[prefix],
                        did: authManager.did ?? ""
                    ) { selectedApp in
                        // Save preferred app
                        preferredApps[prefix] = selectedApp.appUrl
                        Task {
                            guard let did = authManager.did else { return }
                            try? await APIClient.shared.setPreferredApps(did: did, prefs: preferredApps)
                        }
                    }
                }
            }
            .task {
                await loadData()
            }
        }
    }

    @ViewBuilder
    private func appCategoryRow(group: AppGroup, showAdd: Bool = false, showRemove: Bool = false) -> some View {
        DisclosureGroup(
            isExpanded: Binding(
                get: { expandedCategories.contains(group.category) },
                set: { expanded in
                    if expanded {
                        expandedCategories.insert(group.category)
                    } else {
                        expandedCategories.remove(group.category)
                    }
                }
            )
        ) {
            ForEach(group.apps) { app in
                if showAdd {
                    HStack {
                        AppLabelView(app: app)
                        Spacer()
                        Button {
                            Task { await pinApp(app) }
                        } label: {
                            Image(systemName: "plus.circle.fill")
                                .foregroundStyle(.green)
                                .font(.title3)
                        }
                        .buttonStyle(.plain)
                    }
                    .padding(.leading, -20)
                    .listRowSeparator(.hidden)
                } else {
                    AppToggleRow(
                        app: app,
                        isEnabled: Binding(
                            get: { appPrefs[app.collectionPrefix] ?? true },
                            set: { appPrefs[app.collectionPrefix] = $0 }
                        ),
                        showRemove: showRemove,
                        onRemove: { Task { await unpinApp(app) } },
                        hasAlternatives: sharedLexicons.contains(app.collectionPrefix) || sharedLexicons.contains(app.alternativeFor ?? ""),
                        onPickAlternative: {
                            pickerPrefix = app.alternativeFor ?? app.collectionPrefix
                        }
                    )
                    .padding(.leading, -20)
                    .listRowSeparator(.hidden)
                }
            }
        } label: {
            HStack {
                Text(group.category)
                    .font(.headline)
                Spacer()
                let count = group.apps.count
                Text("\(count)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func pinApp(_ app: AppInfo) async {
        guard let did = authManager.did else { return }

        // Call API
        do {
            var req = try await APIClient.shared.makeRequest("/catalog/user/pin", method: "POST")
            req.addValue(did, forHTTPHeaderField: "X-DID")
            req.httpBody = try JSONEncoder().encode(["collectionPrefix": app.collectionPrefix])
            let (_, _) = try await URLSession.shared.data(for: req)
        } catch {
        }

        // Update local state
        appPrefs[app.collectionPrefix] = true

        for i in discoverApps.indices {
            discoverApps[i] = AppGroup(
                category: discoverApps[i].category,
                apps: discoverApps[i].apps.filter { $0.collectionPrefix != app.collectionPrefix }
            )
        }
        discoverApps.removeAll { $0.apps.isEmpty }

        if let idx = yourApps.firstIndex(where: { $0.category == app.category }) {
            var apps = yourApps[idx].apps
            apps.append(app)
            yourApps[idx] = AppGroup(category: app.category, apps: apps)
        } else {
            yourApps.append(AppGroup(category: app.category, apps: [app]))
        }
    }

    private func unpinApp(_ app: AppInfo) async {
        guard let did = authManager.did else { return }

        // Call API
        do {
            var req = try await APIClient.shared.makeRequest("/catalog/user/pin", method: "DELETE")
            req.addValue(did, forHTTPHeaderField: "X-DID")
            req.httpBody = try JSONEncoder().encode(["collectionPrefix": app.collectionPrefix])
            let (_, _) = try await URLSession.shared.data(for: req)
        } catch {
        }

        // Update local state
        appPrefs[app.collectionPrefix] = false

        for i in yourApps.indices {
            yourApps[i] = AppGroup(
                category: yourApps[i].category,
                apps: yourApps[i].apps.filter { $0.collectionPrefix != app.collectionPrefix }
            )
        }
        yourApps.removeAll { $0.apps.isEmpty }

        if let idx = discoverApps.firstIndex(where: { $0.category == app.category }) {
            var apps = discoverApps[idx].apps
            apps.append(app)
            discoverApps[idx] = AppGroup(category: app.category, apps: apps)
        } else {
            discoverApps.append(AppGroup(category: app.category, apps: [app]))
        }
    }

    private func loadData() async {
        guard let did = authManager.did else { return }
        isLoading = true
        do {
            let response: UserAppsResponse = try await APIClient.shared.get("/catalog/user?actor=\(did)")
            yourApps = response.yourApps
            discoverApps = response.discoverApps

            // Track which prefixes are the user's apps
            userAppPrefixes = Set(yourApps.flatMap { $0.apps.map(\.collectionPrefix) })

            let prefs: [String: Bool] = try await APIClient.shared.get("/catalog/user/prefs?did=\(did)")
            appPrefs = prefs

            preferredApps = try await APIClient.shared.getPreferredApps(did: did)

            // Discover apps default to off (only if not already in saved prefs)
            for group in discoverApps {
                for app in group.apps {
                    if appPrefs[app.collectionPrefix] == nil {
                        appPrefs[app.collectionPrefix] = false
                    }
                }
            }

            // Expand your apps categories by default
            expandedCategories = Set(yourApps.map(\.category))
        } catch {
        }
        isLoading = false
    }

    private func save() async {
        guard let did = authManager.did else { return }
        isSaving = true
        saved = false
        do {
            // Save type preferences
            try await APIClient.shared.updatePreferences(did: did, preferences: typePrefs)

            // Save app preferences
            var req = try await APIClient.shared.makeRequest("/catalog/user/prefs", method: "PUT")
            req.addValue(did, forHTTPHeaderField: "X-DID")
            req.httpBody = try JSONEncoder().encode(appPrefs)
            let (_, _) = try await URLSession.shared.data(for: req)

            saved = true
            try? await Task.sleep(for: .seconds(2))
            saved = false
        } catch {
        }
        isSaving = false
    }
}

struct AppLabelView: View {
    let app: AppInfo

    var body: some View {
        HStack(spacing: 10) {
            if let faviconUrl = app.faviconUrl, !faviconUrl.isEmpty,
               let url = URL(string: faviconUrl) {
                AsyncImage(url: url) { image in
                    image.resizable().scaledToFit()
                } placeholder: {
                    appInitial
                }
                .frame(width: 28, height: 28)
                .clipShape(RoundedRectangle(cornerRadius: 6))
            } else {
                appInitial
            }

            VStack(alignment: .leading, spacing: 2) {
                Button {
                    if let url = URL(string: app.appUrl) {
                        UIApplication.shared.open(url)
                    }
                } label: {
                    Text(app.appName)
                        .font(.subheadline)
                        .foregroundStyle(.primary)
                }
                if let desc = app.description, !desc.isEmpty {
                    Text(desc)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        }
    }

    private var appInitial: some View {
        RoundedRectangle(cornerRadius: 6)
            .fill(.quaternary)
            .frame(width: 28, height: 28)
            .overlay {
                Text(String(app.appName.prefix(1)))
                    .font(.caption.bold())
                    .foregroundStyle(.secondary)
            }
    }
}

struct AppToggleRow: View {
    let app: AppInfo
    @Binding var isEnabled: Bool
    var showRemove: Bool = false
    var onRemove: (() -> Void)? = nil
    var hasAlternatives: Bool = false
    var onPickAlternative: (() -> Void)? = nil

    var body: some View {
        HStack(spacing: 10) {
            if let faviconUrl = app.faviconUrl, !faviconUrl.isEmpty,
               let url = URL(string: faviconUrl) {
                AsyncImage(url: url) { image in
                    image.resizable().scaledToFit()
                } placeholder: {
                    appInitialIcon
                }
                .frame(width: 28, height: 28)
                .clipShape(RoundedRectangle(cornerRadius: 6))
            } else {
                appInitialIcon
            }

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 4) {
                    Button {
                        if let url = URL(string: app.appUrl) {
                            UIApplication.shared.open(url)
                        }
                    } label: {
                        Text(app.appName)
                            .font(.subheadline)
                            .foregroundStyle(.primary)
                    }
                    if hasAlternatives {
                        Button {
                            onPickAlternative?()
                        } label: {
                            Image(systemName: "arrow.triangle.2.circlepath")
                                .font(.system(size: 10))
                                .foregroundStyle(.blue)
                        }
                        .buttonStyle(.plain)
                    }
                }
                if let desc = app.description, !desc.isEmpty {
                    Text(desc)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }

            Spacer()

            Button {
                isEnabled.toggle()
            } label: {
                Circle()
                    .fill(isEnabled ? Color.green : Color(.systemGray4))
                    .frame(width: 24, height: 24)
                    .overlay {
                        Image(systemName: isEnabled ? "checkmark" : "")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundStyle(.white)
                    }
            }
            .buttonStyle(.plain)

            if showRemove {
                Button { onRemove?() } label: {
                    Image(systemName: "minus.circle.fill")
                        .foregroundStyle(.red)
                        .font(.system(size: 16))
                }
                .buttonStyle(.plain)
            }
        }
    }

    private var appInitialIcon: some View {
        RoundedRectangle(cornerRadius: 6)
            .fill(.quaternary)
            .frame(width: 28, height: 28)
            .overlay {
                Text(String(app.appName.prefix(1)))
                    .font(.caption.bold())
                    .foregroundStyle(.secondary)
            }
    }
}
