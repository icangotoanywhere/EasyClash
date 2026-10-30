package com.easyclash.core

import com.easyclash.data.Settings
import org.yaml.snakeyaml.DumperOptions
import org.yaml.snakeyaml.LoaderOptions
import org.yaml.snakeyaml.Yaml
import org.yaml.snakeyaml.constructor.SafeConstructor
import org.yaml.snakeyaml.representer.Representer
import java.io.File

/**
 * 把"机场订阅 + 用户设置"合成内核真正加载的最终配置。
 *
 * 做的事情：
 *  1. 去掉订阅里与手机环境冲突的字段（端口、tun、控制接口等），换成我们自己的
 *  2. 按地区自动生成 "🇸🇬 新加坡·自动" 这类测速组和一个 "🌍 地区选择" 组
 *  3. 规则：插入用户附加规则 / QUIC 拦截；在线规则集与地理数据库固定走代理下载；订阅没带规则时补基础分流
 *  4. 应用高级页的 YAML 覆写
 *  5. 注入 TUN（VpnService 的文件描述符）与 DNS
 */
object ConfigBuilder {

    const val REGION_SELECTOR = "🌍 地区选择"
    const val TUN_ADDR4 = "172.19.0.1"
    const val TUN_DNS4 = "172.19.0.2"
    const val TUN_ADDR6 = "fdfe:dcba:9876::1"
    const val TUN_MTU = 9000
    const val FALLBACK_MAIN_GROUP = "🚀 节点选择"
    const val GEO_MIRROR_HOST = "testingcf.jsdelivr.net"

    /** 订阅没带分流规则时补上的基础分流（GEOSITE 用内置 geosite.dat，GEOIP 用内置 geoip.metadb） */
    val BASIC_RULES = listOf(
        "GEOIP,LAN,DIRECT,no-resolve",
        "GEOSITE,private,DIRECT",
        "GEOSITE,cn,DIRECT",
        "GEOIP,CN,DIRECT",
    )

    class BuildError(msg: String) : Exception(msg)

    data class BuildResult(
        val file: File,
        val mainGroup: String,            // 主选择组名（MATCH 规则指向的组，或第一个 select 组）
        val regionGroups: List<Region>,   // 本次生成了哪些地区组
        val tieredRegions: Set<String>,   // 其中哪些地区分了"优先/备用"两级（地区代码）
    )

    /** 自动测速组的健康检查间隔（秒）：高级页"自动测速间隔"，默认 5 分钟，兼顾耗电 */
    fun testIntervalSec(settings: Settings) = settings.testIntervalMin.coerceIn(1, 1440) * 60

    private fun yaml(): Yaml {
        val lo = LoaderOptions().apply {
            codePointLimit = 50 * 1024 * 1024
            // 机场配置常用锚点/引用（&a / *a）共享字段，默认上限 50 会报错
            maxAliasesForCollections = Int.MAX_VALUE
        }
        val dumper = DumperOptions().apply {
            defaultFlowStyle = DumperOptions.FlowStyle.BLOCK
            isAllowUnicode = true
            indent = 2
            width = 4096
        }
        return Yaml(SafeConstructor(lo), Representer(dumper), dumper, lo)
    }

    @Suppress("UNCHECKED_CAST")
    fun load(text: String): MutableMap<String, Any?> {
        val obj = try { yaml().load<Any?>(text) } catch (e: Exception) { throw BuildError("YAML 格式错误：${e.message?.lineSequence()?.firstOrNull()}") }
        val map = obj as? Map<String, Any?> ?: throw BuildError("配置顶层不是键值对")
        return LinkedHashMap(map)
    }

    /** 统计节点数。-1 表示不是 Clash 配置；没有 proxies 但有 proxy-providers 时返回 1（数量未知） */
    fun countProxies(text: String): Int {
        val map = try { load(text) } catch (e: Exception) { return -1 }
        val proxies = map["proxies"] as? List<*>
        val providers = map["proxy-providers"] as? Map<*, *>
        if (proxies == null && providers == null) return -1
        val n = proxies?.size ?: 0
        return if (n == 0 && !providers.isNullOrEmpty()) 1 else n
    }

