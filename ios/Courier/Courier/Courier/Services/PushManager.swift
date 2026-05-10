import Foundation
import UIKit
import UserNotifications

@MainActor
final class PushManager: NSObject, ObservableObject {
    static let shared = PushManager()

    @Published var deviceToken: String?
    @Published var permissionGranted = false

    private override init() {
        super.init()
    }

    func requestPermission() async {
        let center = UNUserNotificationCenter.current()
        do {
            let granted = try await center.requestAuthorization(options: [.alert, .badge, .sound])
            permissionGranted = granted
            if granted {
                UIApplication.shared.registerForRemoteNotifications()
            }
        } catch {
        }
    }

    func didRegisterForRemoteNotifications(deviceToken: Data) {
        let token = deviceToken.map { String(format: "%02x", $0) }.joined()
        let oldToken = self.deviceToken
        self.deviceToken = token

        // If token changed and user is logged in, update the backend
        if token != oldToken {
            Task {
                guard UserDefaults.standard.string(forKey: "courier.did") != nil else { return }
                try? await APIClient.shared.updateDeviceToken(token: token)
            }
        }
    }

    func didFailToRegisterForRemoteNotifications(error: Error) {
    }
}
