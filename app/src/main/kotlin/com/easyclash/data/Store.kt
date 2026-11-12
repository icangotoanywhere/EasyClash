package com.easyclash.data

import android.content.Context
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/**
 * 本地持久化：SharedPreferences 里存 JSON，订阅原始 YAML 存在 files/subs/ 下。
 * 对外用 StateFlow 暴露，界面自动跟随刷新。
 */
class Store(private val ctx: Context) {
    private val prefs = ctx.getSharedPreferences("easyclash", Context.MODE_PRIVATE)

    private val _subs = MutableStateFlow(loadSubs())
    val subs: StateFlow<List<Subscription>> = _subs

    private val _settings = MutableStateFlow(loadSettings())
    val settings: StateFlow<Settings> = _settings

    // ---------- 目录 ----------
    val subsDir: File get() = File(ctx.filesDir, "subs").apply { mkdirs() }
    val runDir: File get() = File(ctx.filesDir, "run").apply { mkdirs() }   // 生成的最终配置
    val homeDir: File get() = File(ctx.filesDir, "home").apply { mkdirs() } // 内核工作目录（geo 数据库、缓存）

    fun subFile(id: String) = File(subsDir, "$id.yaml")

    // ---------- 订阅 ----------
    private fun loadSubs(): List<Subscription> {
        val raw = prefs.getString("subs", null) ?: return emptyList()
        return runCatching {
            val arr = JSONArray(raw)
            (0 until arr.length()).map { Subscription.fromJson(arr.getJSONObject(it)) }
        }.getOrDefault(emptyList())
    }

    fun saveSubs(list: List<Subscription>) {
        val arr = JSONArray().also { a -> list.forEach { a.put(it.toJson()) } }
        prefs.edit().putString("subs", arr.toString()).apply()
        _subs.value = list.map { it.copy() }
    }

    fun upsertSub(sub: Subscription) {
        val list = _subs.value.toMutableList()
        val i = list.indexOfFirst { it.id == sub.id }
        if (i >= 0) list[i] = sub else list.add(sub)
        saveSubs(list)
    }

    fun removeSub(id: String) {
        saveSubs(_subs.value.filter { it.id != id })
        subFile(id).delete()
        if (_settings.value.selectedSubId == id) update { it.selectedSubId = _subs.value.firstOrNull()?.id }
    }

    fun selectedSub(): Subscription? = _subs.value.firstOrNull { it.id == _settings.value.selectedSubId }

    // ---------- 设置 ----------
    private fun loadSettings(): Settings {
        val raw = prefs.getString("settings", null) ?: return Settings()
        return runCatching { Settings.fromJson(JSONObject(raw)) }.getOrDefault(Settings())
    }

    /** 修改设置并保存。用法：store.update { it.ipv6 = true } */
    fun update(block: (Settings) -> Unit) {
        val s = Settings.fromJson(_settings.value.toJson()) // 深拷贝
        block(s)
        prefs.edit().putString("settings", s.toJson().toString()).apply()
        _settings.value = s
    }
}
