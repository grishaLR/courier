package social.courier.app.model

data class AppInfo(
    val collectionPrefix: String,
    val appName: String,
    val appUrl: String,
    val category: String,
    val description: String? = null,
    val faviconUrl: String? = null,
    val alternativeFor: String? = null
)

data class AppGroup(
    val category: String,
    val apps: List<AppInfo>
)

data class UserAppsResponse(
    val yourApps: List<AppGroup>,
    val discoverApps: List<AppGroup>?
)
