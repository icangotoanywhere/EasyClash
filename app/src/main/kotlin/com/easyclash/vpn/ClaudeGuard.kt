package com.easyclash.vpn

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import com.easyclash.R
import com.easyclash.core.ConfigBuilder
import com.easyclash.core.CoreApi
import com.easyclash.data.Settings
import com.easyclash.ui.ClaudeGateActivity
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/**
 * 「Claude 联网前先确认」（仅安卓，默认关）。
 *
 * 开启后配置里多一个选择组 [ConfigBuilder.CLAUDE_GATE]（成员：REJECT、主组），Claude 的几个域名（[ConfigBuilder.CLAUDE_DOMAINS]）
 * 的规则指向它（排在国际 AI 服务规则前面）。平时选 REJECT：Claude 的连接一律被挡住；用户在「打开 Claude」检查卡片上点「打开」后
 * 切到主组放行，到时间（锁屏 / 30 分钟 / 2 小时，见设置 claudeAllowMinutes）再切回 REJECT，并断开已有的 Claude 连接。
 * 只比域名、切组走内核接口，不查连接属于哪个 App，也不用重载配置（教训见 CLAUDE.md「不要按连接查所属 App」）。
 * 被挡住时，日志里会出现「… using EasyClash·Claude 放行[REJECT]」，服务据此发一条通知，点它打开检查卡片（每分钟最多一条）。
 */
object ClaudeGuard {
    /** 放行到什么时候（毫秒时间戳）；0 = 挡住；Long.MAX_VALUE = 到锁屏为止 */
    @Volatile private var allowedUntil = 0L
    private val _allowed = MutableStateFlow(false)
    val allowed: StateFlow<Boolean> = _allowed

    private const val CHANNEL = "easyclash_claude_guard"
    private const val NOTIFY_ID = 7
    @Volatile private var lastNotify = 0L

    fun isOn(s: Settings) = s.claudeConfirm

    /** 还剩多久（给界面显示）：null = 挡住；"直到锁屏"；"还剩 N 分钟" */
    fun remainingText(): String? {
        val u = allowedUntil
        return when {
            u == 0L -> null
            u == Long.MAX_VALUE -> "直到锁屏"
            else -> "还剩 ${((u - System.currentTimeMillis()) / 60_000).coerceAtLeast(1)} 分钟"
        }
    }

    /** 放行（「打开」按钮）。minutes：0 = 到锁屏为止 */
    suspend fun allow(api: CoreApi, mainGroup: String, minutes: Int) {
        runCatching { api.select(ConfigBuilder.CLAUDE_GATE, mainGroup) }.onSuccess {
            allowedUntil = if (minutes <= 0) Long.MAX_VALUE else System.currentTimeMillis() + minutes * 60_000L
            _allowed.value = true
            VpnState.appendLog("[APP] Claude 联网已放行（${remainingText()}）")
        }
    }

    /** 挡住，并断开已有的 Claude 连接（否则已经连上的还能继续用） */
    suspend fun lock(api: CoreApi, reason: String) {
        runCatching { api.select(ConfigBuilder.CLAUDE_GATE, "REJECT") }
        val was = allowedUntil != 0L
        allowedUntil = 0L
        _allowed.value = false
        runCatching {
            api.connections().first.filter { isClaudeHost(it.host) }.forEach { api.closeConnection(it.id) }
        }
        if (was) VpnState.appendLog("[APP] Claude 联网重新挡住（$reason）")
    }

    /** 连接 / 重载后调用：内核会记住上次的选择，这里按当前状态重新设一次 */
    suspend fun apply(api: CoreApi, mainGroup: String) {
        if (expired()) lock(api, "放行已到时") else runCatching { api.select(ConfigBuilder.CLAUDE_GATE, mainGroup) }
    }

    fun expired(): Boolean { val u = allowedUntil; return u == 0L || (u != Long.MAX_VALUE && System.currentTimeMillis() >= u) }
    fun untilScreenOff() = allowedUntil == Long.MAX_VALUE
    fun reset() { allowedUntil = 0L; _allowed.value = false }

    fun isClaudeHost(host: String): Boolean {
        val h = host.lowercase().trimEnd('.')
        return ConfigBuilder.CLAUDE_DOMAINS.any { h == it || h.endsWith(".$it") }
    }

    /** 内核日志里出现 Claude 的连接被这个组挡住时，提醒用户去检查并放行 */
    fun onLogLine(ctx: Context, line: String) {
        if (!line.contains(ConfigBuilder.CLAUDE_GATE) || !line.contains("REJECT")) return
        val now = System.currentTimeMillis()
        if (now - lastNotify < 60_000) return
        lastNotify = now
        val nm = ctx.getSystemService(NotificationManager::class.java) ?: return
        if (nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, "Claude 联网确认", NotificationManager.IMPORTANCE_HIGH))
        }
        val open = PendingIntent.getActivity(
            ctx, 3, Intent(ctx, ClaudeGateActivity::class.java).setAction(Intent.ACTION_VIEW).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val n = Notification.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_notify)
            .setContentTitle("Claude 想联网，已被挡住")
            .setContentText("点这里先看一眼网络，确认后放行")
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
        runCatching { nm.notify(NOTIFY_ID, n) }
    }

    fun cancelNotice(ctx: Context) { runCatching { ctx.getSystemService(NotificationManager::class.java)?.cancel(NOTIFY_ID) } }
}
