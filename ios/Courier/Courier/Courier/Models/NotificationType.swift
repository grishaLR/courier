import Foundation

enum NotificationType: String, Codable, CaseIterable {
    case like
    case reply
    case repost
    case follow
    case mention
    case quote
    case generic

    var displayName: String {
        switch self {
        case .like: return "Likes"
        case .reply: return "Replies"
        case .repost: return "Reposts"
        case .follow: return "Follows"
        case .mention: return "Mentions"
        case .quote: return "Quotes"
        case .generic: return "Other"
        }
    }

    var iconName: String {
        switch self {
        case .like: return "heart.fill"
        case .reply: return "arrowshape.turn.up.left.fill"
        case .repost: return "arrow.2.squarepath"
        case .follow: return "person.fill.badge.plus"
        case .mention: return "at"
        case .quote: return "quote.opening"
        case .generic: return "bell.fill"
        }
    }
}
