package com.easyclash.ui.screens

import android.content.ClipData
import android.content.ClipboardManager
import android.app.StatusBarManager
import android.content.ComponentName
import android.content.Context
import android.graphics.drawable.Icon
import android.os.Build
import com.easyclash.R
import com.easyclash.vpn.EasyClashTileService
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.easyclash.vpn.KeepAlive
import com.easyclash.vpn.VpnState
import com.easyclash.data.TunStack
import com.easyclash.ui.MainViewModel
import com.easyclash.ui.components.HelpText
import com.easyclash.ui.components.KeyValueRow
import com.easyclash.ui.components.SectionCard
import com.easyclash.ui.components.SettingRow
import com.easyclash.ui.components.SwitchRow
import com.easyclash.ui.components.humanBytes
import com.easyclash.vpn.Status
import kotlinx.coroutines.delay

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun AdvancedScreen(vm: MainViewModel) {
    val settings by vm.settings.collectAsStateWithLifecycle()
    val status by vm.status.collectAsStateWithLifecycle()
    var showLogs by remember { mutableStateOf(false) }
    var showConns by remember { mutableStateOf(false) }
    var coreVer by remember { mutableStateOf("…") }
    val ctx = LocalContext.current
    LaunchedEffect(Unit) { coreVer = vm.coreVersion() }

    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(vertical = 8.dp)) {

        // ================= 规则与地理数据库 =================
        RulesSection(vm)

        // ================= 网络 =================
        SectionCard(title = "网络选项", help = "一般不需要改。遇到具体问题时按说明调整，改完会自动重载（已连接时）。") {
            SwitchRow(
                "拦截 QUIC（UDP 443）", "解决 Google Play 下载卡在「等待中」、YouTube 卡顿等问题。默认开。",
                settings.blockQuic,
                detail = "很多 Google 服务优先用 QUIC（走 UDP 443 端口）。不少代理线路对 UDP 支持不好，会导致一直转圈。开启后会拒绝 QUIC，应用自动改用普通 TCP，几乎没有副作用。",
            ) { v -> vm.updateSettings { it.blockQuic = v } }
            SwitchRow(
                "IPv6", "默认关。打开后代理也转发 IPv6 流量。",
                settings.ipv6,
                detail = "大多数机场节点不支持 IPv6。打开后若出现部分网站打不开、连接时断时续，请关掉。",
            ) { v -> vm.updateSettings { it.ipv6 = v } }
            SwitchRow(
                "使用订阅自带的 DNS 设置", "默认关，用本软件内置的 DNS（国内 DoH + fake-ip）。",
                settings.useSubscriptionDns,
                detail = "机场订阅里通常也带一段 DNS 配置。如果你信任机场的配置或者遇到解析问题想对比一下，可以打开。若订阅里没有 DNS 配置，此开关无效。",
            ) { v -> vm.updateSettings { it.useSubscriptionDns = v } }

            SettingRow("TUN 网络栈", "决定如何处理手机发来的网络数据包。", detail = TunStack.entries.joinToString("\n") { it.label }) { }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                TunStack.entries.forEach { s ->
                    FilterChip(selected = settings.tunStack == s, onClick = { vm.updateSettings { it.tunStack = s } }, label = { Text(s.coreValue) })
                }
            }
            HelpText("改动后需要断开重连才生效。", Modifier.padding(top = 4.dp, bottom = 8.dp))

            SettingRow("日志级别", "排查问题时改成 debug，平时 info 即可。") { }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf("silent", "error", "warning", "info", "debug").forEach { l ->
                    FilterChip(selected = settings.logLevel == l, onClick = { vm.updateSettings { it.logLevel = l } }, label = { Text(l) })
                }
            }

            var port by remember(settings.mixedPort) { mutableStateOf(settings.mixedPort.toString()) }
            Spacer(Modifier.height(8.dp))
            OutlinedTextField(
                value = port, onValueChange = { port = it.filter(Char::isDigit).take(5) },
                label = { Text("本机 HTTP/SOCKS 代理端口") }, singleLine = true, modifier = Modifier.fillMaxWidth(),
            )
            HelpText("给本机其他软件用的 HTTP/SOCKS 代理口（127.0.0.1:端口）。默认 17890，避开其他 Clash 常占的 7890。填 0 表示关闭。")
            TextButton(onClick = { port.toIntOrNull()?.let { p -> if (p == 0 || p in 1024..65535) vm.updateSettings { it.mixedPort = p } } }) { Text("保存端口") }

            SwitchRow(
                "微软服务走国内（含必应国内版）", "Office、Outlook、OneDrive、Windows 更新、必应等直连；Copilot、GitHub 仍走代理。默认开。",
                settings.bingDirect,
                detail = "微软的服务在国内都能直连，而且直连通常更快更稳。开启后这些服务一律直连，不随代理出口切换地区：\n• 必应（访问到的就是国内版 cn.bing.com）\n• Office / Microsoft 365、Outlook / Hotmail、OneDrive、Teams、Skype\n• Windows 更新、微软商店、Xbox、微软账号登录、MSN 等\n\n例外（照常按订阅规则走代理）：\n• Copilot：国内不提供服务，直连会用不了\n• GitHub：虽属微软，但国内直连经常很慢、时断时续\n\n想用国际版必应，或想让微软服务也走代理，关掉它即可。",
            ) { v -> vm.updateSettings { it.bingDirect = v } }
            SwitchRow(
                "出境直连失败自动转代理", "发往境外的直连连不上时，自动改走代理再试一次。默认开。",
                settings.directFallback,
                detail = "有些境外网站按规则是直连的（例如微软服务，或订阅规则没覆盖到、事先说不清会不会被墙的境外地址），偶尔会连不上。开启后：\n\n• 规则让它直连、而目标在境外时，如果直连连不上（超时、被拒绝、域名解析失败），自动改走代理再试一次。走的是首页选的出口地区（没锁定地区时走订阅的主策略组）。\n• 同一个网站失败过一次就记住，本次连接期间直接走代理，不再先试直连，只慢第一次。断开重连后重新判断。\n• 国内网站（在国内网站列表里，或 IP 在国内）直连失败按正常失败处理，不重试；局域网地址也不重试。\n\n只会「直连失败改走代理」，反过来绝不会：代理连不上时宁可打不开，也不会偷偷改用直连，避免你的真实 IP 暴露给 Claude、Google 等服务。\n\n局限：只能发现「连不上」。已经连上、之后才被掐断或限速的情况发现不了；UDP（如 QUIC）也不在此列。哪些网站被改走了代理，可以在内核日志里看到（带 [EasyClash] 字样）。\n\n首页选了「直连」模式时不起作用。",
            ) { v -> vm.updateSettings { it.directFallback = v } }
            SwitchRow(
                "国际 AI 服务强制走代理", "ChatGPT、Claude、Gemini 等国际 AI 服务的网站一律走代理、绝不直连。默认开。",
                settings.aiForceProxy,
                detail = "开启后，下面这些域名（含子域名）的连接一律走代理（首页选的出口地区），并且绝不直连：不管订阅规则怎么写、策略组里选的是不是直连、首页是不是「直连」模式。万一某条连接按规则要直连，会自动改走代理；代理不可用时直接打不开，不会退回直连暴露你的真实 IP。\n\nApp 和浏览器里用都受保护。只按网站域名判断，不影响其他网站，也不拖慢网速。\n\n包括：" + com.easyclash.core.ConfigBuilder.AI_DOMAINS.joinToString("、") + "\n\n「应用」页里被排除（不走代理）的 App 不受保护：它们完全不经过本软件。",
            ) { v -> vm.updateSettings { it.aiForceProxy = v } }
        }

        // ================= 自动测速 =================
        var interval by remember(settings.testIntervalMin) { mutableStateOf(settings.testIntervalMin.toString()) }
        SectionCard(
            title = "自动测速",
            help = "锁定地区后，每隔这么多分钟把该地区的节点测一遍，自动换到最快的。一次测速每个节点只发一个很小的请求，21 个节点约 10 KB 流量；间隔越短换节点越及时，但手机上会略微增加耗电，建议 2～10 分钟。",
        ) {
            OutlinedTextField(
                value = interval, onValueChange = { interval = it.filter(Char::isDigit).take(4) },
                label = { Text("自动测速间隔（分钟）") }, singleLine = true, modifier = Modifier.fillMaxWidth(),
            )
            HelpText("默认 5。锁定地区时每隔这么久自动重测并切到最快节点。")
            TextButton(onClick = {
                val m = (interval.toIntOrNull() ?: 5).coerceIn(1, 1440)
                vm.updateSettings { it.testIntervalMin = m }
            }) { Text("保存") }
        }

        // ================= 附加规则 =================
        var rules by remember(settings.extraRules) { mutableStateOf(settings.extraRules) }
        SectionCard(
            title = "附加规则",
            help = "每行一条，插在订阅规则的最前面（优先级最高）。格式：类型,内容,去向\n\n" +
                "常用类型：\n" +
                "DOMAIN-SUFFIX,example.com,DIRECT    域名后缀匹配\n" +
                "DOMAIN-KEYWORD,google,PROXY        域名包含关键词\n" +
                "DOMAIN,www.a.com,REJECT            完整域名\n" +
                "IP-CIDR,8.8.8.0/24,DIRECT          IP 段\n" +
                "GEOIP,CN,DIRECT                    按 IP 所属国家\n" +
                "GEOSITE,cn,DIRECT                  按网站分类（内置数据库）\n\n" +
                "去向可以是 DIRECT（直连）、REJECT（拒绝）或你订阅里任何一个策略组的名字。\n\n" +
                "以 # 开头的行是注释，会被忽略。",
        ) {
            HelpText("想让某个网站强制直连/走代理，在这里加一行。", Modifier.padding(bottom = 4.dp))
            OutlinedTextField(
                value = rules, onValueChange = { rules = it },
                placeholder = { Text("DOMAIN-SUFFIX,bing.com,DIRECT\nDOMAIN-SUFFIX,gvt1.com,DIRECT") },
                modifier = Modifier.fillMaxWidth().heightIn(min = 120.dp),
                textStyle = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { vm.updateSettings { it.extraRules = rules } }) { Text("保存规则") }
                OutlinedButton(onClick = { rules = PRESET_RULES }) { Text("填入常用示例") }
            }
        }

        // ================= 覆写 =================
        var ov by remember(settings.overrideYaml) { mutableStateOf(settings.overrideYaml) }
        SectionCard(
            title = "YAML 覆写（高级）",
            help = "直接修改最终配置，写法与 Clash Verge / Clash Party 一致：\n\n" +
                "+rules:            插到规则最前面\n" +
                "rules+:            追加到规则最后面\n" +
                "+proxy-groups:     在策略组前面插入\n" +
                "dns:               整段替换/合并 DNS 设置\n" +
                "其他任何顶层字段：直接覆盖订阅里的同名字段\n\n" +
                "注意：tun、external-controller、端口等字段由软件接管，写了也无效。写错会导致连接失败，报错信息会显示在首页。",
        ) {
            HelpText("不懂 YAML 就别碰。附加规则和地区分组已经能满足绝大多数需求。", Modifier.padding(bottom = 4.dp))
            OutlinedTextField(
                value = ov, onValueChange = { ov = it },
                placeholder = { Text("+rules:\n  - DOMAIN-SUFFIX,example.com,DIRECT") },
                modifier = Modifier.fillMaxWidth().heightIn(min = 120.dp),
                textStyle = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
            )
            Button(onClick = { vm.updateSettings { it.overrideYaml = ov } }) { Text("保存覆写") }
        }

        // ================= 后台常驻 =================
        KeepAliveSection()

        // ================= 通知与快捷开关 =================
        SectionCard(title = "通知与快捷开关", help = "常驻通知：连接后通知栏（和锁屏）上显示当前节点、最近一次延迟和速度，带「测速」按钮。\n\n快捷开关：在手机顶部下拉的快捷设置里放一个 EasyClash 磁贴，点一下连接、再点一下断开，不用打开 App。安卓 13 及以上可一键添加；更老的系统请下拉通知栏 → 点编辑（铅笔图标）→ 把 EasyClash 拖到上面。") {
            SwitchRow(
                "通知里显示「断开」按钮", "默认不显示，免得误触。断开可以在 App 首页或下拉快捷开关里操作。",
                settings.notifyStopButton,
            ) { v -> vm.updateSettings { it.notifyStopButton = v } }
            HelpText("把连接开关放到系统下拉菜单里。", Modifier.padding(top = 8.dp, bottom = 4.dp))
            if (Build.VERSION.SDK_INT >= 33) {
                OutlinedButton(onClick = {
                    runCatching {
                        val sbm = ctx.getSystemService(StatusBarManager::class.java)
                        sbm.requestAddTileService(
                            ComponentName(ctx, EasyClashTileService::class.java),
                            "EasyClash",
                            Icon.createWithResource(ctx, R.drawable.ic_notify),
                            ctx.mainExecutor,
                        ) { }
                    }
                }) { Text("添加到快捷设置") }
            } else {
                HelpText("本机系统版本较老：下拉通知栏 → 编辑磁贴 → 把 EasyClash 拖上去即可。")
            }
        }

        // ================= 诊断 =================
        SectionCard(title = "诊断", help = "日志：内核运行时的记录，连接出问题时看这里。\n连接：当前所有网络连接走了哪条规则、哪个节点，可以用来确认某个 App 有没有走代理。") {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = { showLogs = true }, enabled = status == Status.CONNECTED) { Text("查看日志") }
                OutlinedButton(onClick = { showConns = true }, enabled = status == Status.CONNECTED) { Text("查看连接") }
            }
            if (status != Status.CONNECTED) HelpText("连接后可用。")
            val crashFile = remember { com.easyclash.EasyClashApp.instance.crashFile }
            var crashText by remember { mutableStateOf(runCatching { if (crashFile.exists()) crashFile.readText() else "" }.getOrDefault("")) }
            if (crashText.isNotBlank()) {
                Spacer(Modifier.height(8.dp))
                Text("上次异常退出记录", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.error)
                Text(crashText.lines().take(12).joinToString("\n"), fontSize = 11.sp, fontFamily = FontFamily.Monospace, modifier = Modifier.padding(vertical = 4.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = {
                        val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                        cm.setPrimaryClip(ClipData.newPlainText("EasyClash 崩溃记录", crashText))
                    }) { Text("复制崩溃记录") }
                    TextButton(onClick = { crashFile.delete(); crashText = "" }) { Text("清除") }
                }
                HelpText("App 上次不是正常退出的，把这段复制发给 Claude 可以定位原因。")
            }
        }

        // ================= 关于 =================
        val appVer = remember { runCatching { ctx.packageManager.getPackageInfo(ctx.packageName, 0).versionName }.getOrNull() ?: "?" }
        SectionCard(title = "关于") {
            KeyValueRow("软件", "EasyClash $appVer")
            KeyValueRow("内核", "mihomo $coreVer")
            HelpText("本软件只是 mihomo 内核的一个简化界面，节点和线路都来自你自己的订阅。", Modifier.padding(top = 4.dp))
        }
        Spacer(Modifier.height(24.dp))
    }

    if (showLogs) LogsDialog(vm) { showLogs = false }
    if (showConns) ConnectionsDialog(vm) { showConns = false }
}

