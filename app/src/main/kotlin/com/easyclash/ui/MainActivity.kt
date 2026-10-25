package com.easyclash.ui

import android.Manifest
import android.app.StatusBarManager
import android.content.ComponentName
import com.easyclash.R
import com.easyclash.vpn.EasyClashTileService
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Apps
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.RssFeed
import androidx.compose.material.icons.filled.Tune
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import com.easyclash.ui.screens.AdvancedScreen
import com.easyclash.ui.screens.AppsScreen
import com.easyclash.ui.screens.HomeScreen
import com.easyclash.ui.screens.ProxiesScreen
import com.easyclash.ui.screens.SubsScreen

class MainActivity : ComponentActivity() {

    companion object {
        /** 快捷设置磁贴打开主界面时附带：请求立即连接 */
        const val EXTRA_CONNECT = "connect"
    }

    private val vm: MainViewModel by viewModels()

    /** 系统 VPN 授权弹窗的结果 */
    private val vpnPermission = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        if (it.resultCode == RESULT_OK) vm.connect()
    }
    private val notifyPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    /** 从 clash:// 链接打开时带进来的订阅地址，交给订阅页弹窗 */
    private val pendingImportUrl = mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        handleIntent(intent)
        if (Build.VERSION.SDK_INT >= 33) notifyPermission.launch(Manifest.permission.POST_NOTIFICATIONS)

        promptAddTileOnce()

        setContent {
            val dark = isSystemInDarkTheme()
            val scheme = if (dark) darkColorScheme(primary = Color(0xFF8AB4F8)) else lightColorScheme(primary = Color(0xFF1E6FD9))
            MaterialTheme(colorScheme = scheme) { Root() }
        }
    }

    override fun onNewIntent(intent: Intent) { super.onNewIntent(intent); handleIntent(intent) }

    private fun handleIntent(i: Intent?) {
        if (i == null) return
        if (i.getBooleanExtra(EXTRA_CONNECT, false)) {
            i.removeExtra(EXTRA_CONNECT)
            // 磁贴请求连接但缺权限/订阅：有订阅就走授权流程，没有订阅则停在首页让用户先添加
            if (vm.store.selectedSub() != null) requestConnect()
        }
        val data = i.data ?: return
        if (data.scheme == "clash" || data.scheme == "clashmeta") {
            data.getQueryParameter("url")?.let { pendingImportUrl.value = it }
        }
    }

    /**
     * 首次启动时弹一次系统的"添加快捷设置磁贴"确认框（安卓 13 以上才有这个接口）。
     * 系统不允许 App 自己静默添加磁贴，所以只能请用户点一次"添加"。
     */
    private fun promptAddTileOnce() {
        if (Build.VERSION.SDK_INT < 33) return
        val prefs = getSharedPreferences("easyclash", MODE_PRIVATE)
        if (prefs.getBoolean("tile_prompted", false)) return
        prefs.edit().putBoolean("tile_prompted", true).apply()
        window.decorView.postDelayed({
            runCatching {
                getSystemService(StatusBarManager::class.java).requestAddTileService(
                    ComponentName(this, EasyClashTileService::class.java),
                    "EasyClash",
                    android.graphics.drawable.Icon.createWithResource(this, R.drawable.ic_notify),
                    mainExecutor,
                ) { }
            }
        }, 800)
    }

    /** 首页点"连接"时调用：先向系统申请 VPN 权限（只需一次），再启动 */
    fun requestConnect() {
        val prep = VpnService.prepare(this)
        if (prep != null) vpnPermission.launch(prep) else vm.connect()
    }

    @androidx.compose.runtime.Composable
    private fun Root() {
        val nav = rememberNavController()
        val snackbar = remember { SnackbarHostState() }
        val toast by vm.toast.collectAsStateWithLifecycle()
        LaunchedEffect(toast) { toast?.let { snackbar.showSnackbar(it); vm.consumeToast() } }

        val tabs = listOf(
            Tab("home", "首页", Icons.Filled.Home),
            Tab("subs", "订阅", Icons.Filled.RssFeed),
            Tab("proxies", "节点", Icons.Filled.Hub),
            Tab("apps", "应用", Icons.Filled.Apps),
            Tab("advanced", "高级", Icons.Filled.Tune),
        )
        val backStack by nav.currentBackStackEntryAsState()
        val current = backStack?.destination

        // 从链接导入时自动跳到订阅页
        val pending by pendingImportUrl
        LaunchedEffect(pending) { if (pending != null) nav.navigate("subs") }

        Scaffold(
            snackbarHost = { SnackbarHost(snackbar) },
            bottomBar = {
                NavigationBar {
                    tabs.forEach { t ->
                        NavigationBarItem(
                            selected = current?.hierarchy?.any { it.route == t.route } == true,
                            onClick = {
                                nav.navigate(t.route) {
                                    popUpTo(nav.graph.findStartDestination().id) { saveState = true }
                                    launchSingleTop = true
                                    restoreState = true
                                }
                            },
                            icon = { Icon(t.icon, contentDescription = t.label) },
                            label = { Text(t.label) },
                        )
                    }
                }
            },
        ) { padding ->
            NavHost(nav, startDestination = "home", modifier = Modifier.padding(padding)) {
                composable("home") { HomeScreen(vm, onConnect = { requestConnect() }) }
                composable("subs") { SubsScreen(vm, pendingImportUrl.value) { pendingImportUrl.value = null } }
                composable("proxies") { ProxiesScreen(vm) }
                composable("apps") { AppsScreen(vm) }
                composable("advanced") { AdvancedScreen(vm) }
            }
        }
    }

    private data class Tab(val route: String, val label: String, val icon: ImageVector)
}
