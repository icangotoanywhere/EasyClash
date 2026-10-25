package com.easyclash.ui

import android.app.Application
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.easyclash.EasyClashApp
import com.easyclash.core.ConfigBuilder
import com.easyclash.core.Regions
import com.easyclash.data.AppInfo
import com.easyclash.data.ConnectionItem
import com.easyclash.data.ProxyItem
import com.easyclash.data.ProxyMode
import com.easyclash.data.Settings
import com.easyclash.data.Subscription
import com.easyclash.vpn.EasyClashVpnService
import com.easyclash.vpn.Status
import com.easyclash.vpn.VpnState
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * 界面状态与所有用户操作。界面层只调用这里的方法，不直接碰服务或内核。
 */
class MainViewModel(app: Application) : AndroidViewModel(app) {
    private val jl get() = EasyClashApp.instance
    val store get() = jl.store
    val repo get() = jl.repo

    val subs: StateFlow<List<Subscription>> get() = store.subs
    val settings: StateFlow<Settings> get() = store.settings
    val status: StateFlow<Status> get() = VpnState.status
    val vpnError: StateFlow<String?> get() = VpnState.error
    val traffic: StateFlow<Pair<Long, Long>> get() = VpnState.traffic

    /** 一次性提示（操作结果），界面用 Snackbar 显示后清空 */
    private val _toast = MutableStateFlow<String?>(null)
    val toast: StateFlow<String?> = _toast
    fun consumeToast() { _toast.value = null }
    private fun say(msg: String) { _toast.value = msg }

    private val _busy = MutableStateFlow(false)
    val busy: StateFlow<Boolean> = _busy

    // ---------- 节点与策略组 ----------
    private val _proxies = MutableStateFlow<Map<String, ProxyItem>>(emptyMap())
    val proxies: StateFlow<Map<String, ProxyItem>> = _proxies
    val mainGroup: String get() = VpnState.mainGroup
    val regionGroups get() = VpnState.regionGroups

    private var pollJob: Job? = null

    init {
        // 连接状态变化时刷新节点列表并开始轮询
        viewModelScope.launch {
            status.collect { s ->
                if (s == Status.CONNECTED) startPolling() else { pollJob?.cancel(); _proxies.value = emptyMap() }
            }
        }
    }

    private fun startPolling() {
        pollJob?.cancel()
        pollJob = viewModelScope.launch {
            while (true) {
                refreshProxies()
                delay(5000)
            }
        }
    }

    suspend fun refreshProxies() {
        val api = VpnState.api ?: return
        runCatching { _proxies.value = api.proxies() }
    }

    // ---------- 连接 ----------
    fun connect() {
        if (store.selectedSub() == null) { say("请先在「订阅」页添加并选择一个订阅"); return }
        VpnState.clearError()
        EasyClashVpnService.start(getApplication())
    }
    fun disconnect() = EasyClashVpnService.stop(getApplication())
    fun clearError() = VpnState.clearError()

    /** 设置变了需要重载配置时调用（已连接才会真的重载） */
    private fun reloadIfConnected() {
        if (status.value == Status.CONNECTED) EasyClashVpnService.reload(getApplication())
    }

    fun setMode(mode: ProxyMode) {
        store.update { it.mode = mode }
        viewModelScope.launch { VpnState.api?.let { runCatching { it.setMode(mode.coreValue) } } }
    }

    fun selectProxy(group: String, name: String) = viewModelScope.launch {
        val api = VpnState.api ?: return@launch
        runCatching { api.select(group, name) }.onFailure { say("切换失败：${it.message}") }
        // 用户手动改了主组，说明不想再锁地区
        if (group == mainGroup && name != ConfigBuilder.REGION_SELECTOR && settings.value.lockedRegion != null) {
            store.update { it.lockedRegion = null }
        }
        refreshProxies()
    }

    fun testGroup(group: String) = viewModelScope.launch {
        val api = VpnState.api ?: return@launch
        _busy.value = true
        runCatching { api.groupDelay(group) }
        refreshProxies()
        _busy.value = false
    }

    /**
     * 只测某个地区的自动组：内核会重测该组所有节点并按结果重新挑最快的。
     * 当前节点超时必换；几十毫秒之内的差距不换（tolerance），避免来回跳。
     */
    fun testRegion(code: String) = viewModelScope.launch {
        val api = VpnState.api ?: return@launch
        val region = regionGroups.firstOrNull { it.code == code } ?: run { say("当前订阅里没有这个地区"); return@launch }
        _busy.value = true
        val before = _proxies.value[region.groupName]?.now
        runCatching { api.groupDelay(region.groupName) }
        refreshProxies()
        // 内核对自动组的"最快"结果有几秒缓存，稍等再刷一次拿到最终选择
        delay(1500)
        refreshProxies()
        _busy.value = false
        val after = _proxies.value[region.groupName]?.now
        say(
            when {
                after == null -> "${region.flag} ${region.name} 测速完成"
                before != null && before != after -> "${region.flag} ${region.name}：已从「$before」切换到「$after」"
                else -> "${region.flag} ${region.name}：仍是最快节点「$after」"
            }
        )
    }

    fun testOne(name: String) = viewModelScope.launch {
        val api = VpnState.api ?: return@launch
        runCatching { api.delay(name) }
        refreshProxies()
    }