private const val PRESET_RULES = """# Google Play 下载直连（国内有节点，直连更快）
DOMAIN-SUFFIX,gvt1.com,DIRECT
DOMAIN-SUFFIX,gvt2.com,DIRECT
DOMAIN-SUFFIX,dl.google.com,DIRECT
# 局域网直连
IP-CIDR,192.168.0.0/16,DIRECT,no-resolve
IP-CIDR,10.0.0.0/8,DIRECT,no-resolve"""

@Composable
private fun LogsDialog(vm: MainViewModel, onClose: () -> Unit) {
    val logs by vm.logs.collectAsStateWithLifecycle()
    val ctx = LocalContext.current
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("内核日志（${logs.size} 行）") },
        text = {
            Column {
                HelpText("从本次连接开始收集。排查问题时点「复制」，粘贴发给 Claude。", Modifier.padding(bottom = 4.dp))
                LazyColumn(Modifier.fillMaxWidth().heightIn(max = 400.dp)) {
                    if (logs.isEmpty()) item { HelpText("暂无日志") }
                    items(logs) { Text(it, fontSize = 11.sp, fontFamily = FontFamily.Monospace, modifier = Modifier.padding(vertical = 1.dp)) }
                }
            }
        },
        confirmButton = {
            Row {
                TextButton(onClick = {
                    val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                    cm.setPrimaryClip(ClipData.newPlainText("EasyClash 日志", vm.logsText()))
                }) { Text("复制") }
                TextButton(onClick = onClose) { Text("关闭") }
            }
        },
        dismissButton = { TextButton(onClick = { vm.clearLogs() }) { Text("清空") } },
    )
}