    @Suppress("UNCHECKED_CAST")
    fun build(
        subscriptionYaml: String,
        settings: Settings,
        outFile: File,
        tunFd: Int,
        controllerPort: Int,
        secret: String,
    ): BuildResult {
        val cfg = load(subscriptionYaml)

        // ---------- 4. 高级覆写（先应用，让它能影响到后面的分组逻辑） ----------
        var overrideHasDns = false
        if (settings.overrideYaml.isNotBlank()) {
            val ov = try { load(settings.overrideYaml) } catch (e: Exception) { throw BuildError("覆写内容有误：${e.message}") }
            overrideHasDns = ov.containsKey("dns")
            applyOverride(cfg, ov)
        }

        // ---------- 2. 地区分组 ----------
        val proxies = (cfg["proxies"] as? List<Map<String, Any?>>)?.toMutableList() ?: mutableListOf()
        val providerNames = (cfg["proxy-providers"] as? Map<String, Any?>)?.keys?.toList() ?: emptyList()
        val groups = (cfg["proxy-groups"] as? List<Map<String, Any?>>)?.map { LinkedHashMap(it) }?.toMutableList() ?: mutableListOf()
        val rules = (cfg["rules"] as? List<Any?>)?.map { it.toString() }?.toMutableList() ?: mutableListOf()
        // 订阅自己有没有写分流规则（只有一条 MATCH 不算）
        val subHasRules = rules.any { !it.trim().startsWith("MATCH,") }

        val nodeNames = proxies.mapNotNull { it["name"]?.toString() }
        val existingGroupNames = groups.mapNotNull { it["name"]?.toString() }.toMutableSet()

        val createdRegions = mutableListOf<Region>()
        val tiered = mutableSetOf<String>()
        val tierSelectors = mutableSetOf<String>()
        val keyword = settings.backupKeyword.trim()
        val interval = testIntervalSec(settings)
        for (region in Regions.all) {
            if (region.groupName in existingGroupNames) { createdRegions += region; continue }
            val matched = nodeNames.filter { Regions.match(it) == region }
            if (matched.isEmpty() && providerNames.isEmpty()) continue

            // 分级：名字含备用关键字的节点单独一组。两边都有节点才分，否则就是普通的一个自动组
            val backup = if (keyword.isEmpty()) emptyList() else matched.filter { it.contains(keyword, ignoreCase = true) }
            val primary = matched - backup.toSet()
            val useTiers = keyword.isNotEmpty() && backup.isNotEmpty() && (primary.isNotEmpty() || providerNames.isNotEmpty())

            if (!useTiers) {
                groups += urlTestGroup(region.groupName, matched, providerNames, region.goPattern, Regions.infoNodeRegex.pattern, interval)
            } else {
                val kw = goEscape(keyword)
                val rp = region.goPattern.removePrefix("(?i)")
                // 优先组：该地区所有不含关键字的节点；备用组：该地区含关键字的节点
                groups += urlTestGroup(region.primaryGroup, primary, providerNames, region.goPattern, "(?i)${Regions.infoNodeRegex.pattern}|$kw", interval)
                groups += urlTestGroup(region.backupGroup, backup, providerNames, "(?i)(?:$rp).*(?:$kw)|(?:$kw).*(?:$rp)", Regions.infoNodeRegex.pattern, interval)
                // 自动组本身变成一个选择组，由 App 里的 TierPicker 按"优先比备用慢多少"决定选哪一级
                val sel = LinkedHashMap<String, Any?>()
                sel["name"] = region.groupName
                sel["type"] = "select"
                sel["proxies"] = listOf(region.primaryGroup, region.backupGroup)
                groups += sel
                tiered += region.code
                tierSelectors += region.groupName
            }
            existingGroupNames += region.groupName
            createdRegions += region
        }

        // 主选择组：MATCH 规则指向的组优先，否则第一个 select 组，都没有就自己造一个
        var mainGroup = rules.lastOrNull { it.startsWith("MATCH,") }?.substringAfter("MATCH,")?.trim()
            ?.takeIf { name -> groups.any { it["name"] == name && isSelect(it) } }
            ?: groups.firstOrNull { isSelect(it) }?.get("name")?.toString()
        if (mainGroup == null) {
            val g = LinkedHashMap<String, Any?>()
            g["name"] = FALLBACK_MAIN_GROUP
            g["type"] = "select"
            g["proxies"] = nodeNames.ifEmpty { listOf("DIRECT") }
            if (providerNames.isNotEmpty()) g["use"] = providerNames
            groups.add(0, g)
            mainGroup = FALLBACK_MAIN_GROUP
            if (rules.none { it.startsWith("MATCH,") }) rules += "MATCH,$FALLBACK_MAIN_GROUP"
        }

        if (createdRegions.isNotEmpty()) {
            if (REGION_SELECTOR !in existingGroupNames) {
                val sel = LinkedHashMap<String, Any?>()
                sel["name"] = REGION_SELECTOR
                sel["type"] = "select"
                sel["proxies"] = createdRegions.map { it.groupName }
                groups += sel
            }
            // 每个能选代理的手动组（含主组）末尾都加上「🌍 地区选择」这一项：锁定地区时把它们都切过去（见 RegionLock），
            // 所有走代理的流量就都从该地区出去。加在末尾，不改变订阅原来的默认选择。
            val nonProxy = setOf("DIRECT", "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE")
            for (g in groups) {
                val name = g["name"]?.toString() ?: continue
                if (name == REGION_SELECTOR || !isSelect(g)) continue
                // 分级地区的「·自动」选择组是地区选择的下级，加进去会形成环
                if (name in tierSelectors) continue
                val list = (g["proxies"] as? List<Any?>)?.map { it.toString() }?.toMutableList() ?: mutableListOf()
                val canProxy = name == mainGroup || g["use"] != null || g["include-all"] == true || g["include-all-proxies"] == true ||
                    list.any { it.uppercase() !in nonProxy }
                if (canProxy && REGION_SELECTOR !in list) { list += REGION_SELECTOR; g["proxies"] = list }
            }
        }

        // ---------- 在线规则集 / 地理数据库的下载线路 ----------
        // 订阅引用的在线规则集（Netflix、广告拦截、国内网站列表等）和地理数据库大多放在国外网站上，国内直连下不了。
        // 内核默认按规则去下载，而很多订阅的兜底组默认指向"直连"，结果规则集全部下载失败、等于没有。
        // 这里固定让它们走代理：有地区选择组就用它（即首页选的出口地区），否则用主组。订阅自己写了 proxy 的不动。
        val downloadVia = if (createdRegions.isNotEmpty()) REGION_SELECTOR else mainGroup
        (cfg["rule-providers"] as? Map<String, Any?>)?.let { rp ->
            val fixed = LinkedHashMap<String, Any?>()
            for ((name, v) in rp) {
                val m = (v as? Map<String, Any?>)?.let { LinkedHashMap(it) }
                if (m != null && m["type"]?.toString().equals("http", true) && m["proxy"] == null) m["proxy"] = downloadVia
                fixed[name] = m ?: v
            }
            cfg["rule-providers"] = fixed
        }

        // ---------- 3. 规则 ----------
        if (!subHasRules) {
            // 订阅没带分流规则：补一套基础分流，局域网和国内网站、国内 IP 直连，其余走主组
            val subMatch = rules.lastOrNull { it.trim().startsWith("MATCH,") }
            rules.clear()
            rules += BASIC_RULES
            rules += subMatch ?: "MATCH,$mainGroup"
        } else if (rules.none { it.trim().startsWith("MATCH,") }) {
            // 有规则但没有兜底：没命中的流量内核默认直连，这里补成走主组
            rules += "MATCH,$mainGroup"
        }
        val front = mutableListOf<String>()
        if (settings.blockQuic) front += "AND,((NETWORK,UDP),(DST-PORT,443)),REJECT"
        if (settings.bingDirect) front += microsoftDirectRule()
        settings.extraRules.lines().map { it.trim() }.filter { it.isNotEmpty() && !it.startsWith("#") }.forEach { front += it }
        // 地理数据库的更新下载也固定走代理（理由同上）
        if (!downloadVia.contains(',')) front += "DOMAIN,$GEO_MIRROR_HOST,$downloadVia"
        // 内核自己的控制接口与本地回环永远直连
        front += "IP-CIDR,127.0.0.0/8,DIRECT,no-resolve"
        rules.addAll(0, front)

        cfg["proxies"] = proxies
        cfg["proxy-groups"] = groups
        cfg["rules"] = rules

        // ---------- 1 + 5. 手机环境专属字段 ----------
        listOf("port", "socks-port", "redir-port", "tproxy-port", "mixed-port", "allow-lan", "bind-address",
            "external-controller", "external-controller-tls", "external-controller-unix", "external-ui", "external-ui-url",
            "secret", "tun", "interface-name", "routing-mark", "lan-allowed-ips", "lan-disallowed-ips",
            "external-controller-cors", "ebpf", "iptables").forEach { cfg.remove(it) }

        if (settings.mixedPort > 0) cfg["mixed-port"] = settings.mixedPort
        cfg["allow-lan"] = false
        cfg["mode"] = settings.mode.coreValue
        cfg["log-level"] = settings.logLevel
        cfg["ipv6"] = settings.ipv6
        // 出境直连失败自动转代理：告诉内核侧（core/fallback.go）改走哪个组；没有这一项就是关闭
        cfg.remove("easyclash-direct-fallback")
        if (settings.directFallback) cfg["easyclash-direct-fallback"] = downloadVia
        cfg["external-controller"] = "127.0.0.1:$controllerPort"
        cfg["secret"] = secret
        cfg["unified-delay"] = true
        cfg["tcp-concurrent"] = true
        cfg["find-process-mode"] = "off"
        cfg["geodata-mode"] = false
        cfg["geo-auto-update"] = false
        cfg["profile"] = linkedMapOf("store-selected" to true, "store-fake-ip" to true)
        // 地理数据库的更新下载地址（jsDelivr 镜像；数据库本身已打包在 APK 里，这里只用于"更新"）
        val mirror = "https://$GEO_MIRROR_HOST/gh/MetaCubeX/meta-rules-dat@release"
        cfg["geox-url"] = linkedMapOf(
            "geoip" to "$mirror/geoip.dat",
            "geosite" to "$mirror/geosite.dat",
            "mmdb" to "$mirror/geoip.metadb",
            "asn" to "$mirror/GeoLite2-ASN.mmdb",
        )

        // 地址必须与 VpnService 里配置的一致，否则 system/mixed 栈无法正确回包
        val tun = linkedMapOf<String, Any?>(
            "enable" to true,
            "file-descriptor" to tunFd,
            "device" to "tun0",
            "stack" to settings.tunStack.coreValue,
            "mtu" to TUN_MTU,
            "inet4-address" to listOf("$TUN_ADDR4/30"),
            "dns-hijack" to listOf("any:53"),
            "auto-route" to false,
            "auto-detect-interface" to false,
            "strict-route" to false,
            "endpoint-independent-nat" to true,
        )
        if (settings.ipv6) tun["inet6-address"] = listOf("$TUN_ADDR6/126")
        cfg["tun"] = tun

        val subDns = cfg["dns"] as? Map<String, Any?>
        val keepSubDns = (settings.useSubscriptionDns && subDns?.get("enable") == true) || overrideHasDns
        if (keepSubDns && subDns != null) {
            val d = LinkedHashMap(subDns)
            d["enable"] = true
            d["ipv6"] = settings.ipv6
            d.remove("listen")   // 同上，避免端口冲突
            cfg["dns"] = d
        } else {
            cfg["dns"] = defaultDns(settings.ipv6)
        }

        outFile.parentFile?.mkdirs()
        outFile.writeText(yaml().dump(cfg))
        return BuildResult(outFile, mainGroup, createdRegions, tiered)
    }

