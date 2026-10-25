package com.easyclash.ui.screens

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.easyclash.data.ProxyItem
import com.easyclash.ui.MainViewModel
import com.easyclash.ui.components.HelpText
import com.easyclash.ui.components.InfoButton
import com.easyclash.ui.components.SectionCard
import com.easyclash.vpn.Status

/**
 * 节点页：按层级显示策略组。
 * 顶层只放"没有被其他组引用"的组（通常就是主选择组），组里包含的子组缩进显示，可继续展开。
 */
@Composable
fun ProxiesScreen(vm: MainViewModel) {
    val status by vm.status.collectAsStateWithLifecycle()
    val proxies by vm.proxies.collectAsStateWithLifecycle()
    val busy by vm.busy.collectAsStateWithLifecycle()

    if (status != Status.CONNECTED) {
        Column(Modifier.fillMaxSize().padding(vertical = 8.dp)) {
            SectionCard(title = "节点", help = "这里按层级列出订阅里的策略组和节点。策略组是一组节点的集合，比如「自动选择」会自动挑最快的，「节点选择」由你手动选。") {
                HelpText("连接后才能查看和切换节点。先回首页点「连接」。")
            }
        }
        return
    }

    val main = vm.mainGroup
    val groups = proxies.values.filter { it.isGroup && it.name != "GLOBAL" }
    val referenced = groups.flatMap { it.all }.toSet()
    // 顶层：主组永远第一个；其余只放没被任何组引用的组
    val roots = buildList {
        proxies[main]?.let { add(it) }
        addAll(groups.filter { it.name != main && it.name !in referenced })
    }

    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 8.dp)) {
        item {
            Row(Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("策略组", style = MaterialTheme.typography.titleMedium)
                    HelpText("★ 是主组，决定代理流量最终从哪出去。缩进的是它包含的子组，点 ▸ 展开。")
                }
                InfoButton(
                    "节点页说明",
                    "• ★ 主组：订阅的总开关，选中哪一项，代理流量就走哪一项。\n" +
                        "• 主组里可以选单个节点，也可以选一个子组（如「自动选择」「🌍 地区选择」），再由子组决定具体节点。\n" +
                        "• 「🌍 地区选择」是本软件自动生成的，里面每个地区都会自动选该地区最快的节点。首页的「代理出口地区」就是它的快捷入口，两边是同一个设置。\n" +
                        "• 带「自动」「故障转移」字样的组由内核自动选择，不能手动指定具体节点。\n" +
                        "• 延迟（ms）越小越快；「超时」表示这个节点当前不可用。\n" +
                        "• 在主组里手动选了节点后，首页的地区锁定会自动解除。",
                )
            }
        }
        item {
            SectionCard(title = "和首页「代理出口地区」的关系") {
                HelpText("首页选地区 = 在这里把 ★ 主组选成「🌍 地区选择」，再把「地区选择」选成对应国家的「·自动」组。两边改的是同一个东西，不会冲突。")
                HelpText("• 想固定出口国家、让软件自动挑该国最快节点：用首页的地区按钮，这里不用动。", Modifier.padding(top = 4.dp))
                HelpText("• 想指定某一个具体节点，或用订阅自带的「自动选择」「故障转移」：在这里的 ★ 主组里选，首页的地区锁定会自动解除。")
                HelpText("• 首页显示「不锁定」时，出口就是这里 ★ 主组当前选中的那一项。")
            }
        }
        items(roots, key = { it.name }) { g ->
            Card(
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp),
                colors = CardDefaults.cardColors(containerColor = if (g.name == main) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerLow),
            ) {
                Column(Modifier.padding(12.dp)) {
                    GroupNode(g, proxies, depth = 0, isMain = g.name == main, initiallyExpanded = g.name == main, busy = busy, vm = vm, parent = null)
                }
            }
        }
    }
}

