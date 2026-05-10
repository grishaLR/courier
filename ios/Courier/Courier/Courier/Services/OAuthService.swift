import Foundation
import AuthenticationServices

/// OAuth service that delegates the ATProto OAuth flow to Courier's server.
/// The server handles DPoP/PAR/PKCE and returns a session token via custom URL scheme.
@MainActor
final class OAuthService: ObservableObject {
    static let shared = OAuthService()

    @Published var isAuthenticating = false

    private init() {}

    struct AuthResult {
        let did: String
        let sessionToken: String
    }

    /// Authenticate via Courier's server-side OAuth flow.
    /// 1. POST /auth/oauth/start with handle + mobile flag
    /// 2. Server does PAR/PKCE/DPoP, returns authorization URL
    /// 3. Open browser for user to authorize
    /// 4. Server callback redirects to social.courier:/auth/callback?session=<token>&did=<did>
    /// 5. App receives session token
    func authenticate(handleOrDID: String) async throws -> AuthResult {
        isAuthenticating = true
        defer { isAuthenticating = false }

        // Step 1: Ask server to start OAuth flow
        let startResponse = try await startOAuthFlow(handleOrDID: handleOrDID)

        // Step 2: Open browser for authorization
        let callbackURL = try await openAuthorizationBrowser(url: startResponse.authorizationURL)

        // Step 3: Extract session token and DID from callback
        guard let components = URLComponents(url: callbackURL, resolvingAgainstBaseURL: false),
              let sessionToken = components.queryItems?.first(where: { $0.name == "session" })?.value,
              let did = components.queryItems?.first(where: { $0.name == "did" })?.value else {
            throw OAuthError.noSessionInCallback
        }

        return AuthResult(did: did, sessionToken: sessionToken)
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
            ) { callbackURL, error in
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
