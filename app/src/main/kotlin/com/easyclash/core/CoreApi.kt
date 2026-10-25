package com.easyclash.core

import com.easyclash.data.ConnectionItem
import com.easyclash.data.ProxyItem
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.Response
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/**
 * 通过内核自带的 RESTful 接口（external-controller）查询与控制。
 * 所有方法都是挂起函数，在 IO 线程执行；失败抛出带中文说明的异常。
 */
class CoreApi(private val port: Int, private val secret: String) {

    class ApiError(msg: String) : Exception(msg)

    private val base = "http://127.0.0.1:$port"
    private val json = "application/json; charset=utf-8".toMediaType()
    private val client = OkHttpClient.Builder()
        .connectTimeout(3, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    private fun req(path: String) = Request.Builder().url(base + path).header("Authorization", "Bearer $secret")

    private suspend fun call(b: Request.Builder): String = withContext(Dispatchers.IO) {
        val resp: Response = try { client.newCall(b.build()).execute() } catch (e: Exception) {
            throw ApiError("连不上内核：${e.message}")
        }
        resp.use {
            val body = it.body?.string().orEmpty()
            if (!it.isSuccessful) {
                val msg = runCatching { JSONObject(body).optString("message") }.getOrNull().orEmpty()
                throw ApiError(if (msg.isNotBlank()) msg else "内核返回 ${it.code}")
            }
            body
        }
    }

    suspend fun version(): String = runCatching { JSONObject(call(req("/version"))).optString("version") }.getOrDefault("?")

    suspend fun ping(): Boolean = runCatching { call(req("/version")); true }.getOrDefault(false)

    /** 全部代理与策略组 */
    suspend fun proxies(): Map<String, ProxyItem> {
        val obj = JSONObject(call(req("/proxies"))).getJSONObject("proxies")
        val out = LinkedHashMap<String, ProxyItem>()
        for (name in obj.keys()) out[name] = parseProxy(name, obj.getJSONObject(name))
        return out
    }

    private fun parseProxy(name: String, o: JSONObject): ProxyItem {
        val all = o.optJSONArray("all")?.let { a -> (0 until a.length()).map { a.getString(it) } } ?: emptyList()
        val hist = o.optJSONArray("history")
        val delay = if (hist != null && hist.length() > 0) hist.getJSONObject(hist.length() - 1).optInt("delay", 0) else 0
        return ProxyItem(
            name = name,
            type = o.optString("type"),
            now = o.optString("now").ifBlank { null },
            all = all,
            delay = delay,
            alive = o.optBoolean("alive", true),
        )
    }

    /** 在策略组里选中某个条目 */
    suspend fun select(group: String, name: String) {
        val body = JSONObject().put("name", name).toString().toRequestBody(json)
        call(req("/proxies/${enc(group)}").put(body))
    }

    /** 测单个节点延迟，返回毫秒；超时或失败返回 0 */
    suspend fun delay(name: String, timeoutMs: Int = 5000): Int = runCatching {
        JSONObject(call(req("/proxies/${enc(name)}/delay?timeout=$timeoutMs&url=${enc("https://www.gstatic.com/generate_204")}"))).optInt("delay", 0)
    }.getOrDefault(0)

    /** 测整个策略组（内核并发测，比逐个测快得多） */
    suspend fun groupDelay(group: String, timeoutMs: Int = 5000): Map<String, Int> = runCatching {
        val o = JSONObject(call(req("/group/${enc(group)}/delay?timeout=$timeoutMs&url=${enc("https://www.gstatic.com/generate_204")}")))
        o.keys().asSequence().associateWith { o.optInt(it, 0) }
    }.getOrDefault(emptyMap())

    /** 内核当前生效的完整配置（JSON 文本），诊断用 */
    suspend fun rawConfig(): String = call(req("/configs"))

    suspend fun mode(): String = runCatching { JSONObject(call(req("/configs"))).optString("mode") }.getOrDefault("rule")

    suspend fun setMode(mode: String) {
        call(req("/configs").patch(JSONObject().put("mode", mode).toString().toRequestBody(json)))
    }

    /** 不断开 VPN 直接重载配置文件 */
    suspend fun reload(path: String) {
        call(req("/configs?force=true").put(JSONObject().put("path", path).toString().toRequestBody(json)))
    }

    suspend fun connections(): Pair<List<ConnectionItem>, Pair<Long, Long>> {
        val o = JSONObject(call(req("/connections")))
        val arr = o.optJSONArray("connections")
        val list = mutableListOf<ConnectionItem>()
        if (arr != null) for (i in 0 until arr.length()) {
            val c = arr.getJSONObject(i)
            val m = c.optJSONObject("metadata") ?: JSONObject()
            val chains = c.optJSONArray("chains")?.let { a -> (0 until a.length()).map { a.getString(it) } } ?: emptyList()
            list += ConnectionItem(
                id = c.optString("id"),
                host = m.optString("host").ifBlank { m.optString("destinationIP") },
                destination = "${m.optString("destinationIP")}:${m.optString("destinationPort")}",
                network = m.optString("network").uppercase(),
                chain = chains.reversed().joinToString(" → "),
                rule = listOf(c.optString("rule"), c.optString("rulePayload")).filter { it.isNotBlank() }.joinToString(" "),
                upload = c.optLong("upload"),
                download = c.optLong("download"),
                start = c.optString("start"),
                process = m.optString("process"),
            )
        }
        return list to (o.optLong("uploadTotal") to o.optLong("downloadTotal"))
    }

    suspend fun closeAllConnections() { runCatching { call(req("/connections").delete()) } }
    suspend fun closeConnection(id: String) { runCatching { call(req("/connections/${enc(id)}").delete()) } }

    /** 更新某个订阅提供者（proxy-provider） */
    suspend fun updateProvider(name: String) { call(req("/providers/proxies/${enc(name)}").put("".toRequestBody(json))) }

    /** 实时流量（WebSocket），每秒回调一次 (上行字节/秒, 下行字节/秒) */
    fun trafficSocket(onData: (Long, Long) -> Unit, onClosed: () -> Unit): WebSocket {
        val r = Request.Builder().url("ws://127.0.0.1:$port/traffic?token=$secret").build()
        return client.newWebSocket(r, object : WebSocketListener() {
            override fun onMessage(webSocket: WebSocket, text: String) {
                runCatching { val o = JSONObject(text); onData(o.optLong("up"), o.optLong("down")) }
            }
            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) = onClosed()
            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = onClosed()
        })
    }

    /** 实时日志（WebSocket） */
    fun logSocket(level: String, onLine: (String, String) -> Unit): WebSocket {
        val r = Request.Builder().url("ws://127.0.0.1:$port/logs?level=$level&token=$secret").build()
        return client.newWebSocket(r, object : WebSocketListener() {
            override fun onMessage(webSocket: WebSocket, text: String) {
                runCatching { val o = JSONObject(text); onLine(o.optString("type"), o.optString("payload")) }
            }
        })
    }

    private fun enc(s: String) = java.net.URLEncoder.encode(s, "UTF-8").replace("+", "%20")
}
