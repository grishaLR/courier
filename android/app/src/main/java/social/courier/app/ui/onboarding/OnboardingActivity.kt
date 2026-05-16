package social.courier.app.ui.onboarding

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.browser.customtabs.CustomTabsIntent
import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import social.courier.app.BuildConfig
import social.courier.app.R
import coil.compose.AsyncImage
import com.google.firebase.messaging.FirebaseMessaging
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.tasks.await
import social.courier.app.CourierApplication
import social.courier.app.service.ActorSearchResult
import social.courier.app.service.searchActorsTypeahead
import social.courier.app.ui.inbox.MainActivity
import social.courier.app.ui.theme.CourierTheme

class OnboardingActivity : ComponentActivity() {
    private val authManager get() = (application as CourierApplication).authManager
    private val _oauthRedirect = MutableStateFlow<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        if (authManager.isAuthenticated) {
            startActivity(Intent(this, MainActivity::class.java))
            finish()
            return
        }

        intent?.data?.toString()?.takeIf { it.startsWith("social.courier:") }?.let {
            _oauthRedirect.value = it
        }

        setContent {
            CourierTheme((application as CourierApplication).themeManager) {
                OnboardingScreen(
                    oauthRedirect = _oauthRedirect.asStateFlow(),
                    onSignIn = {
                        startActivity(Intent(this, MainActivity::class.java))
                        finish()
                    }
                )
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        intent.data?.toString()?.takeIf { it.startsWith("social.courier:") }?.let {
            _oauthRedirect.value = it
        }
    }

    @Composable
    fun OnboardingScreen(oauthRedirect: StateFlow<String?>, onSignIn: () -> Unit) {
        var handleValue by remember { mutableStateOf(TextFieldValue("")) }
        var isLoading by remember { mutableStateOf(false) }
        var errorMessage by remember { mutableStateOf<String?>(null) }
        var suggestions by remember { mutableStateOf(listOf<ActorSearchResult>()) }
        var showSuggestions by remember { mutableStateOf(false) }
        val scope = rememberCoroutineScope()
        val redirectUri by oauthRedirect.collectAsState()

        val handle = handleValue.text

        // Debounced typeahead
        LaunchedEffect(handle) {
            if (handle.length >= 2) {
                delay(250)
                suggestions = searchActorsTypeahead(handle)
                showSuggestions = suggestions.isNotEmpty()
            } else {
                suggestions = emptyList()
                showSuggestions = false
            }
        }

        // Complete OAuth when redirect arrives
        LaunchedEffect(redirectUri) {
            val uri = redirectUri ?: return@LaunchedEffect
            isLoading = true
            errorMessage = null
            try {
                val fcmToken = runCatching {
                    FirebaseMessaging.getInstance().token.await()
                }.getOrNull()
                authManager.completeLogin(uri, deviceToken = fcmToken)
                onSignIn()
            } catch (e: Exception) {
                errorMessage = e.message ?: "Sign in failed"
                isLoading = false
            }
        }

        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(32.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Spacer(Modifier.weight(1f))

            Image(
                painter = painterResource(R.mipmap.ic_launcher),
                contentDescription = null,
                modifier = Modifier.size(96.dp),
            )
            Spacer(Modifier.height(16.dp))
            Text("Courier", style = MaterialTheme.typography.headlineLarge)
            Text(
                "universal notifications connecting the atmosphere to you",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(horizontal = 32.dp),
            )

            Spacer(Modifier.weight(1f))

            // Input + inline suggestions (avoids ExposedDropdownMenuBox jitter/two-tap issues)
            Column(modifier = Modifier.fillMaxWidth()) {
                OutlinedTextField(
                    value = handleValue,
                    onValueChange = {
                        handleValue = it
                        showSuggestions = false
                    },
                    label = { Text("atmosphere account") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true,
                    enabled = !isLoading,
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedTextColor = MaterialTheme.colorScheme.onBackground,
                        unfocusedTextColor = MaterialTheme.colorScheme.onBackground,
                        disabledTextColor = MaterialTheme.colorScheme.onBackground,
                        focusedLabelColor = MaterialTheme.colorScheme.primary,
                        unfocusedLabelColor = MaterialTheme.colorScheme.onSurfaceVariant,
                    ),
                )

                if (showSuggestions && suggestions.isNotEmpty() && !isLoading) {
                    Card(
                        modifier = Modifier.fillMaxWidth(),
                        shape = RoundedCornerShape(bottomStart = 8.dp, bottomEnd = 8.dp),
                        elevation = CardDefaults.cardElevation(4.dp),
                    ) {
                        suggestions.forEach { actor ->
                            Row(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .clickable {
                                        val h = actor.handle
                                        handleValue = TextFieldValue(h, selection = TextRange(h.length))
                                        showSuggestions = false
                                    }
                                    .padding(horizontal = 16.dp, vertical = 10.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                AsyncImage(
                                    model = actor.avatar,
                                    contentDescription = null,
                                    modifier = Modifier.size(32.dp).clip(CircleShape),
                                    contentScale = ContentScale.Crop,
                                )
                                Spacer(Modifier.width(10.dp))
                                Column {
                                    actor.displayName?.let {
                                        Text(it, style = MaterialTheme.typography.bodyMedium)
                                    }
                                    Text(
                                        "@${actor.handle}",
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            }
                        }
                    }
                }
            }

            errorMessage?.let {
                Spacer(Modifier.height(8.dp))
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }

            Spacer(Modifier.height(16.dp))

            Button(
                onClick = {
                    scope.launch {
                        isLoading = true
                        showSuggestions = false
                        errorMessage = null
                        try {
                            val authUrl = authManager.beginLogin(handle.trim())
                            val tab = CustomTabsIntent.Builder().build()
                            tab.launchUrl(this@OnboardingActivity, Uri.parse(authUrl))
                        } catch (e: Exception) {
                            errorMessage = e.message ?: "Could not start sign in"
                        } finally {
                            isLoading = false
                        }
                    }
                },
                modifier = Modifier.fillMaxWidth(),
                enabled = handle.isNotBlank() && !isLoading,
            ) {
                if (isLoading) {
                    CircularProgressIndicator(modifier = Modifier.size(20.dp), strokeWidth = 2.dp)
                } else {
                    Text("Sign in")
                }
            }

            Spacer(Modifier.weight(1f))

            Text(
                "v${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
