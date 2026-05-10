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

enum AppTheme: String, CaseIterable {
    case system, light, dark

    var label: String {
        switch self {
        case .system: return "System"
        case .light: return "Light"
        case .dark: return "Dark"
        }
    }

    var colorScheme: ColorScheme? {
        switch self {
        case .system: return nil
        case .light: return .light
        case .dark: return .dark
        }
    }
}

struct PreferencesView: View {
    @EnvironmentObject var authManager: AuthManager
    @AppStorage("appTheme") private var appTheme: AppTheme = .system

    @State private var yourApps: [AppGroup] = []
    @State private var discoverApps: [AppGroup] = []
    @State private var appPrefs: [String: Bool] = [:]
    @State private var typePrefs = Preferences.default
    @State private var expandedCategories: Set<String> = []
    @State private var userAppPrefixes: Set<String> = []
    @State private var appearanceExpanded = false
    @State private var notifTypesExpanded = false
    @State private var yourAppsExpanded = true
    @State private var discoverExpanded = false
    @State private var preferredApps: [String: String] = [:] // prefix → appURL
    @State private var pickerPrefix: String?
    private let sharedLexicons: Set<String> = ["app.bsky", "community.lexicon.calendar"]
    @State private var blogSubs: [APIClient.BlogSub] = []
    @State private var blogSubsExpanded = false
    @State private var isRefreshingBlogs = false
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
                        Section {
                            if appearanceExpanded {
                                Picker("Theme", selection: $appTheme) {
                                    ForEach(AppTheme.allCases, id: \.self) { theme in
                                        Text(theme.label).tag(theme)
                                    }
                                }
                                .pickerStyle(.segmented)
                                .listRowBackground(Color("BackgroundColor"))
                            }
                        } header: {
                            sectionHeader(
                                title: "Appearance",
                                count: 0,
                                isExpanded: $appearanceExpanded
                            )
                            .listRowBackground(Color("BackgroundColor"))
                        }

                        Section {
                            if notifTypesExpanded {
                                Toggle("Likes", isOn: $typePrefs.likes)
                                    .listRowBackground(Color("BackgroundColor"))
                                Toggle("Replies", isOn: $typePrefs.replies)
                                    .listRowBackground(Color("BackgroundColor"))
                                Toggle("Reposts", isOn: $typePrefs.reposts)
                                    .listRowBackground(Color("BackgroundColor"))
                                Toggle("Follows", isOn: $typePrefs.follows)
                                    .listRowBackground(Color("BackgroundColor"))
                                Toggle("Mentions", isOn: $typePrefs.mentions)
                                    .listRowBackground(Color("BackgroundColor"))
                                Toggle("Quotes", isOn: $typePrefs.quotes)
                                    .listRowBackground(Color("BackgroundColor"))
                                Toggle("Other", isOn: $typePrefs.generic)
                                    .listRowBackground(Color("BackgroundColor"))
                            }
                        } header: {
                            sectionHeader(
                                title: "Notification Types",
                                count: 0,
                                isExpanded: $notifTypesExpanded
                            )
                            .listRowBackground(Color("BackgroundColor"))
                        }

                        Section {
                            if blogSubsExpanded {
                                if blogSubs.isEmpty {
                                    HStack {
                                        Text("No blog subscriptions found")
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                        Spacer()
                                    }
                                    .listRowBackground(Color("BackgroundColor"))
                                }

                                ForEach(blogSubs.indices, id: \.self) { index in
                                        HStack(spacing: 10) {
                                            // Blog icon
                                            blogIcon(blogSubs[index])
                                                .frame(width: 28, height: 28)
                                                .clipShape(RoundedRectangle(cornerRadius: 6))

                                            VStack(alignment: .leading, spacing: 2) {
                                                Button {
                                                    if let webUrl = blogSubs[index].webUrl,
                                                       let url = URL(string: webUrl) {
                                                        UIApplication.shared.open(url)
                                                    }
                                                } label: {
                                                    Text(blogSubs[index].blogName.isEmpty ? "Untitled Blog" : blogSubs[index].blogName)
                                                        .font(.subheadline)
                                                        .foregroundStyle(.primary)
                                                }
                                                Text(blogSubs[index].platform)
                                                    .font(.caption2)
                                                    .foregroundStyle(.secondary)
                                            }

                                            Spacer()

                                            Button {
                                                blogSubs[index].enabled.toggle()
                                                let sub = blogSubs[index]
                                                Task {
                                                    try? await APIClient.shared.setBlogPref(
                                                        publicationUri: sub.publicationUri,
                                                        enabled: sub.enabled
                                                    )
                                                }
                                            } label: {
                                                Circle()
                                                    .fill(blogSubs[index].enabled ? Color.green : Color(.systemGray4))
                                                    .frame(width: 24, height: 24)
                                                    .overlay {
                                                        Image(systemName: blogSubs[index].enabled ? "checkmark" : "")
                                                            .font(.system(size: 12, weight: .bold))
                                                            .foregroundStyle(.white)
                                                    }
                                            }
                                            .buttonStyle(.plain)
                                        }
                                        .listRowBackground(Color("BackgroundColor"))
                                    }

                                    Button {
                                        Task { await refreshBlogs() }
                                    } label: {
                                        HStack {
                                            if isRefreshingBlogs {
                                                ProgressView()
                                                    .controlSize(.small)
                                            } else {
                                                Image(systemName: "arrow.clockwise")
                                            }
                                            Text(blogSubs.isEmpty ? "Discover Subscriptions" : "Refresh")
                                        }
                                        .font(.caption)
                                        .foregroundStyle(Color.accentColor)
                                    }
                                    .disabled(isRefreshingBlogs)
                                    .listRowBackground(Color("BackgroundColor"))
                                }
                            } header: {
                                sectionHeader(
                                    title: "Blog Subscriptions",
                                    count: blogSubs.count,
                                    isExpanded: $blogSubsExpanded
                                )
                                .listRowBackground(Color("BackgroundColor"))
                            }