    private fun isSelect(g: Map<String, Any?>) = g["type"]?.toString().equals("select", true)

    private fun goEscape(s: String) = s.replace(Regex("[.*+?^\${}()|\\[\\]\\\\]")) { "\\" + it.value }

    /** 一个按延迟自动选择的组。lazy：只有被用到时才定期测速，省流量省电 */
    private fun urlTestGroup(name: String, nodes: List<String>, providers: List<String>, filter: String, exclude: String, interval: Int): LinkedHashMap<String, Any?> {
        val g = LinkedHashMap<String, Any?>()
        g["name"] = name
        g["type"] = "url-test"
        g["url"] = "https://www.gstatic.com/generate_204"
        g["interval"] = interval
        g["tolerance"] = 50
        g["lazy"] = true
        if (nodes.isNotEmpty()) g["proxies"] = nodes
        if (providers.isNotEmpty()) {
            g["use"] = providers
            g["filter"] = filter
            g["exclude-filter"] = exclude
        }
        return g
    }

    private fun defaultDns(ipv6: Boolean): Map<String, Any?> = linkedMapOf(
        "enable" to true,
        // 不开 listen：TUN 模式下由 dns-hijack 在内核内部接管 53 端口，开了反而容易和其他代理软件抢端口
        "ipv6" to ipv6,
        "prefer-h3" to false,
        "enhanced-mode" to "fake-ip",
        "fake-ip-range" to "198.18.0.1/16",
        "fake-ip-filter" to listOf(
            "*.lan", "*.local", "*.localdomain", "localhost.ptlogin2.qq.com", "localhost.sec.qq.com",
            "+.msftconnecttest.com", "+.msftncsi.com", "dns.msftncsi.com",
            "+.srv.nintendo.net", "+.stun.playstation.net", "+.xboxlive.com", "stun.*.*", "stun.*.*.*",
            "+.market.xiaomi.com", "time.*.com", "ntp.*.com", "+.pool.ntp.org",
        ),
        "default-nameserver" to listOf("223.5.5.5", "119.29.29.29"),
        "nameserver" to listOf("https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"),
        "proxy-server-nameserver" to listOf("https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"),
        "respect-rules" to false,
    )

