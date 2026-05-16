import Foundation

enum NotificationType: String, Codable, CaseIterable {
    case like
    case favorite
    case reply
    case repost
    case follow
    case mention
    case quote
    case star
    case issue
    case pullRequest
    case rsvp
    case subscription
    case reaction
    case play
    case recommend
    case vote
    case blogPost
    case generic
    case unknown

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = NotificationType(rawValue: raw) ?? .unknown
    }

    var displayName: String {
        switch self {
        case .like: return "Likes"
        case .favorite: return "Favorites"
        case .reply: return "Replies"
        case .repost: return "Reposts"
        case .follow: return "Follows"
        case .mention: return "Mentions"
        case .quote: return "Quotes"
        case .star: return "Stars"
        case .issue: return "Issues"
        case .pullRequest: return "Pull Requests"
        case .rsvp: return "RSVPs"
        case .subscription: return "Subscriptions"
        case .reaction: return "Reactions"
        case .play: return "Plays"
        case .recommend: return "Recommends"
        case .vote: return "Votes"
        case .blogPost: return "New Posts"
        case .generic: return "Other"
        case .unknown: return "Other"
        }
    }

    var iconName: String {
        switch self {
        case .like: return "heart.fill"
        case .favorite: return "star.fill"
        case .reply: return "arrowshape.turn.up.left.fill"
        case .repost: return "arrow.2.squarepath"
        case .follow: return "person.fill.badge.plus"
        case .mention: return "at"
        case .quote: return "quote.opening"
        case .star: return "star.fill"
        case .issue: return "exclamationmark.circle.fill"
        case .pullRequest: return "arrow.triangle.merge"
        case .rsvp: return "calendar.badge.checkmark"
        case .subscription: return "envelope.fill"
        case .reaction: return "face.smiling.fill"
        case .play: return "play.circle.fill"
        case .recommend: return "hand.thumbsup.fill"
        case .vote: return "chart.bar.fill"
        case .blogPost: return "doc.text.fill"
        case .generic: return "bell.fill"
        case .unknown: return "bell.fill"
        }
    }
}
