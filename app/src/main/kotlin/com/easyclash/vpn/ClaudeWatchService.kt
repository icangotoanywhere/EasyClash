package com.easyclash.vpn

import android.accessibilityservice.AccessibilityService
import android.content.Context
import android.content.Intent
import android.provider.Settings
import android.view.accessibility.AccessibilityEvent
import com.easyclash.ui.ClaudeGateActivity

/**
 * 「Claude 切到前台时自动弹出检查」（可选，用户在系统「无障碍」里手动打开）。
 *
 * 只在「Claude 联网前先确认」正在生效（[ClaudeGuard.active]）、并且当前是挡住状态时，Claude 一来到前台
 * （原版图标、多任务、通知……），立刻弹出检查卡片盖在 Claude 上面：「打开」= 放行并露出 Claude，「取消」= 回桌面、继续挡住。
 * 没开这个服务时，后台服务不能弹界面（安卓限制），只能从桌面的「打开 Claude」图标进去放行。
 *
 * 只接收窗口切换事件、只看包名（canRetrieveWindowContent=false），不读屏幕内容。
 * 要收所有 App 的事件，才能分辨「从别处切进 Claude」和「在 Claude 里面换页面」。和 To Claude 的 GateService 同一个做法。
 */
class ClaudeWatchService : AccessibilityService() {

    companion object {
        fun enabled(ctx: Context): Boolean {
            val list = Settings.Secure.getString(ctx.contentResolver, Settings.Secure.ENABLED_ACCESSIBILITY_SERVICES) ?: return false
            return list.split(':').any { it.startsWith(ctx.packageName + "/") }
        }
    }

    private var lastPkg: String? = null

    override fun onAccessibilityEvent(e: AccessibilityEvent?) {
        if (e?.eventType != AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED) return
        val pkg = e.packageName?.toString() ?: return
        // 下拉通知栏、弹出输入法不算离开 Claude
        if (pkg == "com.android.systemui" || pkg == imePackage()) return
        val prev = lastPkg
        lastPkg = pkg
        if (pkg != ClaudeGateActivity.CLAUDE_PKG) return
        // 在 Claude 里换页面；或者刚从检查卡片过来
        if (prev == ClaudeGateActivity.CLAUDE_PKG || prev == packageName) return
        // 没在看管 Claude，或者已经放行了：不打扰
        if (!ClaudeGuard.active || !ClaudeGuard.expired()) return
        runCatching {
            startActivity(
                Intent(this, ClaudeGateActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    .putExtra(ClaudeGateActivity.EXTRA_INTERCEPT, true),
            )
        }
    }

    private fun imePackage(): String? =
        Settings.Secure.getString(contentResolver, Settings.Secure.DEFAULT_INPUT_METHOD)?.substringBefore('/')

    override fun onInterrupt() {}
}
