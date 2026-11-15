package com.easyclash.core

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.net.InetSocketAddress
import java.net.Proxy
import java.util.concurrent.TimeUnit

/**
 * 锁定地区时核实节点的实际出口（电脑版对应 desktop/regionverify.go，两边逻辑相同）。
 *
 * 节点名写着「日本」，实际出口不一定在日本。锁定地区后，EasyClash 在地区自动组（url-test）里替内核挑节点：
 *  1. 按延迟从快到慢排候选；当前钉住的节点还活着、且不比最快的慢 50 ms 以上（和 url-test 的 tolerance 一致）就不动；
 *  2. 否则依次核实候选：经专用的本地入口（[LISTENER]，出站固定为 [GROUP]，把这个组切到候选节点）查 ip-api.com，
 *     国家代码和锁定的地区一致才「钉住」（url-test 的固定选择，PUT /proxies/组）；
 *  3. 不一致的写一条「[地区不符]」日志、跳过；全都不符合就保持原来的节点不动。
 *
 * 钉住后内核仍会在节点断开时（5 秒内 5 次连接失败或被拒）立刻自己改用最快的可用节点，网络不断；
 * 每 30 秒检查一次，发现当前用的不是核实过的节点就重新核实。解除锁定时取消钉住（DELETE /proxies/组）。
 * 核实结果缓存 6 小时；ip-api.com 免费版每分钟 45 次，所以每轮最多查 [MAX_CHECKS] 个新节点。
 */
object RegionVerify {
    /** 核实专用的选择组（包含全部节点，界面上不显示） */
    const val GROUP = "EasyClash·核实出口"
    const val LISTENER = "easyclash-verify"
    private const val TTL_MS = 6 * 3600_000L
    private const val TOLERANCE_MS = 50
    private const val MAX_CHECKS = 5

    private data class Check(val code: String, val where: String, val ip: String, val at: Long)

    private val cache = HashMap<String, Check>()
    private val pinned = HashMap<String, String>()   // 地区组 → 钉住的节点
    private val lock = Mutex()

    /** 连接断开 / 换订阅时清空（节点可能变了） */
    fun reset() = synchronized(cache) { cache.clear(); pinned.clear() }

    /** 解除钉住：内核恢复按延迟自动选择 */
    suspend fun unpin(api: CoreApi, group: String) {
        synchronized(cache) { pinned.remove(group) }
        runCatching { api.unfix(group) }
    }

    /** 没锁定的地区组都取消钉住（store-selected 会把钉住记录到下次连接，解除锁定时可能没连着） */
    suspend fun unpinAllExcept(api: CoreApi, regions: List<Region>, keep: String?) {
        for (r in regions) if (r.groupName != keep) unpin(api, r.groupName)
    }

    /**
     * 核实并钉住。force：锁定地区刚变化、或刚测过速时为 true，忽略「当前节点仍然合格」的判断，重新排一次。
     * 正在核实时直接返回（定时检查和手动测速可能同时触发）。
     */
    suspend fun ensure(api: CoreApi, region: Region, port: Int, log: (String) -> Unit, force: Boolean = false) {
        if (!lock.tryLock()) return
        try {
            val px = api.proxies()
            val group = px[region.groupName] ?: return
            val nodes = group.all.mapNotNull { px[it] }
                .filter { !it.isGroup && it.delay > 0 && it.alive && !Regions.infoNodeRegex.containsMatchIn(it.name) }
                .sortedBy { it.delay }
            if (nodes.isEmpty()) return
            val best = nodes.first()
            val cur = synchronized(cache) { pinned[region.groupName] }
            val curItem = cur?.let { c -> nodes.firstOrNull { it.name == c } }
            val curOk = curItem != null && cached(curItem.name)?.code == region.code

            if (curOk && (!force || cur == group.now) && curItem!!.delay <= best.delay + TOLERANCE_MS) {
                if (group.now != cur) runCatching { api.select(region.groupName, cur!!) }   // 被内核临时换走后又恢复了，钉回来
                return
            }

            var checks = 0
            for (n in nodes) {
                // 钉住的节点仍合格且是剩下候选里最快的，就不用换
                if (n.name == cur && curOk) {
                    if (group.now != cur) runCatching { api.select(region.groupName, cur) }
                    return
                }
                var c = cached(n.name)
                if (c == null) {
                    if (checks >= MAX_CHECKS) break
                    checks++
                    c = runCatching { queryVia(api, port, n.name) }.getOrElse {
                        log("[地区核实] 节点「${n.name}」查不到出口（${it.message ?: "网络错误"}），跳过")
                        null
                    } ?: continue
                    synchronized(cache) { cache[n.name] = c }
                    if (c.code != region.code) {
                        log("[地区不符] 节点「${n.name}」标的是${region.displayName}，实际出口在 ${c.where}（${c.code}，IP ${c.ip}），已跳过")
                    }
                }
                if (c.code == region.code) {
                    runCatching { api.select(region.groupName, n.name) }.onSuccess {
                        synchronized(cache) { pinned[region.groupName] = n.name }
                        if (n.name != cur) log("[地区核实] ${region.displayName}：切到「${n.name}」（${n.delay} ms，出口 ${c.where}，IP ${c.ip}）")
                    }
                    return
                }
            }
            log("[地区核实] ${region.displayName}：没有找到出口确实在${region.displayName}、又能连通的节点，" +
                (if (cur != null) "保持「$cur」不变" else "保持当前选择不变"))
        } finally {
            lock.unlock()
        }
    }

    /**
     * 锁定地区变化后（连接、重载、首页切换锁定）调用：别的地区组取消钉住；锁定了地区就先测一次该地区组，再核实挑节点。
     * region 为 null 表示没锁定。
     */
    suspend fun onLockChanged(api: CoreApi, regions: List<Region>, region: Region?, port: Int, log: (String) -> Unit) {
        unpinAllExcept(api, regions, region?.groupName)
        if (region == null) return
        runCatching { api.groupDelay(region.groupName) }
        ensure(api, region, port, log, force = true)
    }

    private fun cached(node: String): Check? = synchronized(cache) {
        cache[node]?.takeIf { System.currentTimeMillis() - it.at < TTL_MS }
    }

    /** 把核实组切到这个节点，再经核实专用入口查出口 IP */
    private suspend fun queryVia(api: CoreApi, port: Int, node: String): Check {
        api.select(GROUP, node)
        return withContext(Dispatchers.IO) {
            val c = OkHttpClient.Builder()
                .proxy(Proxy(Proxy.Type.HTTP, InetSocketAddress("127.0.0.1", port)))
                .connectTimeout(8, TimeUnit.SECONDS).readTimeout(8, TimeUnit.SECONDS).build()
            val req = Request.Builder()
                .url("http://ip-api.com/json/?lang=zh-CN&fields=status,message,country,countryCode,regionName,city,query")
                .header("User-Agent", "EasyClash").build()
            c.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) throw IllegalStateException("HTTP ${resp.code}")
                val o = JSONObject(resp.body?.string() ?: "{}")
                if (o.optString("status") != "success") throw IllegalStateException(o.optString("message").ifBlank { "查询失败" })
                val where = listOf(o.optString("country"), o.optString("regionName"), o.optString("city"))
                    .filter { it.isNotBlank() }.distinct().joinToString(" · ")
                Check(o.optString("countryCode").uppercase(), where, o.optString("query"), System.currentTimeMillis())
            }
        }
    }
}
