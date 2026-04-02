package social.courier.app.service

import com.google.gson.Gson
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import okhttp3.*
import social.courier.app.model.CourierNotification

class NotificationStream {
    private val client = OkHttpClient()
    private val gson = Gson()
    private var webSocket: WebSocket? = null

    private val _notifications = MutableSharedFlow<CourierNotification>(extraBufferCapacity = 10)
    val notifications: SharedFlow<CourierNotification> = _notifications

    fun connect(did: String) {
        disconnect()

        val baseUrl = "ws://10.0.2.2:8080" // debug
        val request = Request.Builder()
            .url("$baseUrl/ws/notifications/$did")
            .build()

        webSocket = client.newWebSocket(request, object : WebSocketListener() {
            override fun onMessage(webSocket: WebSocket, text: String) {
                try {
                    val notif = gson.fromJson(text, CourierNotification::class.java)
                    _notifications.tryEmit(notif)
                } catch (_: Exception) {}
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                // Reconnect after delay
                CoroutineScope(Dispatchers.IO).launch {
                    delay(2000)
                    connect(did)
                }
            }
        })
    }

    fun disconnect() {
        webSocket?.close(1000, null)
        webSocket = null
    }
}
