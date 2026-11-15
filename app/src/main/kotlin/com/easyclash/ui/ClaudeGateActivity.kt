package com.easyclash.ui

import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.drawable.AdaptiveIconDrawable
import android.net.Uri
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.pm.ShortcutInfoCompat
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.core.graphics.drawable.IconCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.easyclash.EasyClashApp
import com.easyclash.R
import com.easyclash.data.AppProxyMode
import com.easyclash.data.ProxyMode
import com.easyclash.vpn.Status
import com.easyclash.vpn.VpnState
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.net.InetSocketAddress
import java.net.Proxy
import java.util.concurrent.TimeUnit

/**
 * 桌面「打开 Claude」快捷方式的落地页：先显示当前网络情况（EasyClash 是否连接、Claude 会走哪个节点、
 * 出口 IP 和所在地，内容和电脑上的 NetGateShow 差不多），用户确认后再拉起手机上的 Claude App。
 *
 * 出口 IP：本应用自己的流量不进 VPN 隧道，所以连接时经内核的本地端口去查（和应用内升级一样），
 * 查到的才是真正的出口；没连接时直接查，显示的就是本机真实 IP。
 */
class ClaudeGateActivity : ComponentActivity() {

    companion object {
        const val CLAUDE_PKG = "com.anthropic.claude"
        private const val SHORTCUT_ID = "open-claude"

        fun claudeInstalled(ctx: Context): Boolean = ctx.packageManager.getLaunchIntentForPackage(CLAUDE_PKG) != null

        /**
         * 请桌面添加快捷方式。返回给用户看的提示。已经添加过的就只更新图标（老版本带 EasyClash 小标的图标会换掉）。
         * 图标就是手机上 Claude App 自己的图标，不加角标：桌面本身会给「由某个 App 创建的快捷方式」叠一个该 App 的小标，
         * 自己再加一个就会出现两个错开的小标（v1.0.36 的问题）。没装 Claude 时用 EasyClash 图标。
         */
        fun pinShortcut(ctx: Context): String {
            val info = shortcutInfo(ctx)
            val pinned = runCatching {
                ShortcutManagerCompat.getShortcuts(ctx, ShortcutManagerCompat.FLAG_MATCH_PINNED).any { it.id == SHORTCUT_ID }
            }.getOrDefault(false)
            if (pinned) {
                val ok = runCatching { ShortcutManagerCompat.updateShortcuts(ctx, listOf(info)) }.getOrDefault(false)
                return if (ok) "桌面上已经有这个图标了，已更新它的样子。" else "桌面上已经有这个图标了。"
            }
            if (!ShortcutManagerCompat.isRequestPinShortcutSupported(ctx)) return "这台手机的桌面不支持由 App 添加快捷方式。"
            val ok = runCatching { ShortcutManagerCompat.requestPinShortcut(ctx, info, null) }.getOrDefault(false)
            return if (ok) "已请求添加。如果桌面上没出现，请到系统设置 → 应用 → EasyClash → 权限，允许「创建桌面快捷方式」后再试。"
            else "添加失败：桌面拒绝了请求。请到系统设置 → 应用 → EasyClash → 权限，允许「创建桌面快捷方式」后再试。"
        }

        /** 每次打开 App 时调用：桌面上已有这个图标的话，把图标刷新成当前样子（Claude 换了图标、或老版本的角标图标） */
        fun refreshPinned(ctx: Context) {
            runCatching {
                if (ShortcutManagerCompat.getShortcuts(ctx, ShortcutManagerCompat.FLAG_MATCH_PINNED).any { it.id == SHORTCUT_ID }) {
                    ShortcutManagerCompat.updateShortcuts(ctx, listOf(shortcutInfo(ctx)))
                }
            }
        }

        private fun shortcutInfo(ctx: Context): ShortcutInfoCompat =
            ShortcutInfoCompat.Builder(ctx, SHORTCUT_ID)
                .setShortLabel("Claude")
                .setLongLabel("先看网络再打开 Claude")
                .setIcon(claudeIcon(ctx))
                .setIntent(Intent(ctx, ClaudeGateActivity::class.java).setAction(Intent.ACTION_VIEW))
                .build()

        /** Claude App 自己的图标。自适应图标按前景 + 背景两层交给系统，让桌面按自己的形状裁切，和真正的 Claude 图标一样大小 */
        private fun claudeIcon(ctx: Context): IconCompat = runCatching {
            val d = ctx.packageManager.getApplicationIcon(CLAUDE_PKG)
            if (d is AdaptiveIconDrawable) {
                val size = 432   // 自适应图标是 108dp，四周各留 18dp 给桌面裁切
                val bmp = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888)
                val c = Canvas(bmp)
                d.background?.let { it.setBounds(0, 0, size, size); it.draw(c) }
                d.foreground?.let { it.setBounds(0, 0, size, size); it.draw(c) }
                IconCompat.createWithAdaptiveBitmap(bmp)
            } else {
                val size = 192
                val bmp = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888)
                d.setBounds(0, 0, size, size); d.draw(Canvas(bmp))
                IconCompat.createWithBitmap(bmp)
            }
        }.getOrElse { IconCompat.createWithResource(ctx, R.mipmap.ic_launcher) }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            val dark = isSystemInDarkTheme()
            val scheme = if (dark) darkColorScheme(primary = Color(0xFF8AB4F8)) else lightColorScheme(primary = Color(0xFF1E6FD9))
            MaterialTheme(colorScheme = scheme) {
                Gate(onCancel = { finish() }, onOpen = { openClaude() }, onConnect = { connect() })
            }
        }
    }

    private fun openClaude() {
        val i = packageManager.getLaunchIntentForPackage(CLAUDE_PKG)
        if (i != null) {
            startActivity(i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        } else {
            // 没装 Claude：打开应用商店的 Claude 页面
            val ok = runCatching { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("market://details?id=$CLAUDE_PKG")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }.isSuccess
            if (!ok) Toast.makeText(this, "这台手机上没有安装 Claude App", Toast.LENGTH_LONG).show()
        }
        finish()
    }

    /** 就地连接；没有订阅或系统还没授予 VPN 权限时交给主界面处理 */
    private fun connect() {
        val store = EasyClashApp.instance.store
        if (store.selectedSub() == null || android.net.VpnService.prepare(this) != null) {
            startActivity(
                Intent(this, MainActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)
                    .putExtra(MainActivity.EXTRA_CONNECT, true),
            )
            finish()
            return
        }
        VpnState.clearError()
        com.easyclash.vpn.EasyClashVpnService.start(this)
    }
}

