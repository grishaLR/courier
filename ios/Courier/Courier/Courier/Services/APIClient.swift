import Foundation

actor APIClient {
    static let shared = APIClient()

    let baseURLValue: URL
    private let session: URLSession

    private var baseURL: URL { baseURLValue }

    private init() {
        #if DEBUG
        self.baseURLValue = URL(string: "http://localhost:8080")!
        #else
        self.baseURLValue = URL(string: "https://api.courier.social")!
        #endif
        self.session = URLSession.shared
    }

    // MARK: - Auth

    struct ChallengeResponse: Codable {
        let challenge: String
    }

    struct VerifyResponse: Codable {
        let did: String
        let verified: Bool
    }

    func requestChallenge(did: String) async throws -> String {
        let body = ["did": did]
        let resp: ChallengeResponse = try await post("/auth/challenge", body: body)
        return resp.challenge
    }

    func verifyChallenge(did: String, signature: Data) async throws -> VerifyResponse {
        let body: [String: String] = [
            "did": did,
            "signature": signature.base64EncodedString(),
            "encoding": "base64"
        ]
        return try await post("/auth/verify", body: body)
    }

    // MARK: - Registration

    struct RegisterResponse: Codable {
        let did: String
        let status: String
    }

    func register(handle: String?, did: String?, deviceToken: String, platform: String = "ios", preferences: Preferences? = nil) async throws -> RegisterResponse {
        var body: [String: Any] = [
            "deviceToken": deviceToken,
            "platform": platform
        ]
        if let handle { body["handle"] = handle }
        if let did { body["did"] = did }
        if let preferences {
            let data = try JSONEncoder().encode(preferences)
            body["preferences"] = try JSONSerialization.jsonObject(with: data)
        }
        return try await post("/register", body: body)
    }

    func updatePreferences(did: String, preferences: Preferences) async throws {
        var req = try makeRequest("/preferences", method: "PUT")
        req.addValue(did, forHTTPHeaderField: "X-DID")
        req.httpBody = try JSONEncoder().encode(preferences)
        let (_, response) = try await session.data(for: req)
        try checkResponse(response)
    }

    func updateDeviceToken(token: String) async throws {
        var req = try makeRequest("/device-token", method: "PUT")
        req.httpBody = try JSONSerialization.data(withJSONObject: ["deviceToken": token])
        let (_, response) = try await session.data(for: req)
        try checkResponse(response)
    }

    func unregister(did: String) async throws {
        var req = try makeRequest("/unregister", method: "DELETE")
        req.addValue(did, forHTTPHeaderField: "X-DID")
        let (_, response) = try await session.data(for: req)
        try checkResponse(response)
    }

    // MARK: - Notifications

    func getNotifications(did: String) async throws -> [CourierNotification] {
        return try await get("/notifications/\(did)")
    }

    func clearNotifications() async throws {
        let req = try makeRequest("/notifications", method: "DELETE")
        let (_, response) = try await session.data(for: req)
        try checkResponse(response)
    }

    func deleteNotification(uri: String) async throws {
        var req = try makeRequest("/notifications/delete", method: "POST")
        req.httpBody = try JSONSerialization.data(withJSONObject: ["uri": uri])
        let (_, response) = try await session.data(for: req)
        try checkResponse(response)
    }

    // MARK: - App Registry

    struct SuggestResponse: Codable {
        let status: String
    }

    func suggestApp(collection: String, appName: String, appURL: String) async throws {
        let body: [String: String] = [
            "collection": collection,
            "appName": appName,
            "appURL": appURL
        ]
        let _: SuggestResponse = try await post("/apps/suggest", body: body)
    }

    // MARK: - Preferred Apps

    func getAlternatives(prefix: String) async throws -> [AppInfo] {
        return try await get("/catalog/alternatives?prefix=\(prefix)")
    }

    func getPreferredApps(did: String) async throws -> [String: String] {
        return try await get("/catalog/user/preferred?did=\(did)")
    }

    func setPreferredApps(did: String, prefs: [String: String]) async throws {
        var req = try makeRequest("/catalog/user/preferred", method: "PUT")
        req.addValue(did, forHTTPHeaderField: "X-DID")
        req.httpBody = try JSONSerialization.data(withJSONObject: prefs)
        let (_, response) = try await session.data(for: req)
        try checkResponse(response)
    }

    // MARK: - Blog Subscriptions

    struct BlogSub: Codable, Identifiable {
        let publicationUri: String
        let authorDid: String
        let blogName: String
        let platform: String
        let webUrl: String?
        let iconUrl: String?
        var enabled: Bool

        var id: String { publicationUri }
    }

    func getBlogSubs() async throws -> [BlogSub] {
        return try await get("/subscriptions/blogs")
    }

    func setBlogPref(publicationUri: String, enabled: Bool) async throws {
        var req = try makeRequest("/subscriptions/blogs", method: "PUT")
        req.httpBody = try JSONSerialization.data(withJSONObject: [
            "publicationUri": publicationUri,
            "enabled": enabled
        ])
        let (_, response) = try await session.data(for: req)
        try checkResponse(response)
    }

    func refreshBlogSubs() async throws -> [BlogSub] {
        var req = try makeRequest("/subscriptions/blogs/refresh", method: "POST")
        req.httpBody = Data("{}".utf8)
        let (data, response) = try await session.data(for: req)
        try checkResponse(response)
        return try JSONDecoder().decode([BlogSub].self, from: data)
    }

    // MARK: - Networking

    func get<T: Decodable>(_ path: String) async throws -> T {
        let req = try makeRequest(path, method: "GET")
        let (data, response) = try await session.data(for: req)
        try checkResponse(response)
        return try JSONDecoder().decode(T.self, from: data)
    }

    private func post<T: Decodable>(_ path: String, body: Any) async throws -> T {
        var req = try makeRequest(path, method: "POST")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, response) = try await session.data(for: req)
        try checkResponse(response)
        return try JSONDecoder().decode(T.self, from: data)
    }

    private var _sessionToken: String?

    func setSessionToken(_ token: String?) {
        _sessionToken = token
    }

    func makeRequest(_ path: String, method: String) throws -> URLRequest {
        guard let url = URL(string: path, relativeTo: baseURL) else {
            throw APIError.invalidURL
        }
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let token = _sessionToken {
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return req
    }

    private func checkResponse(_ response: URLResponse) throws {
        guard let http = response as? HTTPURLResponse else {
            throw APIError.invalidResponse
        }
        guard (200...299).contains(http.statusCode) else {
            throw APIError.httpError(http.statusCode)
        }
    }
}

enum APIError: LocalizedError {
    case invalidURL
    case invalidResponse
    case httpError(Int)

    var errorDescription: String? {
        switch self {
        case .invalidURL: return "Invalid URL"
        case .invalidResponse: return "Invalid response"
        case .httpError(let code): return "HTTP error \(code)"
        }
    }
}
