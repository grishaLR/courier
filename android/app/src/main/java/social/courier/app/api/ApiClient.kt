package social.courier.app.api

import com.google.gson.GsonBuilder
import okhttp3.OkHttpClient
import retrofit2.Retrofit
import retrofit2.converter.gson.GsonConverterFactory
import social.courier.app.BuildConfig
import java.util.concurrent.TimeUnit

object ApiClient {
    private const val BASE_URL_DEBUG = "http://10.0.2.2:8080/" // Android emulator → host
    private const val BASE_URL_PROD = "https://api.courier.social/"

    private val client = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(10, TimeUnit.SECONDS)
        .build()

    private val gson = GsonBuilder().create()

    val api: CourierApi by lazy {
        val baseUrl = if (BuildConfig.DEBUG) BASE_URL_DEBUG else BASE_URL_PROD
        Retrofit.Builder()
            .baseUrl(baseUrl)
            .client(client)
            .addConverterFactory(GsonConverterFactory.create(gson))
            .build()
            .create(CourierApi::class.java)
    }
}
