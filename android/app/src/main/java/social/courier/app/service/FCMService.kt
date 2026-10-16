package social.courier.app.service

import android.app.ActivityManager
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.core.app.NotificationCompat
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import social.courier.app.R
import social.courier.app.api.ApiClient

class FCMService : FirebaseMessagingService() {

    override fun onMessageReceived(message: RemoteMessage) {
        val title = message.data["title"] ?: message.notification?.title ?: "Courier"
        val body = message.data["body"] ?: message.notification?.body ?: ""
        val type = message.data["type"] ?: "generic"
        val uri = message.data["uri"] ?: ""

        // When the app is backgrounded/killed, the system already shows the notification
        // via the AndroidNotification field. Only show manually when in the foreground.
        if (isAppInForeground()) {
            showNotification(title, body, type, uri)
        }
    }

    private fun isAppInForeground(): Boolean {
        val am = getSystemService(Context.ACTIVITY_SERVICE) as ActivityManager
        val tasks = am.getRunningTasks(1)
        return tasks.isNotEmpty() && tasks[0].topActivity?.packageName == packageName
    }

    override fun onNewToken(token: String) {
        val prefs = getSharedPreferences("courier_auth", MODE_PRIVATE)
        prefs.edit().putString("fcm_token", token).apply()

        // If the user is already signed in, push the rotated token to the server so
        // delivery keeps working without waiting for the next login.
        val sessionToken = prefs.getString("session_token", null) ?: return
        CoroutineScope(Dispatchers.IO).launch {
            runCatching {
                ApiClient.api.updateDeviceToken(
                    "Bearer $sessionToken",
                    mapOf("deviceToken" to token),
                )
            }
        }
    }

    private fun showNotification(title: String, body: String, type: String, uri: String) {
        val channelId = channelForType(type)

        val intent = if (uri.isNotEmpty()) {
            Intent(Intent.ACTION_VIEW, Uri.parse(deepLinkFromURI(uri)))
        } else {
            packageManager.getLaunchIntentForPackage(packageName)
        }

        val pendingIntent = PendingIntent.getActivity(
            this, 0, intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val notification = NotificationCompat.Builder(this, channelId)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(body)
            .setAutoCancel(true)
            .setContentIntent(pendingIntent)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .build()

        val manager = getSystemService(NOTIFICATION_SERVICE) as NotificationManager
        manager.notify(System.currentTimeMillis().toInt(), notification)
    }

    private fun channelForType(type: String): String = when (type) {
        "like" -> "likes"
        "reply" -> "replies"
        "follow" -> "follows"
        "repost" -> "reposts"
        "mention" -> "mentions"
        "quote" -> "quotes"
        else -> "other"
    }

    private fun deepLinkFromURI(uri: String): String {
        // Simple fallback — the server should provide deepLink in the future
        if (uri.startsWith("https://")) return uri
        // at:// URI → bsky.app fallback
        val stripped = uri.removePrefix("at://")
        val parts = stripped.split("/", limit = 3)
        return if (parts.size == 3 && parts[1] == "app.bsky.feed.post") {
            "https://bsky.app/profile/${parts[0]}/post/${parts[2]}"
        } else {
            "https://bsky.app/profile/${parts.getOrElse(0) { "" }}"
        }
    }

}
