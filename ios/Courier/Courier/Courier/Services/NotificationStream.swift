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

            while !Task.isCancelled {
                do {
                    let session = URLSession.shared
                    let ws = session.webSocketTask(with: url)
                    ws.resume()

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
                } catch {
                    // Reconnect after 2s on disconnect
                    if !Task.isCancelled {
                        try? await Task.sleep(for: .seconds(2))
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