    /**
     * 覆写合并规则（与 Clash Verge / Clash Party 一致）：
     *   +rules:  插到原列表前面     rules+:  追加到原列表后面
     *   其他键：两边都是键值对则合并，否则整体替换
     */
    @Suppress("UNCHECKED_CAST")
    private fun applyOverride(base: MutableMap<String, Any?>, ov: Map<String, Any?>) {
        for ((k, v) in ov) {
            when {
                k.startsWith("+") -> {
                    val key = k.drop(1)
                    val cur = (base[key] as? List<Any?>) ?: emptyList()
                    base[key] = ((v as? List<Any?>) ?: emptyList()) + cur
                }
                k.endsWith("+") -> {
                    val key = k.dropLast(1)
                    val cur = (base[key] as? List<Any?>) ?: emptyList()
                    base[key] = cur + ((v as? List<Any?>) ?: emptyList())
                }
                v is Map<*, *> && base[k] is Map<*, *> -> {
                    val merged = LinkedHashMap(base[k] as Map<String, Any?>)
                    merged.putAll(v as Map<String, Any?>)
                    base[k] = merged
                }
                else -> base[k] = v
            }
        }
    }
}

/**
 * 微软服务走国内（含必应国内版）：微软的服务在国内都能直连，直连更快更稳，必应也会自动落到国内版（cn.bing.com）。
 * 例外：Copilot（国内不提供服务）不直连；GitHub 不在名单里（国内直连不稳）。
 * 例外的域名不会被这条规则命中，交给订阅自己的规则处理（一般就是走代理）。
 */
