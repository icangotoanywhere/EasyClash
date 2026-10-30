package com.easyclash.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.easyclash.data.RuleProviderInfo
import com.easyclash.ui.MainViewModel
import com.easyclash.ui.components.HelpText
import com.easyclash.ui.components.KeyValueRow
import com.easyclash.ui.components.SectionCard
import com.easyclash.ui.components.SwitchRow
import com.easyclash.vpn.Status
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

private const val RULES_HELP =
    "规则从哪来（从上往下逐条匹配，先命中的生效）：\n" +
        "① 本软件加在最前面的：拦截 QUIC、微软服务走国内，以及你在「附加规则」里填的；\n" +
        "② 订阅自带的规则：机场已经写好哪些网站直连、哪些走代理，原样使用；\n" +
        "③ 订阅引用的规则集（如 Netflix、ChatGPT、广告拦截、国内网站列表）：内核自动下载，并按订阅设定定期更新（通常每天一次）。这些规则集大多放在国外网站上，本软件固定让它们经代理下载；\n" +
        "④ 订阅没带规则时，本软件自动补一套基础分流：局域网、国内网站、国内 IP 直连，其余走代理。\n" +
        "没被任何规则命中的流量，交给订阅的兜底策略组（首页锁定了出口地区时，就走该地区）。\n\n" +
        "地理数据库（安装包里已自带）：\n" +
        "· GeoIP：判断一个 IP 属于哪个国家，「国内 IP 直连」这类规则靠它；\n" +
        "· GeoSite：按类别整理好的域名表，比如「国内常用网站」；\n" +
        "· ASN：判断 IP 属于哪家运营商或机构，少数规则会用到。\n" +
        "Clash Mi 等软件里的 GeoIP 功能就是这几个数据库，本软件同样具备。\n\n" +
        "更新数据库时只下载当前规则用得到的那几个，用不到的不更新，不浪费流量。"

private const val GEO_AUTO_HELP =
    "开启后，每次连接成功会检查数据库是否超过 7 天没更新；超过就在后台经代理下载最新版（GeoIP 约 8 MB），不影响正常上网。内容没变化时不会重复写入。下载失败会在 12 小时后再试。\n\n" +
        "关掉后数据库保持现状，仍可随时点「立即更新数据库」手动更新。"

/** 高级页顶部：说明规则来源与地理数据库，并显示当前状态 */
@Composable
fun RulesSection(vm: MainViewModel) {
    val settings by vm.settings.collectAsStateWithLifecycle()
    val status by vm.status.collectAsStateWithLifecycle()
    val info by vm.rulesInfo.collectAsStateWithLifecycle()
    val rulesBusy by vm.rulesBusy.collectAsStateWithLifecycle()
    val geoBusy by vm.geoBusy.collectAsStateWithLifecycle()
    val geoTick by vm.geoTick.collectAsStateWithLifecycle()
    var showProviders by remember { mutableStateOf(false) }
    val connected = status == Status.CONNECTED
    LaunchedEffect(connected) { if (connected) vm.refreshRulesInfo() }
    val geoFiles = remember(geoTick, connected) { vm.geoFiles() }
    val i = info

    SectionCard(title = "分流规则与地理数据库", help = RULES_HELP) {
        HelpText("分流规则和 GeoIP 等地理数据库都已内置，无需另外下载或设置。点右上角 ⓘ 看详细说明。", Modifier.padding(bottom = 8.dp))

        Text("规则", style = MaterialTheme.typography.titleSmall)
        if (connected && i != null) {
            KeyValueRow("当前生效规则", "${i.ruleCount} 条")
            val list = i.providers
            if (list.isEmpty()) {
                KeyValueRow("订阅引用的规则集", "无")
            } else {
                val loaded = list.count { !it.neverLoaded }
                KeyValueRow("订阅引用的规则集", "$loaded / ${list.size} 个已加载")
                KeyValueRow("规则集里共有", "${list.sumOf { it.ruleCount }} 条")
                if (loaded < list.size) {
                    HelpText("有规则集还没下载成功，点「更新规则集」重试；仍然失败可以在「诊断 → 查看日志」里看原因。", Modifier.padding(top = 2.dp))
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(top = 4.dp)) {
                OutlinedButton(onClick = { vm.updateRuleProviders() }, enabled = !rulesBusy && list.any { it.isHttp }) {
                    Text(if (rulesBusy) "更新中…" else "更新规则集")
                }
                if (list.isNotEmpty()) TextButton(onClick = { showProviders = true }) { Text("查看明细") }
            }
        } else {
            HelpText("连接后这里会显示当前生效的规则数和规则集下载情况。")
        }

        Spacer(Modifier.height(12.dp))
        Text("地理数据库", style = MaterialTheme.typography.titleSmall)
        geoFiles.forEach { f ->
            val tail = when {
                !connected || i == null -> ""
                f.key in i.geoInUse -> " · 使用中"
                else -> " · 当前规则未用到"
            }
            KeyValueRow(f.label, (f.date ?: "已内置，首次连接时启用") + tail)
        }
        KeyValueRow("上次检查更新", if (settings.geoLastCheck > 0) fmtTime(settings.geoLastCheck) else "还没有")
        SwitchRow(
            "自动更新地理数据库", "每 7 天检查一次，连接后在后台进行。默认开。",
            settings.geoAutoUpdate,
            detail = GEO_AUTO_HELP,
        ) { v -> vm.updateSettings(reload = false) { it.geoAutoUpdate = v } }
        OutlinedButton(onClick = { vm.updateGeoNow() }, enabled = connected && !geoBusy) {
            Text(if (geoBusy) "正在更新…（约 1 分钟）" else "立即更新数据库")
        }
        if (!connected) HelpText("连接后可用（更新时经代理下载）。")
    }

    if (showProviders) ProvidersDialog(i?.providers.orEmpty()) { showProviders = false }
}

@Composable
private fun ProvidersDialog(list: List<RuleProviderInfo>, onClose: () -> Unit) {
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("规则集明细（${list.size} 个）") },
        text = {
            Column {
                HelpText("规则集由订阅指定，内核自动下载，并按订阅设定的间隔更新。", Modifier.padding(bottom = 4.dp))
                LazyColumn(Modifier.fillMaxWidth().heightIn(max = 420.dp)) {
                    items(list, key = { it.name }) { p ->
                        Row(Modifier.fillMaxWidth().padding(vertical = 3.dp), horizontalArrangement = Arrangement.SpaceBetween) {
                            Text(p.name, style = MaterialTheme.typography.bodyMedium)
                            Text(
                                if (p.neverLoaded) "未下载" else "${p.ruleCount} 条 · ${p.updatedAt.take(10)}",
                                style = MaterialTheme.typography.bodySmall,
                                color = if (p.neverLoaded) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onClose) { Text("关闭") } },
    )
}

private fun fmtTime(ms: Long): String = SimpleDateFormat("yyyy-MM-dd HH:mm", Locale.CHINA).format(Date(ms))
