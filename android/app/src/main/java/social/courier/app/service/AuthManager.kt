package social.courier.app.service

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject
import social.courier.app.api.ApiClient
import social.courier.app.api.RegisterRequest
import java.net.URL

class AuthManager(context: Context) {
    private val prefs: SharedPreferences =
        context.getSharedPreferences("courier_auth", Context.MODE_PRIVATE)

    var did: String?
        get() = prefs.getString("did", null)
        private set(value) = prefs.edit().putString("did", value).apply()

    var handle: String?
        get() = prefs.getString("handle", null)
        private set(value) = prefs.edit().putString("handle", value).apply()

    val isAuthenticated: Boolean
        get() = did != null

    suspend fun resolveHandle(handle: String): String = withContext(Dispatchers.IO) {
        val cleanHandle = handle.removePrefix("@")
        val url = URL("https://public.api.bsky.app/xrpc/com.atproto.identity.resolveHandle?handle=$cleanHandle")
        val response = url.readText()
        JSONObject(response).getString("did")
    }

    suspend fun authenticate(handleOrDID: String, deviceToken: String?) {
        val resolvedDID: String
        val resolvedHandle: String

        if (handleOrDID.startsWith("did:")) {
            resolvedDID = handleOrDID
            resolvedHandle = handleOrDID
        } else {
            resolvedHandle = handleOrDID
            resolvedDID = resolveHandle(handleOrDID)
        }

        // Register with backend
        if (deviceToken != null) {
            ApiClient.api.register(
                RegisterRequest(
                    handle = resolvedHandle,
                    did = resolvedDID,
                    deviceToken = deviceToken,
                    platform = "android"
                )
            )
        }

        did = resolvedDID
        handle = resolvedHandle
    }

    suspend fun signOut() {
        did?.let {
            try { ApiClient.api.unregister(it) } catch (_: Exception) {}
        }
        prefs.edit().clear().apply()
    }
}
