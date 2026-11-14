package com.easyclash.data

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONArray
import java.io.File
import java.net.InetSocketAddress
import java.net.Proxy
import java.util.concurrent.TimeUnit

/**
 * 检查更新与原地升级（安卓）。
 *
 * 版本信息来自 GitHub Releases 接口（最近 3 个正式版，含发布说明）。升级：下载新 APK，交给系统安装器覆盖安装。
 * 所有版本签名相同，覆盖安装保留订阅和设置。本应用自己的流量不经过 VPN 隧道，所以连接时改走内核的本地端口，
 * 否则直连——直连 GitHub 在国内常常不通，界面上会提示。
 */
object Updater {
    private const val API = "https://api.github.com/repos/icangotoanywhere/EasyClash/releases?per_page=3"

    /** 检查和下载失败时附上的提示 */
    const val GITHUB_HINT = "检查和下载新版本都要访问 GitHub：请先连接 EasyClash（连接后会自动走代理下载），或确认手机能打开 github.com，再试一次。"

    data class Release(val version: String, val date: String, val notes: String, val apkUrl: String?, val apkSize: Long)

    fun currentVersion(ctx: Context): String =
        runCatching { ctx.packageManager.getPackageInfo(ctx.packageName, 0).versionName }.getOrNull() ?: "?"

    /** a 是否比 b 新（三段数字比较） */
    fun newer(a: String, b: String): Boolean {
        fun parts(v: String) = v.trim().removePrefix("v").split(".").map { p -> p.filter(Char::isDigit).toIntOrNull() ?: 0 } + listOf(0, 0, 0)
        val pa = parts(a); val pb = parts(b)
        for (i in 0 until 3) if (pa[i] != pb[i]) return pa[i] > pb[i]
        return false
    }

    private fun client(viaLocalProxy: Int?, readSec: Long): OkHttpClient {
        val b = OkHttpClient.Builder().connectTimeout(20, TimeUnit.SECONDS).readTimeout(readSec, TimeUnit.SECONDS).followRedirects(true)
        if (viaLocalProxy != null) b.proxy(Proxy(Proxy.Type.HTTP, InetSocketAddress("127.0.0.1", viaLocalProxy)))
        return b.build()
    }

    /** 先走本地代理（若已连接），失败再直连 */
    private fun <T> withRoutes(viaLocalProxy: Int?, block: (Int?) -> T): T {
        val routes: List<Int?> = if (viaLocalProxy != null) listOf(viaLocalProxy, null) else listOf(null)
        var last: Throwable? = null
        for (r in routes) {
            try { return block(r) } catch (e: Throwable) { last = e }
        }
        throw last ?: IllegalStateException("网络错误")
    }

    suspend fun fetchReleases(ctx: Context, viaLocalProxy: Int?): List<Release> = withContext(Dispatchers.IO) {
        withRoutes(viaLocalProxy) { port ->
            val req = Request.Builder().url(API).header("User-Agent", "EasyClash/" + currentVersion(ctx)).build()
            client(port, 30).newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) throw IllegalStateException("HTTP ${resp.code}")
                val arr = JSONArray(resp.body?.string() ?: "[]")
                (0 until arr.length()).map { arr.getJSONObject(it) }
                    .filter { !it.optBoolean("draft") && !it.optBoolean("prerelease") }
                    .map { o ->
                        val ver = o.optString("tag_name").removePrefix("v")
                        val assets = o.optJSONArray("assets") ?: JSONArray()
                        var url: String? = null; var size = 0L
                        for (i in 0 until assets.length()) {
                            val a = assets.getJSONObject(i)
                            if (a.optString("name") == "EasyClash-v$ver.apk") { url = a.optString("browser_download_url"); size = a.optLong("size") }
                        }
                        Release(ver, o.optString("published_at").take(10), o.optString("body").trim(), url, size)
                    }
            }
        }
    }

    /** 下载 APK，onProgress(已下载, 总大小) */
    suspend fun download(ctx: Context, rel: Release, viaLocalProxy: Int?, onProgress: (Long, Long) -> Unit): File = withContext(Dispatchers.IO) {
        val url = rel.apkUrl ?: throw IllegalStateException("这个版本没有安卓安装包")
        val dir = File(ctx.cacheDir, "update").apply { deleteRecursively(); mkdirs() }
        val out = File(dir, "EasyClash-v${rel.version}.apk")
        withRoutes(viaLocalProxy) { port ->
            val req = Request.Builder().url(url).header("User-Agent", "EasyClash/" + currentVersion(ctx)).build()
            client(port, 120).newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) throw IllegalStateException("HTTP ${resp.code}")
                val body = resp.body ?: throw IllegalStateException("下载内容为空")
                val total = body.contentLength().takeIf { it > 0 } ?: rel.apkSize
                var done = 0L
                body.byteStream().use { input ->
                    out.outputStream().use { o ->
                        val buf = ByteArray(64 * 1024)
                        while (true) {
                            val n = input.read(buf); if (n < 0) break
                            o.write(buf, 0, n); done += n; onProgress(done, total)
                        }
                    }
                }
                if (total > 0 && done != total) throw IllegalStateException("下载不完整")
            }
        }
        out
    }

    /** 安卓 8+ 需要用户允许本应用「安装未知应用」 */
    fun canInstall(ctx: Context): Boolean = Build.VERSION.SDK_INT < 26 || ctx.packageManager.canRequestPackageInstalls()

    fun unknownSourcesIntent(ctx: Context): Intent =
        Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${ctx.packageName}")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    /** 交给系统安装器覆盖安装 */
    fun install(ctx: Context, apk: File) {
        val uri = FileProvider.getUriForFile(ctx, ctx.packageName + ".files", apk)
        ctx.startActivity(
            Intent(Intent.ACTION_VIEW).setDataAndType(uri, "application/vnd.android.package-archive")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK),
        )
    }

    /** 发布说明是 Markdown，界面上去掉标题和加粗符号 */
    fun plainNotes(md: String): String =
        md.lines().joinToString("\n") { it.replace(Regex("^#+\\s*"), "").replace("**", "").replace("`", "") }.trim()
}
