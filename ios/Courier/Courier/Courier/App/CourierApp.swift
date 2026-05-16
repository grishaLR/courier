import SwiftUI

@main
struct CourierApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @StateObject private var authManager = AuthManager.shared
    @StateObject private var pushManager = PushManager.shared
    @AppStorage("appTheme") private var appTheme: AppTheme = .system

    var body: some Scene {
        WindowGroup {
            Group {
                if authManager.isAuthenticated {
                    MainTabView()
                } else {
                    OnboardingView()
                }
            }
            .environmentObject(authManager)
            .environmentObject(pushManager)
            .tint(Color("AccentColor"))
            .background(Color("BackgroundColor"))
            .preferredColorScheme(appTheme.colorScheme)
        }
    }

    init() {
        let bg = UIColor(named: "BackgroundColor") ?? .systemBackground

        // List backgrounds
        UITableView.appearance().backgroundColor = bg
        UITableViewCell.appearance().backgroundColor = bg
        UITableViewHeaderFooterView.appearance().backgroundConfiguration = .clear()
        UICollectionView.appearance().backgroundColor = bg

        // Navigation bar
        let navAppearance = UINavigationBarAppearance()
        navAppearance.configureWithDefaultBackground()
        navAppearance.backgroundColor = bg
        UINavigationBar.appearance().standardAppearance = navAppearance
        UINavigationBar.appearance().scrollEdgeAppearance = navAppearance

        // Tab bar
        let tabAppearance = UITabBarAppearance()
        tabAppearance.configureWithDefaultBackground()
        tabAppearance.backgroundColor = bg
        UITabBar.appearance().standardAppearance = tabAppearance
        UITabBar.appearance().scrollEdgeAppearance = tabAppearance

        // Global tint: hot pink (light) / dracula pink (dark)
        let pink = UIColor { traits in
            if traits.userInterfaceStyle == .dark {
                return UIColor(red: 1.0, green: 0.475, blue: 0.776, alpha: 1.0) // #ff79c6
            } else {
                return UIColor(red: 0.996, green: 0.0, blue: 0.459, alpha: 1.0) // #fe0075
            }
        }
        UIView.appearance().tintColor = pink

        // Inactive tab bar items: muted secondary
        let secondary = UIColor(named: "SecondaryText") ?? .secondaryLabel
        UITabBar.appearance().unselectedItemTintColor = secondary
    }
}

class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        Task { @MainActor in
            let settings = await UNUserNotificationCenter.current().notificationSettings()
            if settings.authorizationStatus == .authorized {
                application.registerForRemoteNotifications()
            }
        }
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        Task { @MainActor in
            PushManager.shared.didRegisterForRemoteNotifications(deviceToken: deviceToken)
        }
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        Task { @MainActor in
            PushManager.shared.didFailToRegisterForRemoteNotifications(error: error)
        }
    }

    // Handle notification tap — prefer server-resolved deep link over raw AT URI
    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let userInfo = response.notification.request.content.userInfo
        if let deepLink = userInfo["deepLink"] as? String,
           !deepLink.isEmpty,
           let url = URL(string: deepLink) {
            await MainActor.run { UIApplication.shared.open(url) }
        } else if let uri = userInfo["uri"] as? String {
            DeepLinkHandler.open(atURI: uri)
        }
    }

    // Show notification even when app is in foreground
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        return [.banner, .badge, .sound]
    }
}
