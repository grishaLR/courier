package social.courier.app.ui.inbox

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import coil.compose.AsyncImage
import kotlinx.coroutines.launch
import social.courier.app.api.ApiClient
import social.courier.app.model.CourierNotification
import social.courier.app.model.NotificationType
import social.courier.app.service.AuthManager
import social.courier.app.service.NotificationStream
import social.courier.app.ui.onboarding.OnboardingActivity
import social.courier.app.ui.preferences.PreferencesActivity

class MainActivity : ComponentActivity() {
    private lateinit var authManager: AuthManager
    private val stream = NotificationStream()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        authManager = AuthManager(this)

        setContent {
            MaterialTheme {
                InboxScreen()
            }
        }
    }

    override fun onStart() {
        super.onStart()
        authManager.did?.let { stream.connect(it) }
    }

    override fun onStop() {
        super.onStop()
        stream.disconnect()
    }

    @OptIn(ExperimentalMaterial3Api::class)
    @Composable
    fun InboxScreen() {
        var notifications by remember { mutableStateOf(listOf<CourierNotification>()) }
        var isLoading by remember { mutableStateOf(true) }
        val scope = rememberCoroutineScope()

        // Load initial notifications
        LaunchedEffect(Unit) {
            authManager.did?.let { did ->
                try {
                    notifications = ApiClient.api.getNotifications(did)
                } catch (_: Exception) {}
                isLoading = false
            }
        }

        // Live updates via WebSocket
        LaunchedEffect(Unit) {
            stream.notifications.collect { notif ->
                notifications = listOf(notif) + notifications.take(49)
            }
        }

        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text("Inbox") },
                    actions = {
                        IconButton(onClick = {
                            startActivity(Intent(this@MainActivity, PreferencesActivity::class.java))
                        }) {
                            Icon(Icons.Default.Settings, "Preferences")
                        }
                        IconButton(onClick = {
                            scope.launch {
                                authManager.signOut()
                                startActivity(Intent(this@MainActivity, OnboardingActivity::class.java))
                                finish()
                            }
                        }) {
                            Icon(Icons.Default.ExitToApp, "Sign out")
                        }
                    }
                )
            }
        ) { padding ->
            when {
                isLoading -> {
                    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        CircularProgressIndicator()
                    }
                }
                notifications.isEmpty() -> {
                    Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                        Column(horizontalAlignment = Alignment.CenterHorizontally) {
                            Icon(Icons.Default.Notifications, null, modifier = Modifier.size(48.dp))
                            Spacer(Modifier.height(8.dp))
                            Text("No notifications yet")
                        }
                    }
                }
                else -> {
                    LazyColumn(modifier = Modifier.padding(padding)) {
                        items(notifications, key = { it.uri + it.createdAt }) { notif ->
                            NotificationRow(notif) {
                                val url = notif.deepLink ?: return@NotificationRow
                                startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
                            }
                            HorizontalDivider()
                        }
                    }
                }
            }
        }
    }

    @Composable
    fun NotificationRow(notification: CourierNotification, onClick: () -> Unit) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clickable(onClick = onClick)
                .padding(horizontal = 16.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            AsyncImage(
                model = notification.fromAvatar,
                contentDescription = "Avatar",
                modifier = Modifier
                    .size(44.dp)
                    .clip(CircleShape),
                contentScale = ContentScale.Crop
            )

            Spacer(Modifier.width(12.dp))

            Column(modifier = Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(
                        iconForType(notification.type),
                        contentDescription = null,
                        modifier = Modifier.size(16.dp),
                        tint = colorForType(notification.type)
                    )
                    Spacer(Modifier.width(6.dp))
                    Text(
                        notification.displayName,
                        style = MaterialTheme.typography.bodyMedium
                    )
                }
                Text(
                    notification.body,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
    }

    private fun iconForType(type: NotificationType): ImageVector = when (type) {
        NotificationType.LIKE -> Icons.Default.Favorite
        NotificationType.REPLY -> Icons.Default.Favorite
        NotificationType.REPOST -> Icons.Default.Favorite
        NotificationType.FOLLOW -> Icons.Default.Favorite
        NotificationType.MENTION -> Icons.Default.Favorite
        NotificationType.QUOTE -> Icons.Default.Favorite
        NotificationType.GENERIC -> Icons.Default.Notifications
    }

    private fun colorForType(type: NotificationType): Color = when (type) {
        NotificationType.LIKE -> Color(0xFFE91E63)
        NotificationType.REPLY -> Color(0xFF2196F3)
        NotificationType.REPOST -> Color(0xFF4CAF50)
        NotificationType.FOLLOW -> Color(0xFF9C27B0)
        NotificationType.MENTION -> Color(0xFFFF9800)
        NotificationType.QUOTE -> Color(0xFF3F51B5)
        NotificationType.GENERIC -> Color.Gray
    }
}