private data class ExitInfo(val ip: String, val place: String, val countryCode: String, val isp: String)

private data class GateInfo(
    val route: String,          // Claude 会走的线路
    val routeOk: Boolean,       // 是否经代理
    val exit: ExitInfo?,
    val exitErr: String?,
    val warnings: List<String>,
)

@Composable
private fun Gate(onCancel: () -> Unit, onOpen: () -> Unit, onConnect: () -> Unit) {
    val ctx = androidx.compose.ui.platform.LocalContext.current
    val status by VpnState.status.collectAsStateWithLifecycle()
    var info by remember { mutableStateOf<GateInfo?>(null) }
    var refresh by remember { mutableIntStateOf(0) }
    val installed = remember { ClaudeGateActivity.claudeInstalled(ctx) }

    LaunchedEffect(status, refresh) {
        if (status == Status.CONNECTING) return@LaunchedEffect
        info = null
        info = loadInfo(ctx, status)
    }

    // 半透明遮罩 + 居中卡片；点遮罩等于取消
    Box(
        Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.45f))
            .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { onCancel() },
        contentAlignment = Alignment.Center,
    ) {
        Surface(
            shape = RoundedCornerShape(24.dp),
            tonalElevation = 6.dp,
            modifier = Modifier.padding(20.dp).widthIn(max = 420.dp).fillMaxWidth()
                .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { },
        ) {
            Column(Modifier.padding(22.dp)) {
                Text("打开 Claude 前看一眼网络", fontWeight = FontWeight.Bold, fontSize = 18.sp)
                Spacer(Modifier.height(14.dp))

                val i = info
                when {
                    status == Status.CONNECTING -> Loading("EasyClash 正在连接…")
                    i == null -> Loading("正在检查网络…")
                    else -> {
                        val good = i.warnings.isEmpty()
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Box(Modifier.size(10.dp).background(if (good) Color(0xFF2E7D32) else Color(0xFFE65100), CircleShape))
                            Text(
                                if (good) "  可以放心打开" else "  有需要注意的地方",
                                fontWeight = FontWeight.SemiBold,
                                color = if (good) Color(0xFF2E7D32) else Color(0xFFE65100),
                            )
                        }
                        Spacer(Modifier.height(10.dp))
                        Line("EasyClash", if (status == Status.CONNECTED) "已连接" else "未连接")
                        Line("Claude 线路", i.route, if (i.routeOk) Color(0xFF2E7D32) else Color(0xFFE65100))
                        if (i.exit != null) {
                            Line("出口所在地", i.exit.place)
                            Line("出口 IP", i.exit.ip)
                            if (i.exit.isp.isNotBlank()) Line("运营商", i.exit.isp)
                        } else {
                            Line("出口 IP", i.exitErr ?: "查不到")
                        }
                        i.warnings.forEach {
                            Text("• $it", color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 6.dp))
                        }
                        if (!installed) {
                            Text("• 这台手机上还没有安装 Claude App，点「打开」会跳到应用商店。", style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 6.dp))
                        }
                    }
                }

                Spacer(Modifier.height(18.dp))
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                    Row {
                        if (status == Status.IDLE) TextButton(onClick = onConnect) { Text("连接 EasyClash") }
                        else TextButton(onClick = { refresh++ }, enabled = info != null) { Text("刷新") }
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        OutlinedButton(onClick = onCancel) { Text("取消") }
                        Button(onClick = onOpen) { Text("打开") }
                    }
                }
            }
        }
    }
}