    /** 锁定地区：主组 → 地区选择 → 对应地区的自动组。code 为 null 表示解除锁定 */
    fun lockRegion(code: String?) = viewModelScope.launch {
        store.update { it.lockedRegion = code }
        val api = VpnState.api ?: return@launch
        if (code == null) {
            // 解锁：主组改回第一个非地区选择的条目
            val main = _proxies.value[mainGroup]
            val first = main?.all?.firstOrNull { it != ConfigBuilder.REGION_SELECTOR }
            if (first != null) runCatching { api.select(mainGroup, first) }
        } else {
            val region = Regions.of(code) ?: return@launch
            if (regionGroups.none { it.code == code }) { say("当前订阅里没有${region.name}的节点"); return@launch }
            runCatching {
                api.select(ConfigBuilder.REGION_SELECTOR, region.groupName)
                api.select(mainGroup, ConfigBuilder.REGION_SELECTOR)
            }.onFailure { say("切换失败：${it.message}") }
        }
        refreshProxies()
    }

    // ---------- 订阅 ----------
    private fun localProxyPort(): Int? = if (status.value == Status.CONNECTED && settings.value.mixedPort > 0) settings.value.mixedPort else null

    fun addSubscription(url: String, name: String, onDone: (Boolean) -> Unit = {}) = viewModelScope.launch {
        _busy.value = true
        val r = runCatching { repo.add(url, name, localProxyPort()) }
        _busy.value = false
        r.onSuccess { say("已添加「${it.name}」，共 ${it.nodeCount} 个节点"); onDone(true) }
            .onFailure { say(it.message ?: "添加失败"); onDone(false) }
    }

    fun importSubscriptionText(text: String, name: String) = viewModelScope.launch {
        runCatching { repo.importFromText(text, name) }
            .onSuccess { say("已导入「${it.name}」") }
            .onFailure { say(it.message ?: "导入失败") }
    }

    fun refreshSubscription(sub: Subscription) = viewModelScope.launch {
        _busy.value = true
        runCatching { repo.refresh(sub, localProxyPort()) }
            .onSuccess { say("「${it.name}」已更新，共 ${it.nodeCount} 个节点"); if (it.id == settings.value.selectedSubId) reloadIfConnected() }
            .onFailure { say(it.message ?: "更新失败") }
        _busy.value = false
    }

    fun removeSubscription(sub: Subscription) {
        store.removeSub(sub.id)
        if (sub.id == settings.value.selectedSubId) reloadIfConnected()
    }

    fun renameSubscription(sub: Subscription, name: String) { store.upsertSub(sub.copy(name = name)) }

    fun selectSubscription(sub: Subscription) {
        store.update { it.selectedSubId = sub.id }
        reloadIfConnected()
    }

    // ---------- 设置 ----------
    fun updateSettings(reload: Boolean = true, block: (Settings) -> Unit) {
        store.update(block)
        if (reload) reloadIfConnected()
    }

    // ---------- 分应用 ----------
    private val _apps = MutableStateFlow<List<AppInfo>>(emptyList())
    val apps: StateFlow<List<AppInfo>> = _apps

    fun loadApps() = viewModelScope.launch {
        if (_apps.value.isNotEmpty()) return@launch
        _apps.value = withContext(Dispatchers.IO) {
            val pm = getApplication<Application>().packageManager
            val self = getApplication<Application>().packageName
            pm.getInstalledApplications(PackageManager.GET_META_DATA)
                .filter { it.packageName != self }
                .filter { pm.getLaunchIntentForPackage(it.packageName) != null || (it.flags and ApplicationInfo.FLAG_SYSTEM) == 0 }
                .map { AppInfo(it.packageName, pm.getApplicationLabel(it).toString(), (it.flags and ApplicationInfo.FLAG_SYSTEM) != 0) }
                .sortedWith(compareBy({ it.isSystem }, { it.label.lowercase() }))
        }
    }

    fun toggleApp(pkg: String, checked: Boolean) {
        store.update { if (checked) it.appList.add(pkg) else it.appList.remove(pkg) }
        // 分应用列表变化需要重新建立 VPN，提示用户重连
        if (status.value == Status.CONNECTED) say("分应用设置将在下次连接时生效")
    }

    // ---------- 连接列表 ----------
    private val _connections = MutableStateFlow<List<ConnectionItem>>(emptyList())
    val connections: StateFlow<List<ConnectionItem>> = _connections
    fun refreshConnections() = viewModelScope.launch {
        val api = VpnState.api ?: run { _connections.value = emptyList(); return@launch }
        runCatching { _connections.value = api.connections().first.sortedByDescending { it.start } }
    }
    fun closeConnection(id: String) = viewModelScope.launch { VpnState.api?.closeConnection(id); refreshConnections() }
    fun closeAllConnections() = viewModelScope.launch { VpnState.api?.closeAllConnections(); refreshConnections() }

    // ---------- 日志 ----------
    /** 日志由服务从内核启动那一刻起持续收集，这里只是透传 */
    val logs: StateFlow<List<String>> get() = VpnState.logs
    fun clearLogs() = VpnState.clearLogs()
    fun logsText(): String = VpnState.logs.value.joinToString("\n")

    suspend fun coreVersion(): String = withContext(Dispatchers.IO) { runCatching { core.Core.version() }.getOrDefault("未知") }

}
