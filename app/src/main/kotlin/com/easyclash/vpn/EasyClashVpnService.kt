package com.easyclash.vpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import com.easyclash.EasyClashApp
import com.easyclash.R
import com.easyclash.core.ConfigBuilder
import com.easyclash.core.CoreApi
import com.easyclash.core.RegionLock
import com.easyclash.data.AppProxyMode
import com.easyclash.data.ProxyMode
import com.easyclash.ui.MainActivity
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import okhttp3.WebSocket
import java.io.File
import java.security.SecureRandom

/**
 * VPN 服务：建立 TUN → 生成配置 → 启动 mihomo 内核 → 保持前台通知。
 */
class EasyClashVpnService : VpnService() {

    companion object {
        const val ACTION_START = "com.easyclash.START"
        const val ACTION_STOP = "com.easyclash.STOP"
        const val ACTION_RELOAD = "com.easyclash.RELOAD"
        const val ACTION_TEST = "com.easyclash.TEST"
        // 渠道一旦创建，重要级别就改不了，所以换了新 ID。旧渠道 easyclash_vpn 是"低重要级"，很多系统不在锁屏显示
        private const val CHANNEL = "easyclash_status"
        private const val OLD_CHANNEL = "easyclash_vpn"
        private const val NOTIFY_ID = 1
        const val CONTROLLER_PORT = 16090
        private const val GEO_CHECK_INTERVAL = 7L * 24 * 3600 * 1000   // 地理数据库每 7 天检查一次
        private const val GEO_RETRY_INTERVAL = 12L * 3600 * 1000       // 失败后 12 小时内不再自动重试

        fun start(ctx: Context) {
            val i = Intent(ctx, EasyClashVpnService::class.java).setAction(ACTION_START)
            if (Build.VERSION.SDK_INT >= 26) ctx.startForegroundService(i) else ctx.startService(i)
        }
        fun stop(ctx: Context) { ctx.startService(Intent(ctx, EasyClashVpnService::class.java).setAction(ACTION_STOP)) }
        fun reload(ctx: Context) { ctx.startService(Intent(ctx, EasyClashVpnService::class.java).setAction(ACTION_RELOAD)) }
        fun test(ctx: Context) { ctx.startService(Intent(ctx, EasyClashVpnService::class.java).setAction(ACTION_TEST)) }
    }

