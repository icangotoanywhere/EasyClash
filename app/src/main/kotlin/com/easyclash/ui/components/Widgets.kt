package com.easyclash.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

/**
 * 通用控件：每个设置项都带一行小字说明，右侧还有 ⓘ 按钮可弹出更详细的解释。
 * 这是"每个按钮都有清楚说明"这一要求的统一实现。
 */

/** 灰色小字说明 */
@Composable
fun HelpText(text: String, modifier: Modifier = Modifier) {
    Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = modifier)
}

/** ⓘ 按钮，点击弹出说明对话框 */
@Composable
fun InfoButton(title: String, detail: String) {
    var show by remember { mutableStateOf(false) }
    IconButton(onClick = { show = true }) {
        Icon(Icons.Outlined.Info, contentDescription = "说明", tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
    if (show) {
        AlertDialog(
            onDismissRequest = { show = false },
            title = { Text(title) },
            text = { Text(detail) },
            confirmButton = { TextButton(onClick = { show = false }) { Text("知道了") } },
        )
    }
}

/** 带标题的分组卡片 */
@Composable
fun SectionCard(title: String, help: String? = null, content: @Composable () -> Unit) {
    Card(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
    ) {
        Column(Modifier.padding(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                if (help != null) InfoButton(title, help)
            }
            content()
        }
    }
}

/** 一行设置：标题 + 说明 + 右侧控件 */
@Composable
fun SettingRow(title: String, help: String, detail: String? = null, trailing: @Composable () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            HelpText(help)
        }
        if (detail != null) InfoButton(title, detail)
        Spacer(Modifier.width(4.dp))
        trailing()
    }
}

/** 开关行 */
@Composable
fun SwitchRow(title: String, help: String, checked: Boolean, detail: String? = null, onChange: (Boolean) -> Unit) {
    SettingRow(title, help, detail) { Switch(checked = checked, onCheckedChange = onChange) }
}

/** 简单的确认对话框 */
@Composable
fun ConfirmDialog(title: String, text: String, confirmText: String = "确定", onDismiss: () -> Unit, onConfirm: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(text) },
        confirmButton = { TextButton(onClick = { onConfirm(); onDismiss() }) { Text(confirmText) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

/** 两端对齐的一行文字（左标签右数值） */
@Composable
fun KeyValueRow(key: String, value: String) {
    Row(Modifier.fillMaxWidth().padding(vertical = 2.dp), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(key, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, style = MaterialTheme.typography.bodyMedium)
    }
}

fun humanBytes(b: Long): String = when {
    b < 0 -> "—"
    b >= 1L shl 30 -> String.format("%.2f GB", b / (1L shl 30).toDouble())
    b >= 1L shl 20 -> String.format("%.1f MB", b / (1L shl 20).toDouble())
    b >= 1L shl 10 -> String.format("%.0f KB", b / 1024.0)
    else -> "$b B"
}
