import Foundation
import CryptoKit

@MainActor
final class AuthManager: ObservableObject {
    static let shared = AuthManager()

    @Published var isAuthenticated = false
    @Published var did: String?
    @Published var handle: String?

    private let defaults = UserDefaults.standard
    private let oauthService = OAuthService.shared

    private init() {
        did = defaults.string(forKey: "courier.did")
        handle = defaults.string(forKey: "courier.handle")
        isAuthenticated = did != nil
    }

    func authenticate(handleOrDID: String) async throws {
        let inputHandle = handleOrDID.starts(with: "did:") ? nil : handleOrDID

        // OAuth: proves DID ownership via ATProto OAuth flow
        let authenticatedDID = try await oauthService.authenticate(handleOrDID: handleOrDID)

        // Resolve handle if we started with a DID
        let resolvedHandle = inputHandle ?? authenticatedDID

        // Register with backend
        if let token = PushManager.shared.deviceToken {
            _ = try await APIClient.shared.register(
                handle: resolvedHandle,
                did: authenticatedDID,
                deviceToken: token
            )
        }

        self.did = authenticatedDID
        self.handle = resolvedHandle
        self.isAuthenticated = true

        defaults.set(authenticatedDID, forKey: "courier.did")
        defaults.set(resolvedHandle, forKey: "courier.handle")
    }

    func signOut() async {
        if let did {
            try? await APIClient.shared.unregister(did: did)
        }
        self.did = nil
        self.handle = nil
        self.isAuthenticated = false
        defaults.removeObject(forKey: "courier.did")
        defaults.removeObject(forKey: "courier.handle")
    }
}
