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
    let subjectText: String?
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

    private var context: String {
        guard let appName else { return "" }
        // Show "on AppName" unless it's the default Bluesky client
        if collection.hasPrefix("app.bsky.") && appName == "Bluesky" {
            return ""
        }
        return " on \(appName)"
    }

    /// Infer the content noun from the subject URI's collection
    private var subjectNoun: String {
        guard let uri = subjectUri else { return "post" }
        // Specific overrides for compound or non-obvious nouns
        if uri.contains("repo.issue") { return "issue" }
        if uri.contains("repo.pull") { return "pull request" }
        if uri.contains("calendar.event") { return "event" }
        if uri.contains("document") || uri.contains("blog") || uri.contains("entry") { return "post" }

        // Extract the last segment of the collection as the noun
        // e.g., "at://did/social.arabica.alpha.recipe/rkey" → "recipe"
        if uri.hasPrefix("at://") {
            let stripped = String(uri.dropFirst(5)) // remove "at://"
            let parts = stripped.split(separator: "/", maxSplits: 2)
            if parts.count >= 2 {
                let collection = String(parts[1])
                let segments = collection.split(separator: ".")
                let noun = String(segments.last ?? "post")
                let generic: Set<String> = ["feed", "graph", "interactions", "app", "social", "alpha", "dev"]
                if generic.contains(noun) { return "post" }
                return noun
            }
        }
        return "post"
    }

    /// Action text without the user's name (name is shown separately in the row)
    var actionText: String {
        switch type {
        case .like: return "liked your \(subjectNoun)\(context)"
        case .favorite: return "favorited your \(subjectNoun)\(context)"
        case .reply: return "replied to you\(context)"
        case .repost: return "reposted your \(subjectNoun)\(context)"
        case .follow: return "followed you\(context)"
        case .mention: return "mentioned you\(context)"
        case .quote: return "quoted your post\(context)"
        case .star: return "starred your repo\(context)"
        case .issue: return "opened an issue\(context)"
        case .pullRequest: return "opened a pull request\(context)"
        case .rsvp:
            // subjectText may start with "going", "interested", or "not going"
            if let st = subjectText {
                if st.hasPrefix("going") { return "is going to your event\(context)" }
                if st.hasPrefix("interested") { return "is interested in your event\(context)" }
                if st.hasPrefix("not going") { return "is not going to your event\(context)" }
            }
            return "RSVPed to your event\(context)"
        case .subscription: return "subscribed to your publication\(context)"
        case .reaction: return "reacted to your post\(context)"
        case .play: return "played your track\(context)"
        case .recommend: return "recommended your post\(context)"
        case .vote: return "voted on your poll\(context)"
        case .blogPost:
            if let app = appName, !app.isEmpty {
                return "published a new post on \(app)"
            }
            return "published a new post\(context)"
        case .generic: return "via \(appName ?? collectionShortName)"
        }
    }

    var body: String {
        "\(displayName) \(actionText)"
    }
}
