package com.easyclash.data

import org.json.JSONArray
import org.json.JSONObject

/** 一条机场订阅 */
data class Subscription(
    val id: String,
    var name: String,
    var url: String,
    var lastUpdate: Long = 0L,          // 上次成功更新时间（毫秒）
    var upload: Long = -1L,             // 已用上行，-1 表示机场未提供
    var download: Long = -1L,
    var total: Long = -1L,
    var expire: Long = -1L,             // 到期时间（秒），-1 未提供
    var updateIntervalHours: Int = 24,  // 自动更新间隔，0 表示不自动更新
    var nodeCount: Int = 0,
) {
    fun toJson(): JSONObject = JSONObject().apply {
        put("id", id); put("name", name); put("url", url)
        put("lastUpdate", lastUpdate); put("upload", upload); put("download", download)
        put("total", total); put("expire", expire)
        put("updateIntervalHours", updateIntervalHours); put("nodeCount", nodeCount)
    }

    companion object {
        fun fromJson(o: JSONObject) = Subscription(
            id = o.getString("id"),
            name = o.optString("name"),
            url = o.optString("url"),
            lastUpdate = o.optLong("lastUpdate", 0L),
            upload = o.optLong("upload", -1L),
            download = o.optLong("download", -1L),
            total = o.optLong("total", -1L),
            expire = o.optLong("expire", -1L),
            updateIntervalHours = o.optInt("updateIntervalHours", 24),
            nodeCount = o.optInt("nodeCount", 0),
        )
    }
}

/** 出站模式：规则分流 / 全部走代理 / 全部直连 */
enum class ProxyMode(val coreValue: String, val label: String, val help: String) {
    RULE("rule", "规则", "按规则分流：国内网站直连、国外网站走代理。日常推荐。"),
    GLOBAL("global", "全局", "所有流量都走代理，不分流。排查问题或临时需要时用。"),
    DIRECT("direct", "直连", "所有流量都不走代理，相当于关闭代理但保留 VPN 连接。"),
}

/** 分应用代理模式 */
enum class AppProxyMode(val label: String, val help: String) {
    ALL("全部应用", "手机上所有应用的流量都经过本软件。"),
    ALLOW_LIST("仅选中的应用", "只有你勾选的应用走代理，其他应用正常直连，不受影响。"),
    DENY_LIST("排除选中的应用", "勾选的应用不走代理，其余所有应用都走代理。适合排除银行、支付、国内视频等应用。"),
}

/** TUN 网络栈 */
enum class TunStack(val coreValue: String, val label: String) {
    GVISOR("gvisor", "gvisor（兼容性最好，默认）"),
    MIXED("mixed", "mixed（TCP 用系统栈，速度更快）"),
    SYSTEM("system", "system（速度最快，部分机型不稳定）"),
}

/** 所有可持久化的设置 */
data class Settings(
    var selectedSubId: String? = null,
    var mode: ProxyMode = ProxyMode.RULE,
    var appProxyMode: AppProxyMode = AppProxyMode.ALL,
    var appList: MutableSet<String> = mutableSetOf(),
    var lockedRegion: String? = null,           // 锁定的地区代码，如 "SG"，null 表示不锁定
    var ipv6: Boolean = false,
    var blockQuic: Boolean = true,
    var bingDirect: Boolean = true,              // 必应始终直连（用国内版）
    var useSubscriptionDns: Boolean = false,
    var tunStack: TunStack = TunStack.GVISOR,
    var logLevel: String = "info",
    var extraRules: String = "",                // 用户附加规则，每行一条，插在最前面
    var overrideYaml: String = "",              // 高级：YAML 覆写片段
    var mixedPort: Int = 17890,   // 7890 常被其他 Clash 占用
) {
    fun toJson(): JSONObject = JSONObject().apply {
        put("selectedSubId", selectedSubId ?: JSONObject.NULL)
        put("mode", mode.name); put("appProxyMode", appProxyMode.name)
        put("appList", JSONArray(appList.toList()))
        put("lockedRegion", lockedRegion ?: JSONObject.NULL)
        put("ipv6", ipv6); put("blockQuic", blockQuic); put("bingDirect", bingDirect)
        put("useSubscriptionDns", useSubscriptionDns)
        put("tunStack", tunStack.name); put("logLevel", logLevel)
        put("extraRules", extraRules); put("overrideYaml", overrideYaml)
        put("mixedPort", mixedPort)
    }

    companion object {
        fun fromJson(o: JSONObject): Settings {
            val s = Settings()
            s.selectedSubId = if (o.isNull("selectedSubId")) null else o.optString("selectedSubId")
            s.mode = runCatching { ProxyMode.valueOf(o.optString("mode")) }.getOrDefault(ProxyMode.RULE)
            s.appProxyMode = runCatching { AppProxyMode.valueOf(o.optString("appProxyMode")) }.getOrDefault(AppProxyMode.ALL)
            val arr = o.optJSONArray("appList") ?: JSONArray()
            s.appList = (0 until arr.length()).map { arr.getString(it) }.toMutableSet()
            s.lockedRegion = if (o.isNull("lockedRegion")) null else o.optString("lockedRegion")
            s.ipv6 = o.optBoolean("ipv6", false)
            s.blockQuic = o.optBoolean("blockQuic", true)
            s.bingDirect = o.optBoolean("bingDirect", true)
            s.useSubscriptionDns = o.optBoolean("useSubscriptionDns", false)
            s.tunStack = runCatching { TunStack.valueOf(o.optString("tunStack")) }.getOrDefault(TunStack.GVISOR)
            s.logLevel = o.optString("logLevel", "info")
            s.extraRules = o.optString("extraRules", "")
            s.overrideYaml = o.optString("overrideYaml", "")
            s.mixedPort = o.optInt("mixedPort", 17890)
            return s
        }
    }
}

/** 代理组里的一个条目（来自内核 /proxies 接口） */
data class ProxyItem(
    val name: String,
    val type: String,
    val now: String?,          // 策略组当前选中项；普通节点为 null
    val all: List<String>,     // 策略组包含的条目
    val delay: Int,            // 最近一次延迟（毫秒），0 表示未测或超时
    val alive: Boolean,
) {
    val isGroup: Boolean get() = all.isNotEmpty()
    val isSelectable: Boolean get() = type.equals("Selector", true)
}

data class ConnectionItem(
    val id: String,
    val host: String,
    val destination: String,
    val network: String,
    val chain: String,
    val rule: String,
    val upload: Long,
    val download: Long,
    val start: String,
    val process: String,
)

/** 本地安装的应用（分应用代理用） */
data class AppInfo(
    val packageName: String,
    val label: String,
    val isSystem: Boolean,
)
