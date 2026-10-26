#!/bin/bash
# macOS 版自动测试：在 GitHub Actions 的云端 Mac 上运行（没有真机，靠它把每个环节都走一遍）。
#   bash macos/ci-test.sh dist/EasyClash.app <输出目录>
# 每项打印 PASS / FAIL；有 FAIL 时以非 0 退出。日志、截图等放到输出目录。
set -u

APP="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
OUT="$2"
mkdir -p "$OUT"
BIN="$APP/Contents/MacOS/EasyClash"
DATA="$HOME/Library/Application Support/EasyClash"
LOG="$DATA/logs/easyclash.log"
PORT=18765
MIXED=17890
FAILS=0
PASSES=0

pass() { echo "PASS: $*"; PASSES=$((PASSES + 1)); }
fail() { echo "FAIL: $*"; FAILS=$((FAILS + 1)); }
step() { echo; echo "======== $* ========"; }

inst() { python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$DATA/instance.json" "$1" 2>/dev/null; }
api() { # api 路径 [JSON 请求体]
  local p t
  p=$(inst port) || return 1
  t=$(inst token) || return 1
  if [ $# -ge 2 ]; then
    curl -sS -m 90 -H "X-EC-Token: $t" -H 'Content-Type: application/json' -d "$2" "http://127.0.0.1:$p/api/$1"
  else
    curl -sS -m 30 -H "X-EC-Token: $t" "http://127.0.0.1:$p/api/$1"
  fi
}
field() { # 从标准输入的 JSON 里取字段（a.b.c）
  python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print(""); sys.exit()
for k in sys.argv[1].split("."):
    d = d.get(k) if isinstance(d, dict) else None
print("" if d is None else (len(d) if isinstance(d, list) else d))' "$1"
}
wait_instance() { # 等界面服务可用
  for _ in $(seq 1 80); do
    [ -f "$DATA/instance.json" ] && api state >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  return 1
}
wait_status() { # 等到连接状态变成 $1
  local s=""
  for _ in $(seq 1 90); do
    s=$(api state | field status)
    [ "$s" = "$1" ] && return 0
    sleep 1
  done
  echo "状态一直是「${s}」，错误：$(api state | field error)"
  return 1
}
wait_exit() { # 等进程 $1 结束（本脚本启动的子进程结束后是僵尸进程，也算结束）
  local s
  for _ in $(seq 1 40); do
    s=$(ps -o stat= -p "$1" 2>/dev/null)
    [ -z "$s" ] && return 0
    case "$s" in Z*) return 0 ;; esac
    sleep 0.5
  done
  return 1
}
app_pid() { pgrep -f "Contents/MacOS/EasyClash" | head -1; }
proxy_is_ours() {
  local p
  p=$(scutil --proxy)
  echo "$p" | grep -q "HTTPEnable : 1" && echo "$p" | grep -q "HTTPProxy : 127.0.0.1" && echo "$p" | grep -q "HTTPPort : $MIXED" &&
    echo "$p" | grep -q "HTTPSEnable : 1" && echo "$p" | grep -q "SOCKSEnable : 1"
}
proxy_restored() { # 恢复成测试前设好的「原来的代理」：服务器 10.9.9.9:3128、处于关闭状态
  local w
  w=$(networksetup -getwebproxy "$SVC")
  echo "$w" | grep -q "Server: 10.9.9.9" && echo "$w" | grep -q "Port: 3128" && echo "$w" | grep -q "^Enabled: No" &&
    ! scutil --proxy | grep -q "HTTPEnable : 1" && ! scutil --proxy | grep -q "SOCKSEnable : 1"
}
start_headless() {
  rm -f "$DATA/instance.json"
  "$BIN" --headless --ui-port $PORT >>"$OUT/headless-stdout.txt" 2>&1 &
  PID=$!
}
shot() { screencapture -x "$OUT/$1.png" 2>/dev/null && echo "截图：$1.png" || echo "（截图失败：$1）"; }

# ---------- 准备 ----------
step "准备"
sw_vers
uname -m
lipo -info "$BIN"
codesign -dv "$APP" 2>&1 | head -3
rm -rf "$DATA"
mkdir -p "$DATA"
touch "$DATA/.test-no-browser" # 不真的打开浏览器，只记日志「[TEST] open」
networksetup -listallnetworkservices
SVC=$(networksetup -listallnetworkservices | sed 1d | grep -v '^\*' | head -1)
echo "检查用的网络服务：$SVC"
# 模拟"原来有别的代理设置"（关着的）：断开 / 退出后应当原样恢复
networksetup -setwebproxy "$SVC" 10.9.9.9 3128
networksetup -setwebproxystate "$SVC" off
networksetup -getwebproxy "$SVC"
echo "--- 测试前的系统代理 ---"
scutil --proxy

# ---------- 1. 后台模式 ----------
step "1. 启动（后台模式）"
start_headless
if wait_instance; then pass "启动成功，界面服务可用"; else
  fail "启动失败"
  cat "$OUT/headless-stdout.txt"
fi
st=$(api state)
echo "版本 $(echo "$st" | field version)，平台 $(echo "$st" | field platform)，内核 $(echo "$st" | field coreVersion)，管理员 $(echo "$st" | field isAdmin)"
[ "$(echo "$st" | field platform)" = "darwin" ] && pass "平台识别为 darwin" || fail "平台识别错误"
[ "$(echo "$st" | field tunSupported)" = "False" ] && pass "TUN 标记为不支持" || fail "tunSupported 应为 false"

step "2. 导入订阅"
SUB='proxies:
  - {name: "🇸🇬 新加坡 01", type: direct}
  - {name: "🇸🇬 新加坡 中转 02", type: direct}
  - {name: "🇯🇵 日本 01", type: direct}
  - {name: "🇺🇸 美国 01", type: direct}
proxy-groups:
  - {name: 节点选择, type: select, proxies: ["🇸🇬 新加坡 01", "🇸🇬 新加坡 中转 02", "🇯🇵 日本 01", "🇺🇸 美国 01"]}
rules:
  - MATCH,节点选择
'
body=$(SUB="$SUB" python3 -c 'import json,os;print(json.dumps({"name":"ci-test.yaml","text":os.environ["SUB"]}))')
r=$(api subs/import "$body")
echo "$r"
[ "$(echo "$r" | field ok)" = "True" ] && pass "导入订阅" || fail "导入订阅失败"
[ "$(api state | field subs)" = "1" ] && pass "订阅列表里有 1 个订阅" || fail "订阅数量不对"

step "3. 连接（系统代理）"
api connect '{}' >/dev/null
if wait_status connected; then pass "连接成功"; else fail "连接失败"; fi
sleep 2
st=$(api state)
echo "主组：$(echo "$st" | field mainGroup)，地区组数：$(echo "$st" | field regions)，sysProxyOn=$(echo "$st" | field sysProxyOn)，提醒=$(echo "$st" | field nodeWarn)"
[ "$(echo "$st" | field regions)" -ge 3 ] 2>/dev/null && pass "按地区分组（≥3 个）" || fail "地区组数量不对"
scutil --proxy
networksetup -getwebproxy "$SVC"
networksetup -getsecurewebproxy "$SVC"
networksetup -getsocksfirewallproxy "$SVC"
networksetup -getproxybypassdomains "$SVC"
proxy_is_ours && pass "系统代理（网页 / 安全网页 / SOCKS）都指向 127.0.0.1:$MIXED" || fail "系统代理没有指向本软件"
[ -f "$DATA/sysproxy-backup.json" ] && pass "已备份原来的系统代理" || fail "没有备份文件"
cat "$DATA/sysproxy-backup.json" 2>/dev/null
echo
[ -z "$(echo "$st" | field sysProxyOther)" ] && pass "首页没有误报「系统代理被改」" || fail "误报系统代理被改：$(echo "$st" | field sysProxyOther)"
python3 -c 'import urllib.request;print("Python 读到的系统代理：", urllib.request.getproxies())' | tee "$OUT/py-proxies.txt"
grep -q "127.0.0.1:$MIXED" "$OUT/py-proxies.txt" && pass "其他程序（Python）能读到系统代理" || fail "Python 没读到系统代理"
code=$(curl -sS -m 30 -o /dev/null -w '%{http_code}' -x "http://127.0.0.1:$MIXED" https://www.example.com)
[ "$code" = "200" ] && pass "经本软件的 HTTP 代理访问 https://www.example.com（${code}）" || fail "HTTP 代理访问失败（${code}）"
code=$(curl -sS -m 30 -o /dev/null -w '%{http_code}' --socks5-hostname "127.0.0.1:$MIXED" https://www.example.com)
[ "$code" = "200" ] && pass "经本软件的 SOCKS5 代理访问（${code}）" || fail "SOCKS5 代理访问失败（${code}）"

step "4. 锁定出口地区、测速、内核接口"
r=$(api region/lock '{"code":"JP"}')
echo "$r"
[ "$(echo "$r" | field ok)" = "True" ] && [ "$(api state | field settings.lockedRegion)" = "JP" ] && pass "锁定日本" || fail "锁定地区失败"
r=$(api region/test '{"code":"SG"}')
echo "$r"
[ "$(echo "$r" | field ok)" = "True" ] && pass "新加坡测速：$(echo "$r" | field msg)" || fail "地区测速失败"
code=$(curl -sS -m 30 -o /dev/null -w '%{http_code}' -x "http://127.0.0.1:$MIXED" https://www.example.com)
[ "$code" = "200" ] && pass "锁定后仍能访问（${code}）" || fail "锁定后访问失败（${code}）"
p=$(inst port)
t=$(inst token)
n=$(curl -sS -m 20 -H "X-EC-Token: $t" "http://127.0.0.1:$p/core/proxies" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["proxies"]))' 2>/dev/null)
[ "${n:-0}" -gt 4 ] && pass "内核接口（经界面服务转发）可用：$n 个代理/组" || fail "内核接口不可用"
r=$(api processes)
c=$(echo "$r" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))' 2>/dev/null)
[ "${c:-0}" -gt 5 ] && pass "读取进程列表（分程序代理用）：$c 个" || fail "进程列表为空"
echo "$r" | python3 -c 'import json,sys;print([p["name"] for p in json.load(sys.stdin) if not p["system"]][:15])' 2>/dev/null

step "5. 开机自启（LaunchAgent）"
api settings '{"autoStart":true}' >/dev/null
LA="$HOME/Library/LaunchAgents/com.easyclash.desktop.plist"
if [ -f "$LA" ] && plutil -lint "$LA" >/dev/null && grep -q -- "--autostart" "$LA" && grep -q "$BIN" "$LA"; then pass "已写入 LaunchAgent"; else fail "LaunchAgent 不对"; fi
cat "$LA" 2>/dev/null
api settings '{"autoStart":false}' >/dev/null
[ ! -f "$LA" ] && pass "关闭后已删除 LaunchAgent" || fail "关闭后 LaunchAgent 仍在"

step "6. 断开：恢复原来的系统代理"
api disconnect '{}' >/dev/null
if wait_status idle; then pass "已断开"; else fail "断开失败"; fi
sleep 1
networksetup -getwebproxy "$SVC"
scutil --proxy
proxy_restored && pass "恢复成原来的设置（10.9.9.9:3128，关闭）" || fail "没有恢复原来的系统代理"
[ ! -f "$DATA/sysproxy-backup.json" ] && pass "备份文件已清理" || fail "备份文件还在"

step "7. 强制结束（kill -9）后再启动：先恢复系统代理"
api connect '{}' >/dev/null
wait_status connected >/dev/null
sleep 1
proxy_is_ours && pass "重新连接，系统代理指向本软件" || fail "重新连接后系统代理不对"
kill -9 "$PID"
wait "$PID" 2>/dev/null
proxy_is_ours && echo "（强制结束后系统代理仍指向本软件——预期如此，下次启动时恢复）"
start_headless
wait_instance || fail "再次启动失败"
sleep 1
proxy_restored && pass "再次启动时恢复了系统代理" || fail "再次启动没有恢复系统代理"
grep -q "上次没有正常退出" "$LOG" && pass "日志里记录了异常退出后的恢复" || fail "日志里没有恢复记录"
[ "$(api state | field settings.lockedRegion)" = "JP" ] && pass "设置已保存（重启后仍锁定日本）" || fail "设置没有保存"

step "8. 单实例：再启动一个，应把请求交给已在运行的那个后自己退出"
before=$(grep -c "\[TEST\] open" "$LOG")
"$BIN" --headless >"$OUT/second-instance.txt" 2>&1 &
P2=$!
if wait_exit "$P2"; then pass "第二个实例自动退出"; else
  fail "第二个实例没有退出"
  kill -9 "$P2" 2>/dev/null
fi
sleep 1
after=$(grep -c "\[TEST\] open" "$LOG")
[ "$after" -gt "$before" ] && pass "已在运行的实例收到「打开控制面板」请求" || fail "没有收到打开请求"

step "9. 收到结束信号（SIGTERM）：断开并恢复系统代理"
api connect '{}' >/dev/null
wait_status connected >/dev/null
sleep 1
proxy_is_ours || fail "连接后系统代理不对"
kill -TERM "$PID"
if wait_exit "$PID"; then pass "收到 SIGTERM 后退出"; else fail "SIGTERM 后没有退出"; fi
proxy_restored && pass "SIGTERM 退出时恢复了系统代理" || fail "SIGTERM 退出时没有恢复系统代理"

# ---------- 2. 菜单栏模式（和用户双击打开一样） ----------
step "10. 菜单栏模式：用 open 打开 .app"
rm -f "$DATA/instance.json"
before=$(grep -c "\[TEST\] open" "$LOG")
open "$APP"
if wait_instance; then pass "菜单栏模式启动成功"; else fail "菜单栏模式启动失败"; fi
sleep 3
GP=$(app_pid)
echo "进程：$GP"
ps -o pid,stat,etime,command -p "$GP" 2>/dev/null
[ -n "$GP" ] && pass "进程在运行" || fail "进程不在"
[ "$(grep -c "\[TEST\] open" "$LOG")" -gt "$before" ] && pass "启动时打开了控制面板" || fail "启动时没有打开控制面板"
shot "1-menubar"
# 尝试点开菜单栏图标截图（云端 Mac 不一定有辅助功能权限，失败不算错）
osascript -e 'tell application "System Events" to tell process "EasyClash" to click menu bar item 1 of menu bar 2' >/dev/null 2>&1 && sleep 1 && shot "2-menu-open"
osascript -e 'tell application "System Events" to key code 53' >/dev/null 2>&1
osascript -e 'tell application "System Events" to get name of every menu item of menu 1 of menu bar item 1 of menu bar 2 of process "EasyClash"' >"$OUT/menu-items.txt" 2>&1
echo "菜单项：$(cat "$OUT/menu-items.txt")"

step "11. 菜单栏模式下连接"
api connect '{}' >/dev/null
if wait_status connected; then pass "菜单栏模式连接成功"; else fail "菜单栏模式连接失败"; fi
sleep 2
proxy_is_ours && pass "系统代理指向本软件" || fail "系统代理不对"
shot "3-menubar-connected"
osascript -e 'tell application "System Events" to get name of every menu item of menu 1 of menu bar item 1 of menu bar 2 of process "EasyClash"' >"$OUT/menu-items-connected.txt" 2>&1
echo "菜单项：$(cat "$OUT/menu-items-connected.txt")"

step "12. 再次打开（程序已在运行时又双击了它）"
before=$(grep -c "程序被再次打开" "$LOG")
open "$APP"
sleep 3
[ "$(grep -c "程序被再次打开" "$LOG")" -gt "$before" ] && pass "收到「再次打开」，打开控制面板" || fail "没有收到「再次打开」事件"
[ "$(app_pid)" = "$GP" ] && pass "仍是同一个进程（没有重复启动）" || fail "进程变了：$(app_pid)"

step "13. clash:// 一键导入链接（程序运行中）"
open "clash://install-config?url=https%3A%2F%2Fexample.com%2Frunning.yaml&name=ci"
sleep 3
pi=$(api state | field pendingImport)
echo "pendingImport=$pi"
echo "$pi" | grep -q "running.yaml" && pass "收到 clash:// 链接" || fail "没有收到 clash:// 链接"
api pending-import/clear '{}' >/dev/null

step "14. 退出（和点菜单栏「退出」一样）：断开并恢复系统代理"
api quit '{}' >/dev/null
if wait_exit "$GP"; then pass "已退出"; else fail "退出超时"; fi
sleep 1
proxy_restored && pass "退出时恢复了系统代理" || fail "退出时没有恢复系统代理"
[ ! -f "$DATA/instance.json" ] && pass "已清理 instance.json" || fail "instance.json 没有清理"

step "15. 点 clash:// 链接把程序启动起来"
rm -f "$DATA/instance.json"
open "clash://install-config?url=https%3A%2F%2Fexample.com%2Fcold.yaml&name=cold"
if wait_instance; then pass "通过链接启动成功"; else fail "通过链接启动失败"; fi
sleep 3
pi=$(api state | field pendingImport)
echo "pendingImport=$pi"
echo "$pi" | grep -q "cold.yaml" && pass "启动时随之送来的链接也收到了" || fail "启动时送来的链接没收到"
GP=$(app_pid)
api quit '{}' >/dev/null
wait_exit "$GP" || { fail "退出超时"; kill -9 "$GP" 2>/dev/null; }

# ---------- 结果 ----------
step "结果：$PASSES 项通过，$FAILS 项失败"
cp "$LOG" "$OUT/easyclash.log" 2>/dev/null
cp "$DATA/crash.txt" "$OUT/crash.txt" 2>/dev/null
ls -la "$DATA" "$DATA/run" 2>/dev/null | head -40
[ "$FAILS" -eq 0 ]