/**
 * 递归渲染一个策略组：
 * 标题行（可展开/测速）+ 展开后的子项；子项若也是组，则缩进一层继续递归。
 * @param parent 若不为 null，说明本组是 parent 组里的一个可选项，标题行前面要带单选按钮
 */
@Composable
private fun GroupNode(
    g: ProxyItem,
    proxies: Map<String, ProxyItem>,
    depth: Int,
    isMain: Boolean,
    initiallyExpanded: Boolean,
    busy: Boolean,
    vm: MainViewModel,
    parent: ProxyItem?,
) {
    var expanded by remember(g.name) { mutableStateOf(initiallyExpanded) }
    val indent = (depth * 20).dp

    // ---- 标题行 ----
    Row(
        Modifier.fillMaxWidth().padding(start = indent, top = 4.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (parent != null) {
            RadioButton(
                selected = parent.now == g.name,
                onClick = if (parent.isSelectable) ({ vm.selectProxy(parent.name, g.name) }) else null,
                enabled = parent.isSelectable,
            )
        }
        Column(Modifier.weight(1f).clickable { expanded = !expanded }) {
            Text((if (isMain) "★ " else "") + g.name, style = if (depth == 0) MaterialTheme.typography.titleMedium else MaterialTheme.typography.bodyLarge)
            HelpText("${typeLabel(g.type)} · 当前：${g.now ?: "—"}" + (if (g.now != null && proxies[g.now]?.isGroup == true) " → ${resolveLeaf(proxies, g.name)}" else ""))
        }
        if (depth < 2 || g.isSelectable) TextButton(onClick = { vm.testGroup(g.name) }, enabled = !busy) { Text("测速") }
        IconButton(onClick = { expanded = !expanded }) {
            Icon(if (expanded) Icons.Filled.ExpandLess else Icons.Filled.ExpandMore, contentDescription = if (expanded) "收起" else "展开")
        }
    }

    // ---- 子项 ----
    if (!expanded) return
    if (!g.isSelectable) HelpText("此组由内核自动选择，不能手动指定。", Modifier.padding(start = indent + 12.dp, bottom = 4.dp))
    if (depth >= 4) { HelpText("层级太深，不再展开", Modifier.padding(start = indent + 12.dp)); return }
    g.all.forEach { name ->
        val p = proxies[name]
        if (p != null && p.isGroup) {
            GroupNode(p, proxies, depth + 1, isMain = false, initiallyExpanded = false, busy = busy, vm = vm, parent = g)
        } else {
            Row(
                Modifier.fillMaxWidth()
                    .clickable(enabled = g.isSelectable) { vm.selectProxy(g.name, name) }
                    .padding(start = indent + 20.dp, top = 2.dp, bottom = 2.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceBetween,
            ) {
                Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
                    RadioButton(selected = g.now == name, onClick = if (g.isSelectable) ({ vm.selectProxy(g.name, name) }) else null, enabled = g.isSelectable)
                    Text(name, style = MaterialTheme.typography.bodyMedium)
                }
                DelayText(p)
            }
        }
    }
}

@Composable
private fun DelayText(p: ProxyItem?) {
    if (p == null) return
    if (p.type.equals("Direct", true) || p.type.equals("Reject", true)) return
    val (text, color) = when {
        p.delay <= 0 -> "未测" to MaterialTheme.colorScheme.onSurfaceVariant
        p.delay < 200 -> "${p.delay}ms" to Color(0xFF2E7D32)
        p.delay < 500 -> "${p.delay}ms" to Color(0xFFF9A825)
        else -> "${p.delay}ms" to MaterialTheme.colorScheme.error
    }
    Text(text, color = color, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(end = 8.dp))
}

private fun typeLabel(t: String) = when (t.lowercase()) {
    "selector" -> "手动选择"
    "urltest" -> "自动测速"
    "fallback" -> "故障转移"
    "loadbalance" -> "负载均衡"
    "relay" -> "链式代理"
    else -> t
}
