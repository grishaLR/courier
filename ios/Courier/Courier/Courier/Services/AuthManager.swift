import Foundation

@MainActor
final class AuthManager: ObservableObject {
    static let shared = AuthManager()

    @Published var isAuthenticated = false
    @Published var did: String?
    @Published var handle: String?

    private(set) var sessionToken: String?
    private let defaults = UserDefaults.standard
    private let oauthService = OAuthService.shared
    private let tokenKeychainKey = "courier.sessionToken"

    private init() {
        did = defaults.string(forKey: "courier.did")
        handle = defaults.string(forKey: "courier.handle")
        sessionToken = KeychainHelper.load(forKey: "courier.sessionToken")
        isAuthenticated = did != nil && sessionToken != nil

        // Sync token to APIClient
        if let token = sessionToken {
            Task { await APIClient.shared.setSessionToken(token) }
        }
    }

    func authenticate(handleOrDID: String) async throws {
        let inputHandle = handleOrDID.starts(with: "did:") ? nil : handleOrDID

        // Server-side OAuth: returns session token + authenticated DID
        let result = try await oauthService.authenticate(handleOrDID: handleOrDID)

        let resolvedHandle = inputHandle ?? result.did

        // Store session
        self.sessionToken = result.sessionToken
        self.did = result.did
        self.handle = resolvedHandle
        self.isAuthenticated = true

        defaults.set(result.did, forKey: "courier.did")
        defaults.set(resolvedHandle, forKey: "courier.handle")
        KeychainHelper.save(result.sessionToken, forKey: tokenKeychainKey)

        await APIClient.shared.setSessionToken(result.sessionToken)

        // Register device token with backend (now authenticated)
        if let token = PushManager.shared.deviceToken {
            _ = try await APIClient.shared.register(
                handle: resolvedHandle,
                did: result.did,
                deviceToken: token
            )
        }
    }

    func signOut() async {
        if let did {
            try? await APIClient.shared.unregister(did: did)
        }
        self.did = nil
        self.handle = nil
        self.sessionToken = nil
        self.isAuthenticated = false
        defaults.removeObject(forKey: "courier.did")
        defaults.removeObject(forKey: "courier.handle")
        KeychainHelper.delete(forKey: tokenKeychainKey)
        await NotificationCache.shared.clear()
        await APIClient.shared.setSessionToken(nil)
    }
}
