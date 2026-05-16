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
import androidx.compose.material.icons.automirrored.filled.ArrowBack
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
import social.courier.app.CourierApplication
import social.courier.app.api.ApiClient
import social.courier.app.api.BlogPrefRequest
import social.courier.app.api.BlogSub
import social.courier.app.model.AppGroup
import social.courier.app.model.AppInfo
import social.courier.app.ui.theme.AppTheme
import social.courier.app.ui.theme.CourierTheme

class PreferencesActivity : ComponentActivity() {
    private val authManager get() = (application as CourierApplication).authManager
    private val themeManager get() = (application as CourierApplication).themeManager

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            CourierTheme(themeManager) {
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
        var blogSubs by remember { mutableStateOf(listOf<BlogSub>()) }
        var isLoading by remember { mutableStateOf(true) }
        var isSaving by remember { mutableStateOf(false) }
        var isRefreshingBlogs by remember { mutableStateOf(false) }

        // Top-level section expand state — all collapsed by default like iOS
        var appearanceExpanded by remember { mutableStateOf(false) }
        var yourAppsExpanded by remember { mutableStateOf(false) }
        var discoverAppsExpanded by remember { mutableStateOf(false) }
        var blogSubsExpanded by remember { mutableStateOf(false) }
        var selectedTheme by remember { mutableStateOf(themeManager.theme.value) }

        // Sub-category expand state
        var expandedCategories by remember { mutableStateOf(mutableSetOf<String>()) }

        val scope = rememberCoroutineScope()

        LaunchedEffect(Unit) {
            val did = authManager.did ?: return@LaunchedEffect
            val bearer = "Bearer ${authManager.sessionToken}"

            runCatching { ApiClient.api.getUserApps(did) }.onSuccess { response ->
                yourApps = response.yourApps
                discoverApps = response.discoverApps ?: emptyList()
                val prefs = runCatching { ApiClient.api.getAppPrefs(did) }
                    .getOrDefault(emptyMap()).toMutableMap()
                for (group in discoverApps) {
                    for (app in group.apps) {
                        if (app.collectionPrefix !in prefs) prefs[app.collectionPrefix] = false
                    }
                }
                appPrefs = prefs
            }

            var subs = runCatching { ApiClient.api.getBlogSubs(bearer) }.getOrDefault(emptyList())
            if (subs.isEmpty()) {
                subs = runCatching { ApiClient.api.refreshBlogSubs(bearer) }.getOrDefault(emptyList())
            }
            blogSubs = subs

            isLoading = false
        }

        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text("Preferences") },
                    navigationIcon = {
                        IconButton(onClick = { finish() }) {
                            Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                        }
                    }
                )
            }
        ) { padding ->
            if (isLoading) {
                Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                return@Scaffold
            }

            LazyColumn(
                modifier = Modifier.padding(padding),
                contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                // Account handle
                item {
                    Text(
                        "@${authManager.handle ?: authManager.did ?: ""}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(vertical = 8.dp),
                    )
                }

                // --- Appearance ---
                item {
                    TopLevelSection(
                        title = "Appearance",
                        count = 0,
                        expanded = appearanceExpanded,
                        onToggle = { appearanceExpanded = !appearanceExpanded },
                    )
                }
                if (appearanceExpanded) {
                    item {
                        SingleChoiceSegmentedButtonRow(modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp)) {
                            AppTheme.entries.forEachIndexed { index, theme ->
                                SegmentedButton(
                                    selected = selectedTheme == theme,
                                    onClick = {
                                        selectedTheme = theme
                                        themeManager.setTheme(theme)
                                    },
                                    shape = SegmentedButtonDefaults.itemShape(index, AppTheme.entries.size),
                                ) {
                                    Text(theme.name)
                                }
                            }
                        }
                    }
                }

                // --- Blog Subscriptions ---
                item {
                    TopLevelSection(
                        title = "Blog Subscriptions",
                        count = blogSubs.size,
                        expanded = blogSubsExpanded,
                        onToggle = { blogSubsExpanded = !blogSubsExpanded },
                        trailingAction = if (blogSubsExpanded) ({
                            TextButton(
                                onClick = {
                                    scope.launch {
                                        isRefreshingBlogs = true
                                        val did = authManager.did ?: return@launch
                                        val bearer = "Bearer ${authManager.sessionToken}"
                                        blogSubs = runCatching {
                                            ApiClient.api.refreshBlogSubs(bearer)
                                        }.getOrDefault(blogSubs)
                                        isRefreshingBlogs = false
                                    }
                                },
                                enabled = !isRefreshingBlogs,
                            ) { Text(if (isRefreshingBlogs) "…" else "Refresh", style = MaterialTheme.typography.bodySmall) }
                        }) else null,
                    )
                }
                if (blogSubsExpanded) {
                    if (blogSubs.isEmpty()) {
                        item {
                            Text(
                                "No blog subscriptions found. Follow publications on Bluesky and tap Refresh.",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp),
                            )
                        }
                    } else {
                        items(blogSubs, key = { "blog-${it.publicationUri}" }) { sub ->
                            BlogSubRow(
                                sub = sub,
                                onToggle = { enabled ->
                                    blogSubs = blogSubs.map {
                                        if (it.publicationUri == sub.publicationUri) it.copy(enabled = enabled) else it
                                    }
                                    scope.launch {
                                        runCatching {
                                            authManager.sessionToken?.let { token ->
                                                ApiClient.api.setBlogPref("Bearer $token", BlogPrefRequest(sub.publicationUri, enabled))
                                            }
                                        }
                                    }
                                },
                                onTap = {
                                    sub.webUrl?.let { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(it))) }
                                },
                            )
                        }
                    }
                }

                // --- Apps You're On ---
                item {
                    val total = yourApps.sumOf { it.apps.size }
                    TopLevelSection(
                        title = "Apps You're On",
                        count = total,
                        expanded = yourAppsExpanded,
                        onToggle = { yourAppsExpanded = !yourAppsExpanded },
                    )
                }
                if (yourAppsExpanded) {
                    items(yourApps, key = { "your-${it.category}" }) { group ->
                        CategorySection(
                            group = group,
                            expanded = group.category in expandedCategories,
                            onToggleExpand = {
                                expandedCategories = expandedCategories.toMutableSet().apply {
                                    if (group.category in this) remove(group.category) else add(group.category)
                                }
                            },
                        ) { app ->
                            AppToggleRow(
                                app = app,
                                isEnabled = appPrefs[app.collectionPrefix] ?: true,
                                onToggle = { appPrefs = appPrefs.toMutableMap().apply { put(app.collectionPrefix, it) } },
                                onTap = { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(app.appUrl))) },
                            )
                        }
                    }

                    item {
                        Spacer(Modifier.height(4.dp))
                        Button(
                            onClick = {
                                scope.launch {
                                    isSaving = true
                                    try {
                                        authManager.did?.let { ApiClient.api.setAppPrefs(it, appPrefs) }
                                        Toast.makeText(this@PreferencesActivity, "Saved", Toast.LENGTH_SHORT).show()
                                    } catch (e: Exception) {
                                        Toast.makeText(this@PreferencesActivity, "Error: ${e.message}", Toast.LENGTH_SHORT).show()
                                    }
                                    isSaving = false
                                }
                            },
                            modifier = Modifier.fillMaxWidth(),
                            enabled = !isSaving,
                        ) {
                            Text(if (isSaving) "Saving…" else "Save")
                        }
                    }
                }

                // --- Apps You Could Be On ---
                item {
                    val total = discoverApps.sumOf { it.apps.size }
                    TopLevelSection(
                        title = "Apps You Could Be On",
                        count = total,
                        expanded = discoverAppsExpanded,
                        onToggle = { discoverAppsExpanded = !discoverAppsExpanded },
                    )
                }
                if (discoverAppsExpanded) {
                    items(discoverApps, key = { "discover-${it.category}" }) { group ->
                        CategorySection(
                            group = group,
                            expanded = group.category in expandedCategories,
                            onToggleExpand = {
                                expandedCategories = expandedCategories.toMutableSet().apply {
                                    if (group.category in this) remove(group.category) else add(group.category)
                                }
                            },
                        ) { app ->
                            AppAddRow(
                                app = app,
                                onAdd = {
                                    appPrefs = appPrefs.toMutableMap().apply { put(app.collectionPrefix, true) }
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
                                onTap = { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(app.appUrl))) },
                            )
                        }
                    }
                }

                item { Spacer(Modifier.height(16.dp)) }
            }
        }
    }

    @Composable
    fun TopLevelSection(
        title: String,
        count: Int,
        expanded: Boolean,
        onToggle: () -> Unit,
        trailingAction: (@Composable () -> Unit)? = null,
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clickable(onClick = onToggle)
                .padding(vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.weight(1f)) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                if (count > 0) {
                    Spacer(Modifier.width(8.dp))
                    Surface(
                        shape = RoundedCornerShape(10.dp),
                        color = MaterialTheme.colorScheme.surfaceVariant,
                    ) {
                        Text(
                            "$count",
                            style = MaterialTheme.typography.labelSmall,
                            modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
                        )
                    }
                }
            }
            trailingAction?.invoke()
            Icon(
                if (expanded) Icons.Default.KeyboardArrowDown else Icons.Default.KeyboardArrowRight,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        HorizontalDivider()
    }

    @Composable
    fun CategorySection(
        group: AppGroup,
        expanded: Boolean,
        onToggleExpand: () -> Unit,
        content: @Composable (AppInfo) -> Unit,
    ) {
        Card(modifier = Modifier.fillMaxWidth().animateContentSize()) {
            Column {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable(onClick = onToggleExpand)
                        .padding(16.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(group.category, style = MaterialTheme.typography.titleSmall)
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            "${group.apps.size}",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Icon(
                            if (expanded) Icons.Default.KeyboardArrowDown else Icons.Default.KeyboardArrowRight,
                            contentDescription = null,
                            modifier = Modifier.size(20.dp),
                        )
                    }
                }
                if (expanded) {
                    HorizontalDivider()
                    group.apps.forEach { app ->
                        content(app)
                        if (app != group.apps.last()) HorizontalDivider(modifier = Modifier.padding(start = 54.dp))
                    }
                }
            }
        }
    }

    @Composable
    fun BlogSubRow(sub: BlogSub, onToggle: (Boolean) -> Unit, onTap: () -> Unit) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 4.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (!sub.iconUrl.isNullOrEmpty()) {
                AsyncImage(
                    model = sub.iconUrl,
                    contentDescription = sub.blogName,
                    modifier = Modifier.size(28.dp).clip(RoundedCornerShape(6.dp)),
                    contentScale = ContentScale.Crop,
                )
            } else {
                Surface(modifier = Modifier.size(28.dp), shape = RoundedCornerShape(6.dp), color = MaterialTheme.colorScheme.surfaceVariant) {
                    Box(contentAlignment = Alignment.Center) {
                        Text(sub.blogName.take(1), style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
            Spacer(Modifier.width(10.dp))
            Column(modifier = Modifier.weight(1f).clickable(onClick = onTap)) {
                Text(sub.blogName, style = MaterialTheme.typography.bodyMedium)
                Text(sub.platform, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Switch(checked = sub.enabled, onCheckedChange = onToggle)
        }
    }

    @Composable
    fun AppToggleRow(app: AppInfo, isEnabled: Boolean, onToggle: (Boolean) -> Unit, onTap: () -> Unit) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
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
            verticalAlignment = Alignment.CenterVertically,
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
                Icon(Icons.Default.KeyboardArrowRight, "Add", tint = MaterialTheme.colorScheme.primary)
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
                contentScale = ContentScale.Crop,
            )
        } else {
            Surface(modifier = Modifier.size(28.dp), shape = RoundedCornerShape(6.dp), color = MaterialTheme.colorScheme.surfaceVariant) {
                Box(contentAlignment = Alignment.Center) {
                    Text(app.appName.take(1), style = MaterialTheme.typography.bodySmall)
                }
            }
        }
    }
}
