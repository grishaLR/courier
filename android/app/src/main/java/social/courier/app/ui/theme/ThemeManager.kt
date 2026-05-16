package social.courier.app.ui.theme

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

enum class AppTheme { System, Light, Dark }

class ThemeManager(context: Context) {
    private val prefs: SharedPreferences =
        context.getSharedPreferences("courier_theme", Context.MODE_PRIVATE)

    private val _theme = MutableStateFlow(loadTheme())
    val theme: StateFlow<AppTheme> = _theme

    fun setTheme(theme: AppTheme) {
        prefs.edit().putString("theme", theme.name).apply()
        _theme.value = theme
    }

    private fun loadTheme(): AppTheme =
        runCatching { AppTheme.valueOf(prefs.getString("theme", AppTheme.System.name)!!) }
            .getOrDefault(AppTheme.System)
}
