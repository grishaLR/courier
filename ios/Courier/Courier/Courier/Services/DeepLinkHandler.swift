import UIKit

enum DeepLinkHandler {
    /// Known ATProto app URL patterns.
    /// Maps collection prefix → URL builder. The `identifier` param is a handle when resolved, DID otherwise.
    private static let appRegistry: [(prefix: String, buildURL: (String, String) -> String)] = [
        // Bluesky
        ("app.bsky.feed.post", { id, rkey in
            "https://bsky.app/profile/\(id)/post/\(rkey)"
        }),
        ("app.bsky.feed.like", { id, _ in
            "https://bsky.app/profile/\(id)"
        }),
        ("app.bsky.feed.repost", { id, _ in
            "https://bsky.app/profile/\(id)"
        }),
        ("app.bsky.graph.follow", { id, _ in
            "https://bsky.app/profile/\(id)"
        }),
        // Atmo (events/RSVP)
        ("community.lexicon.calendar", { id, rkey in
            "https://atmo.rsvp/p/\(id)/e/\(rkey)"
        }),
        // WhiteWind (blog)
        ("com.whtwnd.blog", { id, rkey in
            "https://whtwnd.com/\(id)/\(rkey)"
        }),
        // Frontpage (links)
        ("fyi.unravel.frontpage", { _, _ in
            "https://frontpage.fyi"
        }),
        // Tangled (code collaboration)
        ("sh.tangled", { id, _ in
            "https://tangled.org/\(id)"
        }),
        // Picosky
        ("blue.pico", { _, _ in
            "https://pico.blue"
        }),
        // Smoke Signal (events)
        ("events.smokesignal", { id, rkey in
            "https://smokesignal.events/\(id)/\(rkey)"
        }),
    ]

    static func open(atURI: String) {
        guard let parsed = parseATURI(atURI) else { return }

        Task {
            // Resolve DID to handle for better URLs
            let identifier: String
            if parsed.did.starts(with: "did:") {
                identifier = (try? await resolveHandle(did: parsed.did)) ?? parsed.did
            } else {
                identifier = parsed.did
            }

            var urlString: String?
            for (prefix, buildURL) in appRegistry {
                if parsed.collection.hasPrefix(prefix) {
                    urlString = buildURL(identifier, parsed.rkey)
                    break
                }
            }

            if urlString == nil {
                urlString = "https://bsky.app/profile/\(identifier)"
            }

            guard let urlString, let url = URL(string: urlString) else { return }
            await MainActor.run {
                UIApplication.shared.open(url)
            }
        }
    }

    private static func resolveHandle(did: String) async throws -> String {
        let url = URL(string: "https://public.api.bsky.app/xrpc/app.bsky.actor.getProfile?actor=\(did)")!
        let (data, _) = try await URLSession.shared.data(from: url)
        let result = try JSONDecoder().decode(ProfileResponse.self, from: data)
        return result.handle
    }

    private struct ProfileResponse: Codable {
        let handle: String
    }

    struct ATURI {
        let did: String
        let collection: String
        let rkey: String
    }

    static func parseATURI(_ uri: String) -> ATURI? {
        let stripped = uri.replacingOccurrences(of: "at://", with: "")
        let parts = stripped.split(separator: "/", maxSplits: 2)
        guard parts.count == 3 else { return nil }
        return ATURI(
            did: String(parts[0]),
            collection: String(parts[1]),
            rkey: String(parts[2])
        )
    }
}
