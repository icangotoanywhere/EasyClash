package com.easyclash.vpn

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings

/**
 * 后台常驻相关的检测和系统设置跳转（高级页「后台常驻」卡片、首次连接后的提醒用）。
 *
 * App 自己能做的：前台服务 + 常驻通知、START_STICKY（被杀后系统重新拉起并自动重连）、
 * 支持系统「始终开启的 VPN」。其余几项（电池优化、自启动、后台活动）只能由用户在系统设置里放开，
 * 这里负责检测状态、打开对应的设置页、按手机品牌给出菜单位置提示。
 */
object KeepAlive {

    /** 是否已经不受电池优化限制。也是安卓 12+ 允许被杀后从后台重新启动前台服务的条件之一 */
    fun ignoringBatteryOptimizations(ctx: Context): Boolean =
        ctx.getSystemService(PowerManager::class.java)?.isIgnoringBatteryOptimizations(ctx.packageName) == true

    /** 直接弹出系统的"允许在后台运行 / 不优化电池"对话框；个别系统不支持时退到电池优化列表 */
    @SuppressLint("BatteryLife")
    fun batteryIntent(ctx: Context): Intent =
        Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:${ctx.packageName}"))

    fun batteryListIntent(): Intent = Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS)

    /** 系统 VPN 设置页（在这里打开「始终开启的 VPN」） */
    fun vpnSettingsIntent(): Intent = Intent(Settings.ACTION_VPN_SETTINGS)

    /** 本应用的系统详情页（自启动、后台活动、耗电等各家都能从这里进） */
    fun appDetailsIntent(ctx: Context): Intent =
        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${ctx.packageName}"))

    /** 依次尝试打开，哪个系统支持就用哪个 */
    fun open(ctx: Context, vararg intents: Intent): Boolean {
        for (i in intents) {
            val ok = runCatching { ctx.startActivity(i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }.isSuccess
            if (ok) return true
        }
        return false
    }

    /** 按手机品牌提示「自启动 / 后台活动」在哪里设置。各家版本菜单名称会变，所以都提示可以直接搜索 */
    fun romTip(): String {
        val b = (Build.MANUFACTURER + " " + Build.BRAND).lowercase()
        val where = when {
            "honor" in b || "huawei" in b ->
                "荣耀 / 华为：设置 → 应用和服务 → 应用启动管理 → EasyClash，关掉「自动管理」，再打开「允许自启动」「允许关联启动」「允许后台活动」。"
            "xiaomi" in b || "redmi" in b || "poco" in b ->
                "小米 / 红米：设置 → 应用设置 → 应用管理 → EasyClash，打开「自启动」，「省电策略」选「无限制」。"
            "oppo" in b || "oneplus" in b || "realme" in b ->
                "OPPO / 一加 / 真我：设置 → 应用 → 应用管理 → EasyClash → 耗电管理，打开「允许后台运行」「允许自启动」「允许关联启动」。"
            "vivo" in b || "iqoo" in b ->
                "vivo / iQOO：设置 → 电池 → 后台耗电管理 → EasyClash 选「允许后台高耗电」；再到 i管家 → 应用管理 → 自启动管理，打开 EasyClash。"
            "samsung" in b ->
                "三星：设置 → 电池 → 后台使用限制 → 永不休眠的应用，加入 EasyClash。"
            "meizu" in b ->
                "魅族：手机管家 → 权限管理 → 后台管理 → EasyClash 选「允许后台运行」。"
            else -> "在系统设置里找到 EasyClash 的「自启动」「后台活动」「耗电管理」之类的选项，全部允许。"
        }
        return where + "\n\n不同系统版本菜单名称可能略有差别，可以直接在系统设置里搜索「自启动」「应用启动管理」「后台」。"
    }
}
