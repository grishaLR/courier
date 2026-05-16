package social.courier.app.ui.inbox

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil.compose.AsyncImage
import kotlinx.coroutines.launch
import social.courier.app.CourierApplication
import social.courier.app.api.ApiClient
import social.courier.app.api.BlogPrefRequest
import social.courier.app.api.BlogSub
import social.courier.app.model.AppGroup
import social.courier.app.model.AppInfo
import social.courier.app.model.CourierNotification
import social.courier.app.model.NotificationType
import social.courier.app.service.NotificationStream
import social.courier.app.ui.onboarding.OnboardingActivity
import social.courier.app.ui.theme.AppTheme
import social.courier.app.ui.theme.CourierTheme

enum class AppTab { Inbox, Preferences }

class MainActivity : ComponentActivity() {
    private val authManager get() = (application as CourierApplication).authManager
    private val themeManager get() = (application as CourierApplication).themeManager
    private val stream = NotificationStream()

    private val requestNotificationPermission =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { /* no-op */ }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED) {
                requestNotificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
            }
        }
        setContent {
            CourierTheme(themeManager) {
                AppShell()
            }
        }
    }

    override fun onStart() {
        super.onStart()
        authManager.did?.let { stream.connect(it, authManager.sessionToken) }
    }

    override fun onStop() {
        super.onStop()
        stream.disconnect()
    }

    @OptIn(ExperimentalMaterial3Api::class)
    @Composable
    fun AppShell() {
        var currentTab by remember { mutableStateOf(AppTab.Inbox) }
        var notifications by remember { mutableStateOf(listOf<CourierNotification>()) }
        var isLoading by remember { mutableStateOf(true) }
        val scope = rememberCoroutineScope()

        LaunchedEffect(Unit) {
            authManager.did?.let { did ->
                try { notifications = ApiClient.api.getNotifications(did) } catch (_: Exception) {}
                isLoading = false
            }
        }

        LaunchedEffect(Unit) {
            stream.notifications.collect { notif ->
                notifications = listOf(notif) + notifications.take(99)
            }
        }

        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text(if (currentTab == AppTab.Inbox) "Inbox" else "Preferences") },
                    navigationIcon = {
                        if (currentTab == AppTab.Inbox) {
                            IconButton(onClick = {
                                scope.launch {
                                    authManager.did?.let { did ->
                                        try {
                                            ApiClient.api.clearNotifications(did)
                                            notifications = emptyList()
                                        } catch (_: Exception) {}
                                    }
                                }
                            }) {
                                Icon(Icons.Default.Delete, "Clear all")
                            }
                        }
                    },
                    actions = {
                        IconButton(onClick = {
                            scope.launch {
                                authManager.signOut()
                                startActivity(Intent(this@MainActivity, OnboardingActivity::class.java))
                                finish()
                            }
                        }) {
                            Icon(Icons.Default.ExitToApp, "Sign out")
                        }
                    },
                )
            },
            bottomBar = {
                NavigationBar {
                    NavigationBarItem(
                        selected = currentTab == AppTab.Inbox,
                        onClick = { currentTab = AppTab.Inbox },
                        icon = { Icon(Icons.Default.Notifications, null) },
                        label = { Text("Inbox") },
                    )
                    NavigationBarItem(
                        selected = currentTab == AppTab.Preferences,
                        onClick = { currentTab = AppTab.Preferences },
                        icon = { Icon(Icons.Default.Settings, null) },
                        label = { Text("Preferences") },
                    )
                }
            },
        ) { padding ->
            when (currentTab) {
                AppTab.Inbox -> InboxContent(
                    notifications = notifications,
                    isLoading = isLoading,
                    padding = padding,
                )
                AppTab.Preferences -> PreferencesContent(padding = padding)
            }
        }
    }

    // ── Inbox ─────────────────────────────────────────────────────────────────

    @Composable
    fun InboxContent(
        notifications: List<CourierNotification>,
        isLoading: Boolean,
        padding: PaddingValues,
    ) {
        var selectedFilter by remember { mutableStateOf<String?>(null) }

        val filters = remember(notifications) {
            notifications.mapNotNull { it.resolvedAppName }.distinct()
        }
        val filtered = remember(notifications, selectedFilter) {
            if (selectedFilter == null) notifications
            else notifications.filter { it.resolvedAppName == selectedFilter }
        }

        Column(modifier = Modifier.padding(padding)) {
            if (filters.isNotEmpty()) {
                LazyRow(
                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    item {
                        FilterChip(
                            selected = selectedFilter == null,
                            onClick = { selectedFilter = null },
                            label = { Text("All") },
                            colors = FilterChipDefaults.filterChipColors(
                                selectedContainerColor = MaterialTheme.colorScheme.primary,
                                selectedLabelColor = MaterialTheme.colorScheme.onPrimary,
                            ),
                        )
                    }
                    items(filters) { app ->
                        FilterChip(
                            selected = selectedFilter == app,
                            onClick = { selectedFilter = if (selectedFilter == app) null else app },
                            label = { Text(app) },
                            colors = FilterChipDefaults.filterChipColors(
                                selectedContainerColor = MaterialTheme.colorScheme.primary,
                                selectedLabelColor = MaterialTheme.colorScheme.onPrimary,
                            ),
                        )
                    }
                }
            }

            when {
                isLoading -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                filtered.isEmpty() -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Icon(Icons.Default.Notifications, null, modifier = Modifier.size(48.dp))
                        Spacer(Modifier.height(8.dp))
                        Text("No notifications yet")
                    }
                }
                else -> LazyColumn {
                    items(filtered, key = { it.uri + it.createdAt }) { notif ->
                        NotificationRow(notif) {
                            notif.deepLink?.let { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(it))) }
                        }
                        HorizontalDivider(modifier = Modifier.padding(start = 72.dp))
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
                .padding(horizontal = 16.dp, vertical = 10.dp),
            verticalAlignment = Alignment.Top,
        ) {
            // App favicon or colored monogram (matches iOS layout)
            val favicon = notification.appFavicon
            if (!favicon.isNullOrEmpty()) {
                AsyncImage(
                    model = favicon,
                    contentDescription = null,
                    modifier = Modifier.size(40.dp).clip(RoundedCornerShape(8.dp)),
                    contentScale = ContentScale.Crop,
                )
            } else {
                val appLabel = notification.resolvedAppName ?: notification.collectionShortName
                Box(
                    modifier = Modifier
                        .size(40.dp)
                        .clip(RoundedCornerShape(8.dp))
                        .background(monogramColor(appLabel)),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        appLabel.take(1).uppercase(),
                        style = MaterialTheme.typography.titleMedium,
                        color = Color.White,
                        fontWeight = FontWeight.Bold,
                    )
                }
            }

            Spacer(Modifier.width(12.dp))

            Column(modifier = Modifier.weight(1f)) {
                // Icon + bold name + secondary action text — all inline like iOS
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(
                        iconForType(notification.type),
                        contentDescription = null,
                        modifier = Modifier.size(13.dp),
                        tint = colorForType(notification.type),
                    )
                    Spacer(Modifier.width(4.dp))
                    Text(
                        buildAnnotatedString {
                            withStyle(SpanStyle(fontWeight = FontWeight.Bold)) {
                                append(notification.displayName)
                            }
                            withStyle(SpanStyle(color = Color.Gray)) {
                                append(" ${notification.actionText}")
                            }
                        },
                        style = MaterialTheme.typography.bodySmall,
                        lineHeight = 18.sp,
                    )
                }
                if (!notification.subjectText.isNullOrBlank()) {
                    Spacer(Modifier.height(2.dp))
                    Text(
                        notification.subjectText,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.primary,
                        maxLines = 1,
                    )
                }
            }
        }
    }

    private fun monogramColor(name: String): Color {
        val colors = listOf(
            Color(0xFF9C27B0), Color(0xFF4CAF50), Color(0xFF2196F3), Color(0xFFFF9800),
            Color(0xFFE91E63), Color(0xFF009688), Color(0xFF3F51B5), Color(0xFF4DB6AC),
            Color(0xFF00BCD4), Color(0xFF795548),
        )
        val hash = name.fold(0) { acc, c -> acc + c.code }
        return colors[Math.abs(hash) % colors.size]
    }

    private fun iconForType(type: NotificationType?): ImageVector = when (type) {
        NotificationType.LIKE -> Icons.Default.Favorite
        NotificationType.FAVORITE -> Icons.Default.Star
        NotificationType.REPLY -> Icons.Default.Reply
        NotificationType.REPOST -> Icons.Default.Repeat
        NotificationType.FOLLOW -> Icons.Default.PersonAdd
        NotificationType.MENTION -> Icons.Default.AlternateEmail
        NotificationType.QUOTE -> Icons.Default.FormatQuote
        NotificationType.STAR -> Icons.Default.Star
        NotificationType.ISSUE -> Icons.Default.Warning
        NotificationType.PULL_REQUEST -> Icons.Default.CallMerge
        NotificationType.RSVP -> Icons.Default.DateRange
        NotificationType.SUBSCRIPTION -> Icons.Default.Email
        NotificationType.REACTION -> Icons.Default.Mood
        NotificationType.PLAY -> Icons.Default.PlayArrow
        NotificationType.RECOMMEND -> Icons.Default.ThumbUp
        NotificationType.VOTE -> Icons.Default.BarChart
        NotificationType.BLOG_POST -> Icons.Default.Description
        NotificationType.GENERIC, NotificationType.UNKNOWN, null -> Icons.Default.Notifications
    }

    private fun colorForType(type: NotificationType?): Color = when (type) {
        NotificationType.LIKE -> Color(0xFFE91E63)
        NotificationType.FAVORITE -> Color(0xFFFFC107)
        NotificationType.REPLY -> Color(0xFF2196F3)
        NotificationType.REPOST -> Color(0xFF4CAF50)
        NotificationType.FOLLOW -> Color(0xFF9C27B0)
        NotificationType.MENTION -> Color(0xFFFF9800)
        NotificationType.QUOTE -> Color(0xFF3F51B5)
        NotificationType.STAR -> Color(0xFFFFC107)
        NotificationType.ISSUE -> Color(0xFF4CAF50)
        NotificationType.PULL_REQUEST -> Color(0xFF9C27B0)
        NotificationType.RSVP -> Color(0xFF009688)
        NotificationType.SUBSCRIPTION -> Color(0xFF4DB6AC)
        NotificationType.REACTION -> Color(0xFFFF9800)
        NotificationType.PLAY -> Color(0xFF00BCD4)
        NotificationType.RECOMMEND -> Color(0xFF4CAF50)
        NotificationType.VOTE -> Color(0xFF3F51B5)
        NotificationType.BLOG_POST -> Color(0xFF2196F3)
        NotificationType.GENERIC, NotificationType.UNKNOWN, null -> Color.Gray
    }

    // ── Preferences ───────────────────────────────────────────────────────────

    @Composable
    fun PreferencesContent(padding: PaddingValues) {
        var yourApps by remember { mutableStateOf(listOf<AppGroup>()) }
        var discoverApps by remember { mutableStateOf(listOf<AppGroup>()) }
        var appPrefs by remember { mutableStateOf(mutableMapOf<String, Boolean>()) }
        var blogSubs by remember { mutableStateOf(listOf<BlogSub>()) }
        var isLoading by remember { mutableStateOf(true) }
        var isSaving by remember { mutableStateOf(false) }
        var isRefreshingBlogs by remember { mutableStateOf(false) }

        var appearanceExpanded by remember { mutableStateOf(false) }
        var yourAppsExpanded by remember { mutableStateOf(false) }
        var discoverAppsExpanded by remember { mutableStateOf(false) }
        var blogSubsExpanded by remember { mutableStateOf(false) }
        var selectedTheme by remember { mutableStateOf(themeManager.theme.value) }
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

        if (isLoading) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }
            return
        }

        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            item {
                Text(
                    "@${authManager.handle ?: authManager.did ?: ""}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(vertical = 8.dp),
                )
            }

            // Appearance
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

            // Blog Subscriptions
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

            // Apps You're On
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
                                    Toast.makeText(this@MainActivity, "Saved", Toast.LENGTH_SHORT).show()
                                } catch (e: Exception) {
                                    Toast.makeText(this@MainActivity, "Error: ${e.message}", Toast.LENGTH_SHORT).show()
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

            // Apps You Could Be On
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
                Text(
                    title,
                    style = MaterialTheme.typography.titleMedium,
                    color = MaterialTheme.colorScheme.primary,
                )
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
