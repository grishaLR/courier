import Foundation

struct Preferences: Codable {
    var likes: Bool
    var replies: Bool
    var reposts: Bool
    var follows: Bool
    var mentions: Bool
    var quotes: Bool
    var generic: Bool

    static let `default` = Preferences(
        likes: true,
        replies: true,
        reposts: true,
        follows: true,
        mentions: true,
        quotes: true,
        generic: false
    )
}
