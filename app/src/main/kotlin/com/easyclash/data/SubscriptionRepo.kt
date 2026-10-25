package com.easyclash.data

import android.content.Context
import android.net.Uri
import com.easyclash.core.ConfigBuilder
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.File
import java.net.InetSocketAddress
import java.net.Proxy
import java.util.UUID
import java.util.concurrent.TimeUnit

/**
 * 订阅的下载、解析、保存。
 */
class SubscriptionRepo(private val ctx: Context, private val store: Store) {

    class SubError(msg: String) : Exception(msg)

    private fun client(viaLocalProxy: Int?): OkHttpClient {
        val b = OkHttpClient.Builder()
            .connectTimeout(20, TimeUnit.SECONDS)
            .readTimeout(60, TimeUnit.SECONDS)
            .followRedirects(true)
        // VPN 已连接时，通过内核的本地端口下载，避免订阅地址被墙时更新失败
        if (viaLocalProxy != null) {
            b.proxy(Proxy(Proxy.Type.HTTP, InetSocketAddress("127.0.0.1", viaLocalProxy)))
        }
        return b.build()
    }

    /**
     * 新增订阅：下载并保存。成功返回订阅对象，失败抛 SubError（信息可直接展示）。
     * @param viaLocalProxy 内核运行中时传本地混合端口，否则 null
     */
    suspend fun add(url: String, name: String?, viaLocalProxy: Int?): Subscription = withContext(Dispatchers.IO) {
        val clean = normalizeUrl(url)
        val sub = Subscription(id = UUID.randomUUID().toString(), name = name?.ifBlank { null } ?: "订阅", url = clean)
        download(sub, viaLocalProxy)
        store.upsertSub(sub)
        if (store.settings.value.selectedSubId == null) store.update { it.selectedSubId = sub.id }
        sub
    }

    /** 更新已有订阅 */
    suspend fun refresh(sub: Subscription, viaLocalProxy: Int?): Subscription = withContext(Dispatchers.IO) {
        val copy = sub.copy()
        download(copy, viaLocalProxy)
        store.upsertSub(copy)
        copy
    }

    /** 从本地文件导入（文件管理器选择的 YAML） */
    suspend fun importFromText(text: String, name: String): Subscription = withContext(Dispatchers.IO) {
        val count = ConfigBuilder.countProxies(text)
        if (count < 0) throw SubError("文件不是有效的 Clash 配置（需要包含 proxies 或 proxy-providers）")
        val sub = Subscription(id = UUID.randomUUID().toString(), name = name, url = "", lastUpdate = System.currentTimeMillis(), nodeCount = count)
        store.subFile(sub.id).writeText(text)
        store.upsertSub(sub)
        if (store.settings.value.selectedSubId == null) store.update { it.selectedSubId = sub.id }
        sub
    }

    private fun download(sub: Subscription, viaLocalProxy: Int?) {
        if (sub.url.isBlank()) throw SubError("这个订阅是从文件导入的，没有更新地址")
        val req = Request.Builder()
            .url(sub.url)
            // 机场看到 clash 字样的 UA 才会返回 Clash 格式配置
            .header("User-Agent", "clash.meta/1.19.0 mihomo EasyClash/1.0")
            .header("Accept", "*/*")
            .build()
        val resp = try {
            client(viaLocalProxy).newCall(req).execute()
        } catch (e: Exception) {
            throw SubError("无法连接订阅服务器：${e.message ?: e.javaClass.simpleName}\n如果手机当前只能通过代理访问该地址，请先连接后再更新。")
        }
        resp.use {
            if (!it.isSuccessful) throw SubError("订阅服务器返回错误 ${it.code}，请检查链接是否过期")
            val body = it.body?.string() ?: throw SubError("订阅内容为空")
            val count = ConfigBuilder.countProxies(body)
            if (count < 0) {
                val hint = if (looksLikeBase64(body)) "这个链接返回的是 V2Ray/SS 分享链接格式，不是 Clash 格式。请回到机场网站选择「Clash」或「Mihomo」订阅链接。"
                else "订阅内容不是有效的 Clash 配置。"
                throw SubError(hint)
            }
            if (count == 0) throw SubError("订阅里没有任何节点，可能已到期或流量用尽")

            store.subFile(sub.id).writeText(body)
            sub.lastUpdate = System.currentTimeMillis()
            sub.nodeCount = count

            // 机场常用的流量信息头：upload=..; download=..; total=..; expire=..
            it.header("subscription-userinfo")?.let { info -> parseUserInfo(sub, info) }
            // 机场提供的名称
            it.header("content-disposition")?.let { cd ->
                Regex("filename\\*?=(?:UTF-8'')?\"?([^\";]+)").find(cd)?.groupValues?.get(1)?.let { fn ->
                    val decoded = runCatching { Uri.decode(fn) }.getOrDefault(fn).removeSuffix(".yaml").removeSuffix(".yml")
                    if (sub.name == "订阅" && decoded.isNotBlank()) sub.name = decoded
                }
            }
            it.header("profile-update-interval")?.toIntOrNull()?.let { h -> if (h > 0) sub.updateIntervalHours = h }
        }
    }

    private fun parseUserInfo(sub: Subscription, info: String) {
        info.split(";").forEach { kv ->
            val (k, v) = kv.trim().split("=", limit = 2).let { if (it.size == 2) it[0] to it[1] else return@forEach }
            val n = v.trim().toDoubleOrNull()?.toLong() ?: return@forEach
            when (k.trim()) {
                "upload" -> sub.upload = n
                "download" -> sub.download = n
                "total" -> sub.total = n
                "expire" -> sub.expire = n
            }
        }
    }

    private fun looksLikeBase64(s: String): Boolean {
        val t = s.trim()
        return t.length > 20 && t.lines().size < 5 && Regex("^[A-Za-z0-9+/=_-]+$").matches(t)
    }

    /** 支持直接粘贴 clash://install-config?url=... 这类一键导入链接 */
    fun normalizeUrl(raw: String): String {
        val t = raw.trim()
        if (t.startsWith("clash://") || t.startsWith("clashmeta://")) {
            val u = Uri.parse(t).getQueryParameter("url")
            if (!u.isNullOrBlank()) return u
        }
        if (!t.startsWith("http://") && !t.startsWith("https://")) throw SubError("订阅链接必须以 http:// 或 https:// 开头")
        return t
    }

    fun rawYaml(sub: Subscription): String? = store.subFile(sub.id).takeIf(File::exists)?.readText()
}
