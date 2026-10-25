package com.easyclash.core

/**
 * "优先 / 备用"两级选择。
 *
 * 地区分级后，内核里是：
 *   🇸🇬 新加坡·自动（选择组） → 🇸🇬 新加坡·优先（测速组） / 🇸🇬 新加坡·备用（测速组）
 * 内核只负责在每一级里挑最快的节点；选哪一级由这里决定：
 *   优先级里最快的 比 备用级里最快的 慢 超过 penaltyMs → 用备用，否则用优先。
 * 优先级全部超时 → 备用；备用全部超时 → 优先。
 */
object TierPicker {

    data class Result(
        val chosen: String,        // 选中的子组名
        val primaryMin: Int?,      // 优先级最快延迟（ms），null = 全部超时
        val backupMin: Int?,
        val changed: Boolean,      // 相比之前是否换了级
    ) {
        fun describe(region: Region, penaltyMs: Int): String {
            val p = primaryMin?.let { "${it}ms" } ?: "全部超时"
            val b = backupMin?.let { "${it}ms" } ?: "全部超时"
            val why = when {
                primaryMin == null && backupMin == null -> "两级都超时，保持优先"
                primaryMin == null -> "优先全部超时，改用备用"
                backupMin == null -> "备用全部超时，用优先"
                chosen == region.backupGroup -> "优先比备用慢 ${primaryMin - backupMin}ms，超过 ${penaltyMs}ms，改用备用"
                else -> "优先没有比备用慢 ${penaltyMs}ms 以上，用优先"
            }
            return "${region.flag} ${region.name}：优先 $p / 备用 $b → $why"
        }
    }

    /** 测两级的所有节点，然后把地区自动组指到该用的那一级 */
    suspend fun run(api: CoreApi, region: Region, penaltyMs: Int): Result {
        val p = api.groupDelay(region.primaryGroup)
        val b = api.groupDelay(region.backupGroup)
        val pMin = p.values.filter { it > 0 }.minOrNull()
        val bMin = b.values.filter { it > 0 }.minOrNull()
        val chosen = when {
            pMin == null && bMin == null -> region.primaryGroup
            pMin == null -> region.backupGroup
            bMin == null -> region.primaryGroup
            pMin > bMin + penaltyMs -> region.backupGroup
            else -> region.primaryGroup
        }
        val before = api.proxy(region.groupName)?.now
        if (before != chosen) runCatching { api.select(region.groupName, chosen) }
        return Result(chosen, pMin, bMin, before != chosen)
    }
}
