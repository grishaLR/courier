package social.courier.app.service

import android.content.Context
import android.content.SharedPreferences
import android.net.Uri
import social.courier.app.api.ApiClient
import social.courier.app.api.OAuthStartRequest
import social.courier.app.api.RegisterRequest

class AuthManager(context: Context) {
    private val prefs: SharedPreferences =
        context.getSharedPreferences("courier_auth", Context.MODE_PRIVATE)

    val did: String?
        get() = prefs.getString("did", null)

    val handle: String?
        get() = prefs.getString("handle", null)

    val sessionToken: String?
        get() = prefs.getString("session_token", null)

    val isAuthenticated: Boolean
        get() = did != null

    suspend fun beginLogin(handle: String): String {
        val h = handle.removePrefix("@")
        val response = ApiClient.api.oauthStart(OAuthStartRequest(handle = h))
        prefs.edit().putString("handle", h).apply()
        return response.authorizationURL
    }

    suspend fun completeLogin(redirectUri: String, deviceToken: String?) {
        val uri = Uri.parse(redirectUri)
        val oauthError = uri.getQueryParameter("error")
        if (oauthError != null) {
            error(uri.getQueryParameter("error_description")?.replace('+', ' ') ?: oauthError)
        }
        // Server completes the token exchange and redirects with session+did directly
        val sessionToken = uri.getQueryParameter("session") ?: error("No session in redirect")
        val did = uri.getQueryParameter("did") ?: error("No did in redirect")

        prefs.edit()
            .putString("did", did)
            .putString("session_token", sessionToken)
            .commit()

        if (deviceToken != null) {
            ApiClient.api.register(
                "Bearer $sessionToken",
                RegisterRequest(
                    did = did,
                    deviceToken = deviceToken,
                    platform = "android",
                )
            )
        }
    }

    suspend fun signOut() {
        did?.let {
            try { ApiClient.api.unregister(it) } catch (_: Exception) {}
        }
        prefs.edit().clear().apply()
    }
}