    // 单线程调度：连接 / 重载 / 停止 串行执行，避免互相打架
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default.limitedParallelism(1))
    private var tun: ParcelFileDescriptor? = null
    private var trafficWs: WebSocket? = null
    private var logWs: WebSocket? = null
    private var secret = ""
    // 连接后的后台维护（补下载规则集、更新地理数据库）。耗时较长，不能放进上面的单线程 scope，否则会挡住断开/重载
    private val bgScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var maintJob: Job? = null

    // 通知栏显示的内容：当前节点、最近一次延迟、实时速度。节点信息每 10 秒从内核刷新一次
    private var nodeJob: Job? = null
    @Volatile private var nodeTitle: String? = null      // 例如 "🇯🇵 日本 · 东京 03"
    @Volatile private var nodeDelay: String = ""          // 例如 "延迟 85 ms"
    @Volatile private var leafName: String? = null        // 当前实际出口节点名（内核里的名字）
    @Volatile private var trafficText: String = ""
    @Volatile private var testing = false
    @Volatile private var startReason: String? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> { scope.launch { stopEverything("收到断开指令") } ; return START_NOT_STICKY }
            ACTION_RELOAD -> { scope.launch { reloadConfig() }; return START_STICKY }
            ACTION_TEST -> { testFromNotification(); return START_STICKY }
            else -> {
                if (VpnState.status.value == Status.CONNECTED || VpnState.status.value == Status.CONNECTING) return START_STICKY
                // 记下是谁拉起的服务，方便排查"后台被杀"的问题（连接开始时会清空日志，所以先存起来）
                startReason = when {
                    intent == null -> "服务曾被系统杀掉，系统已自动重新拉起，正在重连"
                    intent.action == VpnService.SERVICE_INTERFACE -> "由系统「始终开启的 VPN」启动（开机或被杀后自动恢复）"
                    else -> null
                }
                val n = buildNotification("正在连接…")
                if (Build.VERSION.SDK_INT >= 34) startForeground(NOTIFY_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
                else startForeground(NOTIFY_ID, n)
                scope.launch { connect() }
                return START_STICKY
            }
        }
    }

    // ---------------- 连接 ----------------
    private suspend fun connect() {
        VpnState.clearError()
        VpnState.resetLogs()
        startReason?.let { VpnState.appendLog("[APP] $it") }; startReason = null
        VpnState.setStatus(Status.CONNECTING)
        val app = EasyClashApp.instance
        val store = app.store
        val settings = store.settings.value
        try {
            val sub = store.selectedSub() ?: throw IllegalStateException("还没有选择订阅，请先在「订阅」页添加一个机场订阅")
            val yamlText = app.repo.rawYaml(sub) ?: throw IllegalStateException("订阅文件不存在，请先更新一次订阅")

            copyBundledGeoFiles(store.homeDir)

            // 1. 建立 TUN
            val pfd = buildTun(settings.appProxyMode, settings.appList, settings.ipv6)
                ?: throw IllegalStateException("系统拒绝建立 VPN（可能有另一个 VPN 正在运行，或未授予 VPN 权限）")
            tun = pfd

            // 2. 生成最终配置
            secret = randomSecret()
            val out = File(store.runDir, "config.yaml")
            val result = ConfigBuilder.build(yamlText, settings, out, pfd.fd, CONTROLLER_PORT, secret)
            VpnState.appendLog("[APP] TUN fd=${pfd.fd} stack=${settings.tunStack.coreValue} mode=${settings.appProxyMode} 主组=${result.mainGroup} 地区组=${result.regionGroups.size}")

            // 3. 启动内核
            core.Core.setProtector(object : core.SocketProtector {
                override fun protect(fd: Long): Boolean = this@EasyClashVpnService.protect(fd.toInt())
            })
            try {
                withContext(Dispatchers.IO) { core.Core.start(store.homeDir.absolutePath, out.absolutePath) }
            } finally {
                // 启动阶段内核打的日志（含 TUN / 端口监听失败的原因），从 Go 层取回
                runCatching { core.Core.startupLog() }.getOrNull()?.lines()?.filter { it.isNotBlank() }?.forEach { VpnState.appendLog("[CORE启动] $it") }
            }

            // 4. 等待控制接口就绪
            val api = CoreApi(CONTROLLER_PORT, secret)
            var ok = false
            repeat(40) { if (!ok) { ok = api.ping(); if (!ok) delay(150) } }
            if (!ok) throw IllegalStateException("内核已启动但控制接口无响应")
            // 从一开始就收集内核日志（含 TUN 启动失败等只在启动时出现的错误）
            logWs?.cancel()
            logWs = api.logSocket(settings.logLevel) { type, payload -> VpnState.appendLog("[${type.uppercase()}] $payload") }
            VpnState.appendLog("[APP] 内核控制接口已就绪，版本 ${api.version()}")
            // 把内核实际生效的 tun / dns 配置记下来：TUN 启动失败时内核会把 tun.enable 改成 false
            runCatching {
                val o = org.json.JSONObject(api.rawConfig())
                VpnState.appendLog("[APP] 内核生效配置 tun=${o.optJSONObject("tun")} mode=${o.optString("mode")} mixed-port=${o.optInt("mixed-port")}")
            }

            VpnState.setRuntime(api, result.mainGroup, result.regionGroups, out.absolutePath)
            applyRegionLock(api, result.mainGroup)
            VpnState.setStatus(Status.CONNECTED)
            if (Build.VERSION.SDK_INT >= 29) VpnState.setAlwaysOn(isAlwaysOn, isLockdownEnabled)
            if (!KeepAlive.ignoringBatteryOptimizations(this@EasyClashVpnService)) {
                VpnState.appendLog("[APP] 提示：本应用仍受电池优化限制，后台容易被系统杀掉。可在「高级」页的「后台常驻」里放开")
            }
            updateNotification("已连接 · ${sub.name}")
            startTrafficMonitor(api)
            startNodeMonitor(api)
            startMaintenance(api)
        } catch (e: Throwable) {
            VpnState.appendLog("[APP] 启动失败：${e.message}")
            VpnState.setError(friendly(e))
            stopEverything("启动失败")
        }
    }

    /**
     * 连接 / 重载后调用：已锁定地区就重新应用（所有走代理的组切到「地区选择」）；
     * 已解除但还有没恢复的组（断开时解除的）就恢复锁定前的选择。
     */
    private suspend fun applyRegionLock(api: CoreApi, mainGroup: String) {
        val store = EasyClashApp.instance.store
        val s = store.settings.value
        val code = s.lockedRegion
        if (code == null) {
            if (s.regionSaved.isNotEmpty()) runCatching { RegionLock.release(api, store, mainGroup) }
            return
        }
        val region = VpnState.regionGroups.firstOrNull { it.code == code } ?: return
        runCatching { RegionLock.apply(api, store, mainGroup, region) }
    }

    private fun buildTun(mode: AppProxyMode, apps: Set<String>, ipv6: Boolean): ParcelFileDescriptor? {
        val b = Builder()
            .setSession("EasyClash")
            .setMtu(ConfigBuilder.TUN_MTU)
            .addAddress(ConfigBuilder.TUN_ADDR4, 30)
            .addRoute("0.0.0.0", 0)
            .addDnsServer(ConfigBuilder.TUN_DNS4)   // 任意地址即可，内核的 dns-hijack 会接管 53 端口
            .setBlocking(true)                       // 内核用阻塞方式读 TUN
        if (ipv6) {
            b.addAddress(ConfigBuilder.TUN_ADDR6, 126)
            b.addRoute("::", 0)
        }
        if (Build.VERSION.SDK_INT >= 29) b.setMetered(false)
        val self = packageName
        when (mode) {
            AppProxyMode.ALL -> runCatching { b.addDisallowedApplication(self) }
            AppProxyMode.ALLOW_LIST -> {
                val list = apps.filter { it != self }
                if (list.isEmpty()) runCatching { b.addDisallowedApplication(self) }
                list.forEach { runCatching { b.addAllowedApplication(it) } }
            }
            AppProxyMode.DENY_LIST -> {
                (apps + self).forEach { runCatching { b.addDisallowedApplication(it) } }
            }
        }
        val pi = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        b.setConfigureIntent(pi)
        return b.establish()
    }

    private fun startTrafficMonitor(api: CoreApi) {
        trafficWs?.cancel()
        trafficWs = api.trafficSocket(
            onData = { up, down ->
                VpnState.setTraffic(up, down)
                trafficText = "↑ ${human(up)}/s   ↓ ${human(down)}/s"
                pushNotification()
            },
            onClosed = { }
        )
    }

    // ---------------- 重载 ----------------
    private suspend fun reloadConfig() {
        val api = VpnState.api ?: return
        val app = EasyClashApp.instance
        val store = app.store
        val settings = store.settings.value
        try {
            val sub = store.selectedSub() ?: return
            val yamlText = app.repo.rawYaml(sub) ?: return
            val pfd = tun ?: return
            val out = File(store.runDir, "config.yaml")
            val result = ConfigBuilder.build(yamlText, settings, out, pfd.fd, CONTROLLER_PORT, secret)
            withContext(Dispatchers.IO) { core.Core.reload(out.absolutePath) }
            VpnState.setRuntime(api, result.mainGroup, result.regionGroups, out.absolutePath)
            applyRegionLock(api, result.mainGroup)
            updateNotification("已连接 · ${sub.name}")
            startNodeMonitor(api)
            startMaintenance(api, checkGeo = false)
        } catch (e: Throwable) {
            VpnState.setError("重载配置失败：${friendly(e)}")
        }
    }

    // ---------------- 后台维护 ----------------
    private fun startMaintenance(api: CoreApi, checkGeo: Boolean = true) {
        maintJob?.cancel()
        maintJob = bgScope.launch {
            // 1. 规则集补下载：启动瞬间节点可能还没就绪，首次下载失败的规则集要等订阅设定的间隔（通常一天）才会重试
            delay(8_000)
            runCatching {
                val failed = api.ruleProviders().filter { it.isHttp && it.neverLoaded }
                if (failed.isNotEmpty()) {
                    VpnState.appendLog("[APP] ${failed.size} 个规则集首次下载失败，重试中…")
                    var ok = 0
                    for (p in failed) if (runCatching { api.updateRuleProvider(p.name) }.isSuccess) ok++
                    VpnState.appendLog("[APP] 规则集重试完成：成功 $ok / ${failed.size}")
                }
            }
            if (!checkGeo) return@launch
            // 2. 地理数据库自动更新（内核嵌入模式下自己不会更新，由这里定期触发）
            val store = EasyClashApp.instance.store
            val s = store.settings.value
            val now = System.currentTimeMillis()
            if (!s.geoAutoUpdate || now - s.geoLastCheck < GEO_CHECK_INTERVAL || now - s.geoLastAttempt < GEO_RETRY_INTERVAL) return@launch
            store.update { it.geoLastAttempt = now }
            VpnState.appendLog("[APP] 地理数据库超过 7 天未检查，后台更新中…")
            val err = runCatching { core.Core.updateGeo() }.getOrElse { it.message ?: "未知错误" }
            if (err.isEmpty()) {
                store.update { it.geoLastCheck = System.currentTimeMillis() }
                VpnState.appendLog("[APP] 地理数据库检查完成，已是最新")
            } else {
                VpnState.appendLog("[APP] 地理数据库更新失败：$err（12 小时后再试）")
            }
        }
    }

    // ---------------- 停止 ----------------
    private suspend fun stopEverything(reason: String) {
        VpnState.appendLog("[APP] 断开：$reason")
        VpnState.setStatus(Status.STOPPING)
        maintJob?.cancel(); maintJob = null
        nodeJob?.cancel(); nodeJob = null
        nodeTitle = null; nodeDelay = ""; leafName = null; trafficText = ""; testing = false
        trafficWs?.cancel(); trafficWs = null
        logWs?.cancel(); logWs = null
        withContext(Dispatchers.IO) { runCatching { core.Core.stop() } }
        runCatching { tun?.close() }; tun = null
        VpnState.setRuntime(null, "", emptyList(), "")
        VpnState.setTraffic(0, 0)
        VpnState.setStatus(Status.IDLE)
        // 不切到主线程：onDestroy 里用 runBlocking 调用本方法时主线程正被阻塞，切过去会死锁
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    /**
     * 用户在系统设置里断开了 VPN，或者另一个 VPN 顶替了本应用。
     * 不自动重连：这时本应用已经没有 VPN 权限，强行重连只会和别的 VPN 来回抢。
     */
    override fun onRevoke() { scope.launch { stopEverything("系统撤销了 VPN 权限（在系统设置里断开了，或另一个 VPN 被打开）") } }

    override fun onDestroy() {
        // 必须同步清理，否则系统销毁服务时内核会残留运行、TUN 泄漏
        if (VpnState.status.value != Status.IDLE) runBlocking { stopEverything("服务被系统销毁") }
        scope.cancel()
        bgScope.cancel()
        super.onDestroy()
    }

    // ---------------- 通知 ----------------
    /** 当前节点 + 延迟。锁定了地区时前面带上地区名；直连模式显示"直连" */
    private fun startNodeMonitor(api: CoreApi) {
        nodeJob?.cancel()
        nodeJob = bgScope.launch {
            while (isActive) {
                refreshNodeInfo(api)
                delay(10_000)
            }
        }
    }

    private suspend fun refreshNodeInfo(api: CoreApi) {
        val s = EasyClashApp.instance.store.settings.value
        if (s.mode == ProxyMode.DIRECT) {
            nodeTitle = "直连模式（不走代理）"; nodeDelay = ""; leafName = null
            pushNotification(); return
        }
        // 沿策略组一路找到真实节点
        var name = if (s.mode == ProxyMode.GLOBAL) "GLOBAL" else VpnState.mainGroup
        var leaf: com.easyclash.data.ProxyItem? = null
        for (hop in 1..8) {   // 最多跟 8 层，防止策略组互相引用时死循环
            val p = runCatching { api.proxy(name) }.getOrNull() ?: break
            leaf = p
            name = p.now ?: break
        }
        val found = leaf ?: return
        leafName = found.name
        val shown = when (found.name) { "DIRECT" -> "直连"; "REJECT" -> "拒绝"; else -> found.name }
        val region = lockedRegion(s.lockedRegion)
        nodeTitle = if (region != null) "${region.flag} ${region.displayName} · $shown" else shown
        nodeDelay = when {
            found.name == "DIRECT" || found.name == "REJECT" -> ""
            found.delay > 0 -> "延迟 ${found.delay} ms"
            !found.alive -> "延迟 超时"
            else -> "延迟 未测"
        }
        pushNotification()
    }

    private fun lockedRegion(code: String?) = code?.let { c -> VpnState.regionGroups.firstOrNull { it.code == c } }

    /** 通知栏"测速"按钮：锁定了地区就重测该地区的自动组（内核会按结果切到最快节点），否则只测当前节点 */
    private fun testFromNotification() {
        val api = VpnState.api ?: return
        if (VpnState.status.value != Status.CONNECTED || testing) return
        bgScope.launch {
            testing = true
            pushNotification()
            val before = leafName
            val region = lockedRegion(EasyClashApp.instance.store.settings.value.lockedRegion)
            try {
                if (region != null) {
                    runCatching { api.groupDelay(region.groupName) }
                    delay(1500)   // 内核对自动组的"最快"结果有几秒缓存，稍等再取最终选择
                } else {
                    before?.let { n -> runCatching { api.delay(n) } }
                }
            } finally {
                testing = false
            }
            refreshNodeInfo(api)
            val after = leafName
            VpnState.appendLog(
                "[APP] 通知栏测速" + (region?.let { " ${it.flag} ${it.displayName}" } ?: "") + "：" +
                    (if (before != null && after != null && before != after) "已从「$before」切换到「$after」，" else "") + nodeDelay
            )
        }
    }

    /** 连接后的常驻通知：标题是节点，正文是延迟和速度 */
    private fun pushNotification() {
        if (VpnState.status.value != Status.CONNECTED) return   // 断开过程中迟到的回调不能把通知又挂回去
        val title = nodeTitle ?: return
        val body = listOf(if (testing) "测速中…" else nodeDelay, trafficText).filter { it.isNotBlank() }.joinToString("   ")
        runCatching { getSystemService(NotificationManager::class.java).notify(NOTIFY_ID, buildNotification(body, title, withTest = true)) }
    }

    private fun buildNotification(text: String, title: String = "EasyClash", withTest: Boolean = false): Notification {
        val nm = getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL) == null) {
            // 默认重要级才会在锁屏显示；关掉声音和振动，速度每秒刷新也不会打扰
            val ch = NotificationChannel(CHANNEL, getString(R.string.notification_channel), NotificationManager.IMPORTANCE_DEFAULT).apply {
                setSound(null, null)
                enableVibration(false)
                setShowBadge(false)
                lockscreenVisibility = Notification.VISIBILITY_PUBLIC
            }
            nm.createNotificationChannel(ch)
            runCatching { nm.deleteNotificationChannel(OLD_CHANNEL) }
        }
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val stop = PendingIntent.getBroadcast(this, 1, Intent(this, StopReceiver::class.java), PendingIntent.FLAG_IMMUTABLE)
        val b = Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_notify)
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setVisibility(Notification.VISIBILITY_PUBLIC)   // 锁屏上显示完整内容（节点、延迟、速度）
        if (withTest) {
            val test = PendingIntent.getBroadcast(this, 2, Intent(this, TestReceiver::class.java), PendingIntent.FLAG_IMMUTABLE)
            b.addAction(Notification.Action.Builder(null, if (testing) "测速中…" else "测速", test).build())
        }
        // 「断开」按钮默认不显示，免得误触；高级页可打开
        if (EasyClashApp.instance.store.settings.value.notifyStopButton) {
            b.addAction(Notification.Action.Builder(null, "断开", stop).build())
        }
        return b.build()
    }

    private fun updateNotification(text: String) {
        runCatching { getSystemService(NotificationManager::class.java).notify(NOTIFY_ID, buildNotification(text)) }
    }

    // ---------------- 工具 ----------------
    private fun randomSecret(): String {
        val chars = "abcdefghijklmnopqrstuvwxyz0123456789"
        val r = SecureRandom()
        return (1..24).map { chars[r.nextInt(chars.length)] }.joinToString("")
    }

    /** 把打包进 APK 的 geo 数据库复制到内核工作目录（只复制一次） */
    private fun copyBundledGeoFiles(home: File) {
        for (name in listOf("geoip.metadb", "geosite.dat", "GeoLite2-ASN.mmdb")) {
            val dst = File(home, name)
            if (dst.exists()) continue
            runCatching { assets.open(name).use { input -> dst.outputStream().use { input.copyTo(it) } } }
        }
    }

    private fun friendly(e: Throwable): String {
        val m = e.message ?: e.javaClass.simpleName
        return when {
            m.contains("address already in use", true) -> "端口被占用，请关闭其他代理软件后重试"
            m.contains("proxy", true) && m.contains("not found", true) -> "订阅里的策略组引用了不存在的节点：$m"
            else -> m
        }
    }

    private fun human(b: Long): String = when {
        b >= 1_048_576 -> String.format("%.1f MB", b / 1_048_576.0)
        b >= 1024 -> String.format("%.0f KB", b / 1024.0)
        else -> "$b B"
    }
}

/** 通知栏"断开"按钮 */
class StopReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) { EasyClashVpnService.stop(context) }
}

/** 通知栏"测速"按钮 */
class TestReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) { EasyClashVpnService.test(context) }
}
