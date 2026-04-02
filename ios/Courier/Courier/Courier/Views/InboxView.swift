import SwiftUI

struct InboxView: View {
    @EnvironmentObject var authManager: AuthManager
    @StateObject private var stream = NotificationStream()

    @State private var notifications: [CourierNotification] = []
    @State private var isLoading = false
    @State private var suggestionCollection: String?

    var body: some View {
        NavigationStack {
            Group {
                if isLoading && notifications.isEmpty {
                    ProgressView()
                } else if notifications.isEmpty {
                    ContentUnavailableView(
                        "No Notifications",
                        systemImage: "bell.slash",
                        description: Text("Notifications will appear here when someone interacts with your posts.")
                    )
                } else {
                    List(notifications) { notif in
                        NotificationRow(notification: notif)
                            .onTapGesture {
                                if let deepLink = notif.deepLink, let url = URL(string: deepLink) {
                                    UIApplication.shared.open(url)
                                } else {
                                    let linkURI = notif.subjectUri ?? notif.uri
                                    DeepLinkHandler.open(atURI: linkURI)
                                }
                                // Prompt for unknown apps
                                if notif.type == .generic && notif.appName == nil {
                                    suggestionCollection = notif.collection
                                }
                            }
                    }
                    .refreshable {
                        await loadNotifications()
                    }
                }
            }
            .navigationTitle("Inbox")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        Task { await authManager.signOut() }
                    } label: {
                        Image(systemName: "rectangle.portrait.and.arrow.right")
                    }
                }
            }
            .task {
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
                    if notifications.count > 50 {
                        notifications = Array(notifications.prefix(50))
                    }
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
            notifications = try await APIClient.shared.getNotifications(did: did)
        } catch {
        }
        isLoading = false
    }
}

struct NotificationRow: View {
    let notification: CourierNotification

    var body: some View {
        HStack(spacing: 12) {
            AsyncImage(url: notification.fromAvatar.flatMap { URL(string: $0) }) { image in
                image.resizable().scaledToFill()
            } placeholder: {
                Image(systemName: "person.circle.fill")
                    .resizable()
                    .foregroundStyle(.secondary)
            }
            .frame(width: 44, height: 44)
            .clipShape(Circle())

            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Image(systemName: notification.type.iconName)
                        .font(.caption)
                        .foregroundStyle(iconColor)

                    Text(notification.displayName)
                        .font(.subheadline.bold())
                        .lineLimit(1)
                }

                Text(notification.body)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)

                if let app = notification.appName {
                    HStack(spacing: 4) {
                        if let favicon = notification.appFavicon,
                           !favicon.isEmpty,
                           let url = URL(string: favicon) {
                            AsyncImage(url: url) { image in
                                image.resizable().scaledToFit()
                            } placeholder: {
                                EmptyView()
                            }
                            .frame(width: 12, height: 12)
                            .clipShape(RoundedRectangle(cornerRadius: 2))
                        }
                        Text(app)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }

            Spacer()
        }
        .padding(.vertical, 4)
    }

    private var iconColor: Color {
        switch notification.type {
        case .like: return .pink
        case .reply: return .blue
        case .repost: return .green
        case .follow: return .purple
        case .mention: return .orange
        case .quote: return .indigo
        case .generic: return .gray
        }
    }
}
