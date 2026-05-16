package social.courier.app.service

import com.google.gson.Gson
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import okhttp3.*
import social.courier.app.BuildConfig
import social.courier.app.model.CourierNotification

class NotificationStream {
    private val gson = Gson()
    private var webSocket: WebSocket? = null
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())

    private val _notifications = MutableSharedFlow<CourierNotification>(extraBufferCapacity = 10)
    val notifications: SharedFlow<CourierNotification> = _notifications

    fun connect(did: String, sessionToken: String?, backoffMs: Long = 1_000L) {
        webSocket?.close(1000, null)

        val baseUrl = if (BuildConfig.DEBUG) "ws://10.0.2.2:8080" else "wss://api.courier.social"
        val request = Request.Builder()
            .url("$baseUrl/ws/notifications/$did")
            .build()

        webSocket = CourierHttpClient.client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                // Server requires auth as first message within 10s
                val auth = if (sessionToken != null) {
                    """{"token":"$sessionToken"}"""
                } else {
                    """{"did":"$did"}"""
                }
                webSocket.send(auth)
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                try {
                    val notif = gson.fromJson(text, CourierNotification::class.java)
                    _notifications.tryEmit(notif)
                } catch (_: Exception) {}
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                scope.launch {
                    delay(backoffMs)
                    connect(did, sessionToken, (backoffMs * 2).coerceAtMost(60_000L))
                }
            }
        })
    }

    fun disconnect() {
        webSocket?.close(1000, null)
        webSocket = null
        scope.cancel()
    }
}
