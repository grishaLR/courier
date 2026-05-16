import SwiftUI

struct InboxView: View {
    @EnvironmentObject var authManager: AuthManager
    @StateObject private var stream = NotificationStream()

    @State private var notifications: [CourierNotification] = []
    @State private var isLoading = false
    @State private var suggestionCollection: String?
    @State private var showClearConfirm = false
    @State private var selectedFilter: String? = nil // nil = "All"

    /// Unique app names from current notifications for filter tabs
    private var appFilters: [String] {
        let names = Set(notifications.compactMap { $0.appName })
        return names.sorted()
    }

    private var filteredNotifications: [CourierNotification] {
        guard let filter = selectedFilter else { return notifications }
        return notifications.filter { $0.appName == filter }
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                // Filter bar
                if !notifications.isEmpty && !appFilters.isEmpty {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 8) {
                            FilterChip(label: "All", isSelected: selectedFilter == nil) {
                                selectedFilter = nil
                            }
                            ForEach(appFilters, id: \.self) { app in
                                FilterChip(label: app, isSelected: selectedFilter == app) {
                                    selectedFilter = app
                                }
                            }
                        }
                        .padding(.horizontal)
                        .padding(.vertical, 8)
                    }
                    Divider()
                }

                // Content
                Group {
                    if isLoading && notifications.isEmpty {
                        ProgressView()
                            .frame(maxHeight: .infinity)
                    } else if notifications.isEmpty {
                        ContentUnavailableView(
                            "No Notifications",
                            systemImage: "bell.slash",
                            description: Text("Notifications will appear here when someone interacts with your posts.")
                        )
                    } else if filteredNotifications.isEmpty {
                        ContentUnavailableView(
                            "No \(selectedFilter ?? "") Notifications",
                            systemImage: "bell.slash",
                            description: Text("No notifications from this app yet.")
                        )
                    } else {
                        List {
                            ForEach(filteredNotifications) { notif in
                                NotificationRow(notification: notif)
                                    .onTapGesture {
                                        if let deepLink = notif.deepLink, let url = URL(string: deepLink) {
                                            UIApplication.shared.open(url)
                                        } else {
                                            let linkURI = notif.subjectUri ?? notif.uri
                                            DeepLinkHandler.open(atURI: linkURI)
                                        }
                                        if notif.type == .generic && notif.appName == nil {
                                            suggestionCollection = notif.collection
                                        }
                                    }
                                    .listRowBackground(Color("BackgroundColor"))
                            }
                            .onDelete { indexSet in
                                // Map filtered indices back to main array
                                let filtered = filteredNotifications
                                let toDelete = indexSet.map { filtered[$0] }
                                notifications.removeAll { notif in toDelete.contains(where: { $0.id == notif.id }) }
                                Task {
                                    for notif in toDelete {
                                        try? await APIClient.shared.deleteNotification(uri: notif.uri)
                                    }
                                    await NotificationCache.shared.save(notifications)
                                }
                            }
                        }
                        .listStyle(.plain)
                        .scrollContentBackground(.hidden)
                        .refreshable {
                            await loadNotifications()
                        }
                    }
                }
            }
            .background(Color("BackgroundColor"))
            .navigationTitle("Inbox")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarLeading) {
                    if !notifications.isEmpty {
                        Button {
                            showClearConfirm = true
                        } label: {
                            Image(systemName: "trash")
                        }
                    }
                }
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button {
                        Task { await authManager.signOut() }
                    } label: {
                        Image(systemName: "rectangle.portrait.and.arrow.right")
                    }
                }
            }
            .alert("Clear Notifications", isPresented: $showClearConfirm) {
                Button("Clear All", role: .destructive) {
                    Task {
                        await NotificationCache.shared.clear()
                        try? await APIClient.shared.clearNotifications()
                        notifications = []
                    }
                }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("This will remove all notifications from this device.")
            }
            .task {
                let cached = await NotificationCache.shared.load()
                if !cached.isEmpty {
                    notifications = cached
                }
                await loadNotifications()
                if let did = authManager.did {
                    stream.connect(did: did)
                }
            }
            .onDisappear {
                stream.disconnect()
            }
            .onChange(of: stream.latestNotification) { _, notif in
                if let notif {
                    notifications.insert(notif, at: 0)
                    if notifications.count > 100 {
                        notifications = Array(notifications.prefix(100))
                    }
                    Task { await NotificationCache.shared.save(notifications) }
                }
            }
            .sheet(isPresented: Binding(
                get: { suggestionCollection != nil },
                set: { if !$0 { suggestionCollection = nil } }
            )) {
                if let collection = suggestionCollection {
                    AppSuggestionSheet(collection: collection)
                }
            }
        }
    }

    private func loadNotifications() async {
        guard let did = authManager.did else { return }
        isLoading = true
        do {
            let fetched = try await APIClient.shared.getNotifications(did: did)
            let merged = await NotificationCache.shared.merge(existing: notifications, incoming: fetched)
            notifications = merged
            await NotificationCache.shared.save(merged)
        } catch {
            print("loadNotifications error: \(error)")
        }
        isLoading = false
    }
}

