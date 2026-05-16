import Foundation
import AuthenticationServices

/// OAuth service that delegates the ATProto OAuth flow to Courier's server.
/// The server handles DPoP/PAR/PKCE and returns a session token via custom URL scheme.
@MainActor
final class OAuthService: ObservableObject {
    static let shared = OAuthService()

    @Published var isAuthenticating = false

    private var activeSession: ASWebAuthenticationSession?

    private init() {}

    struct AuthResult {
        let did: String
        let sessionToken: String
    }

    /// Authenticate via Courier's mobile OAuth flow.
    /// 1. POST /auth/oauth/start — server does PAR/PKCE/DPoP, returns authorization URL
    /// 2. Open browser; PDS redirects to social.courier:/auth/callback?code=...&state=...
    /// 3. App intercepts callback, POSTs code+state to /auth/oauth/exchange
    /// 4. Server completes token exchange and returns session token
    func authenticate(handleOrDID: String) async throws -> AuthResult {
        isAuthenticating = true
        defer { isAuthenticating = false }

        let startResponse = try await startOAuthFlow(handleOrDID: handleOrDID)
        let callbackURL = try await openAuthorizationBrowser(url: startResponse.authorizationURL)

        guard let components = URLComponents(url: callbackURL, resolvingAgainstBaseURL: false) else {
            throw OAuthError.noSessionInCallback
        }
        let items = components.queryItems ?? []

        // New flow: PDS redirects code+state directly to the app
        if let code = items.first(where: { $0.name == "code" })?.value,
           let state = items.first(where: { $0.name == "state" })?.value {
            return try await exchangeCode(code: code, state: state)
        }

        // Legacy fallback: server-side callback already exchanged the token
        // TODO: remove this 7 days after release it will be moot. today 05/15/2026
        if let sessionToken = items.first(where: { $0.name == "session" })?.value,
           let did = items.first(where: { $0.name == "did" })?.value {
            return AuthResult(did: did, sessionToken: sessionToken)
        }

        throw OAuthError.noSessionInCallback
    }

    private func exchangeCode(code: String, state: String) async throws -> AuthResult {
        let url = APIClient.shared.baseURLValue.appendingPathComponent("auth/oauth/exchange")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: ["code": code, "state": state])

        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse, (200...299).contains(http.statusCode) else {
            throw OAuthError.serverError((response as? HTTPURLResponse)?.statusCode ?? 0)
        }

        struct ExchangeResponse: Decodable {
            let sessionToken: String
            let did: String
        }
        let result = try JSONDecoder().decode(ExchangeResponse.self, from: data)
        return AuthResult(did: result.did, sessionToken: result.sessionToken)
    }

    // MARK: - Private

    private struct StartResponse {
        let authorizationURL: String
        let state: String
    }

    private func startOAuthFlow(handleOrDID: String) async throws -> StartResponse {
        let url = APIClient.shared.baseURLValue.appendingPathComponent("auth/oauth/start")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        let body: [String: Any] = ["handle": handleOrDID, "mobile": true]
        request.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, response) = try await URLSession.shared.data(for: request)

        guard let http = response as? HTTPURLResponse, (200...299).contains(http.statusCode) else {
            let http = response as? HTTPURLResponse
            throw OAuthError.serverError(http?.statusCode ?? 0)
        }

        let result = try JSONDecoder().decode(StartResponseJSON.self, from: data)
        return StartResponse(authorizationURL: result.authorizationURL, state: result.state)
    }

    private func openAuthorizationBrowser(url authURLString: String) async throws -> URL {
        guard let authURL = URL(string: authURLString) else {
            throw OAuthError.invalidURL
        }

        return try await withCheckedThrowingContinuation { continuation in
            let session = ASWebAuthenticationSession(
                url: authURL,
                callbackURLScheme: "social.courier"
            ) { [weak self] callbackURL, error in
                self?.activeSession = nil
                if let error {
                    continuation.resume(throwing: error)
                } else if let callbackURL {
                    continuation.resume(returning: callbackURL)
                } else {
                    continuation.resume(throwing: OAuthError.cancelled)
                }
            }
            session.prefersEphemeralWebBrowserSession = false
            session.presentationContextProvider = ASWebAuthPresentationContext.shared
            activeSession = session
            session.start()
        }
    }
}

// MARK: - Models

private struct StartResponseJSON: Codable {
    let authorizationURL: String
    let state: String
}

enum OAuthError: LocalizedError {
    case serverError(Int)
    case invalidURL
    case noSessionInCallback
    case cancelled

    var errorDescription: String? {
        switch self {
        case .serverError(let code): return "Server error (\(code))"
        case .invalidURL: return "Invalid authorization URL"
        case .noSessionInCallback: return "No session token received"
        case .cancelled: return "Authentication cancelled"
        }
    }
}

// MARK: - ASWebAuthenticationSession Presentation

class ASWebAuthPresentationContext: NSObject, ASWebAuthenticationPresentationContextProviding {
    static let shared = ASWebAuthPresentationContext()
    func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .first?.windows.first { $0.isKeyWindow } ?? ASPresentationAnchor()
    }
}
