package com.easyclash.vpn

import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import com.easyclash.EasyClashApp
import com.easyclash.ui.MainActivity
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch

/**
 * 系统下拉菜单里的"快捷设置磁贴"：点一下连接，再点一下断开，磁贴上显示当前状态。
 */
class EasyClashTileService : TileService() {

    private var scope: CoroutineScope? = null
    private var watchJob: Job? = null

    /** 磁贴出现在屏幕上时开始跟随连接状态刷新 */
    override fun onStartListening() {
        super.onStartListening()
        val s = CoroutineScope(SupervisorJob() + Dispatchers.Main)
        scope = s
        watchJob = s.launch { VpnState.status.collect { render(it) } }
    }

    override fun onStopListening() {
        watchJob?.cancel(); watchJob = null
        scope?.cancel(); scope = null
        super.onStopListening()
    }

    override fun onClick() {
        super.onClick()
        when (VpnState.status.value) {
            Status.CONNECTED -> EasyClashVpnService.stop(this)
            Status.IDLE -> {
                val store = EasyClashApp.instance.store
                // 没有订阅，或系统还没授予 VPN 权限：打开主界面处理
                if (store.selectedSub() == null || VpnService.prepare(this) != null) {
                    openApp(connect = true)
                } else {
                    VpnState.clearError()
                    EasyClashVpnService.start(this)
                }
            }
            else -> { /* 正在连接/断开中，忽略点击 */ }
        }
    }

    private fun render(status: Status) {
        val tile = qsTile ?: return
        tile.label = "EasyClash"
        tile.state = when (status) {
            Status.CONNECTED -> Tile.STATE_ACTIVE
            Status.IDLE -> Tile.STATE_INACTIVE
            else -> Tile.STATE_UNAVAILABLE
        }
        if (Build.VERSION.SDK_INT >= 29) {
            tile.subtitle = when (status) {
                Status.CONNECTED -> "已连接"
                Status.CONNECTING -> "连接中…"
                Status.STOPPING -> "断开中…"
                Status.IDLE -> "未连接"
            }
        }
        tile.updateTile()
    }

    private fun openApp(connect: Boolean) {
        val intent = Intent(this, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)
            .putExtra(MainActivity.EXTRA_CONNECT, connect)
        if (Build.VERSION.SDK_INT >= 34) {
            val pi = PendingIntent.getActivity(this, 2, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
            startActivityAndCollapse(pi)
        } else {
            @Suppress("DEPRECATION")
            startActivityAndCollapse(intent)
        }
    }
}
