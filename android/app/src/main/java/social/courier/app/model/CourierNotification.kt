package social.courier.app.model

import com.google.gson.annotations.SerializedName

data class CourierNotification(
    val type: NotificationType,
    val fromDid: String,
    val forDid: String,
    val collection: String,
    val uri: String,
    val subjectUri: String? = null,
    val deepLink: String? = null,
    val fromHandle: String? = null,
    val fromName: String? = null,
    val fromAvatar: String? = null,
    val createdAt: String
) {
    val displayName: String
        get() = fromName ?: fromHandle ?: fromDid

    val appName: String?
        get() {
            val mapping = listOf(
                "sh.tangled" to "Tangled",
                "community.lexicon.calendar" to "Atmo",
                "com.whtwnd.blog" to "WhiteWind",
                "fyi.unravel.frontpage" to "Frontpage",
                "blue.pico" to "Picosky",
                "events.smokesignal" to "Smoke Signal",
            )
            return mapping.firstOrNull { collection.startsWith(it.first) }?.second
        }

    private val isBluesky: Boolean
        get() = collection.startsWith("app.bsky.")

    private val context: String
        get() = if (appName != null && !isBluesky) " on $appName" else ""

    val body: String
        get() = when (type) {
            NotificationType.LIKE -> "$displayName liked your post$context"
            NotificationType.REPLY -> "$displayName replied to you$context"
            NotificationType.REPOST -> "$displayName reposted your post$context"
            NotificationType.FOLLOW -> "$displayName followed you$context"
            NotificationType.MENTION -> "$displayName mentioned you$context"
            NotificationType.QUOTE -> "$displayName quoted your post$context"
            NotificationType.GENERIC -> "$displayName via ${appName ?: collectionShortName}"
        }

    private val collectionShortName: String
        get() {
            val parts = collection.split(".")
            return if (parts.size >= 2) parts.takeLast(2).joinToString(".") else collection
        }
}

enum class NotificationType {
    @SerializedName("like") LIKE,
    @SerializedName("reply") REPLY,
    @SerializedName("repost") REPOST,
    @SerializedName("follow") FOLLOW,
    @SerializedName("mention") MENTION,
    @SerializedName("quote") QUOTE,
    @SerializedName("generic") GENERIC
}
