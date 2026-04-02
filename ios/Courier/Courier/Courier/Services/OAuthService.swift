import Foundation
import AuthenticationServices

/// ATProto OAuth service using OAuthenticator for DPoP/PAR/PKCE.
/// This handles the full ATProto OAuth dance for DID ownership proof.
@MainActor
final class OAuthService: ObservableObject {
    static let shared = OAuthService()

    @Published var isAuthenticating = false

    private let clientMetadataURL = "https://courier.social/oauth-client-metadata.json"
    private let redirectURI = "social.courier:/auth/callback"

    private init() {}

    /// Authenticate a user by their handle or DID.
    /// Returns the authenticated DID on success.
    func authenticate(handleOrDID: String) async throws -> String {
        isAuthenticating = true
        defer { isAuthenticating = false }

        // Step 1: Resolve handle → DID → PDS
        let did: String
        let pdsURL: String

        if handleOrDID.starts(with: "did:") {
            did = handleOrDID
            pdsURL = try await resolvePDS(did: did)
        } else {
            did = try await resolveHandle(handleOrDID)
            pdsURL = try await resolvePDS(did: did)
        }

        // Step 2: Fetch authorization server metadata
        let authServer = try await fetchAuthServerMetadata(pdsURL: pdsURL)

        // Step 3: Generate PKCE
        let codeVerifier = generateCodeVerifier()
        let codeChallenge = generateCodeChallenge(verifier: codeVerifier)

        // Step 4: Generate DPoP keypair
        let dpopKey = P256.Signing.PrivateKey()
        let dpopJWK = dpopKey.publicKey.jwkRepresentation

        // Step 5: PAR request
        let requestURI = try await pushAuthorizationRequest(
            authServer: authServer,
            codeChallenge: codeChallenge,
            dpopKey: dpopKey,
            dpopJWK: dpopJWK,
            loginHint: handleOrDID
        )

        // Step 6: Open browser for authorization
        let callbackURL = try await openAuthorizationBrowser(
            authServer: authServer,
            requestURI: requestURI
        )

        // Step 7: Extract auth code from callback
        guard let code = extractCode(from: callbackURL) else {
            throw OAuthError.noAuthCode
        }

        // Step 8: Exchange code for token (proves DID ownership)
        let tokenResponse = try await exchangeCodeForToken(
            authServer: authServer,
            code: code,
            codeVerifier: codeVerifier,
            dpopKey: dpopKey,
            dpopJWK: dpopJWK
        )

        // The sub field contains the authenticated DID
        guard let authenticatedDID = tokenResponse.sub, !authenticatedDID.isEmpty else {
            throw OAuthError.noDIDInToken
        }

        return authenticatedDID
    }

    // MARK: - Discovery

    private func resolveHandle(_ handle: String) async throws -> String {
        let clean = handle.hasPrefix("@") ? String(handle.dropFirst()) : handle
        let url = URL(string: "https://public.api.bsky.app/xrpc/com.atproto.identity.resolveHandle?handle=\(clean)")!
        let (data, _) = try await URLSession.shared.data(from: url)
        let result = try JSONDecoder().decode(ResolveResponse.self, from: data)
        return result.did
    }

    private func resolvePDS(did: String) async throws -> String {
        let url: URL
        if did.starts(with: "did:plc:") {
            url = URL(string: "https://plc.directory/\(did)")!
        } else {
            throw OAuthError.unsupportedDIDMethod
        }

        let (data, _) = try await URLSession.shared.data(from: url)
        let doc = try JSONDecoder().decode(DIDDocument.self, from: data)

        guard let pds = doc.service.first(where: { $0.type == "AtprotoPersonalDataServer" })?.serviceEndpoint else {
            throw OAuthError.noPDS
        }
        return pds
    }

    private func fetchAuthServerMetadata(pdsURL: String) async throws -> AuthServerMetadata {
        let url = URL(string: "\(pdsURL)/.well-known/oauth-authorization-server")!
        let (data, _) = try await URLSession.shared.data(from: url)
        return try JSONDecoder().decode(AuthServerMetadata.self, from: data)
    }

    // MARK: - PKCE

