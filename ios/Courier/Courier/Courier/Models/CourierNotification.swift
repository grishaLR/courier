import Foundation

struct CourierNotification: Codable, Identifiable, Equatable {
    let type: NotificationType
    let fromDid: String
    let forDid: String
    let collection: String
    let uri: String
    let subjectUri: String?
    let deepLink: String?
    let fromHandle: String?
    let fromName: String?
    let fromAvatar: String?
    let appName: String?
    let appFavicon: String?
    let createdAt: String

    var id: String { uri + createdAt }

    var displayName: String {
        fromName ?? fromHandle ?? fromDid
    }

    // appName is now provided by the server based on user's preferred app choice

    /// Extracts a readable name from the collection (e.g., "community.lexicon.calendar.rsvp" → "calendar.rsvp")
    var collectionShortName: String {
        let parts = collection.split(separator: ".")
        if parts.count >= 2 {
            return parts.suffix(2).joined(separator: ".")
        }
        return collection
    }

    private var isBluesky: Bool {
        collection.hasPrefix("app.bsky.")
    }

    private var context: String {
        if let appName, !isBluesky {
            return " on \(appName)"
        }
        return ""
    }

    var body: String {
        switch type {
        case .like: return "\(displayName) liked your post\(context)"
        case .reply: return "\(displayName) replied to you\(context)"
        case .repost: return "\(displayName) reposted your post\(context)"
        case .follow: return "\(displayName) followed you\(context)"
        case .mention: return "\(displayName) mentioned you\(context)"
        case .quote: return "\(displayName) quoted your post\(context)"
        case .generic: return "\(displayName) via \(appName ?? collectionShortName)"
        }
    }
}
