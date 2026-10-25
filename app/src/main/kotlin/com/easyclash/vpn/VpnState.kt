package com.easyclash.vpn

import com.easyclash.core.CoreApi
import com.easyclash.core.Region
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

enum class Status { IDLE, CONNECTING, CONNECTED, STOPPING }

/**
 * 服务与界面之间共享的状态。服务写，界面读。
 */
object VpnState {
    private val _status = MutableStateFlow(Status.IDLE)
    val status: StateFlow<Status> = _status

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error

    private val _traffic = MutableStateFlow(0L to 0L)        // 上行/下行 字节每秒
    val traffic: StateFlow<Pair<Long, Long>> = _traffic

    private val _startedAt = MutableStateFlow(0L)
    val startedAt: StateFlow<Long> = _startedAt

    /** 内核日志（从启动那一刻开始收集，最多保留 500 行），供高级页查看/复制 */
    private val _logs = MutableStateFlow<List<String>>(emptyList())
    val logs: StateFlow<List<String>> = _logs
    /** 前 80 行（启动阶段，最有诊断价值）永远保留，之后的只留最近 420 行 */
    internal fun appendLog(line: String) {
        val cur = _logs.value
        _logs.value = if (cur.size < 500) cur + line else cur.take(80) + (cur.drop(80) + line).takeLast(420)
    }
    internal fun resetLogs() { _logs.value = emptyList() }
    fun clearLogs() = resetLogs()

    /** 连接成功后可用的接口；断开后为 null */
    @Volatile var api: CoreApi? = null
        private set
    @Volatile var mainGroup: String = ""
        private set
    @Volatile var regionGroups: List<Region> = emptyList()
        private set
    @Volatile var configPath: String = ""
        private set

    internal fun setStatus(s: Status) { _status.value = s; if (s == Status.CONNECTED) _startedAt.value = System.currentTimeMillis() }
    internal fun setError(msg: String?) { _error.value = msg }
    internal fun setTraffic(up: Long, down: Long) { _traffic.value = up to down }
    internal fun setRuntime(api: CoreApi?, mainGroup: String, regions: List<Region>, configPath: String) {
        this.api = api; this.mainGroup = mainGroup; this.regionGroups = regions; this.configPath = configPath
    }
    fun clearError() { _error.value = null }
}
