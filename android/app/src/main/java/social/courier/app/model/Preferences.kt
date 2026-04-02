package social.courier.app.model

data class Preferences(
    val likes: Boolean = true,
    val replies: Boolean = true,
    val reposts: Boolean = true,
    val follows: Boolean = true,
    val mentions: Boolean = true,
    val quotes: Boolean = true,
    val generic: Boolean = false
)
