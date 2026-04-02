import UserNotifications

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

        // Download avatar for rich notification
        if let avatarURLString = content.userInfo["fromAvatar"] as? String,
           let avatarURL = URL(string: avatarURLString) {
            downloadAvatar(url: avatarURL) { attachment in
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
        // Deliver whatever we have before time runs out
        if let contentHandler, let content = bestAttemptContent {
            contentHandler(content)
        }
    }

    private func downloadAvatar(url: URL, completion: @escaping (UNNotificationAttachment?) -> Void) {
        URLSession.shared.downloadTask(with: url) { localURL, response, error in
            guard let localURL, error == nil else {
                completion(nil)
                return
            }

            // Move to a temp file with proper extension
            let tmpURL = localURL.appendingPathExtension("jpg")
            try? FileManager.default.moveItem(at: localURL, to: tmpURL)

            let attachment = try? UNNotificationAttachment(
                identifier: "avatar",
                url: tmpURL,
                options: [UNNotificationAttachmentOptionsThumbnailClippingRectKey: CGRect(x: 0, y: 0, width: 1, height: 1).dictionaryRepresentation]
            )
            completion(attachment)
        }.resume()
    }
}
