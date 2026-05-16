import Foundation

@MainActor
final class NotificationStream: ObservableObject {
    @Published var latestNotification: CourierNotification?

    private var task: Task<Void, Never>?

    func connect(did: String) {
        disconnect()

        task = Task {
            #if DEBUG
            let scheme = "ws"
            let host = "localhost:8080"
            #else
            let scheme = "wss"
            let host = "api.courier.social"
            #endif

            guard let url = URL(string: "\(scheme)://\(host)/ws/notifications/\(did)") else { return }

            var delay: TimeInterval = 1

            while !Task.isCancelled {
                do {
                    guard let token = AuthManager.shared.sessionToken else {
                        try? await Task.sleep(for: .seconds(5))
                        continue
                    }

                    let ws = URLSession.shared.webSocketTask(with: url)
                    ws.resume()
                    defer { ws.cancel(with: .goingAway, reason: nil) }

                    let authData = try JSONSerialization.data(withJSONObject: ["token": token])
                    let authJSON = String(data: authData, encoding: .utf8) ?? ""
                    try await ws.send(.string(authJSON))

                    // Connected successfully — reset backoff
                    delay = 1

                    while !Task.isCancelled {
                        let message = try await ws.receive()
                        switch message {
                        case .string(let text):
                            if let data = text.data(using: .utf8),
                               let notif = try? JSONDecoder().decode(CourierNotification.self, from: data) {
                                self.latestNotification = notif
                            }
                        case .data(let data):
                            if let notif = try? JSONDecoder().decode(CourierNotification.self, from: data) {
                                self.latestNotification = notif
                            }
                        @unknown default:
                            break
                        }
                    }
                } catch let error as URLError where error.code == .userAuthenticationRequired {
                    // 401-equivalent: stop reconnecting and signal sign-out
                    NotificationCenter.default.post(name: .notificationStreamAuthFailed, object: nil)
                    return
                } catch {
                    if !Task.isCancelled {
                        try? await Task.sleep(for: .seconds(delay))
                        delay = min(delay * 2, 60)
                    }
                }
            }
        }
    }

    func disconnect() {
        task?.cancel()
        task = nil
    }
}

extension Notification.Name {
    static let notificationStreamAuthFailed = Notification.Name("notificationStreamAuthFailed")
}