// MARK: - Filter Chip

struct FilterChip: View {
    let label: String
    let isSelected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(label)
                .font(.subheadline)
                .fontWeight(isSelected ? .semibold : .regular)
                .padding(.horizontal, 14)
                .padding(.vertical, 6)
                .background(isSelected ? Color.accentColor : Color("SecondaryText").opacity(0.15))
                .foregroundStyle(isSelected ? .white : Color("SecondaryText"))
                .clipShape(Capsule())
        }
    }
}

// MARK: - Notification Row

struct NotificationRow: View {
    let notification: CourierNotification

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            // App favicon
            if let favicon = notification.appFavicon,
               !favicon.isEmpty,
               let url = URL(string: favicon) {
                AsyncImage(url: url) { image in
                    image.resizable().scaledToFit()
                } placeholder: {
                    Image(systemName: "app.fill")
                        .resizable()
                        .foregroundStyle(.secondary)
                }
                .frame(width: 40, height: 40)
                .clipShape(RoundedRectangle(cornerRadius: 8))
            } else {
                // Colored monogram fallback
                ZStack {
                    RoundedRectangle(cornerRadius: 8)
                        .fill(monogramColor)
                    Text(appInitial)
                        .font(.headline.bold())
                        .foregroundStyle(.white)
                }
                .frame(width: 40, height: 40)
            }

            VStack(alignment: .leading, spacing: 3) {
                (
                    Text(Image(systemName: notification.type.iconName)).foregroundColor(iconColor).font(.caption) +
                    Text(" ") +
                    Text(notification.displayName).bold() +
                    Text(" " + notification.actionText).foregroundColor(.secondary)
                )
                    .font(.subheadline)
                    .lineLimit(2)

                if let preview = notification.subjectText, !preview.isEmpty {
                    Text(preview)
                        .font(.caption)
                        .foregroundStyle(Color("SecondaryText"))
                        .lineLimit(1)
                }
            }

            Spacer()
        }
        .padding(.vertical, 2)
    }

    private var appInitial: String {
        String((notification.appName ?? notification.collectionShortName).prefix(1)).uppercased()
    }

    private var monogramColor: Color {
        let colors: [Color] = [.purple, .green, .blue, .orange, .pink, .teal, .indigo, .mint, .cyan, .brown]
        let name = notification.appName ?? notification.collection
        let hash = name.unicodeScalars.reduce(0) { $0 &+ Int($1.value) }
        return colors[abs(hash) % colors.count]
    }

    private var iconColor: Color {
        switch notification.type {
        case .like: return .pink
        case .favorite: return .yellow
        case .reply: return .blue
        case .repost: return .green
        case .follow: return .purple
        case .mention: return .orange
        case .quote: return .indigo
        case .star: return .yellow
        case .issue: return .green
        case .pullRequest: return .purple
        case .rsvp: return .teal
        case .subscription: return .mint
        case .reaction: return .orange
        case .play: return .cyan
        case .recommend: return .green
        case .vote: return .indigo
        case .blogPost: return .blue
        case .generic: return .gray
        case .unknown: return .gray
        }
    }
}
