package com.easyclash.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.easyclash.data.ProxyMode
import com.easyclash.ui.MainViewModel
import com.easyclash.ui.components.HelpText
import com.easyclash.ui.components.KeyValueRow
import com.easyclash.ui.components.SectionCard
import com.easyclash.ui.components.humanBytes
import com.easyclash.vpn.Status

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun HomeScreen(vm: MainViewModel, onConnect: () -> Unit) {
    val status by vm.status.collectAsStateWithLifecycle()
    val error by vm.vpnError.collectAsStateWithLifecycle()
    val traffic by vm.traffic.collectAsStateWithLifecycle()
    val settings by vm.settings.collectAsStateWithLifecycle()
    val subs by vm.subs.collectAsStateWithLifecycle()
    val proxies by vm.proxies.collectAsStateWithLifecycle()
    val busy by vm.busy.collectAsStateWithLifecycle()

    val selectedSub = subs.firstOrNull { it.id == settings.selectedSubId }
    val connected = status == Status.CONNECTED

    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(vertical = 8.dp)) {

        // ================= 连接 =================
        SectionCard(
            title = "连接",
            help = "点「连接」后手机所有网络流量先经过本软件，再按下面的规则决定走代理还是直连。首次连接系统会弹出 VPN 授权，点「确定」即可。通知栏会常驻一个图标，用来随时断开。",
        ) {
            Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        when (status) {
                            Status.IDLE -> "未连接"
                            Status.CONNECTING -> "正在连接…"
                            Status.CONNECTED -> "已连接"
                            Status.STOPPING -> "正在断开…"
                        },
                        fontSize = 22.sp, fontWeight = FontWeight.Bold,
                    )
                    HelpText(selectedSub?.let { "当前订阅：${it.name}" } ?: "还没有订阅，请到「订阅」页添加")
                }
                when (status) {
                    Status.IDLE -> Button(onClick = onConnect, enabled = selectedSub != null) { Text("连接") }
                    Status.CONNECTED -> Button(onClick = { vm.disconnect() }, colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error)) { Text("断开") }
                    else -> CircularProgressIndicator(Modifier.padding(8.dp))
                }
            }
            if (error != null) {
                Spacer(Modifier.height(8.dp))
                Text("连接失败：$error", color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
                TextButton(onClick = { vm.clearError() }) { Text("知道了") }
            }
            if (connected) {
                Spacer(Modifier.height(8.dp))
                KeyValueRow("上行", humanBytes(traffic.first) + "/s")
                KeyValueRow("下行", humanBytes(traffic.second) + "/s")
                val main = proxies[vm.mainGroup]
                if (main != null) KeyValueRow("当前出口", resolveLeaf(proxies, vm.mainGroup))
            }
        }

        // ================= 模式 =================
        SectionCard(
            title = "出站模式",
            help = ProxyMode.entries.joinToString("\n\n") { "「${it.label}」：${it.help}" },
        ) {
            HelpText("决定流量怎么走。日常用「规则」就好。", Modifier.padding(bottom = 8.dp))
            SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                ProxyMode.entries.forEachIndexed { i, m ->
                    SegmentedButton(
                        selected = settings.mode == m,
                        onClick = { vm.setMode(m) },
                        shape = SegmentedButtonDefaults.itemShape(i, ProxyMode.entries.size),
                    ) { Text(m.label) }
                }
            }
            HelpText(settings.mode.help, Modifier.padding(top = 8.dp))
        }

        // ================= 地区 =================
        SectionCard(
            title = "代理出口地区",
            help = "软件会按节点名字自动把订阅里的节点按国家/地区分组，并在每个地区里自动挑延迟最低的那个节点。\n\n" +
                "选中一个地区后，所有走代理的流量都从该地区出去——包括订阅里单独分流的 Claude、ChatGPT、Gemini、Netflix 等；本来就直连的（国内网站等）仍然直连。哪怕订阅更新或重新连接也不变。\n\n" +
                "锁定期间如果在「节点」页给某个服务单独指定了别的节点（比如 Netflix 用日本），会保留你的这个选择。\n\n" +
                "注意：地区按机场给节点起的名字判断，并不能保证节点的真实出口 IP 就在那个国家。\n\n" +
                "选「不锁定」则恢复锁定前各策略组的选择。",
        ) {
            HelpText("把所有走代理的流量固定在某个国家/地区，自动用该地区最快的节点。", Modifier.padding(bottom = 8.dp))
            if (!connected) {
                HelpText("连接后会在这里显示订阅里有哪些地区可选。")
                if (settings.lockedRegion != null) HelpText("当前已锁定：${settings.lockedRegion}（连接后生效）")
            } else {
                val regions = vm.regionGroups
                if (regions.isEmpty()) {
                    HelpText("没能从节点名字里识别出地区。可以到「节点」页手动选择。")
                } else {
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        FilterChip(
                            selected = settings.lockedRegion == null,
                            onClick = { vm.lockRegion(null) },
                            label = { Text("不锁定") },
                        )
                        regions.forEach { r ->
                            val g = proxies[r.groupName]
                            val delay = g?.now?.let { proxies[it]?.delay } ?: 0
                            FilterChip(
                                selected = settings.lockedRegion == r.code,
                                onClick = { vm.lockRegion(r.code) },
                                label = { Text(if (delay > 0) "${r.flag} ${r.displayName} ${delay}ms" else "${r.flag} ${r.displayName}") },
                            )
                        }
                    }
                    Spacer(Modifier.height(8.dp))
                    val locked = settings.lockedRegion?.let { code -> regions.firstOrNull { it.code == code } }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        if (locked != null) {
                            Button(onClick = { vm.testRegion(locked.code) }, enabled = !busy) {
                                Text(if (busy) "测速中…" else "测速 ${locked.flag} ${locked.displayName}")
                            }
                        }
                        OutlinedButton(onClick = { regions.forEach { vm.testGroup(it.groupName) } }, enabled = !busy) {
                            Text(if (locked == null && busy) "测速中…" else "测速全部地区")
                        }
                    }
                    HelpText(
                        if (locked != null) "「测速 ${locked.displayName}」：只重测该地区的节点，并立刻换到其中最快的（当前节点卡了、不想等 5 分钟自动测速时点它）。「测速全部地区」会把所有地区都测一遍，用时较长。"
                        else "选中一个地区后，这里会多出一个只测该地区的按钮。「测速全部地区」会把所有地区都测一遍，各地区各自切到最快节点。",
                        Modifier.padding(top = 4.dp),
                    )
                    if (locked != null) {
                        Spacer(Modifier.height(4.dp))
                        KeyValueRow("锁定地区", "${locked.flag} ${locked.displayName}")
                        KeyValueRow("实际节点", resolveLeaf(proxies, locked.groupName))
                        HelpText("每 5 分钟自动测速一次并切到最快节点；手动测速立即生效。")
                    }
                }
            }
        }

        // ================= 提示 =================
        SectionCard(title = "使用小贴士") {
            HelpText("• 第一次使用：订阅页添加机场链接 → 回到首页点连接。")
            HelpText("• 某个 App 不想走代理（比如银行、国内视频）：到「应用」页排除它。")
            HelpText("• 网页打不开但全局模式能打开：说明被分到了直连，可到「高级」页加一条规则。")
            HelpText("• 看不懂的选项，点旁边的 ⓘ 都有解释。")
        }
        Spacer(Modifier.height(24.dp))
    }
}

/** 沿着策略组一路找到最终的真实节点名 */
fun resolveLeaf(proxies: Map<String, com.easyclash.data.ProxyItem>, start: String): String {
    var cur = start
    repeat(8) {
        val p = proxies[cur] ?: return cur
        val next = p.now ?: return cur
        cur = next
    }
    return cur
}
