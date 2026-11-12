package com.easyclash.ui.screens

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
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
import com.easyclash.data.AppProxyMode
import com.easyclash.ui.MainViewModel
import com.easyclash.ui.components.HelpText
import com.easyclash.ui.components.SectionCard
import com.easyclash.vpn.Status

@Composable
fun AppsScreen(vm: MainViewModel) {
    val settings by vm.settings.collectAsStateWithLifecycle()
    val apps by vm.apps.collectAsStateWithLifecycle()
    val status by vm.status.collectAsStateWithLifecycle()
    var query by remember { mutableStateOf("") }
    var showSystem by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { vm.loadApps() }

    val listVisible = settings.appProxyMode != AppProxyMode.ALL
    val filtered = apps.filter { (showSystem || !it.isSystem) && (query.isBlank() || it.label.contains(query, true) || it.packageName.contains(query, true)) }

    LazyColumn(Modifier.fillMaxSize(), contentPadding = androidx.compose.foundation.layout.PaddingValues(vertical = 8.dp)) {
        item {
            SectionCard(
                title = "分应用代理",
                help = "控制哪些应用的流量经过本软件。\n\n" +
                    AppProxyMode.entries.joinToString("\n\n") { "「${it.label}」：${it.help}" } +
                    "\n\n修改后需要断开再重新连接才会生效（系统限制）。",
            ) {
                HelpText("选择哪些 App 走代理。改动后要重新连接才生效。", Modifier.padding(bottom = 4.dp))
                AppProxyMode.entries.forEach { m ->
                    Row(Modifier.fillMaxWidth().clickable { vm.updateSettings(reload = false) { it.appProxyMode = m } }.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                        RadioButton(selected = settings.appProxyMode == m, onClick = { vm.updateSettings(reload = false) { it.appProxyMode = m } })
                        Column { Text(m.label); HelpText(m.help) }
                    }
                }
                if (status == Status.CONNECTED) {
                    TextButton(onClick = { vm.disconnect() }) { Text("断开以便重新连接生效") }
                }
            }
        }
        if (listVisible) {
            item {
                Column(Modifier.padding(horizontal = 16.dp)) {
                    OutlinedTextField(value = query, onValueChange = { query = it }, label = { Text("搜索应用") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(checked = showSystem, onCheckedChange = { showSystem = it })
                        Text("显示系统应用", style = MaterialTheme.typography.bodyMedium)
                    }
                    HelpText(
                        when (settings.appProxyMode) {
                            AppProxyMode.ALLOW_LIST -> "勾选的应用走代理，其他不走。已选 ${settings.appList.size} 个。"
                            else -> "勾选的应用不走代理，其他都走。已选 ${settings.appList.size} 个。"
                        },
                        Modifier.padding(bottom = 4.dp),
                    )
                }
            }
            items(filtered, key = { it.packageName }) { app ->
                val checked = app.packageName in settings.appList
                Row(
                    Modifier.fillMaxWidth().clickable { vm.toggleApp(app.packageName, !checked) }.padding(horizontal = 16.dp, vertical = 2.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Checkbox(checked = checked, onCheckedChange = { vm.toggleApp(app.packageName, it) })
                    Column {
                        Text(app.label, style = MaterialTheme.typography.bodyLarge)
                        HelpText(app.packageName)
                    }
                }
            }
            if (apps.isEmpty()) item { HelpText("正在读取应用列表…", Modifier.padding(16.dp)) }
        }
    }
}