    private func generateCodeVerifier() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return Data(bytes).base64URLEncoded
    }

    private func generateCodeChallenge(verifier: String) -> String {
        let data = Data(verifier.utf8)
        let hash = SHA256.hash(data: data)
        return Data(hash).base64URLEncoded
    }

    // MARK: - DPoP

    private func createDPoPJWT(key: P256.Signing.PrivateKey, jwk: [String: Any], method: String, url: String, nonce: String? = nil, accessToken: String? = nil) throws -> String {
        let header: [String: Any] = [
            "alg": "ES256",
            "typ": "dpop+jwt",
            "jwk": jwk
        ]

        var payload: [String: Any] = [
            "jti": UUID().uuidString,
            "htm": method,
            "htu": url,
            "iat": Int(Date().timeIntervalSince1970)
        ]

        if let nonce {
            payload["nonce"] = nonce
        }

        if let accessToken {
            let hash = SHA256.hash(data: Data(accessToken.utf8))
            payload["ath"] = Data(hash).base64URLEncoded
        }

        let headerData = try JSONSerialization.data(withJSONObject: header)
        let payloadData = try JSONSerialization.data(withJSONObject: payload)

        let signingInput = "\(headerData.base64URLEncoded).\(payloadData.base64URLEncoded)"
        let signature = try key.signature(for: Data(signingInput.utf8))

        return "\(signingInput).\(signature.rawRepresentation.base64URLEncoded)"
    }

    // MARK: - PAR

    private func pushAuthorizationRequest(authServer: AuthServerMetadata, codeChallenge: String, dpopKey: P256.Signing.PrivateKey, dpopJWK: [String: Any], loginHint: String) async throws -> String {
        guard let parEndpoint = authServer.pushed_authorization_request_endpoint else {
            throw OAuthError.noPAREndpoint
        }

        let dpopJWT = try createDPoPJWT(key: dpopKey, jwk: dpopJWK, method: "POST", url: parEndpoint)

        let thumbprint = dpopJWKThumbprint(jwk: dpopJWK)

        let body = [
            "client_id": clientMetadataURL,
            "response_type": "code",
            "redirect_uri": redirectURI,
            "scope": "atproto",
            "state": UUID().uuidString,
            "code_challenge": codeChallenge,
            "code_challenge_method": "S256",
            "login_hint": loginHint,
            "dpop_jkt": thumbprint
        ].map { "\($0.key)=\($0.value.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? $0.value)" }
            .joined(separator: "&")

        var request = URLRequest(url: URL(string: parEndpoint)!)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.setValue(dpopJWT, forHTTPHeaderField: "DPoP")
        request.httpBody = body.data(using: .utf8)

        let (data, response) = try await URLSession.shared.data(for: request)
        let httpResp = response as? HTTPURLResponse

        // Handle DPoP nonce — server may return one we need to use
        if let httpResponse = httpResp,
           httpResponse.statusCode == 400 || httpResponse.statusCode == 401,
           let nonce = httpResponse.value(forHTTPHeaderField: "DPoP-Nonce") {
            let dpopJWT2 = try createDPoPJWT(key: dpopKey, jwk: dpopJWK, method: "POST", url: parEndpoint, nonce: nonce)
            var request2 = request
            request2.setValue(dpopJWT2, forHTTPHeaderField: "DPoP")
            let (data2, resp2) = try await URLSession.shared.data(for: request2)
            let result = try JSONDecoder().decode(PARResponse.self, from: data2)
            return result.request_uri
        }

        let result = try JSONDecoder().decode(PARResponse.self, from: data)
        return result.request_uri
    }

    // MARK: - Browser Auth

    private func openAuthorizationBrowser(authServer: AuthServerMetadata, requestURI: String) async throws -> URL {
        let authURL = URL(string: "\(authServer.authorization_endpoint)?client_id=\(clientMetadataURL.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed)!)&request_uri=\(requestURI.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed)!)")!

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

    // MARK: - Token Exchange

    private func exchangeCodeForToken(authServer: AuthServerMetadata, code: String, codeVerifier: String, dpopKey: P256.Signing.PrivateKey, dpopJWK: [String: Any]) async throws -> TokenResponse {
        let tokenEndpoint = authServer.token_endpoint

        let dpopJWT = try createDPoPJWT(key: dpopKey, jwk: dpopJWK, method: "POST", url: tokenEndpoint)

        let body = [
            "grant_type": "authorization_code",
            "code": code,
            "redirect_uri": redirectURI,
            "client_id": clientMetadataURL,
            "code_verifier": codeVerifier
        ].map { "\($0.key)=\($0.value.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? $0.value)" }
            .joined(separator: "&")

        var request = URLRequest(url: URL(string: tokenEndpoint)!)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.setValue(dpopJWT, forHTTPHeaderField: "DPoP")
        request.httpBody = body.data(using: .utf8)

        let (data, response) = try await URLSession.shared.data(for: request)

        // Handle DPoP nonce retry
        if let httpResponse = response as? HTTPURLResponse,
           httpResponse.statusCode == 400,
           let nonce = httpResponse.value(forHTTPHeaderField: "DPoP-Nonce") {
            let dpopJWT2 = try createDPoPJWT(key: dpopKey, jwk: dpopJWK, method: "POST", url: tokenEndpoint, nonce: nonce)
            var request2 = request
            request2.setValue(dpopJWT2, forHTTPHeaderField: "DPoP")
            let (data2, _) = try await URLSession.shared.data(for: request2)
            return try JSONDecoder().decode(TokenResponse.self, from: data2)
        }

        return try JSONDecoder().decode(TokenResponse.self, from: data)
    }

    // MARK: - Helpers

    private func extractCode(from url: URL) -> String? {
        URLComponents(url: url, resolvingAgainstBaseURL: false)?
            .queryItems?
            .first(where: { $0.name == "code" })?
            .value
    }

    private func dpopJWKThumbprint(jwk: [String: Any]) -> String {
        // Simplified JWK thumbprint (RFC 7638)
        let ordered: [(String, Any)] = [
            ("crv", jwk["crv"] ?? "P-256"),
            ("kty", jwk["kty"] ?? "EC"),
            ("x", jwk["x"] ?? ""),
            ("y", jwk["y"] ?? "")
        ]
        let json = "{\(ordered.map { "\"\($0.0)\":\"\($0.1)\"" }.joined(separator: ","))}"
        let hash = SHA256.hash(data: Data(json.utf8))
        return Data(hash).base64URLEncoded
    }
}

