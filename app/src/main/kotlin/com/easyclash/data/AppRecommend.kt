package com.easyclash.data

/**
 * 「一键推荐」：给每个 App 推荐走法。
 *
 * 原则：
 *  - 中国的银行、证券、支付，微信、QQ 等社交办公，生活政务必备 App → 仅直连
 *  - 海外 AI（ChatGPT、Claude、Gemini……）→ 仅代理
 *  - 其余所有、吃不准的、不是非直连 / 非代理不可的 → 按规则
 *
 * 只认确定的包名和应用名里的明确字样（如"银行""证券"），宁可漏判成「按规则」也不误判。
 * 国产 AI（DeepSeek、豆包、Kimi 等）算国内 App，保持「按规则」。
 */
object AppRecommend {

    /** 包名 → 仅直连 */
    private val DIRECT_PACKAGES = setOf(
        // 支付
        "com.eg.android.AlipayGphone",          // 支付宝
        "com.unionpay",                         // 云闪付
        // 社交、办公
        "com.tencent.mm",                       // 微信
        "com.tencent.mobileqq",                 // QQ
        "com.tencent.tim",                      // TIM
        "com.tencent.wework",                   // 企业微信
        "com.alibaba.android.rimet",            // 钉钉
        "com.ss.android.lark",                  // 飞书
        "com.tencent.wemeet.app",               // 腾讯会议
        // 证券行情（应用名里不一定带"证券"）
        "com.hexin.plat.android",               // 同花顺
        "com.eastmoney.android.berlin",         // 东方财富
        // 购物、生活、出行
        "com.taobao.taobao",                    // 淘宝
        "com.tmall.wireless",                   // 天猫
        "com.jingdong.app.mall",                // 京东
        "com.xunmeng.pinduoduo",                // 拼多多
        "com.sankuai.meituan",                  // 美团
        "com.sankuai.meituan.takeoutnew",       // 美团外卖
        "me.ele",                               // 饿了么
        "com.dianping.v1",                      // 大众点评
        "com.sdu.didi.psnger",                  // 滴滴出行
        "com.autonavi.minimap",                 // 高德地图
        "com.baidu.BaiduMap",                   // 百度地图
        "com.tencent.map",                      // 腾讯地图
        // 政务、出行证件
        "com.MobileTicket",                     // 铁路 12306
        "com.tmri.app.main",                    // 交管 12123
        "cn.gov.tax.its",                       // 个人所得税
        // 运营商
        "com.greenpoint.android.mc10086.activity", // 中国移动
        "com.sinovatech.unicom.ui",             // 中国联通
        "com.ct.client",                        // 中国电信
    )

    /** 应用名里出现这些字样 → 仅直连（银行、券商、保险、政务等，各家包名五花八门，按名字认更可靠） */
    private val DIRECT_LABEL_WORDS = listOf(
        "银行", "证券", "期货", "基金", "保险", "信用卡", "农信", "信用社",
        "医保", "社保", "公积金", "税务", "政务", "12306", "12123",
    )

    /** 包名 → 仅代理 */
    private val PROXY_PACKAGES = setOf(
        "com.openai.chatgpt",                   // ChatGPT
        "com.anthropic.claude",                 // Claude
        "com.google.android.apps.bard",         // Gemini
        "ai.perplexity.app.android",            // Perplexity
        "com.microsoft.copilot",                // Microsoft Copilot
        "ai.x.grok",                            // Grok
    )

    /** 应用名（英文）完全等于或以这些开头 → 仅代理 */
    private val PROXY_LABEL_PREFIXES = listOf("ChatGPT", "Claude", "Gemini", "Perplexity", "Grok", "Poe", "Microsoft Copilot")

    fun recommend(app: AppInfo): AppRoute {
        if (app.packageName in PROXY_PACKAGES) return AppRoute.PROXY_ONLY
        if (app.packageName in DIRECT_PACKAGES) return AppRoute.DIRECT_ONLY
        val label = app.label.trim()
        if (PROXY_LABEL_PREFIXES.any { label.equals(it, true) || label.startsWith("$it ", true) }) return AppRoute.PROXY_ONLY
        if (DIRECT_LABEL_WORDS.any { label.contains(it) }) return AppRoute.DIRECT_ONLY
        return AppRoute.RULE
    }
}
