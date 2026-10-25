package com.easyclash.ui

import android.app.Activity
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.widget.Toast
import com.easyclash.EasyClashApp
import com.easyclash.vpn.EasyClashVpnService
import com.easyclash.vpn.Status
import com.easyclash.vpn.VpnState

/**
 * 长按桌面图标的「连接」「断开」快捷方式落到这里：不显示界面，执行完立刻结束。
 * 只有在没有订阅或系统尚未授予 VPN 权限时才会打开主界面。
 */
class ShortcutActivity : Activity() {

    companion object {
        const val ACTION_CONNECT = "com.easyclash.action.CONNECT"
        const val ACTION_DISCONNECT = "com.easyclash.action.DISCONNECT"
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        when (intent?.action) {
            ACTION_CONNECT -> connect()
            ACTION_DISCONNECT -> {
                if (VpnState.status.value == Status.CONNECTED || VpnState.status.value == Status.CONNECTING) {
                    EasyClashVpnService.stop(this)
                    Toast.makeText(this, "EasyClash 已断开", Toast.LENGTH_SHORT).show()
                } else {
                    Toast.makeText(this, "EasyClash 未在连接", Toast.LENGTH_SHORT).show()
                }
            }
        }
        finish()
    }

    private fun connect() {
        if (VpnState.status.value == Status.CONNECTED) {
            Toast.makeText(this, "EasyClash 已经连接", Toast.LENGTH_SHORT).show()
            return
        }
        val store = EasyClashApp.instance.store
        if (store.selectedSub() == null || VpnService.prepare(this) != null) {
            // 交给主界面处理订阅 / 授权
            startActivity(
                Intent(this, MainActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)
                    .putExtra(MainActivity.EXTRA_CONNECT, true)
            )
            return
        }
        VpnState.clearError()
        EasyClashVpnService.start(this)
        Toast.makeText(this, "EasyClash 正在连接…", Toast.LENGTH_SHORT).show()
    }
}
