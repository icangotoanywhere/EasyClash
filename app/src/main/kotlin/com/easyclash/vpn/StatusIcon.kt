package com.easyclash.vpn

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.PorterDuff
import android.graphics.PorterDuffXfermode
import android.graphics.RectF
import android.graphics.Typeface
import android.graphics.drawable.Icon

/**
 * 状态栏小图标的轮换内容：EasyClash 图标 → 出口国家代码（JP）→ 延迟数字（85）。
 *
 * 状态栏小图标只能是单色（系统只用透明度，统一染成白色或灰色），所以延迟好坏用形状区分：
 * 好 = 只有数字，一般 = 数字加方框，差 / 超时 = 实心方块里镂空数字。
 * 颜色（绿 / 黄 / 红）用在下拉后的通知卡片上（前台服务通知允许整卡上色）。
 */
object StatusIcon {
    enum class Level(val color: Int) {
        GOOD(0xFF2E7D32.toInt()),   // < 200 ms，和「节点」页的颜色一致
        MID(0xFFF9A825.toInt()),    // < 500 ms
        BAD(0xFFC62828.toInt()),    // ≥ 500 ms 或超时
    }

    /** ms > 0 为延迟；ms < 0 表示超时；0 表示还没测 */
    fun level(ms: Int): Level? = when {
        ms == 0 -> null
        ms < 0 -> Level.BAD
        ms < 200 -> Level.GOOD
        ms < 500 -> Level.MID
        else -> Level.BAD
    }

    private const val SIZE = 96   // 系统会缩放到状态栏图标大小（24dp）
    private val cache = HashMap<String, Icon>()

    /** 国家代码，例如 "JP" */
    fun country(code: String): Icon = cached("c:$code") { draw(code, null) }

    /** 延迟数字；超时显示 "--" */
    fun delay(ms: Int): Icon {
        val lv = level(ms) ?: Level.MID
        val text = if (ms < 0) "--" else if (ms > 9999) "9999" else ms.toString()
        return cached("d:$text:$lv") { draw(text, lv) }
    }

    private fun cached(key: String, make: () -> Icon): Icon = synchronized(cache) {
        if (cache.size > 300) cache.clear()
        cache.getOrPut(key, make)
    }

    private fun draw(text: String, lv: Level?): Icon {
        val bmp = Bitmap.createBitmap(SIZE, SIZE, Bitmap.Config.ARGB_8888)
        val c = Canvas(bmp)
        val box = RectF(3f, 14f, SIZE - 3f, SIZE - 14f)
        val radius = 14f
        val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = Color.WHITE
            typeface = Typeface.create(Typeface.DEFAULT, Typeface.BOLD)
            textAlign = Paint.Align.CENTER
        }
        // 先按高度定字号，再按宽度缩小，保证 4 位数也放得下
        val pad = if (lv == null || lv == Level.GOOD) 0f else 12f
        val maxW = box.width() - pad * 2
        val maxH = box.height() - pad * 1.2f
        paint.textSize = maxH
        val w = paint.measureText(text)
        if (w > maxW) paint.textSize = paint.textSize * maxW / w
        val fm = paint.fontMetrics
        val baseline = box.centerY() - (fm.ascent + fm.descent) / 2

        when (lv) {
            Level.MID -> {
                val stroke = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = Color.WHITE; style = Paint.Style.STROKE; strokeWidth = 7f }
                c.drawRoundRect(RectF(box.left + 3.5f, box.top + 3.5f, box.right - 3.5f, box.bottom - 3.5f), radius, radius, stroke)
                c.drawText(text, box.centerX(), baseline, paint)
            }
            Level.BAD -> {
                c.drawRoundRect(box, radius, radius, Paint(Paint.ANTI_ALIAS_FLAG).apply { color = Color.WHITE })
                paint.xfermode = PorterDuffXfermode(PorterDuff.Mode.CLEAR)   // 镂空
                c.drawText(text, box.centerX(), baseline, paint)
            }
            else -> c.drawText(text, box.centerX(), baseline, paint)
        }
        return Icon.createWithBitmap(bmp)
    }
}
