package com.easyclash.ui.screens

import android.content.ClipboardManager
import android.content.Context
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.easyclash.data.Subscription
import com.easyclash.ui.MainViewModel
import com.easyclash.ui.components.ConfirmDialog
import com.easyclash.ui.components.HelpText
import com.easyclash.ui.components.KeyValueRow
import com.easyclash.ui.components.SectionCard
import com.easyclash.ui.components.humanBytes
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

@Composable
fun SubsScreen(vm: MainViewModel, pendingUrl: String?, onPendingConsumed: () -> Unit) {
    val subs by vm.subs.collectAsStateWithLifecycle()
    val settings by vm.settings.collectAsStateWithLifecycle()
    val busy by vm.busy.collectAsStateWithLifecycle()
    val ctx = LocalContext.current

    var showAdd by remember { mutableStateOf(false) }
    var prefillUrl by remember { mutableStateOf("") }
    LaunchedEffect(pendingUrl) { if (pendingUrl != null) { prefillUrl = pendingUrl; showAdd = true; onPendingConsumed() } }

    val filePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) {
            val text = runCatching { ctx.contentResolver.openInputStream(uri)?.bufferedReader()?.readText() }.getOrNull()
            if (text.isNullOrBlank()) return@rememberLauncherForActivityResult
            val name = uri.lastPathSegment?.substringAfterLast('/')?.removeSuffix(".yaml")?.removeSuffix(".yml") ?: "本地配置"
            vm.importSubscriptionText(text, name)
        }
    }

    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(vertical = 8.dp)) {
        SectionCard(
            title = "添加订阅",
            help = "订阅就是机场（代理服务商）给你的一个链接，里面包含所有节点和分流规则。\n\n" +
                "在机场网站的用户中心找到「Clash」或「Mihomo」订阅，复制链接后粘贴进来。\n\n" +
                "如果机场页面有「一键导入 Clash」按钮，点它也能直接跳到本软件。\n\n" +
                "也可以导入电脑上导出的 .yaml 配置文件。",
        ) {
            HelpText("粘贴机场给的 Clash 订阅链接，或者导入 .yaml 文件。", Modifier.padding(bottom = 8.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { prefillUrl = ""; showAdd = true }) { Text("添加链接") }
                OutlinedButton(onClick = {
                    val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                    val t = cm.primaryClip?.getItemAt(0)?.coerceToText(ctx)?.toString()?.trim().orEmpty()
                    prefillUrl = t; showAdd = true
                }) { Text("从剪贴板") }
                OutlinedButton(onClick = { filePicker.launch(arrayOf("*/*")) }) { Text("导入文件") }
            }
            if (busy) LinearProgressIndicator(Modifier.fillMaxWidth().padding(top = 8.dp))
        }

        if (subs.isEmpty()) {
            SectionCard(title = "还没有订阅") { HelpText("点上面的「添加链接」开始。添加后在这里点选要用的订阅，再回首页连接。") }
        }

        subs.forEach { sub ->
            SubCard(sub, selected = sub.id == settings.selectedSubId, busy = busy, vm = vm)
        }
        Spacer(Modifier.height(24.dp))
    }

    if (showAdd) AddDialog(vm, prefillUrl) { showAdd = false }
}

@Composable
private fun AddDialog(vm: MainViewModel, prefill: String, onClose: () -> Unit) {
    var url by remember { mutableStateOf(prefill) }
    var name by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("添加订阅") },
        text = {
            Column {
                OutlinedTextField(value = url, onValueChange = { url = it }, label = { Text("订阅链接") }, placeholder = { Text("https://…") }, modifier = Modifier.fillMaxWidth(), maxLines = 4)
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(value = name, onValueChange = { name = it }, label = { Text("名称（可不填）") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
                Spacer(Modifier.height(8.dp))
                HelpText("添加后会立刻下载一次订阅内容。如果提示不是 Clash 格式，请回机场网站换成 Clash/Mihomo 专用链接。")
            }
        },
        confirmButton = { Button(onClick = { vm.addSubscription(url, name) { ok -> if (ok) onClose() } }, enabled = url.isNotBlank()) { Text("添加") } },
        dismissButton = { TextButton(onClick = onClose) { Text("取消") } },
    )
}

@Composable
private fun SubCard(sub: Subscription, selected: Boolean, busy: Boolean, vm: MainViewModel) {
    var confirmDelete by remember { mutableStateOf(false) }
    var rename by remember { mutableStateOf(false) }
    val fmt = remember { SimpleDateFormat("yyyy-MM-dd HH:mm", Locale.getDefault()) }

    Card(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp).clickable { vm.selectSubscription(sub) },
        colors = CardDefaults.cardColors(containerColor = if (selected) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerLow),
    ) {
        Column(Modifier.padding(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                RadioButton(selected = selected, onClick = { vm.selectSubscription(sub) })
                Column(Modifier.weight(1f)) {
                    Text(sub.name, style = MaterialTheme.typography.titleMedium)
                    HelpText(if (selected) "正在使用 · ${sub.nodeCount} 个节点" else "${sub.nodeCount} 个节点 · 点击切换为此订阅")
                }
            }
            if (sub.total > 0) {
                val used = (sub.upload.coerceAtLeast(0) + sub.download.coerceAtLeast(0))
                LinearProgressIndicator(progress = { (used.toFloat() / sub.total).coerceIn(0f, 1f) }, modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp))
                KeyValueRow("流量", "已用 ${humanBytes(used)} / 共 ${humanBytes(sub.total)}")
            }
            if (sub.expire > 0) KeyValueRow("到期", fmt.format(Date(sub.expire * 1000)))
            KeyValueRow("上次更新", if (sub.lastUpdate > 0) fmt.format(Date(sub.lastUpdate)) else "从未")
            Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                TextButton(onClick = { vm.refreshSubscription(sub) }, enabled = !busy && sub.url.isNotBlank()) { Text("更新") }
                TextButton(onClick = { rename = true }) { Text("重命名") }
                TextButton(onClick = { confirmDelete = true }) { Text("删除", color = MaterialTheme.colorScheme.error) }
            }
            HelpText("更新：重新从机场下载最新的节点列表。节点连不上、机场通知换节点时点一下。")
        }
    }

    if (confirmDelete) ConfirmDialog("删除订阅", "确定删除「${sub.name}」？已下载的节点信息会一起删除。", "删除", { confirmDelete = false }) { vm.removeSubscription(sub) }
    if (rename) {
        var n by remember { mutableStateOf(sub.name) }
        AlertDialog(
            onDismissRequest = { rename = false },
            title = { Text("重命名") },
            text = { OutlinedTextField(value = n, onValueChange = { n = it }, singleLine = true) },
            confirmButton = { TextButton(onClick = { vm.renameSubscription(sub, n.ifBlank { sub.name }); rename = false }) { Text("保存") } },
            dismissButton = { TextButton(onClick = { rename = false }) { Text("取消") } },
        )
    }
}
