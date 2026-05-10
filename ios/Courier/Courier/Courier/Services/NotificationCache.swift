import Foundation

/// Caches notifications locally on device using UserDefaults.
/// Persists across logout/login. User can clear manually.
actor NotificationCache {
    static let shared = NotificationCache()

    private let key = "courier.cachedNotifications"
    private let maxCount = 100

    func load() -> [CourierNotification] {
        guard let data = UserDefaults.standard.data(forKey: key),
              let notifications = try? JSONDecoder().decode([CourierNotification].self, from: data) else {
            return []
        }
        return notifications
    }

    func save(_ notifications: [CourierNotification]) {
        let capped = Array(notifications.prefix(maxCount))
        if let data = try? JSONEncoder().encode(capped) {
            UserDefaults.standard.set(data, forKey: key)
        }
    }

    /// Merge server notifications into cached ones, deduplicating by ID.
    func merge(existing: [CourierNotification], incoming: [CourierNotification]) -> [CourierNotification] {
        let existingIDs = Set(existing.map { $0.id })
        var merged = existing
        for notif in incoming {
            if !existingIDs.contains(notif.id) {
                merged.append(notif)
            }
        }
        merged.sort { $0.createdAt > $1.createdAt }
        return Array(merged.prefix(maxCount))
    }

    func clear() {
        UserDefaults.standard.removeObject(forKey: key)
    }
}
