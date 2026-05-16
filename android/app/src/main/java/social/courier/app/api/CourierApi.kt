package social.courier.app.api

import retrofit2.http.*
import social.courier.app.model.CourierNotification
import social.courier.app.model.Preferences
import social.courier.app.model.UserAppsResponse

data class ChallengeResponse(val challenge: String)
data class VerifyResponse(val did: String, val verified: Boolean)
data class RegisterRequest(
    val handle: String? = null,
    val did: String? = null,
    val deviceToken: String,
    val platform: String = "android",
    val preferences: Preferences? = null
)
data class RegisterResponse(val did: String, val status: String)
data class StatusResponse(val status: String)
data class SuggestAppRequest(val collection: String, val appName: String, val appURL: String)
data class BlogSub(
    val publicationUri: String,
    val authorDid: String,
    val blogName: String,
    val platform: String,
    val webUrl: String?,
    val iconUrl: String?,
    val enabled: Boolean,
)
data class BlogPrefRequest(val publicationUri: String, val enabled: Boolean)
data class OAuthStartRequest(val handle: String, val mobile: Boolean = true, val platform: String = "android")
data class OAuthStartResponse(val authorizationURL: String, val state: String)
data class OAuthExchangeResponse(val sessionToken: String, val did: String)

interface CourierApi {
    // OAuth
    @POST("auth/oauth/start")
    suspend fun oauthStart(@Body body: OAuthStartRequest): OAuthStartResponse

    @POST("auth/oauth/exchange")
    suspend fun oauthExchange(@Body body: Map<String, String>): OAuthExchangeResponse

    // Auth
    @POST("auth/challenge")
    suspend fun requestChallenge(@Body body: Map<String, String>): ChallengeResponse

    @POST("auth/verify")
    suspend fun verifyChallenge(@Body body: Map<String, String>): VerifyResponse

    // Registration
    @POST("register")
    suspend fun register(
        @Header("Authorization") bearer: String,
        @Body request: RegisterRequest
    ): RegisterResponse

    @PUT("preferences")
    suspend fun updatePreferences(
        @Header("X-DID") did: String,
        @Body preferences: Preferences
    ): StatusResponse

    @DELETE("unregister")
    suspend fun unregister(@Header("X-DID") did: String): StatusResponse

    // Notifications
    @GET("notifications/{did}")
    suspend fun getNotifications(@Path("did") did: String): List<CourierNotification>

    @DELETE("notifications")
    suspend fun clearNotifications(@Header("X-DID") did: String): StatusResponse

    // App catalog
    @GET("catalog/user")
    suspend fun getUserApps(@Query("actor") actor: String): UserAppsResponse

    @GET("catalog/user/prefs")
    suspend fun getAppPrefs(@Query("did") did: String): Map<String, Boolean>

    @PUT("catalog/user/prefs")
    suspend fun setAppPrefs(
        @Header("X-DID") did: String,
        @Body prefs: Map<String, Boolean>
    ): StatusResponse

    // App registry
    @POST("apps/suggest")
    suspend fun suggestApp(@Body request: SuggestAppRequest): StatusResponse

    // Blog subscriptions
    @GET("subscriptions/blogs")
    suspend fun getBlogSubs(@Header("Authorization") bearer: String): List<BlogSub>

    @PUT("subscriptions/blogs")
    suspend fun setBlogPref(
        @Header("Authorization") bearer: String,
        @Body body: BlogPrefRequest
    ): StatusResponse

    @POST("subscriptions/blogs/refresh")
    suspend fun refreshBlogSubs(@Header("Authorization") bearer: String): List<BlogSub>
}