internal val MICROSOFT_DIRECT_DOMAINS = listOf(
            "bing.com",
            "bing.net",
            "bingapis.com",
            "bing.com.cn",
            "microsoft.com",
            "microsoftonline.com",
            "microsoftonline-p.com",
            "msauth.net",
            "msftauth.net",
            "live.com",
            "live.net",
            "office.com",
            "office.net",
            "office365.com",
            "microsoft365.com",
            "outlook.com",
            "hotmail.com",
            "onedrive.com",
            "sharepoint.com",
            "onenote.com",
            "msn.com",
            "skype.com",
            "xbox.com",
            "xboxlive.com",
            "windows.com",
            "windowsupdate.com",
            "msftconnecttest.com",
            "msftncsi.com",
            "microsoftstore.com",
            "visualstudio.com",
            "s-microsoft.com",
            "aka.ms",
        )
internal val MICROSOFT_PROXY_EXCEPTIONS = listOf(
            "copilot.microsoft.com",
            "designer.microsoft.com",
            "sydney.bing.com",
            "edgeservices.bing.com",
        )

/** 一条逻辑规则：属于微软域名、又不是例外（Copilot）的 → 直连 */
internal fun microsoftDirectRule(): String {
    fun or(list: List<String>) = list.joinToString(",", prefix = "OR,(", postfix = ")") { "(DOMAIN-SUFFIX,$it)" }
    return "AND,((${or(MICROSOFT_DIRECT_DOMAINS)}),(NOT,((${or(MICROSOFT_PROXY_EXCEPTIONS)})))),DIRECT"
}
