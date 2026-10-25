package com.easyclash.core

import com.easyclash.data.ProxyItem
import com.easyclash.data.Store

/**
 * 锁定地区：让所有走代理的流量都从选中的地区出去。
 *
 *  - 先把「🌍 地区选择」指到该地区的自动测速组；
 *  - ★ 主组（MATCH 规则指向的组）一律切到「🌍 地区选择」；
 *  - 其他手动选择组，当前选的是具体节点或自动测速 / 故障转移组时，也切过去；
 *  - 当前选的是另一个手动选择组（如「🚀 节点选择」）的不动——它跟着那个组走；
 *  - 当前选的是直连 / 拒绝的不动（国内网站、广告拦截照旧）。
 *
 * 切之前记下原来的选择，解除锁定时恢复；锁定期间用户在某个组里手动选了别的，就尊重这个选择（记为例外）。
 * 「🌍 地区选择」这一项由 [ConfigBuilder] 加到每个能选代理的手动组末尾。
 */
object RegionLock {
    private val NON_PROXY = setOf("direct", "reject", "rejectdrop", "pass", "compatible")
    private const val SEL = ConfigBuilder.REGION_SELECTOR

    private fun isNonProxy(p: ProxyItem) = p.type.lowercase() in NON_PROXY

    /** 沿着策略组走到底，最终是不是一个真正的代理节点 */
    private fun leadsToProxy(px: Map<String, ProxyItem>, start: String): Boolean {
        var cur = start
        repeat(8) {
            val p = px[cur] ?: return false
            if (!p.isGroup) return !isNonProxy(p)
            cur = p.now ?: return false
        }
        return false
    }

    /** 锁定时需要切到「地区选择」的组 */
    fun targets(px: Map<String, ProxyItem>, mainGroup: String, exempt: Set<String>): List<String> {
        val out = mutableListOf<String>()
        for (g in px.values) {
            if (!g.isSelectable || g.name == SEL || g.name == "GLOBAL" || g.now == SEL || g.name in exempt || SEL !in g.all) continue
            if (g.name == mainGroup) { out += g.name; continue }
            val t = px[g.now ?: continue] ?: continue
            val switch = when {
                !t.isGroup -> !isNonProxy(t)          // 具体节点
                t.isSelectable -> false               // 另一个手动组：跟着它走
                else -> leadsToProxy(px, t.name)      // 自动测速 / 故障转移 / 负载均衡组
            }
            if (switch) out += g.name
        }
        return out
    }

    /** 锁定（或重新应用锁定）到某个地区 */
    suspend fun apply(api: CoreApi, store: Store, mainGroup: String, region: Region) {
        api.select(SEL, region.groupName)
        val px = api.proxies()
        val s = store.settings.value
        val saved = s.regionSaved.toMutableMap()
        for (g in targets(px, mainGroup, s.regionExempt)) {
            val now = px[g]?.now ?: continue
            if (g !in saved) saved[g] = now
            runCatching { api.select(g, SEL) }
        }
        store.update { it.regionSaved = saved }
    }

    /** 解除锁定：把锁定时切过去的组恢复原来的选择（skip 是用户刚手动改过、不用恢复的组） */
    suspend fun release(api: CoreApi, store: Store, mainGroup: String, skip: String? = null) {
        val px = runCatching { api.proxies() }.getOrDefault(emptyMap())
        val saved = store.settings.value.regionSaved
        suspend fun restore(g: String, prev: String?) {
            val grp = px[g] ?: return
            if (grp.now != SEL) return
            val target = prev?.takeIf { it in grp.all && it != SEL } ?: grp.all.firstOrNull { it != SEL } ?: return
            runCatching { api.select(g, target) }
        }
        for ((g, prev) in saved) if (g != skip) restore(g, prev)
        // 没有记录的主组（旧版本锁定的）退回第一个非地区选择的条目
        if (mainGroup !in saved && mainGroup != skip) restore(mainGroup, null)
        store.update { it.regionSaved = mutableMapOf(); it.regionExempt = mutableSetOf() }
    }
}
