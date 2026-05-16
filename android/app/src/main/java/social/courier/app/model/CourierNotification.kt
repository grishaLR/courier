package social.courier.app.model

import com.google.gson.annotations.SerializedName

data class CourierNotification(
    val type: NotificationType?,
    val fromDid: String,
    val forDid: String,
    val collection: String,
    val uri: String,
    val subjectUri: String? = null,
    val subjectText: String? = null,
    val deepLink: String? = null,
    val fromHandle: String? = null,
    val fromName: String? = null,
    val fromAvatar: String? = null,
    val appName: String? = null,
    val appFavicon: String? = null,
    val createdAt: String,
) {
    val displayName: String
        get() = fromName ?: fromHandle ?: fromDid

    val resolvedAppName: String?
        get() = appName ?: run {
            // Client-side fallback for older server responses
            val mapping = listOf(
                "sh.tangled" to "Tangled",
                "community.lexicon.calendar" to "Atmo",
                "com.whtwnd.blog" to "WhiteWind",
                "fyi.unravel.frontpage" to "Frontpage",
                "blue.pico" to "Picosky",
                "events.smokesignal" to "Smoke Signal",
                "social.arabica" to "Arabica",
                "xyz.statusphere" to "Statusphere",
            )
            mapping.firstOrNull { collection.startsWith(it.first) }?.second
        }

    val collectionShortName: String
        get() {
            val parts = collection.split(".")
            return if (parts.size >= 2) parts.takeLast(2).joinToString(".") else collection
        }

    private val context: String
        get() {
            val name = resolvedAppName ?: return ""
            if (collection.startsWith("app.bsky.") && name == "Bluesky") return ""
            return " on $name"
        }

    private val subjectNoun: String
        get() {
            val uri = subjectUri ?: return "post"
            if (uri.contains("repo.issue")) return "issue"
            if (uri.contains("repo.pull")) return "pull request"
            if (uri.contains("calendar.event")) return "event"
            if (uri.contains("document") || uri.contains("blog") || uri.contains("entry")) return "post"
            if (uri.startsWith("at://")) {
                val stripped = uri.removePrefix("at://")
                val parts = stripped.split("/", limit = 3)
                if (parts.size >= 2) {
                    val coll = parts[1]
                    val segments = coll.split(".")
                    val noun = segments.lastOrNull() ?: "post"
                    val generic = setOf("feed", "graph", "interactions", "app", "social", "alpha", "dev")
                    if (noun !in generic) return noun
                }
            }
            return "post"
        }

    val actionText: String
        get() = when (type) {
            NotificationType.LIKE -> "liked your $subjectNoun$context"
            NotificationType.FAVORITE -> "favorited your $subjectNoun$context"
            NotificationType.REPLY -> "replied to you$context"
            NotificationType.REPOST -> "reposted your $subjectNoun$context"
            NotificationType.FOLLOW -> "followed you$context"
            NotificationType.MENTION -> "mentioned you$context"
            NotificationType.QUOTE -> "quoted your post$context"
            NotificationType.STAR -> "starred your repo$context"
            NotificationType.ISSUE -> "opened an issue$context"
            NotificationType.PULL_REQUEST -> "opened a pull request$context"
            NotificationType.RSVP -> {
                val st = subjectText
                when {
                    st?.startsWith("going") == true -> "is going to your event$context"
                    st?.startsWith("interested") == true -> "is interested in your event$context"
                    st?.startsWith("not going") == true -> "is not going to your event$context"
                    else -> "RSVPed to your event$context"
                }
            }
            NotificationType.SUBSCRIPTION -> "subscribed to your publication$context"
            NotificationType.REACTION -> "reacted to your post$context"
            NotificationType.PLAY -> "played your track$context"
            NotificationType.RECOMMEND -> "recommended your post$context"
            NotificationType.VOTE -> "voted on your poll$context"
            NotificationType.BLOG_POST -> {
                val name = resolvedAppName
                if (!name.isNullOrEmpty()) "published a new post on $name" else "published a new post$context"
            }
            NotificationType.GENERIC, NotificationType.UNKNOWN, null ->
                "via ${resolvedAppName ?: collectionShortName}"
        }

    val body: String get() = "$displayName $actionText"
}

enum class NotificationType {
    @SerializedName("like") LIKE,
    @SerializedName("favorite") FAVORITE,
    @SerializedName("reply") REPLY,
    @SerializedName("repost") REPOST,
    @SerializedName("follow") FOLLOW,
    @SerializedName("mention") MENTION,
    @SerializedName("quote") QUOTE,
    @SerializedName("star") STAR,
    @SerializedName("issue") ISSUE,
    @SerializedName("pullRequest") PULL_REQUEST,
    @SerializedName("rsvp") RSVP,
    @SerializedName("subscription") SUBSCRIPTION,
    @SerializedName("reaction") REACTION,
    @SerializedName("play") PLAY,
    @SerializedName("recommend") RECOMMEND,
    @SerializedName("vote") VOTE,
    @SerializedName("blogPost") BLOG_POST,
    @SerializedName("generic") GENERIC,
    @SerializedName("unknown") UNKNOWN,
}