@Composable
private fun Loading(text: String) {
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(vertical = 12.dp)) {
        CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
        Text("  $text", style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun Line(key: String, value: String, color: Color = Color.Unspecified) {
    Row(Modifier.fillMaxWidth().padding(vertical = 3.dp)) {
        Text(key, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.widthIn(min = 86.dp))
        Text(value, style = MaterialTheme.typography.bodyMedium, color = color, fontWeight = FontWeight.Medium)
    }
}

/** 收集要显示的信息：Claude 会走的线路、出口 IP，以及需要提醒的问题 */
private suspend fun loadInfo(ctx: Context, status: Status): GateInfo = withContext(Dispatchers.IO) {
    val s = EasyClashApp.instance.store.settings.value
    val warnings = mutableListOf<String>()
    val connected = status == Status.CONNECTED
    val api = VpnState.api

    // 1. Claude 会走的线路：国际 AI 服务强制走主组（无论首页模式）；关了这个开关时按首页模式
    var route = "未经过 EasyClash（直连，会用你的真实 IP）"
    var routeOk = false
    if (connected && api != null) {
        val start = when {
            s.aiForceProxy -> VpnState.mainGroup
            s.mode == ProxyMode.GLOBAL -> "GLOBAL"
            s.mode == ProxyMode.DIRECT -> null
            else -> VpnState.mainGroup
        }
        if (start == null) {
            route = "直连模式，且关了「国际 AI 服务强制走代理」：会用你的真实 IP"
        } else {
            var name: String = start
            var leaf: com.easyclash.data.ProxyItem? = null
            for (hop in 1..8) {
                val p = runCatching { api.proxy(name) }.getOrNull() ?: break
                leaf = p
                name = p.now ?: break
            }
            val lf = leaf
            route = when {
                lf == null -> "读取节点失败"
                lf.name == "DIRECT" -> "直连（会用你的真实 IP）"
                lf.name == "REJECT" -> "拒绝（打不开）"
                else -> {
                    routeOk = true
                    val region = s.lockedRegion?.let { c -> VpnState.regionGroups.firstOrNull { it.code == c } }
                    val d = when { lf.delay > 0 -> " · ${lf.delay} ms"; !lf.alive -> " · 超时"; else -> "" }
                    (if (region != null) "${region.displayName}（已锁定） · " else "") + lf.name + d
                }
            }
        }
    }
    if (!connected) warnings += "EasyClash 没有连接：Claude 会直接用你的真实 IP，可能打不开，或者被判定为不支持的地区。"
    else if (!routeOk) warnings += "Claude 现在不会经过代理。"

    // 2. 分应用：Claude 被排除在 VPN 外面时，规则管不到它
    if (connected) {
        val outside = when (s.appProxyMode) {
            AppProxyMode.ALL -> false
            AppProxyMode.ALLOW_LIST -> ClaudeGateActivity.CLAUDE_PKG !in s.appList
            AppProxyMode.DENY_LIST -> ClaudeGateActivity.CLAUDE_PKG in s.appList
        }
        if (outside) {
            warnings += "「应用」页的设置让 Claude 不经过 EasyClash，它会直连。请在「应用」页把 Claude 加进来（改完要重新连接）。"
            route = "未经过 EasyClash（被「应用」页排除）"
            routeOk = false
        }
    }

    // 3. 出口 IP：连接时经内核本地端口查
    val port = if (connected && s.mixedPort > 0) s.mixedPort else null
    var exitErr: String? = null
    val exit = runCatching { queryExit(port) }.getOrElse { exitErr = "查询失败（${it.message ?: "网络错误"}）"; null }
    if (connected && s.mixedPort <= 0) warnings += "「高级」页把本地代理端口关了，上面的出口 IP 是没经过代理查到的，不代表 Claude 的出口。"
    // Claude 不提供服务的地区（出口在这些地方时提醒）
    if (exit != null && routeOk && exit.countryCode.uppercase() in UNSUPPORTED) {
        warnings += "出口在「${exit.place}」，这个地区不在 Claude 的服务范围内，可能无法使用。建议在首页换一个出口地区（比如日本、美国、新加坡）。"
    }
    GateInfo(route, routeOk, exit, exitErr, warnings.distinct())
}

private val UNSUPPORTED = setOf("CN", "HK", "MO", "RU", "BY", "IR", "KP", "SY", "CU")

/** 查出口 IP：先 ip-api.com，失败再 ipwho.is（和 NetGateShow 一样） */
private fun queryExit(port: Int?): ExitInfo {
    val b = OkHttpClient.Builder().connectTimeout(8, TimeUnit.SECONDS).readTimeout(8, TimeUnit.SECONDS)
    if (port != null) b.proxy(Proxy(Proxy.Type.HTTP, InetSocketAddress("127.0.0.1", port)))
    val c = b.build()
    fun get(url: String): JSONObject = c.newCall(Request.Builder().url(url).header("User-Agent", "EasyClash").build()).execute().use {
        if (!it.isSuccessful) throw IllegalStateException("HTTP ${it.code}")
        JSONObject(it.body?.string() ?: "{}")
    }
    fun join(vararg p: String) = p.filter { it.isNotBlank() }.distinct().joinToString(" · ")
    return runCatching {
        val o = get("http://ip-api.com/json/?lang=zh-CN&fields=status,message,country,countryCode,regionName,city,isp,org,as,query")
        if (o.optString("status") != "success") throw IllegalStateException(o.optString("message"))
        ExitInfo(
            o.optString("query"), join(o.optString("country"), o.optString("regionName"), o.optString("city")),
            o.optString("countryCode"), join(o.optString("isp").ifBlank { o.optString("org") }, o.optString("as").substringBefore(' ')),
        )
    }.getOrElse {
        val o = get("https://ipwho.is/?lang=zh-CN")
        if (!o.optBoolean("success")) throw IllegalStateException(o.optString("message"))
        val conn = o.optJSONObject("connection")
        ExitInfo(
            o.optString("ip"), join(o.optString("country"), o.optString("region"), o.optString("city")),
            o.optString("country_code"), join(conn?.optString("isp").orEmpty(), conn?.optInt("asn")?.takeIf { it > 0 }?.let { "AS$it" }.orEmpty()),
        )
    }
}
