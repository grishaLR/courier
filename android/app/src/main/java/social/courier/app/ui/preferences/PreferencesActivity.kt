package social.courier.app.ui.preferences

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.AddCircle
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowRight
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import coil.compose.AsyncImage
import kotlinx.coroutines.launch
import social.courier.app.api.ApiClient
import social.courier.app.model.AppGroup
import social.courier.app.model.AppInfo
import social.courier.app.model.Preferences
import social.courier.app.service.AuthManager

class PreferencesActivity : ComponentActivity() {
    private lateinit var authManager: AuthManager

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        authManager = AuthManager(this)

        setContent {
            MaterialTheme {
                PreferencesScreen()
            }
        }
    }

    @OptIn(ExperimentalMaterial3Api::class)
    @Composable
    fun PreferencesScreen() {
        var yourApps by remember { mutableStateOf(listOf<AppGroup>()) }
        var discoverApps by remember { mutableStateOf(listOf<AppGroup>()) }
        var appPrefs by remember { mutableStateOf(mutableMapOf<String, Boolean>()) }
        var expandedCategories by remember { mutableStateOf(mutableSetOf<String>()) }
        var isLoading by remember { mutableStateOf(true) }
        var isSaving by remember { mutableStateOf(false) }
        val scope = rememberCoroutineScope()

        // Load data
        LaunchedEffect(Unit) {
            val did = authManager.did ?: return@LaunchedEffect
            try {
                val response = ApiClient.api.getUserApps(did)
                yourApps = response.yourApps
                discoverApps = response.discoverApps ?: emptyList()

                val prefs = ApiClient.api.getAppPrefs(did).toMutableMap()
                // Default discover apps to off
                for (group in discoverApps) {
                    for (app in group.apps) {
                        if (app.collectionPrefix !in prefs) {
                            prefs[app.collectionPrefix] = false
                        }
                    }
                }
                appPrefs = prefs

                expandedCategories = yourApps.map { it.category }.toMutableSet()
            } catch (_: Exception) {}
            isLoading = false
        }

        Scaffold(
            topBar = { TopAppBar(title = { Text("Preferences") }) }
        ) { padding ->
            if (isLoading) {
                Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
            } else {
                LazyColumn(
                    modifier = Modifier.padding(padding),
                    contentPadding = PaddingValues(16.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    // Account
                    item {
                        Text(
                            authManager.handle ?: "",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }

                    // Apps You're On
                    if (yourApps.isNotEmpty()) {
                        item {
                            Text("Apps You're On", style = MaterialTheme.typography.titleMedium)
                        }
                        items(yourApps, key = { "your-${it.category}" }) { group ->
                            CategorySection(
                                group = group,
                                expanded = group.category in expandedCategories,
                                onToggleExpand = {
                                    expandedCategories = expandedCategories.toMutableSet().apply {
                                        if (group.category in this) remove(group.category) else add(group.category)
                                    }
                                }
                            ) { app ->
                                AppToggleRow(
                                    app = app,
                                    isEnabled = appPrefs[app.collectionPrefix] ?: true,
                                    onToggle = { appPrefs = appPrefs.toMutableMap().apply { put(app.collectionPrefix, it) } },
                                    onTap = { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(app.appUrl))) }
                                )
                            }
                        }
                    }

                    // Apps You Could Be On
                    if (discoverApps.isNotEmpty()) {
                        item {
                            Spacer(Modifier.height(8.dp))
                            Text("Apps You Could Be On", style = MaterialTheme.typography.titleMedium)
                        }
                        items(discoverApps, key = { "discover-${it.category}" }) { group ->
                            CategorySection(
                                group = group,
                                expanded = group.category in expandedCategories,
                                onToggleExpand = {
                                    expandedCategories = expandedCategories.toMutableSet().apply {
                                        if (group.category in this) remove(group.category) else add(group.category)
                                    }
                                }
                            ) { app ->
                                AppAddRow(
                                    app = app,
                                    onAdd = {
                                        appPrefs = appPrefs.toMutableMap().apply { put(app.collectionPrefix, true) }
                                        // Move to your apps
                                        val existingIdx = yourApps.indexOfFirst { it.category == app.category }
                                        yourApps = if (existingIdx >= 0) {
                                            yourApps.toMutableList().apply {
                                                set(existingIdx, AppGroup(app.category, get(existingIdx).apps + app))
                                            }
                                        } else {
                                            yourApps + AppGroup(app.category, listOf(app))
                                        }
                                        discoverApps = discoverApps.map { g ->
                                            AppGroup(g.category, g.apps.filter { it.collectionPrefix != app.collectionPrefix })
                                        }.filter { it.apps.isNotEmpty() }
                                    },
                                    onTap = { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(app.appUrl))) }
                                )
                            }
                        }
                    }

                    // Save button
                    item {
                        Spacer(Modifier.height(16.dp))
                        Button(
                            onClick = {
                                scope.launch {
                                    isSaving = true
                                    try {
                                        authManager.did?.let { did ->
                                            ApiClient.api.setAppPrefs(did, appPrefs)
                                        }
                                        Toast.makeText(this@PreferencesActivity, "Saved", Toast.LENGTH_SHORT).show()
                                    } catch (e: Exception) {
                                        Toast.makeText(this@PreferencesActivity, "Error: ${e.message}", Toast.LENGTH_SHORT).show()
                                    }
                                    isSaving = false
                                }
                            },
                            modifier = Modifier.fillMaxWidth(),
                            enabled = !isSaving
                        ) {
                            Text(if (isSaving) "Saving…" else "Save All")
                        }
                    }
                }
            }
        }
    }

    @Composable
    fun CategorySection(
        group: AppGroup,
        expanded: Boolean,
        onToggleExpand: () -> Unit,
        content: @Composable (AppInfo) -> Unit
    ) {
        Card(modifier = Modifier.fillMaxWidth().animateContentSize()) {
            Column {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable(onClick = onToggleExpand)
                        .padding(16.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text(group.category, style = MaterialTheme.typography.titleSmall)
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            "${group.apps.size}",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                        Icon(
                            if (expanded) Icons.Default.KeyboardArrowDown else Icons.Default.KeyboardArrowRight,
                            contentDescription = null,
                            modifier = Modifier.size(20.dp)
                        )
                    }
                }
                if (expanded) {
                    HorizontalDivider()
                    group.apps.forEach { app ->
                        content(app)
                        if (app != group.apps.last()) {
                            HorizontalDivider(modifier = Modifier.padding(start = 54.dp))
                        }
                    }
                }
            }
        }
    }

    @Composable
    fun AppToggleRow(app: AppInfo, isEnabled: Boolean, onToggle: (Boolean) -> Unit, onTap: () -> Unit) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            AppIcon(app)
            Spacer(Modifier.width(10.dp))
            Column(modifier = Modifier.weight(1f).clickable(onClick = onTap)) {
                Text(app.appName, style = MaterialTheme.typography.bodyMedium)
                app.description?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
                }
            }
            Switch(checked = isEnabled, onCheckedChange = onToggle)
        }
    }

    @Composable
    fun AppAddRow(app: AppInfo, onAdd: () -> Unit, onTap: () -> Unit) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            AppIcon(app)
            Spacer(Modifier.width(10.dp))
            Column(modifier = Modifier.weight(1f).clickable(onClick = onTap)) {
                Text(app.appName, style = MaterialTheme.typography.bodyMedium)
                app.description?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
                }
            }
            IconButton(onClick = onAdd) {
                Icon(Icons.Default.AddCircle, "Add", tint = MaterialTheme.colorScheme.primary)
            }
        }
    }

    @Composable
    fun AppIcon(app: AppInfo) {
        if (!app.faviconUrl.isNullOrEmpty()) {
            AsyncImage(
                model = app.faviconUrl,
                contentDescription = app.appName,
                modifier = Modifier.size(28.dp).clip(RoundedCornerShape(6.dp)),
                contentScale = ContentScale.Crop
            )
        } else {
            Surface(
                modifier = Modifier.size(28.dp),
                shape = RoundedCornerShape(6.dp),
                color = MaterialTheme.colorScheme.surfaceVariant
            ) {
                Box(contentAlignment = Alignment.Center) {
                    Text(
                        app.appName.take(1),
                        style = MaterialTheme.typography.bodySmall
                    )
                }
            }
        }
    }
}