@Composable
private fun ConnectionsDialog(vm: MainViewModel, onClose: () -> Unit) {
    val conns by vm.connections.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { while (true) { vm.refreshConnections(); delay(2000) } }
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("当前连接（${conns.size}）") },
        text = {
            LazyColumn(Modifier.fillMaxWidth().heightIn(max = 420.dp)) {
                if (conns.isEmpty()) item { HelpText("暂无连接") }
                items(conns, key = { it.id }) { c ->
                    Column(Modifier.fillMaxWidth().padding(vertical = 4.dp)) {
                        Text(c.host, style = MaterialTheme.typography.bodyMedium)
                        HelpText("${c.network} ${c.destination}")
                        HelpText("规则：${c.rule}")
                        HelpText("链路：${c.chain}")
                        HelpText("↑${humanBytes(c.upload)} ↓${humanBytes(c.download)}")
                        TextButton(onClick = { vm.closeConnection(c.id) }) { Text("断开此连接") }
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onClose) { Text("关闭") } },
        dismissButton = { TextButton(onClick = { vm.closeAllConnections() }) { Text("全部断开") } },
    )
}

/**
 * 后台常驻：检测电池优化、「始终开启的 VPN」，并跳到对应的系统设置。
 * 从系统设置返回本页时会重新检测。
 */
