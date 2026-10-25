package com.easyclash.core

/**
 * 按节点名称识别地区。机场节点命名五花八门（中文、英文、缩写、国旗 emoji），
 * 这里用正则尽量覆盖常见写法。
 */
data class Region(
    val code: String,
    val flag: String,
    val name: String,
    /** Kotlin 侧用：判断某个节点名属于哪个地区 */
    val regex: Regex,
    /** 同样的匹配逻辑，但写成 Go 正则（RE2 不支持环视），给内核策略组的 filter 字段用 */
    val goPattern: String,
) {
    /** 内核里自动测速组的名字，例如 "🇸🇬 新加坡·自动"（内核侧保持不变） */
    val groupName: String get() = "$flag $name·自动"
    /** 分级时的两个子组：优先节点组、备用节点组（见 ConfigBuilder / TierPicker） */
    val primaryGroup: String get() = "$flag $name·优先"
    val backupGroup: String get() = "$flag $name·备用"

    /** 界面上显示的地区名：港澳台显示为「中国香港」「中国澳门」「中国台湾」。name 仍用于识别节点名和内核组名 */
    val displayName: String get() = if (code in CHINA_SAR) "中国$name" else name

    /** 界面上显示的测速组名，例如 "🇭🇰 中国香港·自动" */
    val displayGroupName: String get() = "$flag $displayName·自动"
}

private val CHINA_SAR = setOf("HK", "MO", "TW")

object Regions {
    private fun goEscape(s: String) = s.replace(Regex("[.*+?^\${}()|\\[\\]\\\\]")) { "\\" + it.value }

    /**
     * 构造一个地区：
     * - 短缩写（SG、HK、US…）要求前后都不是英文字母，避免 "US" 误伤 "Australia"
     * - 其他关键词（中文、英文全名、城市名）直接包含即可
     */
    private fun region(code: String, flag: String, name: String, codes: List<String>, words: List<String>): Region {
        val allWords = listOf(flag, name) + words
        val ktCode = codes.joinToString("|") { "(?<![A-Za-z])$it(?![A-Za-z])" }
        val ktWord = allWords.joinToString("|") { Regex.escape(it) }
        val kt = Regex("$ktCode|$ktWord", RegexOption.IGNORE_CASE)
        val goCode = codes.joinToString("|") { "(^|[^A-Za-z])$it([^A-Za-z]|$)" }
        val goWord = allWords.joinToString("|") { goEscape(it) }
        return Region(code, flag, name, kt, "(?i)$goCode|$goWord")
    }

    val all: List<Region> = listOf(
        region("HK", "🇭🇰", "香港", listOf("HK", "HKG"), listOf("Hong Kong", "HongKong")),
        region("TW", "🇹🇼", "台湾", listOf("TW", "TWN"), listOf("臺灣", "Taiwan")),
        region("SG", "🇸🇬", "新加坡", listOf("SG", "SGP"), listOf("狮城", "Singapore")),
        region("JP", "🇯🇵", "日本", listOf("JP", "JPN"), listOf("东京", "大阪", "Japan", "Tokyo", "Osaka")),
        region("KR", "🇰🇷", "韩国", listOf("KR", "KOR"), listOf("韓國", "首尔", "Korea", "Seoul")),
        region("US", "🇺🇸", "美国", listOf("US", "USA"), listOf("美國", "United States", "America", "洛杉矶", "硅谷", "西雅图", "纽约", "芝加哥", "Los Angeles", "San Jose", "Seattle", "New York")),
        region("GB", "🇬🇧", "英国", listOf("UK", "GB", "GBR"), listOf("英國", "伦敦", "United Kingdom", "Britain", "London")),
        region("DE", "🇩🇪", "德国", listOf("DE", "DEU"), listOf("德國", "法兰克福", "Germany", "Frankfurt")),
        region("FR", "🇫🇷", "法国", listOf("FR", "FRA"), listOf("法國", "巴黎", "France", "Paris")),
        region("NL", "🇳🇱", "荷兰", listOf("NL", "NLD"), listOf("阿姆斯特丹", "Netherlands", "Amsterdam")),
        region("CA", "🇨🇦", "加拿大", listOf("CA", "CAN"), listOf("多伦多", "Canada", "Toronto")),
        region("AU", "🇦🇺", "澳大利亚", listOf("AU", "AUS"), listOf("澳洲", "悉尼", "Australia", "Sydney")),
        region("MY", "🇲🇾", "马来西亚", listOf("MY", "MYS"), listOf("馬來西亞", "Malaysia")),
        region("TH", "🇹🇭", "泰国", listOf("TH", "THA"), listOf("曼谷", "Thailand", "Bangkok")),
        region("VN", "🇻🇳", "越南", listOf("VN", "VNM"), listOf("Vietnam")),
        region("PH", "🇵🇭", "菲律宾", listOf("PH", "PHL"), listOf("Philippines")),
        region("IN", "🇮🇳", "印度", listOf("IN", "IND"), listOf("India", "Mumbai")),
        region("TR", "🇹🇷", "土耳其", listOf("TR", "TUR"), listOf("Turkey", "Türkiye")),
        region("AE", "🇦🇪", "阿联酋", listOf("AE", "UAE"), listOf("迪拜", "Dubai")),
        region("RU", "🇷🇺", "俄罗斯", listOf("RU", "RUS"), listOf("莫斯科", "Russia", "Moscow")),
        region("BR", "🇧🇷", "巴西", listOf("BR", "BRA"), listOf("Brazil")),
        region("AR", "🇦🇷", "阿根廷", listOf("AR", "ARG"), listOf("Argentina")),
        region("MO", "🇲🇴", "澳门", listOf("MO", "MAC"), listOf("澳門", "Macau", "Macao")),
    )

    /** 节点名里出现"剩余/到期/流量/官网/过期"等字样的是信息条目，不是节点 */
    val infoNodeRegex = Regex("剩余|到期|流量|官网|过期|套餐|重置|expire|traffic|群|公告", RegexOption.IGNORE_CASE)

    fun of(code: String?): Region? = all.firstOrNull { it.code == code }

    /** 把内核里的策略组名换成界面显示用的名字（地区自动组用显示名，其余原样返回） */
    fun displayName(kernelName: String): String = all.firstOrNull { it.groupName == kernelName }?.displayGroupName ?: kernelName

    /** 判断一个节点属于哪个地区。先按国旗/中文名精确匹配，再用正则。 */
    fun match(nodeName: String): Region? {
        if (infoNodeRegex.containsMatchIn(nodeName)) return null
        return all.firstOrNull { it.flag in nodeName || it.name in nodeName }
            ?: all.firstOrNull { it.regex.containsMatchIn(nodeName) }
    }
}
