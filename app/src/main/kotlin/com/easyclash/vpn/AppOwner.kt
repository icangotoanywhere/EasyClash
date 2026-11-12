package com.easyclash.vpn

import android.content.Context
import android.net.ConnectivityManager
import android.os.Build
import android.system.OsConstants
import java.net.InetAddress
import java.net.InetSocketAddress
import java.util.concurrent.ConcurrentHashMap

/**
 * 查一条连接属于哪个 App（包名），给内核的 PROCESS-NAME 规则和"仅代理"保护用。
 *
 * 用安卓 10 起才有的 ConnectivityManager.getConnectionOwnerUid：只有当前生效的 VPN 应用能调用，
 * 传入连接在 App 那一侧看到的源地址和目标地址。uid → 包名的结果会缓存。
 * 安卓 9 及以下没有这个接口，此时一律返回空串（查不到），仅代理功能在界面上会提示不可用。
 */
class AppOwner(ctx: Context) : core.PackageResolver {
    private val cm = ctx.getSystemService(ConnectivityManager::class.java)
    private val pm = ctx.packageManager
    private val names = ConcurrentHashMap<Int, String>()

    override fun findPackage(network: String, srcIP: String, srcPort: Int, dstIP: String, dstPort: Int): String {
        if (Build.VERSION.SDK_INT < 29) return ""
        return runCatching {
            val proto = if (network.equals("udp", true)) OsConstants.IPPROTO_UDP else OsConstants.IPPROTO_TCP
            val uid = cm.getConnectionOwnerUid(
                proto,
                InetSocketAddress(InetAddress.getByName(srcIP), srcPort),   // 数字地址，不会触发 DNS
                InetSocketAddress(InetAddress.getByName(dstIP), dstPort),
            )
            if (uid < 0) "" else names.getOrPut(uid) { pm.getPackagesForUid(uid)?.firstOrNull() ?: "" }
        }.getOrDefault("")
    }

    companion object {
        /** 当前系统能不能按 App 区分连接 */
        val supported: Boolean get() = Build.VERSION.SDK_INT >= 29
    }
}