@Composable
private fun KeepAliveSection() {
    val ctx = LocalContext.current
    var battery by remember { mutableStateOf(KeepAlive.ignoringBatteryOptimizations(ctx)) }
    LifecycleResumeEffect(Unit) {
        battery = KeepAlive.ignoringBatteryOptimizations(ctx)
        onPauseOrDispose { }
    }
    val alwaysOn by VpnState.alwaysOn.collectAsStateWithLifecycle()
    val lockdown by VpnState.lockdown.collectAsStateWithLifecycle()
    SectionCard(
        title = "后台常驻",
        help = "手机系统为了省电，会在后台把软件杀掉，VPN 就断了。按下面几项放开，EasyClash 就能一直在后台运行：\n\n" +
            "① 电池优化：允许 EasyClash 在后台运行、不做电池优化。这一项最重要：放开后，即使被系统杀掉，安卓也允许它自动重新启动并重连。\n\n" +
            "② 始终开启的 VPN：系统设置 → VPN → EasyClash 旁边的齿轮，打开「始终开启的 VPN」。开机会自动连接，被杀后系统也会重新拉起。" +
            "注意不要勾「屏蔽未使用 VPN 的连接」：勾了之后，EasyClash 断开时整机断网，「应用」页里被排除的 App 也可能一起断网。\n\n" +
            "③ 自启动 / 后台活动：国产手机还有自己的一套管控，见下面按品牌的提示。\n\n" +
            "④ 多任务界面里把 EasyClash 的卡片锁住（下拉或长按卡片，选「锁定」），一键清理时就不会被关掉。\n\n" +
            "EasyClash 本身已经做了：前台服务 + 常驻通知；被系统杀掉后自动重新拉起并重连。被另一个 VPN 顶掉、或你在系统设置里手动断开时，不会自动抢回来。",
    ) {
        KeyValueRow("电池优化", if (battery) "已放开（不受限制）✓" else "受限制（后台容易被杀）")
        if (!battery) {
            OutlinedButton(onClick = { KeepAlive.open(ctx, KeepAlive.batteryIntent(ctx), KeepAlive.batteryListIntent(), KeepAlive.appDetailsIntent(ctx)) }) {
                Text("允许在后台运行")
            }
        }
        KeyValueRow(
            "始终开启的 VPN",
            when (alwaysOn) {
                true -> if (lockdown == true) "已开启（且勾了屏蔽未使用 VPN 的连接）" else "已开启 ✓"
                false -> "未开启"
                null -> "连接后可查看"
            },
        )
        if (lockdown == true) HelpText("建议取消勾选「屏蔽未使用 VPN 的连接」：否则 EasyClash 断开时整机断网，被排除的 App 也可能连不上。")
        OutlinedButton(onClick = { KeepAlive.open(ctx, KeepAlive.vpnSettingsIntent()) }) { Text("打开系统 VPN 设置") }
        HelpText("在 EasyClash 旁边的齿轮里打开「始终开启的 VPN」。", Modifier.padding(bottom = 8.dp))

        HelpText("自启动 / 后台活动：" + KeepAlive.romTip(), Modifier.padding(bottom = 4.dp))
        OutlinedButton(onClick = { KeepAlive.open(ctx, KeepAlive.appDetailsIntent(ctx)) }) { Text("打开本应用的系统设置") }
        HelpText("另外，在多任务界面把 EasyClash 的卡片锁住，一键清理时就不会被关掉。", Modifier.padding(top = 4.dp))
    }
}
