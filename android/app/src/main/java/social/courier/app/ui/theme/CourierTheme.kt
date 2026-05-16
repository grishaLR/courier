package social.courier.app.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.graphics.Color

private val Pink = Color(0xFFFF2D78)
private val PinkDark = Color(0xFFFF6EA8)
private val PinkContainer = Color(0xFF5C0020)

private val LightColors = lightColorScheme(
    primary = Pink,
    onPrimary = Color.White,
    primaryContainer = Color(0xFFFFD9E2),
    onPrimaryContainer = Color(0xFF3E0019),
    secondary = Color(0xFF9C27B0),
    tertiary = Color(0xFF2196F3),
)

private val DarkColors = darkColorScheme(
    primary = PinkDark,
    onPrimary = Color(0xFF5C0020),
    primaryContainer = PinkContainer,
    onPrimaryContainer = Color(0xFFFFD9E2),
    secondary = Color(0xFFCE93D8),
    tertiary = Color(0xFF90CAF9),
)

@Composable
fun CourierTheme(themeManager: ThemeManager, content: @Composable () -> Unit) {
    val theme by themeManager.theme.collectAsState()
    val isDark = when (theme) {
        AppTheme.Dark -> true
        AppTheme.Light -> false
        AppTheme.System -> isSystemInDarkTheme()
    }
    MaterialTheme(
        colorScheme = if (isDark) DarkColors else LightColors,
        content = content,
    )
}