                        if !yourApps.isEmpty {
                            Section {
                                if yourAppsExpanded {
                                    ForEach(yourApps) { group in
                                        appCategoryRow(group: group, showRemove: true)
                                            .listRowBackground(Color("BackgroundColor"))
                                    }
                                }
                            } header: {
                                sectionHeader(
                                    title: "Apps You're On",
                                    count: yourApps.flatMap(\.apps).count,
                                    isExpanded: $yourAppsExpanded
                                )
                                .listRowBackground(Color("BackgroundColor"))
                            }
                        }

                        if !discoverApps.isEmpty {
                            Section {
                                if discoverExpanded {
                                    ForEach(discoverApps) { group in
                                        appCategoryRow(group: group, showAdd: true)
                                            .listRowBackground(Color("BackgroundColor"))
                                    }
                                }
                            } header: {
                                sectionHeader(
                                    title: "Apps You Could Be On",
                                    count: discoverApps.flatMap(\.apps).count,
                                    isExpanded: $discoverExpanded
                                )
                                .listRowBackground(Color("BackgroundColor"))
                            }
                        }

                    }
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .background(Color("BackgroundColor"))
            .environment(\.defaultMinListHeaderHeight, 0)
            .navigationTitle("Courier")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button {
                        Task { await authManager.signOut() }
                    } label: {
                        Image(systemName: "rectangle.portrait.and.arrow.right")
                            .font(.caption)
                    }
                }
            }
            .onChange(of: typePrefs) { _, _ in
                Task { await save() }
            }
            .onChange(of: appPrefs) { _, _ in
                Task { await save() }
            }
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
    private func blogIcon(_ sub: APIClient.BlogSub) -> some View {
        if let iconUrl = sub.iconUrl, !iconUrl.isEmpty,
           let url = URL(string: iconUrl) {
            AsyncImage(url: url) { image in
                image.resizable().scaledToFill()
            } placeholder: {
                platformIcon(sub.platform)
            }
        } else {
            platformIcon(sub.platform)
        }
    }

    @ViewBuilder
    private func platformIcon(_ platform: String) -> some View {
        let (icon, color): (String, Color) = switch platform {
        case "leaflet": ("leaf.fill", .green)
        case "standard": ("doc.text.fill", .blue)
        case "whitewind": ("wind", .cyan)
        case "pckt": ("bookmark.fill", .orange)
        default: ("doc.text.fill", .gray)
        }
        RoundedRectangle(cornerRadius: 6)
            .fill(color.opacity(0.15))
            .overlay {
                Image(systemName: icon)
                    .font(.system(size: 14))
                    .foregroundStyle(color)
            }
    }

    private func refreshBlogs() async {
        isRefreshingBlogs = true
        do {
            blogSubs = try await APIClient.shared.refreshBlogSubs()
        } catch {}
        isRefreshingBlogs = false
    }

    @ViewBuilder
    private func sectionLabel(_ title: String) -> some View {
        Text(title)
            .font(.subheadline.bold())
            .foregroundStyle(Color.accentColor)
            .listRowBackground(Color("BackgroundColor"))
    }

    private func sectionHeader(title: String, count: Int, isExpanded: Binding<Bool>) -> some View {
        Button {
            withAnimation { isExpanded.wrappedValue.toggle() }
        } label: {
            HStack {
                Text(title)
                    .font(.subheadline.bold())
                    .foregroundStyle(Color.accentColor)
                if count > 0 {
                    Text("\(count)")
                        .font(.caption2)
                        .foregroundStyle(Color("SecondaryText"))
                        .padding(.horizontal, 5)
                        .padding(.vertical, 1)
                        .background(Color("SecondaryText").opacity(0.15))
                        .clipShape(Capsule())
                }
                Spacer()
                Image(systemName: isExpanded.wrappedValue ? "chevron.down" : "chevron.right")
                    .font(.caption)
                    .foregroundStyle(Color("SecondaryText"))
            }
            .padding(.vertical, 4)
            .contentShape(Rectangle())
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

            // Load blog subscriptions
            blogSubs = (try? await APIClient.shared.getBlogSubs()) ?? []
            if !blogSubs.isEmpty {
                blogSubsExpanded = true
            }
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
