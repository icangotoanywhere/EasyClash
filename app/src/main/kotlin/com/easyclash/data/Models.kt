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
    RULE("rule", "规则", "按规则分流：国内网站直连、国外网站走代理。日常推荐。规则和 GeoIP 数据库都已内置，无需另外设置。"),
    GLOBAL("global", "全局", "所有流量都走代理，不分流。排查问题或临时需要时用。"),
    DIRECT("direct", "直连", "所有流量都不走代理，相当于关闭代理但保留 VPN 连接。"),
}

/** 每个 App 的走法。界面上从左到右（窄屏从上到下）依次是 仅直连 / 按规则 / 仅代理 */
enum class AppRoute(val label: String, val help: String) {
    DIRECT_ONLY("仅直连", "完全不经过本软件，就像没开代理。适合银行、支付、微信等国内必备应用。"),
    RULE("按规则", "国内网站直连、国外网站走代理（默认）。"),
    PROXY_ONLY("仅代理", "所有连接都走代理，代理不通就连不上，绝不直连。适合 ChatGPT、Claude 等海外 AI。需要安卓 10 及以上。"),
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
    /** 每个 App 的走法，只记录不是「按规则」的 App */
    var appRoutes: MutableMap<String, AppRoute> = mutableMapOf(),
    /** 旧版「仅选中的应用」模式升级时待换算的名单：需要读取已安装应用后才能换算，见 Store.migrateLegacyAllowList */
    var legacyAllowList: MutableSet<String>? = null,
    /** ChatGPT、Claude 等 AI 服务的域名一律走代理、绝不直连（浏览器里用也受保护） */
    var aiForceProxy: Boolean = true,
    var lockedRegion: String? = null,           // 锁定的地区代码，如 "SG"，null 表示不锁定
    var ipv6: Boolean = false,
    var blockQuic: Boolean = true,
    var bingDirect: Boolean = true,              // 微软服务走国内（含必应国内版），键名沿用旧名以保留用户设置
    var directFallback: Boolean = true,          // 出境直连失败自动转代理
    var useSubscriptionDns: Boolean = false,
    var tunStack: TunStack = TunStack.GVISOR,
    var logLevel: String = "info",
    var extraRules: String = "",                // 用户附加规则，每行一条，插在最前面
    var overrideYaml: String = "",              // 高级：YAML 覆写片段
    var mixedPort: Int = 17890,   // 7890 常被其他 Clash 占用
    /** 自动测速 / 自动选节点的周期（分钟） */
    var testIntervalMin: Int = 5,
    /** 常驻通知里是否显示「断开」按钮。默认不显示，免得误触 */
    var notifyStopButton: Boolean = false,
    var geoAutoUpdate: Boolean = true,           // 连接后自动检查地理数据库（每 7 天一次）
    var geoLastCheck: Long = 0L,                 // 上次成功检查 / 更新数据库的时间（毫秒）
    var geoLastAttempt: Long = 0L,               // 上次尝试时间，失败后 12 小时内不再自动重试
    var regionSaved: MutableMap<String, String> = mutableMapOf(),  // 锁定地区时被切到「地区选择」的组及其原来的选择
    var regionExempt: MutableSet<String> = mutableSetOf(),         // 锁定期间用户单独指定过的组（重新应用锁定时跳过）
) {
    fun toJson(): JSONObject = JSONObject().apply {
        put("selectedSubId", selectedSubId ?: JSONObject.NULL)
        put("mode", mode.name)
        put("appRoutes", JSONObject().apply { appRoutes.forEach { (k, v) -> if (v != AppRoute.RULE) put(k, v.name) } })
        legacyAllowList?.let { put("legacyAllowList", JSONArray(it.toList())) }
        put("aiForceProxy", aiForceProxy)
        put("lockedRegion", lockedRegion ?: JSONObject.NULL)
        put("testIntervalMin", testIntervalMin); put("notifyStopButton", notifyStopButton)
        put("ipv6", ipv6); put("blockQuic", blockQuic); put("bingDirect", bingDirect); put("directFallback", directFallback)
        put("useSubscriptionDns", useSubscriptionDns)
        put("tunStack", tunStack.name); put("logLevel", logLevel)
        put("extraRules", extraRules); put("overrideYaml", overrideYaml)
        put("mixedPort", mixedPort)
        put("geoAutoUpdate", geoAutoUpdate); put("geoLastCheck", geoLastCheck); put("geoLastAttempt", geoLastAttempt)
        put("regionSaved", JSONObject().apply { regionSaved.forEach { (k, v) -> put(k, v) } })
        put("regionExempt", JSONArray(regionExempt.toList()))
    }

    companion object {
        fun fromJson(o: JSONObject): Settings {
            val s = Settings()
            s.selectedSubId = if (o.isNull("selectedSubId")) null else o.optString("selectedSubId")
            s.mode = runCatching { ProxyMode.valueOf(o.optString("mode")) }.getOrDefault(ProxyMode.RULE)
            val routes = o.optJSONObject("appRoutes")
            if (routes != null) {
                routes.keys().forEach { k -> runCatching { AppRoute.valueOf(routes.getString(k)) }.getOrNull()?.let { if (it != AppRoute.RULE) s.appRoutes[k] = it } }
                o.optJSONArray("legacyAllowList")?.let { a -> s.legacyAllowList = (0 until a.length()).map { a.getString(it) }.toMutableSet() }
            } else {
                // v1.0.27 及以前：全局三选一 + 一个名单，换算成每个 App 的走法
                val arr = o.optJSONArray("appList") ?: JSONArray()
                val list = (0 until arr.length()).map { arr.getString(it) }
                when (o.optString("appProxyMode")) {
                    "DENY_LIST" -> list.forEach { s.appRoutes[it] = AppRoute.DIRECT_ONLY }      // 排除的 → 仅直连
                    "ALLOW_LIST" -> if (list.isNotEmpty()) s.legacyAllowList = list.toMutableSet()  // 名单外的 → 仅直连（待换算）
                }
            }
            s.aiForceProxy = o.optBoolean("aiForceProxy", true)
            s.lockedRegion = if (o.isNull("lockedRegion")) null else o.optString("lockedRegion")
            s.ipv6 = o.optBoolean("ipv6", false)
            s.testIntervalMin = o.optInt("testIntervalMin", 5).coerceIn(1, 1440)
            s.notifyStopButton = o.optBoolean("notifyStopButton", false)
            s.blockQuic = o.optBoolean("blockQuic", true)
            s.bingDirect = o.optBoolean("bingDirect", true)
            s.directFallback = o.optBoolean("directFallback", true)
            s.useSubscriptionDns = o.optBoolean("useSubscriptionDns", false)
            s.tunStack = runCatching { TunStack.valueOf(o.optString("tunStack")) }.getOrDefault(TunStack.GVISOR)
            s.logLevel = o.optString("logLevel", "info")
            s.extraRules = o.optString("extraRules", "")
            s.overrideYaml = o.optString("overrideYaml", "")
            s.mixedPort = o.optInt("mixedPort", 17890)
            s.geoAutoUpdate = o.optBoolean("geoAutoUpdate", true)
            s.geoLastCheck = o.optLong("geoLastCheck", 0L)
            s.geoLastAttempt = o.optLong("geoLastAttempt", 0L)
            o.optJSONObject("regionSaved")?.let { j -> j.keys().forEach { k -> s.regionSaved[k] = j.optString(k) } }
            o.optJSONArray("regionExempt")?.let { a -> (0 until a.length()).forEach { s.regionExempt.add(a.getString(it)) } }
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

/** 订阅引用的一个规则集（来自内核 /providers/rules 接口） */
data class RuleProviderInfo(
    val name: String,
    val ruleCount: Int,
    val updatedAt: String,     // 内核给的时间字符串；从没下载成功过时为 0001-01-01...
    val vehicle: String,       // HTTP（在线下载）/ File（本地文件）/ Inline
    val behavior: String,
) {
    val isHttp: Boolean get() = vehicle.equals("HTTP", true)
    val neverLoaded: Boolean get() = updatedAt.isBlank() || updatedAt.startsWith("0001")
}

/** 本地安装的应用（分应用代理用） */
data class AppInfo(
    val packageName: String,
    val label: String,
    val isSystem: Boolean,
)
