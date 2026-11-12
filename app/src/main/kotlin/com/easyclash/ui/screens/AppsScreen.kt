package com.easyclash.ui.screens

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.easyclash.data.AppInfo
import com.easyclash.data.AppRoute
import com.easyclash.ui.MainViewModel
import com.easyclash.ui.components.ConfirmDialog
import com.easyclash.ui.components.HelpText
import com.easyclash.ui.components.SectionCard
import com.easyclash.vpn.AppOwner
import com.easyclash.vpn.Status

/** 三个选项的排列：仅直连在左、按规则居中、仅代理在右 */
private val ROUTE_ORDER = listOf(AppRoute.DIRECT_ONLY, AppRoute.RULE, AppRoute.PROXY_ONLY)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AppsScreen(vm: MainViewModel) {
    val settings by vm.settings.collectAsStateWithLifecycle()
    val apps by vm.apps.collectAsStateWithLifecycle()
    val status by vm.status.collectAsStateWithLifecycle()
    var query by remember { mutableStateOf("") }
    var showSystem by remember { mutableStateOf(false) }
    var onlyChanged by remember { mutableStateOf(false) }
    var confirmRecommend by remember { mutableStateOf<Map<String, AppRoute>?>(null) }
    var confirmReset by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { vm.loadApps() }

    val routes = settings.appRoutes
    fun routeOf(pkg: String) = routes[pkg] ?: AppRoute.RULE
    val filtered = apps.filter {
        (showSystem || !it.isSystem || it.packageName in routes) &&
            (!onlyChanged || it.packageName in routes) &&
            (query.isBlank() || it.label.contains(query, true) || it.packageName.contains(query, true))
    }
    val directCount = routes.count { it.value == AppRoute.DIRECT_ONLY }
    val proxyCount = routes.count { it.value == AppRoute.PROXY_ONLY }

    LazyColumn(Modifier.fillMaxSize(), contentPadding = androidx.compose.foundation.layout.PaddingValues(vertical = 8.dp)) {
        item {
            SectionCard(
                title = "分应用走法",
                help = "给每个 App 选一种走法：\n\n" +
                    ROUTE_ORDER.joinToString("\n\n") { "「${it.label}」：${it.help}" } +
                    "\n\n「一键推荐」：银行、证券、支付、微信、QQ、淘宝、美团、12306 等国内必备 App 设为仅直连；ChatGPT、Claude、Gemini 等海外 AI 设为仅代理；其余和吃不准的保持按规则。推荐后每个 App 仍可单独改。\n\n" +
                    "生效方式：改「仅代理」立即生效；改「仅直连」要断开再重新连接才生效（系统限制）。\n\n" +
                    "「仅代理」靠系统告诉本软件每条连接属于哪个 App，需要安卓 10 及以上。另外，本软件断开时所有 App 都直接上网；若要 VPN 断开时也不漏网，可在系统设置 → VPN → EasyClash 里打开「始终开启的 VPN」和「屏蔽未使用 VPN 的连接」（整机生效）。",
            ) {
                HelpText("每个 App 三选一：仅直连 / 按规则 / 仅代理。默认全部按规则。", Modifier.padding(bottom = 4.dp))
                HelpText("当前：仅直连 $directCount 个，仅代理 $proxyCount 个，其余按规则。", Modifier.padding(bottom = 8.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    OutlinedButton(onClick = { confirmRecommend = vm.recommendedRoutes() }, enabled = apps.isNotEmpty()) { Text("一键推荐") }
                    Spacer(Modifier.width(8.dp))
                    OutlinedButton(onClick = { confirmReset = true }, enabled = routes.isNotEmpty()) { Text("全部按规则") }
                }
                if (!AppOwner.supported) {
                    HelpText("本机系统低于安卓 10，无法区分每条连接属于哪个 App，「仅代理」不起作用（相当于按规则）。仅直连、按规则不受影响。", Modifier.padding(top = 8.dp))
                }
                if (status == Status.CONNECTED) {
                    TextButton(onClick = { vm.disconnect() }) { Text("断开（改了「仅直连」后需要重新连接）") }
                }
            }
        }
        item {
            Column(Modifier.padding(horizontal = 16.dp)) {
                OutlinedTextField(value = query, onValueChange = { query = it }, label = { Text("搜索应用") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Checkbox(checked = showSystem, onCheckedChange = { showSystem = it })
                    Text("显示系统应用", style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.width(12.dp))
                    Checkbox(checked = onlyChanged, onCheckedChange = { onlyChanged = it })
                    Text("只看非「按规则」", style = MaterialTheme.typography.bodyMedium)
                }
            }
        }
        items(filtered, key = { it.packageName }) { app ->
            AppRow(app, routeOf(app.packageName)) { vm.setAppRoute(app.packageName, it) }
        }
        if (apps.isEmpty()) item { HelpText("正在读取应用列表…", Modifier.padding(16.dp)) }
    }

    confirmRecommend?.let { plan ->
        val d = plan.count { it.value == AppRoute.DIRECT_ONLY }
        val p = plan.count { it.value == AppRoute.PROXY_ONLY }
        val names = apps.associate { it.packageName to it.label }
        fun list(r: AppRoute) = plan.filterValues { it == r }.keys.map { names[it] ?: it }.sorted().joinToString("、").ifEmpty { "（无）" }
        ConfirmDialog(
            title = "一键推荐",
            text = "将把 $d 个 App 设为仅直连、$p 个设为仅代理，其余全部按规则（会覆盖你之前的单独设置）。\n\n仅直连：${list(AppRoute.DIRECT_ONLY)}\n\n仅代理：${list(AppRoute.PROXY_ONLY)}",
            confirmText = "应用推荐",
            onDismiss = { confirmRecommend = null },
            onConfirm = { vm.replaceAppRoutes(plan) },
        )
    }
    if (confirmReset) {
        ConfirmDialog(
            title = "全部按规则",
            text = "把所有 App 都改回「按规则」？",
            onDismiss = { confirmReset = false },
            onConfirm = { vm.replaceAppRoutes(emptyMap()) },
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AppRow(app: AppInfo, route: AppRoute, onChange: (AppRoute) -> Unit) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp)) {
        Text(app.label, style = MaterialTheme.typography.bodyLarge)
        HelpText(app.packageName)
        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(top = 4.dp)) {
            ROUTE_ORDER.forEachIndexed { i, r ->
                SegmentedButton(
                    selected = route == r,
                    onClick = { onChange(r) },
                    shape = SegmentedButtonDefaults.itemShape(i, ROUTE_ORDER.size),
                ) { Text(r.label, style = MaterialTheme.typography.labelMedium) }
            }
        }
    }
    HorizontalDivider(Modifier.padding(horizontal = 16.dp))
}
