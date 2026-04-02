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

interface CourierApi {
    // Auth
    @POST("auth/challenge")
    suspend fun requestChallenge(@Body body: Map<String, String>): ChallengeResponse

    @POST("auth/verify")
    suspend fun verifyChallenge(@Body body: Map<String, String>): VerifyResponse

    // Registration
    @POST("register")
    suspend fun register(@Body request: RegisterRequest): RegisterResponse

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
}
