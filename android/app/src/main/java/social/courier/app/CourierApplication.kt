package social.courier.app

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import social.courier.app.service.AuthManager
import social.courier.app.ui.theme.ThemeManager

class CourierApplication : Application() {
    lateinit var authManager: AuthManager
        private set
    lateinit var themeManager: ThemeManager
        private set

    override fun onCreate() {
        super.onCreate()
        authManager = AuthManager(this)
        themeManager = ThemeManager(this)
        createNotificationChannels()
    }

    private fun createNotificationChannels() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val manager = getSystemService(NOTIFICATION_SERVICE) as NotificationManager
        listOf(
            "likes" to "Likes",
            "replies" to "Replies",
            "follows" to "Follows",
            "reposts" to "Reposts",
            "mentions" to "Mentions",
            "quotes" to "Quotes",
            "other" to "Other",
        ).forEach { (id, name) ->
            manager.createNotificationChannel(
                NotificationChannel(id, name, NotificationManager.IMPORTANCE_HIGH)
            )
        }
    }
}
