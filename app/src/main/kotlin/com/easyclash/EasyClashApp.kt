package com.easyclash

import android.app.Application
import com.easyclash.data.Store
import com.easyclash.data.SubscriptionRepo

/**
 * 全局单例入口。
 * Store：本地设置与订阅列表；Repo：订阅下载/更新。
 */
class EasyClashApp : Application() {
    lateinit var store: Store
        private set
    lateinit var repo: SubscriptionRepo
        private set

    override fun onCreate() {
        super.onCreate()
        instance = this
        store = Store(this)
        repo = SubscriptionRepo(this, store)
        installCrashRecorder()
    }

    /** 崩溃时把堆栈写到 files/crash.txt，高级页可以看到并复制，方便排查 */
    private fun installCrashRecorder() {
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, e ->
            runCatching {
                val sw = java.io.StringWriter()
                e.printStackTrace(java.io.PrintWriter(sw))
                val stamp = java.text.SimpleDateFormat("yyyy-MM-dd HH:mm:ss", java.util.Locale.getDefault()).format(java.util.Date())
                crashFile.writeText("时间：$stamp\n线程：${thread.name}\n\n$sw")
            }
            previous?.uncaughtException(thread, e)
        }
    }

    val crashFile: java.io.File get() = java.io.File(filesDir, "crash.txt")

    companion object {
        lateinit var instance: EasyClashApp
            private set
    }
}
