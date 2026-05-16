import UserNotifications
import UIKit

class NotificationService: UNNotificationServiceExtension {
    var contentHandler: ((UNNotificationContent) -> Void)?
    var bestAttemptContent: UNMutableNotificationContent?

    override func didReceive(_ request: UNNotificationRequest, withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        self.contentHandler = contentHandler
        bestAttemptContent = (request.content.mutableCopy() as? UNMutableNotificationContent)

        guard let content = bestAttemptContent else {
            contentHandler(request.content)
            return
        }

        // Prefer app favicon (shows nicely on long-press), fall back to avatar
        let imageURL = faviconURL(from: content.userInfo) ?? avatarURL(from: content.userInfo)

        if let imageURL {
            downloadImage(url: imageURL) { attachment in
                if let attachment {
                    content.attachments = [attachment]
                }
                contentHandler(content)
            }
        } else {
            contentHandler(content)
        }
    }

    override func serviceExtensionTimeWillExpire() {
        if let contentHandler, let content = bestAttemptContent {
            contentHandler(content)
        }
    }

    private func avatarURL(from userInfo: [AnyHashable: Any]) -> URL? {
        guard let urlString = userInfo["fromAvatar"] as? String, !urlString.isEmpty else { return nil }
        return URL(string: urlString)
    }

    private func faviconURL(from userInfo: [AnyHashable: Any]) -> URL? {
        guard let urlString = userInfo["appFavicon"] as? String, !urlString.isEmpty else { return nil }
        return URL(string: urlString)
    }

    private static let urlSession: URLSession = {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 10
        config.timeoutIntervalForResource = 15
        return URLSession(configuration: config)
    }()

    private func downloadImage(url: URL, completion: @escaping (UNNotificationAttachment?) -> Void) {
        Self.urlSession.downloadTask(with: url) { localURL, response, error in
            guard let localURL, error == nil else {
                completion(nil)
                return
            }

            let tmpDir = FileManager.default.temporaryDirectory

            do {
                // Load image data and convert to JPEG (iOS notifications don't support WebP)
                let data = try Data(contentsOf: localURL)
                let jpegURL = tmpDir.appendingPathComponent(UUID().uuidString + ".jpg")

                if let image = UIImage(data: data),
                   let jpegData = image.jpegData(compressionQuality: 0.8) {
                    try jpegData.write(to: jpegURL)
                    let attachment = try UNNotificationAttachment(
                        identifier: "media",
                        url: jpegURL,
                        options: nil
                    )
                    completion(attachment)
                } else {
                    completion(nil)
                }
            } catch {
                completion(nil)
            }
        }.resume()
    }

}
