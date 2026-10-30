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
import androidx.lifecycle.compose.collectAsStateWithLifecycle
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
                detail = "有些境外网站按规则是直连的（例如微软服务，或订阅规则没覆盖到、事先说不清会不会被墙的境外地址），偶尔会连不上。开启后：\n\n• 规则让它直连、而目标在境外时，如果直连连不上（超时、被拒绝、域名解析失败），自动改走代理再试一次。走的是首页选的出口地区（没锁定地区时走订阅的主策略组）。\n• 同一个网站失败过一次就记住，本次连接期间直接走代理，不再先试直连，只慢第一次。断开重连后重新判断。\n• 国内网站（在国内网站列表里，或 IP 在国内）直连失败按正常失败处理，不重试；局域网地址也不重试。\n\n只会"直连失败改走代理"，反过来绝不会：代理连不上时宁可打不开，也不会偷偷改用直连，避免你的真实 IP 暴露给 Claude、Google 等服务。\n\n局限：只能发现"连不上"。已经连上、之后才被掐断或限速的情况发现不了；UDP（如 QUIC）也不在此列。哪些网站被改走了代理，可以在内核日志里看到（带 [EasyClash] 字样）。\n\n首页选了"直连"模式时不起作用。",
            ) { v -> vm.updateSettings { it.directFallback = v } }
        }

        // ================= 自动选节点 =================
        var kw by remember(settings.backupKeyword) { mutableStateOf(settings.backupKeyword) }
        var penalty by remember(settings.backupPenaltyMs) { mutableStateOf(settings.backupPenaltyMs.toString()) }
        var interval by remember(settings.testIntervalMin) { mutableStateOf(settings.testIntervalMin.toString()) }
        SectionCard(
            title = "自动测速与选节点",
            help = "【自动测速间隔】锁定地区后，每隔这么多分钟把该地区的节点测一遍，自动换到最快的（分级时同时判断用优先还是备用）。一次测速每个节点只发一个很小的请求，21 个节点约 10 KB 流量；间隔越短换节点越及时，但手机上会略微增加耗电，建议 2～10 分钟。\n\n【优先 / 备用】有些节点测延迟很快，真用起来却经常卡（典型的是经 CDN「中转」的节点：到 CDN 很近，但 CDN 再回机场源站那一段不稳）。纯按延迟挑，总是会选中它们。\n\n" +
                "开启分级后，每个地区的「·自动」组会拆成两级：\n" +
                "• 优先：名字不含关键字的节点，平时一律在这里面挑最快的\n" +
                "• 备用：名字含关键字的节点\n\n" +
                "每个测速周期（5 分钟）把两级都测一遍：只有当「优先」里最快的节点比「备用」里最快的还慢超过设定毫秒数时，才改用备用；优先节点恢复后自动切回。优先级全部超时也会自动用备用。\n\n" +
                "关键字留空 = 不分级，纯按延迟选。如果你的机场「中转」节点质量很好，就把关键字清掉。",
        ) {
            OutlinedTextField(
                value = interval, onValueChange = { interval = it.filter(Char::isDigit).take(4) },
                label = { Text("自动测速间隔（分钟）") }, singleLine = true, modifier = Modifier.fillMaxWidth(),
            )
            HelpText("默认 5。锁定地区时每隔这么久自动重测并切到最快节点。", Modifier.padding(bottom = 8.dp))
            HelpText("名字含关键字的节点降为备用，只在其他节点明显更慢时才用。", Modifier.padding(bottom = 4.dp))
            OutlinedTextField(
                value = kw, onValueChange = { kw = it.take(20) },
                label = { Text("备用节点关键字（留空 = 不分级）") },
                placeholder = { Text("留空 = 不分级，所有节点只按延迟选") },
                singleLine = true, modifier = Modifier.fillMaxWidth(),
            )
            HelpText(backupKeywordEffect(kw), Modifier.padding(top = 4.dp))
            Spacer(Modifier.height(8.dp))
            OutlinedTextField(
                value = penalty, onValueChange = { penalty = it.filter(Char::isDigit).take(4) },
                label = { Text("优先比备用慢多少毫秒才用备用") }, singleLine = true, modifier = Modifier.fillMaxWidth(),
            )
            HelpText("默认 100：优先节点 250ms、备用 100ms → 慢了 150ms，超过 100 → 用备用；优先 180ms、备用 100ms → 只慢 80ms → 仍用优先。")
            TextButton(onClick = {
                val p = penalty.toIntOrNull() ?: 100
                val m = (interval.toIntOrNull() ?: 5).coerceIn(1, 1440)
                vm.updateSettings { it.backupKeyword = kw.trim(); it.backupPenaltyMs = p.coerceIn(0, 5000); it.testIntervalMin = m }
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

        // ================= 快捷开关 =================
        SectionCard(title = "下拉菜单快捷开关", help = "在手机顶部下拉的快捷设置里放一个 EasyClash 磁贴，点一下连接、再点一下断开，不用打开 App。\n\n安卓 13 及以上可一键添加；更老的系统请下拉通知栏 → 点编辑（铅笔图标）→ 把 EasyClash 拖到上面。") {
            HelpText("把连接开关放到系统下拉菜单里。", Modifier.padding(bottom = 4.dp))
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

/** 备用节点关键字输入框下方的"当前效果"说明：只有空格也按留空处理（保存时会去掉首尾空格） */
private fun backupKeywordEffect(raw: String): String {
    val kw = raw.trim()
    return when {
        kw.isEmpty() && raw.isNotEmpty() -> "当前效果：只填了空格，保存时会去掉，等同留空——不分级，同一地区所有节点只按延迟挑最快的。"
        kw.isEmpty() -> "当前效果：不分级，同一地区所有节点只按延迟挑最快的。"
        kw != raw -> "当前效果：名字含「$kw」的节点作备用，其余为优先（首尾空格保存时会自动去掉）。某地区没有这类节点时，该地区不分级。"
        else -> "当前效果：名字含「$kw」的节点作备用，其余为优先。某地区没有这类节点时，该地区不分级。"
    }
}
