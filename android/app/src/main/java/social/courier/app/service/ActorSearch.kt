package social.courier.app.service

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.Request
import java.net.URLEncoder

@Serializable
data class ActorSearchResult(
    val did: String,
    val handle: String,
    val displayName: String? = null,
    val avatar: String? = null,
)

@Serializable
private data class TypeaheadResponse(val actors: List<ActorSearchResult>)

private val typeaheadJson = Json { ignoreUnknownKeys = true }

suspend fun searchActorsTypeahead(query: String): List<ActorSearchResult> =
    withContext(Dispatchers.IO) {
        val encoded = URLEncoder.encode(query, "UTF-8")
        val request = Request.Builder()
            .url("https://typeahead.waow.tech/xrpc/app.bsky.actor.searchActorsTypeahead?q=$encoded&limit=8")
            .header("X-Client", "courier.social")
            .build()
        val body = runCatching {
            CourierHttpClient.client.newCall(request).execute().use { it.body?.string() }
        }.getOrNull() ?: return@withContext emptyList()
        runCatching {
            typeaheadJson.decodeFromString<TypeaheadResponse>(body).actors
        }.getOrDefault(emptyList())
    }