// MARK: - Models

private struct ResolveResponse: Codable { let did: String }

private struct DIDDocument: Codable {
    let service: [DIDService]
}

private struct DIDService: Codable {
    let type: String
    let serviceEndpoint: String
}

private struct AuthServerMetadata: Codable {
    let authorization_endpoint: String
    let token_endpoint: String
    let pushed_authorization_request_endpoint: String?
    let revocation_endpoint: String?
}

private struct PARResponse: Codable {
    let request_uri: String
}

private struct TokenResponse: Codable {
    let access_token: String?
    let token_type: String?
    let sub: String?
    let scope: String?
}

enum OAuthError: LocalizedError {
    case unsupportedDIDMethod
    case noPDS
    case noPAREndpoint
    case noAuthCode
    case noDIDInToken
    case cancelled

    var errorDescription: String? {
        switch self {
        case .unsupportedDIDMethod: return "Unsupported DID method"
        case .noPDS: return "Could not find PDS for this account"
        case .noPAREndpoint: return "Authorization server doesn't support PAR"
        case .noAuthCode: return "No authorization code received"
        case .noDIDInToken: return "Could not verify identity"
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

// MARK: - CryptoKit imports & helpers

import CryptoKit

extension P256.Signing.PublicKey {
    var jwkRepresentation: [String: Any] {
        let rawKey = rawRepresentation
        let x = rawKey.prefix(32)
        let y = rawKey.suffix(32)
        return [
            "kty": "EC",
            "crv": "P-256",
            "x": x.base64URLEncoded,
            "y": y.base64URLEncoded
        ]
    }
}

extension Data {
    var base64URLEncoded: String {
        base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}
