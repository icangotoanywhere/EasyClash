'use strict';
// EasyClash 桌面版界面（Windows：WebView2 窗口；macOS：浏览器）。和安卓版的五个页面一一对应，每个设置项都有一行说明，ⓘ 弹出详细解释。

const TOKEN = decodeURIComponent(location.hash.slice(1));
const $ = (s) => document.querySelector(s);
// 平台：后台在 /api/state 里告诉界面是 windows 还是 darwin（macOS），说明文字据此切换
const MAC = () => !!(S.st && S.st.platform === 'darwin');
const P = (win, mac) => (MAC() ? mac : win);

const S = {
  st: null,            // 后台状态 /api/state
  px: {},              // 内核里的代理与策略组
  page: 'home',
  expanded: {},        // 节点页展开状态
  traffic: [0, 0],
  rinfo: null,         // 规则与地理数据库信息
  procs: null,         // 进程列表
  procQuery: '',
  showSys: false,
  testing: false,
  offline: false,
  lastHash: '',
  pendingRender: false,
  dialogOpen: false,
};
try { S.page = localStorage.getItem('page') || 'home'; } catch (e) { /* 忽略 */ }

// ======================= 小工具 =======================
function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  if (props) {
    for (const [k, v] of Object.entries(props)) {
      if (v === undefined || v === null || v === false) continue;
      if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2).toLowerCase(), v);
      else if (k === 'class') el.className = v;
      else if (k === 'style') el.style.cssText = v;
      else if (k === 'html') el.innerHTML = v;
      else if (k === 'value') el.value = v;
      else if (k === 'checked') el.checked = !!v;
      else el.setAttribute(k, v === true ? '' : v);
    }
  }
  for (const c of kids.flat(9)) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

const ICONS = {
  home: 'M10 20v-6h4v6h5v-8h3L12 3 2 12h3v8z',
  subs: 'M6.18 15.64a2.18 2.18 0 1 1 0 4.36 2.18 2.18 0 0 1 0-4.36zM4 4.44A15.56 15.56 0 0 1 19.56 20h-2.83A12.73 12.73 0 0 0 4 7.27V4.44zm0 5.66a9.9 9.9 0 0 1 9.9 9.9h-2.83A7.07 7.07 0 0 0 4 12.93V10.1z',
  nodes: 'M12 2a3 3 0 0 1 1 5.83V10h4.17A3 3 0 1 1 17 12h-4v2.17a3 3 0 1 1-2 0V12H7a3 3 0 1 1 .17-2H11V7.83A3 3 0 0 1 12 2z',
  apps: 'M4 8h4V4H4v4zm6 12h4v-4h-4v4zm-6 0h4v-4H4v4zm0-6h4v-4H4v4zm6 0h4v-4h-4v4zm6-10v4h4V4h-4zm-6 4h4V4h-4v4zm6 6h4v-4h-4v4zm0 6h4v-4h-4v4z',
  advanced: 'M3 17v2h6v-2H3zM3 5v2h10V5H3zm10 16v-2h8v-2h-8v-2h-2v6h2zM7 9v2H3v2h4v2h2V9H7zm14 4v-2H11v2h10zm-6-4h2V7h4V5h-4V3h-2v6z',
  info: 'M11 7h2v2h-2zm0 4h2v6h-2zm1-9C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm0 18c-4.41 0-8-3.59-8-8s3.59-8 8-8 8 3.59 8 8-3.59 8-8 8z',
  more: 'M16.59 8.59 12 13.17 7.41 8.59 6 10l6 6 6-6z',
  less: 'm12 8-6 6 1.41 1.41L12 10.83l4.59 4.58L18 14z',
};
function icon(name) {
  const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  s.setAttribute('viewBox', '0 0 24 24');
  const p = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  p.setAttribute('d', ICONS[name]);
  s.append(p);
  return s;
}

function humanBytes(b) {
  if (b === undefined || b === null || b < 0) return '—';
  if (b >= 1 << 30) return (b / (1 << 30)).toFixed(2) + ' GB';
  if (b >= 1 << 20) return (b / (1 << 20)).toFixed(1) + ' MB';
  if (b >= 1 << 10) return (b / 1024).toFixed(0) + ' KB';
  return b + ' B';
}
function fmtTime(ms) {
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

let toastTimer = 0;
function toast(msg) {
  if (!msg) return;
  const t = $('#toast');
  t.textContent = msg;
  t.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), Math.min(8000, 2600 + msg.length * 40));
}

async function api(path, body) {
  const opt = { headers: { 'X-EC-Token': TOKEN } };
  if (body !== undefined) {
    opt.method = 'POST';
    opt.headers['Content-Type'] = 'application/json';
    opt.body = JSON.stringify(body);
  }
  const r = await fetch('/api/' + path, opt);
  let j = {};
  try { j = await r.json(); } catch (e) { /* 忽略 */ }
  if (!r.ok) throw new Error(j.error || j.message || ('请求失败（' + r.status + '）'));
  return j;
}

async function core(path, method = 'GET', body) {
  const opt = { method, headers: { 'X-EC-Token': TOKEN } };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  const r = await fetch('/core' + path, opt);
  const t = await r.text();
  let j = {};
  try { j = t ? JSON.parse(t) : {}; } catch (e) { /* 忽略 */ }
  if (!r.ok) throw new Error(j.message || ('内核返回 ' + r.status));
  return j;
}

// 调用后台操作：成功显示提示并刷新，失败显示原因
async function act(path, body, okMsg) {
  try {
    const j = await api(path, body);
    if (j.msg) toast(j.msg); else if (okMsg) toast(okMsg);
    await refresh(true);
    return j;
  } catch (e) {
    toast(e.message);
    return null;
  }
}

const setSetting = (patch, okMsg) => act('settings', patch, okMsg);

// ======================= 通用控件 =======================
// 备用节点关键字输入框下方的"当前效果"说明（与安卓版 AdvancedScreen.kt 一致）：只有空格也按留空处理
function backupKeywordEffect(raw) {
  const kw = (raw || '').trim();
  if (!kw && raw) return '当前效果：只填了空格，保存时会去掉，等同留空——不分级，同一地区所有节点只按延迟挑最快的。';
  if (!kw) return '当前效果：不分级，同一地区所有节点只按延迟挑最快的。';
  const tail = kw !== raw ? '（首尾空格保存时会自动去掉）' : '';
  return `当前效果：名字含「${kw}」的节点作备用，其余为优先${tail}。某地区没有这类节点时，该地区不分级。`;
}
function helpText(text, cls) { return h('div', { class: 'help ' + (cls || '') }, text); }
function kv(k, v, id) { return h('div', { class: 'kv' }, h('span', { class: 'k' }, k), h('span', { class: 'v', id }, v)); }
function btn(text, onClick, variant, disabled) {
  return h('button', { class: 'btn ' + (variant || ''), onclick: onClick, disabled: !!disabled }, text);
}
function infoBtn(title, detail) {
  return h('button', { class: 'icon', title: '说明', onclick: (e) => { e.stopPropagation(); infoDialog(title, detail); } }, icon('info'));
}
function card(title, help, ...kids) {
  return h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', null, title), help ? infoBtn(title, help) : null), kids);
}
function settingRow(title, help, detail, trailing) {
  return h('div', { class: 'setting' },
    h('div', { class: 'grow' }, h('div', { class: 't' }, title), helpText(help)),
    detail ? infoBtn(title, detail) : null,
    trailing);
}
function switchRow(title, help, checked, detail, onChange) {
  const input = h('input', { type: 'checkbox', checked, onchange: (e) => onChange(e.target.checked) });
  return settingRow(title, help, detail, h('label', { class: 'switch' }, input, h('span')));
}
function chip(label, on, onClick) { return h('button', { class: 'chip' + (on ? ' on' : ''), onclick: onClick }, label); }

// ======================= 对话框 =======================
function openDialog(title, body, actions, wide) {
  const ov = h('div', { class: 'overlay' });
  const close = () => { ov.remove(); S.dialogOpen = document.querySelectorAll('.overlay').length > 0; if (ov._onclose) ov._onclose(); };
  ov.addEventListener('mousedown', (e) => { if (e.target === ov) close(); });
  const acts = (actions || []).map((a) => (a instanceof Node ? a : btn(a.text, () => a.onClick(close), a.variant || 'text', a.disabled)));
  ov.append(h('div', { class: 'dialog' + (wide ? ' wide' : '') }, h('h2', null, title), body, h('div', { class: 'actions' }, acts)));
  document.body.append(ov);
  S.dialogOpen = true;
  ov.close = close;
  return ov;
}
function infoDialog(title, text) {
  openDialog(title, h('div', { class: 'body pre' }, text), [{ text: '知道了', onClick: (c) => c() }]);
}
function confirmDialog(title, text, confirmText, onConfirm) {
  openDialog(title, h('div', { class: 'body pre' }, text), [
    { text: '取消', onClick: (c) => c() },
    { text: confirmText || '确定', variant: 'text danger', onClick: (c) => { c(); onConfirm(); } },
  ]);
}

// ======================= 数据 =======================
function regionByGroup(name) { return (S.st && S.st.allRegions || []).find((r) => r.groupName === name); }
// 「·优先」「·备用」是「·自动」下面的两级，名字只差后缀
function tierSuffix(name) { return name.endsWith('·优先') ? '·优先' : name.endsWith('·备用') ? '·备用' : ''; }
function regionByAnyGroup(name) { const sfx = tierSuffix(name); return regionByGroup(sfx ? name.slice(0, -sfx.length) + '·自动' : name); }
// 本软件生成的地区相关策略组：🌍 地区选择、各地区的 ·自动 / ·优先 / ·备用
function isRegionGroup(name) { return name === '🌍 地区选择' || !!regionByAnyGroup(name); }
// 把内核里的策略组名换成界面显示用的名字（地区组用显示名，其余原样）
function displayGroup(name) {
  const r = regionByAnyGroup(name);
  if (!r) return name;
  const sfx = tierSuffix(name);
  return sfx ? r.displayGroupName.slice(0, -'·自动'.length) + sfx : r.displayGroupName;
}
function resolveLeaf(start) {
  let cur = start;
  for (let i = 0; i < 8; i++) {
    const p = S.px[cur];
    if (!p || !p.now) return cur;
    cur = p.now;
  }
  return cur;
}
function parseProxies(raw) {
  const out = {};
  for (const [name, o] of Object.entries(raw.proxies || {})) {
    const hist = o.history || [];
    out[name] = { name, type: o.type || '', now: o.now || '', all: o.all || [], delay: hist.length ? hist[hist.length - 1].delay : 0 };
  }
  return out;
}
const connected = () => S.st && S.st.status === 'connected';

async function loadProxies() {
  if (!connected()) { S.px = {}; return; }
  try { S.px = parseProxies(await core('/proxies')); } catch (e) { /* 刚断开时可能失败 */ }
}

let lastRinfo = 0;
async function loadRulesInfo(force) {
  if (!force && Date.now() - lastRinfo < 8000) return;
  lastRinfo = Date.now();
  try { S.rinfo = await api('rules-info'); } catch (e) { /* 忽略 */ }
}

async function loadProcs() {
  try { S.procs = await api('processes'); } catch (e) { S.procs = []; }
}

// 实时流量（WebSocket），只改两个数字，不重画页面
let ws = null;
function syncTraffic() {
  if (connected() && !ws) {
    ws = new WebSocket(`ws://${location.host}/core/traffic?ectoken=${encodeURIComponent(TOKEN)}`);
    ws.onmessage = (e) => {
      try {
        const o = JSON.parse(e.data);
        S.traffic = [o.up || 0, o.down || 0];
        const u = document.getElementById('tUp'); if (u) u.textContent = humanBytes(S.traffic[0]) + '/s';
        const d = document.getElementById('tDown'); if (d) d.textContent = humanBytes(S.traffic[1]) + '/s';
      } catch (err) { /* 忽略 */ }
    };
    ws.onclose = () => { ws = null; };
  } else if (!connected() && ws) {
    ws.close(); ws = null; S.traffic = [0, 0];
  }
}

let refreshing = false;
async function refresh(force) {
  if (refreshing && !force) return;
  refreshing = true;
  try {
    S.st = await api('state');
    S.offline = false;
    if (connected() && (S.page === 'home' || S.page === 'nodes')) await loadProxies();
    if (!connected()) S.px = {};
    if (S.page === 'advanced') await loadRulesInfo(force);
    syncTraffic();
    if (S.st.pendingImport && !S.dialogOpen) {
      const u = S.st.pendingImport;
      api('pending-import/clear', {}).catch(() => {});
      goto('subs');
      addDialog(u);
    }
  } catch (e) {
    S.offline = true;
  } finally {
    refreshing = false;
  }
  updateRail();
  render(force);
}

// ======================= 导航 =======================
const TABS = [
  ['home', '首页'], ['subs', '订阅'], ['nodes', '节点'], ['apps', '程序'], ['advanced', '高级'],
];
function buildRail() {
  const tabs = $('#tabs');
  tabs.innerHTML = '';
  for (const [id, label] of TABS) {
    tabs.append(h('button', { 'data-tab': id, onclick: () => goto(id) }, h('div', { class: 'pill' }, icon(id)), label));
  }
}
function goto(page) {
  S.page = page;
  try { localStorage.setItem('page', page); } catch (e) { /* 忽略 */ }
  if (page === 'apps' && !S.procs) loadProcs().then(() => render(true));
  if (page === 'advanced') loadRulesInfo(true).then(() => render(true));
  $('#content').scrollTop = 0;
  updateRail();
  refresh(true);
}
const STATUS_LABEL = { idle: '未连接', connecting: '正在连接…', connected: '已连接', stopping: '正在断开…' };
function updateRail() {
  document.querySelectorAll('#tabs button').forEach((b) => b.classList.toggle('active', b.dataset.tab === S.page));
  const s = S.st ? S.st.status : 'idle';
  $('#statusDot').classList.toggle('on', s === 'connected');
  $('#statusLabel').textContent = S.offline ? '后台无响应' : STATUS_LABEL[s] || s;
}

// ======================= 渲染 =======================
function render(force) {
  const root = $('#content');
  const ae = document.activeElement;
  const typing = ae && root.contains(ae) && (ae.tagName === 'TEXTAREA' || (ae.tagName === 'INPUT' && ae.type !== 'checkbox' && ae.type !== 'radio'));
  if (typing && !force) { S.pendingRender = true; return; }
  const hash = JSON.stringify([S.page, S.st, S.px, S.rinfo, S.procs, S.expanded, S.testing, S.procQuery, S.showSys, S.offline]);
  if (!force && hash === S.lastHash) return;
  S.lastHash = hash;
  S.pendingRender = false;
  const scroll = root.scrollTop;
  const page = h('div', { class: 'page' });
  if (S.offline) page.append(card('后台无响应', null, helpText('界面暂时连不上 EasyClash 后台，正在重试…（如果一直这样，请从' + P('托盘', '菜单栏') + '退出后重新打开软件）', 'err')));
  if (!S.st) { root.replaceChildren(page); return; }
  const pages = { home: pageHome, subs: pageSubs, nodes: pageNodes, apps: pageApps, advanced: pageAdvanced };
  page.append(...[].concat((pages[S.page] || pageHome)()));
  root.replaceChildren(page);
  root.scrollTop = scroll;
}
document.addEventListener('focusout', () => setTimeout(() => { if (S.pendingRender) render(); }, 50));

// ======================= 首页 =======================
const MODES = [
  ['rule', '规则', '按规则分流：国内网站直连、国外网站走代理。日常推荐。规则和 GeoIP 数据库都已内置，无需另外设置。'],
  ['global', '全局', '所有流量都走代理，不分流。排查问题或临时需要时用。'],
  ['direct', '直连', '所有流量都不走代理，相当于关闭代理但软件保持运行。'],
];

function pageHome() {
  const st = S.st, set = st.settings;
  const sel = st.subs.find((s) => s.id === set.selectedSubId);
  const out = [];

  // ---- 连接 ----
  const statusText = STATUS_LABEL[st.status] || st.status;
  let action;
  if (st.status === 'idle') action = btn('连接', () => act('connect', {}), '', !sel);
  else if (st.status === 'connected') action = btn('断开', () => act('disconnect', {}), 'danger');
  else action = h('div', { class: 'spinner' });
  const conn = card('连接',
    '点「连接」后，本软件在后台启动代理内核，并把 ' + P('Windows', 'Mac') + ' 的系统代理指向它：浏览器和大多数软件会自动经过本软件，再按下面的规则决定走代理还是直连。\n\n' +
    (st.tunSupported ? '想让所有程序（包括不认系统代理的游戏、命令行工具）都受控，可以到「高级」页开启 TUN 模式。\n\n' : '') +
    '断开或退出时会自动恢复原来的系统代理设置。' +
    P('关闭窗口不会断开，软件继续在屏幕右下角的托盘里运行。', '关掉浏览器标签页不会断开，EasyClash 继续在屏幕顶部的菜单栏里运行。'),
    h('div', { class: 'row mt8' },
      h('div', { class: 'grow' }, h('div', { class: 'big' }, statusText), helpText(sel ? '当前订阅：' + sel.name : '还没有订阅，请到「订阅」页添加')),
      action));
  if (st.error) {
    conn.append(h('div', { class: 'err mt8' }, '连接失败：' + st.error), btn('知道了', () => act('clear-error', {}), 'text'));
  }
  if (connected()) {
    conn.append(h('div', { class: 'mt8' }),
      kv('上行', humanBytes(S.traffic[0]) + '/s', 'tUp'),
      kv('下行', humanBytes(S.traffic[1]) + '/s', 'tDown'));
    if (S.px[st.mainGroup]) conn.append(kv('当前出口', displayGroup(resolveLeaf(st.mainGroup))));
    if (st.nodeChecking) conn.append(helpText('正在检测节点能不能连上…', 'mt8'));
    if (st.nodeWarn) {
      conn.append(h('div', { class: 'warnbox mt8' }, h('div', { style: 'font-weight:600' }, '⚠ 节点连不上'), h('div', null, st.nodeWarn)),
        h('div', { class: 'row mt4' },
          btn(st.nodeChecking ? '检测中…' : '重新检测', () => act('nodes/check', {}), 'outlined', st.nodeChecking),
          btn('去换订阅', () => goto('subs'), 'text'),
          btn('断开', () => act('disconnect', {}), 'text danger')));
    }
    if (st.sysProxyOther) {
      conn.append(h('div', { class: 'err mt8' }, `系统代理被其他软件改掉了（现在指向 ${st.sysProxyOther}），浏览器可能没在走本软件。请在其他代理软件里关掉「系统代理」，再点下面的按钮。`),
        btn('重新指向本软件', () => act('sysproxy/reapply', {}), 'outlined'));
    }
    conn.append(kv('接管方式', st.tunOn ? 'TUN 虚拟网卡（所有程序）' : st.sysProxyOn ? `系统代理（127.0.0.1:${set.mixedPort}）` : `本机代理端口 127.0.0.1:${set.mixedPort}（未改系统代理）`));
  }
  out.push(conn);

  // ---- 出站模式 ----
  const cur = MODES.find((m) => m[0] === set.mode) || MODES[0];
  out.push(card('出站模式', MODES.map((m) => `「${m[1]}」：${m[2]}`).join('\n\n'),
    helpText('决定流量怎么走。日常用「规则」就好。', 'mb8'),
    h('div', { class: 'seg' }, MODES.map((m) => h('button', { class: set.mode === m[0] ? 'on' : '', onclick: () => act('mode', { mode: m[0] }) }, m[1]))),
    helpText(cur[2], 'mt8')));

  // ---- 代理出口地区 ----
  const reg = card('代理出口地区',
    '软件会按节点名字自动把订阅里的节点按国家/地区分组，并在每个地区里自动挑延迟最低的那个节点。\n\n' +
    '选中一个地区后，所有走代理的流量都从该地区出去——包括订阅里单独分流的 Claude、ChatGPT、Gemini、Netflix 等；本来就直连的（国内网站等）仍然直连。哪怕订阅更新或重新连接也不变。\n\n' +
    '锁定期间如果在「节点」页给某个服务单独指定了别的节点（比如 Netflix 用日本），会保留你的这个选择。\n\n' +
    '注意：地区按机场给节点起的名字判断，并不能保证节点的真实出口 IP 就在那个国家。\n\n' +
    '选「不锁定」则恢复锁定前各策略组的选择。',
    helpText('把所有走代理的流量固定在某个国家/地区，自动用该地区最快的节点。', 'mb8'));
  if (!connected()) {
    reg.append(helpText('连接后会在这里显示订阅里有哪些地区可选。'));
    if (set.lockedRegion) {
      const r = (st.allRegions || []).find((x) => x.code === set.lockedRegion);
      reg.append(helpText('当前已锁定：' + (r ? r.flag + ' ' + r.displayName : set.lockedRegion) + '（连接后生效）'));
    }
  } else if (!st.regions.length) {
    reg.append(helpText('没能从节点名字里识别出地区。可以到「节点」页手动选择。'));
  } else {
    const chips = [chip('不锁定', !set.lockedRegion, () => act('region/lock', { code: '' }))];
    for (const r of st.regions) {
      // 分级地区的自动组下面还有一层，沿着一路找到真实节点取延迟
      const leaf = S.px[resolveLeaf(r.groupName)];
      const delay = leaf ? leaf.delay : 0;
      chips.push(chip(`${r.flag} ${r.displayName}` + (delay > 0 ? ` ${delay}ms` : ''), set.lockedRegion === r.code, () => act('region/lock', { code: r.code })));
    }
    reg.append(h('div', { class: 'chips' }, chips));
    const locked = st.regions.find((r) => r.code === set.lockedRegion);
    const busy = S.testing || st.busy;
    reg.append(h('div', { class: 'row mt12' },
      locked ? btn(busy ? '测速中…' : `测速 ${locked.flag} ${locked.displayName}`, () => testRegion(locked.code), '', busy) : null,
      btn(!locked && busy ? '测速中…' : '测速全部地区', () => testAllRegions(), 'outlined', busy)));
    reg.append(helpText(locked
      ? `「测速 ${locked.displayName}」：只重测该地区的节点，并立刻换到其中最快的（当前节点卡了、不想等自动测速时点它）。「测速全部地区」会把所有地区都测一遍，用时较长。`
      : '选中一个地区后，这里会多出一个只测该地区的按钮。「测速全部地区」会把所有地区都测一遍，各地区各自切到最快节点。', 'mt4'));
    if (locked) {
      reg.append(h('div', { class: 'mt8' }), kv('锁定地区', `${locked.flag} ${locked.displayName}`));
      if (locked.tiered) {
        const tier = S.px[locked.groupName] && S.px[locked.groupName].now;
        reg.append(kv('当前层级', tier && tier.endsWith('·备用') ? '备用（优先节点明显更慢时才用）' : '优先'));
      }
      reg.append(kv('实际节点', resolveLeaf(locked.groupName)),
        helpText(locked.tiered
          ? `每 ${set.testIntervalMin || 2} 分钟把优先、备用两级都测一次：优先里最快的比备用慢超过 ${set.backupPenaltyMs} ms 才用备用（高级页可改）。手动测速立即生效。`
          : `每 ${set.testIntervalMin || 2} 分钟自动测速一次并切到最快节点（高级页可改）；手动测速立即生效。`));
    }
  }
  out.push(reg);

  // ---- 小贴士 ----
  out.push(card('使用小贴士', null,
    helpText('• 第一次使用：订阅页添加机场链接 → 回到首页点连接。'),
    helpText('• 某个程序不想走代理（比如网银、国内视频、游戏）：到「程序」页排除它。'),
    helpText('• 网页打不开但全局模式能打开：说明被分到了直连，可到「高级」页加一条规则。'),
    st.tunSupported ? helpText('• 有的程序不认系统代理（部分游戏、命令行工具）：到「高级」页开启 TUN 模式。') : null,
    helpText(P('• 关闭窗口不会退出，软件在右下角托盘继续运行；右键托盘图标可以断开或退出。',
      '• 关掉这个浏览器标签页不影响代理；EasyClash 常驻在屏幕顶部菜单栏（双环图标），点它可以连接 / 断开 / 重新打开控制面板 / 退出。')),
    helpText('• 看不懂的选项，点旁边的 ⓘ 都有解释。')));
  return out;
}

async function testRegion(code) {
  S.testing = true; render(true);
  try { const j = await api('region/test', { code }); toast(j.msg); } catch (e) { toast(e.message); }
  S.testing = false;
  await refresh(true);
}
async function testAllRegions() {
  S.testing = true; render(true);
  try { await api('groups/test', { names: S.st.regions.map((r) => r.groupName) }); } catch (e) { toast(e.message); }
  S.testing = false;
  await refresh(true);
}

// ======================= 订阅 =======================
function pageSubs() {
  const st = S.st;
  const fileInput = h('input', {
    type: 'file', accept: '.yaml,.yml,.txt', style: 'display:none',
    onchange: (e) => {
      const f = e.target.files[0];
      if (!f) return;
      const rd = new FileReader();
      rd.onload = () => act('subs/import', { name: f.name, text: rd.result });
      rd.readAsText(f);
    },
  });
  const out = [card('添加订阅',
    '订阅就是机场（代理服务商）给你的一个链接，里面包含所有节点和分流规则。\n\n' +
    '在机场网站的用户中心找到「Clash」或「Mihomo」订阅，复制链接后粘贴进来。\n\n' +
    '如果机场页面有「一键导入 Clash」按钮，点它也能直接跳到本软件。\n\n' +
    '也可以导入从别的电脑或软件导出的 .yaml 配置文件。',
    helpText('粘贴机场给的 Clash 订阅链接，或者导入 .yaml 文件。', 'mb8'),
    h('div', { class: 'row wrap' },
      btn('添加链接', () => addDialog('')),
      btn('从剪贴板', async () => {
        let t = '';
        try { t = (await navigator.clipboard.readText()).trim(); } catch (e) { toast('读取剪贴板失败，请手动粘贴'); }
        addDialog(t);
      }, 'outlined'),
      btn('导入文件', () => fileInput.click(), 'outlined'),
      fileInput),
    st.busy ? h('div', { class: 'progress indet' }, h('div')) : null)];

  if (!st.subs.length) out.push(card('还没有订阅', null, helpText('点上面的「添加链接」开始。添加后在这里点选要用的订阅，再回首页连接。')));
  for (const sub of st.subs) out.push(subCard(sub, sub.id === st.settings.selectedSubId, st.busy));
  return out;
}

function subCard(sub, selected, busy) {
  const c = h('section', { class: 'card' + (selected ? ' selected' : ''), style: 'cursor:pointer', onclick: () => { if (!selected) act('subs/select', { id: sub.id }); } },
    h('div', { class: 'row' },
      h('input', { type: 'radio', checked: selected, style: 'accent-color:var(--primary);width:17px;height:17px' }),
      h('div', { class: 'grow' }, h('div', { style: 'font-size:16px;font-weight:600' }, sub.name),
        helpText(selected ? `正在使用 · ${sub.nodeCount} 个节点` : `${sub.nodeCount} 个节点 · 点击切换为此订阅`))));
  if (sub.total > 0) {
    const used = Math.max(0, sub.upload) + Math.max(0, sub.download);
    c.append(h('div', { class: 'progress' }, h('div', { style: `width:${Math.min(100, used / sub.total * 100).toFixed(1)}%` })),
      kv('流量', `已用 ${humanBytes(used)} / 共 ${humanBytes(sub.total)}`));
  }
  if (sub.expire > 0) c.append(kv('到期', fmtTime(sub.expire * 1000)));
  c.append(kv('上次更新', sub.lastUpdate > 0 ? fmtTime(sub.lastUpdate) : '从未'));
  const stop = (f) => (e) => { e.stopPropagation(); f(); };
  c.append(h('div', { class: 'row mt8', style: 'gap:4px' },
    btn('更新', stop(() => act('subs/refresh', { id: sub.id })), 'text', busy || !sub.url),
    btn('重命名', stop(() => renameDialog(sub)), 'text'),
    btn('删除', stop(() => confirmDialog('删除订阅', `确定删除「${sub.name}」？已下载的节点信息会一起删除。`, '删除', () => act('subs/delete', { id: sub.id }))), 'text danger')),
  helpText('更新：重新从机场下载最新的节点列表。节点连不上、机场通知换节点时点一下。'));
  return c;
}

function addDialog(prefill) {
  const url = h('textarea', { rows: 3, placeholder: 'https://…', style: 'min-height:70px' });
  url.value = prefill || '';
  const name = h('input', { type: 'text' });
  const err = h('div', { class: 'err help' });
  const bar = h('div', { class: 'progress indet', style: 'visibility:hidden' }, h('div'));
  let adding = false;
  const dlg = openDialog('添加订阅', h('div', { class: 'body' },
    h('label', { class: 'field' }, '订阅链接'), url,
    h('label', { class: 'field' }, '名称（可不填）'), name,
    bar, err,
    helpText('添加后会立刻下载一次订阅内容。如果提示不是 Clash 格式，请回机场网站换成 Clash/Mihomo 专用链接。')), [
    { text: '取消', onClick: (c) => c() },
    h('button', {
      class: 'btn', onclick: async () => {
        if (adding || !url.value.trim()) return;
        adding = true; bar.style.visibility = 'visible'; err.textContent = '';
        try {
          const j = await api('subs/add', { url: url.value.trim(), name: name.value.trim() });
          dlg.close(); toast(j.msg); refresh(true);
        } catch (e) { err.textContent = e.message; }
        adding = false; bar.style.visibility = 'hidden';
      },
    }, '添加'),
  ]);
  setTimeout(() => url.focus(), 50);
}

function renameDialog(sub) {
  const n = h('input', { type: 'text', value: sub.name });
  openDialog('重命名', h('div', { class: 'body' }, n), [
    { text: '取消', onClick: (c) => c() },
    { text: '保存', onClick: (c) => { c(); act('subs/rename', { id: sub.id, name: n.value.trim() || sub.name }); } },
  ]);
  setTimeout(() => { n.focus(); n.select(); }, 50);
}

// ======================= 节点 =======================
const TYPE_LABEL = { selector: '手动选择', urltest: '自动测速', fallback: '故障转移', loadbalance: '负载均衡', relay: '链式代理' };
const typeLabel = (t) => TYPE_LABEL[(t || '').toLowerCase()] || t;
const isGroup = (p) => p && p.all && p.all.length > 0;
const isSelectable = (p) => p && (p.type || '').toLowerCase() === 'selector';

function delayEl(p) {
  if (!p) return null;
  const t = (p.type || '').toLowerCase();
  if (t === 'direct' || t === 'reject' || t === 'rejectdrop' || t === 'pass') return null;
  if (p.delay <= 0) return h('span', { class: 'delay n' }, '未测');
  return h('span', { class: 'delay ' + (p.delay < 200 ? 'g' : p.delay < 500 ? 'y' : 'r') }, p.delay + 'ms');
}

function pageNodes() {
  const nodesHelp = '这里按层级列出订阅里的策略组和节点。策略组是一组节点的集合，比如「自动选择」会自动挑最快的，「节点选择」由你手动选。';
  if (!connected()) return [card('节点', nodesHelp, helpText('连接后才能查看和切换节点。先回首页点「连接」。'))];
  const main = S.st.mainGroup;
  const groups = Object.values(S.px).filter((p) => isGroup(p) && p.name !== 'GLOBAL');
  const referenced = new Set(groups.flatMap((g) => g.all));
  const roots = [];
  if (S.px[main]) roots.push(S.px[main]);
  for (const g of groups) if (g.name !== main && !referenced.has(g.name)) roots.push(g);

  const out = [
    h('div', { class: 'row', style: 'padding:4px 6px' },
      h('div', { class: 'grow' }, h('div', { style: 'font-size:16px;font-weight:600' }, '策略组'),
        helpText('★ 是主组，决定代理流量最终从哪出去。缩进的是它包含的子组，点 ▸ 展开。淡色块里是按地区出口的策略组，和订阅自己的节点分开列。')),
      infoBtn('节点页说明',
        '• ★ 主组：订阅的总开关，选中哪一项，代理流量就走哪一项。\n' +
        '• 主组里可以选单个节点，也可以选一个子组（如「自动选择」「🌍 地区选择」），再由子组决定具体节点。\n' +
        '• 「🌍 地区选择」是本软件自动生成的，里面每个地区都会自动选该地区最快的节点。每个能选代理的组末尾都多了这一项，首页的「代理出口地区」就是一次性把它们都切过去的快捷入口。\n' +
        '• 「·自动」下面若还有「·优先」「·备用」两级：备用是名字含关键字（默认「中转」）的节点，只在优先节点明显更慢时才用（高级页可改）。这里手动点选哪一级，下个测速周期会被自动结果覆盖。\n' +
        '• 带「自动」「故障转移」字样的组由内核自动选择，不能手动指定具体节点。\n' +
        '• 延迟（ms）越小越快；「超时」表示这个节点当前不可用。\n' +
        '• 锁定地区时，所有当前走代理的组都会切到「🌍 地区选择」；选的是另一个组（如「🚀 节点选择」）或直连的不动，前者会跟着那个组走。\n' +
        '• 锁定期间单独给某个组选了别的节点，会保留这个选择；在 ★ 主组里手动选了别的，地区锁定会解除，其他组恢复锁定前的选择。')),
    card('和首页「代理出口地区」的关系', null,
      helpText('首页选地区 = 把「🌍 地区选择」选成对应国家的「·自动」组，再把所有当前走代理的组（★ 主组、「🚀 节点选择」、单独指定了节点的服务组等）都切到「🌍 地区选择」。两边改的是同一个东西，不会冲突。'),
      helpText('• 想固定出口国家、让软件自动挑该国最快节点：用首页的地区按钮，这里不用动。', 'mt4'),
      helpText('• 想让某个服务单独走别的节点（比如 Netflix 用日本）：在这里把它的组选成那个节点，锁定地区时会保留你的这个选择。'),
      helpText('• 在 ★ 主组里手动选了别的，首页的地区锁定会解除，其他组恢复锁定前的选择。'),
      helpText('• 首页显示「不锁定」时，出口就是这里各组当前选中的那一项。')),
  ];
  for (const g of roots) {
    out.push(h('section', { class: 'card' + (g.name === main ? ' main' : '') }, groupNode(g, 0, g.name === main, null, g.name)));
  }
  return out;
}

function groupNode(g, depth, isMain, parent, path) {
  if (S.expanded[path] === undefined) S.expanded[path] = isMain;
  const expanded = S.expanded[path];
  const indent = depth * 20;
  const toggle = () => { S.expanded[path] = !S.expanded[path]; render(true); };
  const busy = S.testing || S.st.busy;
  const nowLabel = g.now ? displayGroup(g.now) : '—';
  const nodes = [h('div', { class: 'gtitle' + (depth === 0 ? ' top' : ''), style: `padding-left:${indent}px` },
    parent ? h('input', {
      type: 'radio', checked: parent.now === g.name, disabled: !isSelectable(parent),
      style: 'accent-color:var(--primary);width:16px;height:16px',
      onclick: () => selectProxy(parent.name, g.name),
    }) : null,
    h('div', { class: 'grow', onclick: toggle },
      h('div', { class: 'name' }, (isMain ? '★ ' : '') + displayGroup(g.name)),
      helpText(`${typeLabel(g.type)} · 当前：${nowLabel}` + (g.now && isGroup(S.px[g.now]) ? ` → ${resolveLeaf(g.name)}` : ''))),
    depth < 2 || isSelectable(g) ? btn('测速', () => testGroup(g.name), 'text', busy) : null,
    h('button', { class: 'icon', title: expanded ? '收起' : '展开', onclick: toggle }, icon(expanded ? 'less' : 'more')))];
  if (!expanded) return nodes;
  if (!isSelectable(g)) nodes.push(h('div', { class: 'help', style: `padding-left:${indent + 12}px` }, '此组由内核自动选择，不能手动指定。'));
  if (depth >= 4) { nodes.push(h('div', { class: 'help', style: `padding-left:${indent + 12}px` }, '层级太深，不再展开')); return nodes; }
  // 地区出口块：本软件生成的地区策略组紧跟在组标题下面、缩进并换底色，和订阅自己的节点/子组分开
  const regionKids = isRegionGroup(g.name) ? [] : g.all.filter((n) => isRegionGroup(n) && isGroup(S.px[n]));
  if (regionKids.length) {
    const block = h('div', { class: 'region-block', style: `margin-left:${indent + 12}px` },
      h('div', { class: 'help', style: 'padding:0 8px 2px' }, '按地区出口（本软件生成，首页「代理出口地区」改的就是这里）'));
    for (const name of regionKids) block.append(...groupNode(S.px[name], depth + 1, false, g, path + '/' + name));
    nodes.push(block);
  }
  for (const name of g.all) {
    if (regionKids.includes(name)) continue;
    const p = S.px[name];
    if (isGroup(p)) {
      nodes.push(...groupNode(p, depth + 1, false, g, path + '/' + name));
    } else {
      const can = isSelectable(g);
      nodes.push(h('div', { class: 'node' + (can ? '' : ' disabled'), style: `padding-left:${indent + 20}px`, onclick: () => { if (can) selectProxy(g.name, name); } },
        h('input', { type: 'radio', checked: g.now === name, disabled: !can, style: 'accent-color:var(--primary);width:16px;height:16px;flex:none' }),
        h('span', { class: 'name' }, displayGroup(name)),
        delayEl(p)));
    }
  }
  return nodes;
}

async function selectProxy(group, name) {
  try { await api('select', { group, name }); } catch (e) { toast(e.message); }
  await refresh(true);
}
async function testGroup(name) {
  S.testing = true; render(true);
  try { await api('group/test', { name }); } catch (e) { toast(e.message); }
  S.testing = false;
  await refresh(true);
}

// ======================= 程序（分程序代理） =======================
const APP_MODES = [
  ['all', '全部程序', '所有程序的流量都按规则分流。'],
  ['allow', '仅选中的程序', '只有你勾选的程序走代理，其他程序一律直连，不受影响。'],
  ['deny', '排除选中的程序', '勾选的程序不走代理，其余程序照常按规则分流。适合排除网银、国内视频、游戏等。'],
];

function pageApps() {
  const set = S.st.settings;
  const out = [card('分程序代理',
    '控制哪些程序的流量经过代理，按程序文件名（如 ' + P('chrome.exe', 'WeChat') + '）区分。\n\n' +
    APP_MODES.map((m) => `「${m[1]}」：${m[2]}`).join('\n\n') +
    '\n\n改动立即生效，不用重新连接。\n\n' +
    (MAC() ? 'Mac 上很多程序由"辅助进程"负责联网：Chrome 要选「Google Chrome Helper」，Safari 要选「com.apple.WebKit.Networking」（勾选下面的「显示系统进程」才看得到）。\n\n' : '') +
    '注意：系统代理模式下，只有遵守系统代理设置的程序（浏览器和大多数软件）会经过本软件，不认系统代理的程序本来就直连' +
    (S.st.tunSupported ? '；想让所有程序都受控，请在「高级」页开启 TUN 模式。' : '。'),
    helpText('选择哪些程序走代理。改动立即生效。', 'mb8'),
    APP_MODES.map((m) => h('label', { class: 'radio' },
      h('input', { type: 'radio', name: 'appmode', checked: set.appProxyMode === m[0], onchange: () => setSetting({ appProxyMode: m[0] }) }),
      h('div', null, h('div', null, m[1]), helpText(m[2])))))];
  if (set.appProxyMode === 'all') return out;

  const chosen = new Set((set.appList || []).map((x) => x.toLowerCase()));
  const toggle = (name, on) => {
    const list = (set.appList || []).filter((x) => x.toLowerCase() !== name.toLowerCase());
    if (on) list.push(name);
    setSetting({ appList: list });
  };
  const manual = h('input', { type: 'text', placeholder: '手动添加程序名，例如 ' + P('WeChat.exe', 'WeChat') });
  const addManual = () => {
    let v = manual.value.trim();
    if (!v) return;
    if (!MAC() && !/\.exe$/i.test(v)) v += '.exe';
    manual.value = '';
    toggle(v, true);
  };
  manual.addEventListener('keydown', (e) => { if (e.key === 'Enter') addManual(); });
  const search = h('input', { type: 'text', placeholder: '搜索程序', value: S.procQuery, oninput: (e) => { S.procQuery = e.target.value; renderList(); } });

  const listBox = h('div', { class: 'list' });
  const renderList = () => {
    const procs = S.procs || [];
    const map = new Map();
    for (const name of set.appList || []) map.set(name.toLowerCase(), { name, path: '', system: false, saved: true });
    for (const p of procs) {
      const k = p.name.toLowerCase();
      if (map.has(k)) { const o = map.get(k); o.path = p.path; o.system = p.system; o.running = true; } else map.set(k, { ...p, running: true });
    }
    const q = S.procQuery.trim().toLowerCase();
    const items = [...map.values()]
      .filter((p) => (S.showSys || !p.system || chosen.has(p.name.toLowerCase())) && (!q || p.name.toLowerCase().includes(q) || (p.path || '').toLowerCase().includes(q)))
      .sort((a, b) => (chosen.has(b.name.toLowerCase()) - chosen.has(a.name.toLowerCase())) || a.name.localeCompare(b.name));
    listBox.replaceChildren(...(S.procs === null ? [helpText('正在读取程序列表…')] : items.length ? items.map((p) => {
      const on = chosen.has(p.name.toLowerCase());
      return h('label', { class: 'item' },
        h('input', { type: 'checkbox', checked: on, style: 'accent-color:var(--primary);width:17px;height:17px;flex:none', onchange: (e) => toggle(p.name, e.target.checked) }),
        h('div', { class: 'grow' }, h('div', null, p.name), h('div', { class: 'sub' }, p.path || (p.running ? '（系统进程，路径不可读）' : '当前没有运行'))));
    }) : [helpText('没有匹配的程序。')]));
  };
  renderList();

  out.push(card('程序列表', null,
    helpText(set.appProxyMode === 'allow' ? `勾选的程序走代理，其他不走。已选 ${chosen.size} 个。` : `勾选的程序不走代理，其他都走。已选 ${chosen.size} 个。`, 'mb8'),
    h('div', { class: 'row' }, h('div', { class: 'grow' }, search), btn('刷新列表', () => { S.procs = null; renderList(); loadProcs().then(() => render(true)); }, 'outlined')),
    h('div', { class: 'row mt8' },
      h('label', { class: 'check grow' }, h('input', { type: 'checkbox', checked: S.showSys, onchange: (e) => { S.showSys = e.target.checked; render(true); } }), '显示系统进程'),
    ),
    h('div', { class: 'row mt4' }, h('div', { class: 'grow' }, manual), btn('添加', addManual, 'outlined')),
    helpText('列表里是当前正在运行的程序。想排除的程序没在运行时，可以在上面手动输入它的文件名。', 'mt4'),
    listBox));
  return out;
}

// ======================= 高级 =======================
const RULES_HELP =
  '规则从哪来（从上往下逐条匹配，先命中的生效）：\n' +
  '① 本软件加在最前面的：分程序代理、拦截 QUIC、微软服务走国内，以及你在「附加规则」里填的；\n' +
  '② 订阅自带的规则：机场已经写好哪些网站直连、哪些走代理，原样使用；\n' +
  '③ 订阅引用的规则集（如 Netflix、ChatGPT、广告拦截、国内网站列表）：内核自动下载，并按订阅设定定期更新（通常每天一次）。这些规则集大多放在国外网站上，本软件固定让它们经代理下载；\n' +
  '④ 订阅没带规则时，本软件自动补一套基础分流：局域网、国内网站、国内 IP 直连，其余走代理。\n' +
  '没被任何规则命中的流量，交给订阅的兜底策略组（首页锁定了出口地区时，就走该地区）。\n\n' +
  '地理数据库（程序里已自带）：\n' +
  '· GeoIP：判断一个 IP 属于哪个国家，「国内 IP 直连」这类规则靠它；\n' +
  '· GeoSite：按类别整理好的域名表，比如「国内常用网站」；\n' +
  '· ASN：判断 IP 属于哪家运营商或机构，少数规则会用到。\n' +
  '其他 Clash 软件里的 GeoIP 功能就是这几个数据库，本软件同样具备。\n\n' +
  '更新数据库时只下载当前规则用得到的那几个，用不到的不更新，不浪费流量。';
const GEO_AUTO_HELP =
  '开启后，每次连接成功会检查数据库是否超过 7 天没更新；超过就在后台经代理下载最新版（GeoIP 约 8 MB），不影响正常上网。内容没变化时不会重复写入。下载失败会在 12 小时后再试。\n\n' +
  '关掉后数据库保持现状，仍可随时点「立即更新数据库」手动更新。';
const PRESET_RULES_WIN = `# Windows 更新、微软商店下载直连（国内有节点，直连更快）
DOMAIN-SUFFIX,windowsupdate.com,DIRECT
DOMAIN-SUFFIX,delivery.mp.microsoft.com,DIRECT
# 局域网直连
IP-CIDR,192.168.0.0/16,DIRECT,no-resolve
IP-CIDR,10.0.0.0/8,DIRECT,no-resolve`;
const PRESET_RULES_MAC = `# macOS 系统更新、App Store 下载直连（国内有节点，直连更快）
DOMAIN-SUFFIX,swcdn.apple.com,DIRECT
DOMAIN-SUFFIX,swdist.apple.com,DIRECT
DOMAIN-SUFFIX,updates.cdn-apple.com,DIRECT
DOMAIN-SUFFIX,osxapps.itunes.apple.com,DIRECT
# 局域网直连
IP-CIDR,192.168.0.0/16,DIRECT,no-resolve
IP-CIDR,10.0.0.0/8,DIRECT,no-resolve`;
const presetRules = () => P(PRESET_RULES_WIN, PRESET_RULES_MAC);
const TUN_STACKS = [
  ['gvisor', 'gvisor（兼容性最好，默认）'],
  ['mixed', 'mixed（TCP 用系统栈，速度更快）'],
  ['system', 'system（速度最快，部分电脑不稳定）'],
];
const drafts = {};
function draft(key, value) { if (!(key in drafts)) drafts[key] = value; return drafts[key]; }

function pageAdvanced() {
  const st = S.st, set = st.settings;
  const out = [rulesCard()];

  // ---- 接管方式 ----
  const how = !st.tunSupported ? card('接管方式',
    '「系统代理」：把这台 Mac 每个网络（Wi-Fi、以太网等）的网页代理、安全网页代理和 SOCKS 代理都设成 127.0.0.1:端口（就是「系统设置 → 网络 → 详细信息 → 代理」那一页）。浏览器和绝大多数软件会自动遵守。断开或退出时自动恢复原来的设置。\n\n' +
    '修改系统代理需要当前登录的是管理员账户（一般个人 Mac 都是）。\n\n' +
    '少数不认系统代理的命令行工具，可以在终端里先执行：\nexport https_proxy=http://127.0.0.1:' + set.mixedPort + ' http_proxy=http://127.0.0.1:' + set.mixedPort + '\n\n' +
    'Mac 版暂不提供 TUN 虚拟网卡模式。',
    helpText('Mac 版通过系统代理接管：浏览器和绝大多数软件都会走本软件。', 'mb8')) : card('接管方式',
    '决定哪些程序的流量会经过本软件。\n\n' +
    '「系统代理」（默认）：把 Windows 的系统代理设成 127.0.0.1:端口。浏览器和大多数软件会自动遵守，不需要管理员权限。断开或退出时自动恢复原来的设置。\n\n' +
    '「TUN 虚拟网卡」：创建一块虚拟网卡接管整台电脑的流量，所有程序（包括不认系统代理的游戏、命令行工具、微软商店应用）都会经过本软件。需要以管理员身份运行。\n\n' +
    '两种方式用的分流规则完全一样。',
    helpText('一般用系统代理就够了。有程序不认系统代理时再开 TUN。', 'mb8'),
    h('label', { class: 'radio' }, h('input', { type: 'radio', name: 'how', checked: !set.tunMode, onchange: () => setSetting({ tunMode: false }) }),
      h('div', null, h('div', null, '系统代理（推荐）'), helpText('浏览器和大多数软件走代理；不需要管理员。'))),
    h('label', { class: 'radio' }, h('input', { type: 'radio', name: 'how', checked: set.tunMode, onchange: () => setSetting({ tunMode: true }) }),
      h('div', null, h('div', null, 'TUN 虚拟网卡（需要管理员）'), helpText('接管所有程序，包括游戏、命令行工具。'))),
    helpText(st.isAdmin ? '当前已是管理员身份。' : '当前不是管理员身份。', 'mt4'));
  if (st.tunSupported && set.tunMode && !st.isAdmin) {
    how.append(helpText('TUN 模式要以管理员身份运行才能连接。点下面的按钮，Windows 会弹窗确认。', 'err mt4'),
      h('div', { class: 'mt8' }, btn('以管理员身份重新启动', () => act('admin-restart', {}))));
  }
  if (st.tunSupported && set.tunMode) {
    how.append(settingRow('TUN 网络栈', '决定如何处理虚拟网卡收到的数据包。', TUN_STACKS.map((x) => x[1]).join('\n'), null),
      h('div', { class: 'chips' }, TUN_STACKS.map(([v]) => chip(v, set.tunStack === v, () => setSetting({ tunStack: v })))));
  }
  out.push(how);

  // ---- 网络选项 ----
  const portInput = h('input', { type: 'number', min: 1024, max: 65535, value: draft('port', String(set.mixedPort)), oninput: (e) => { drafts.port = e.target.value; } });
  out.push(card('网络选项', '一般不需要改。遇到具体问题时按说明调整，改完会自动重载（已连接时）。',
    !st.tunSupported ? null : switchRow('拦截 QUIC（UDP 443）', '解决 YouTube 卡顿、部分网页一直转圈等问题（TUN 模式下起作用）。默认开。', set.blockQuic,
      '很多 Google 服务优先用 QUIC（走 UDP 443 端口）。不少代理线路对 UDP 支持不好，会导致一直转圈。开启后会拒绝 QUIC，程序自动改用普通 TCP，几乎没有副作用。\n\n系统代理模式下浏览器本来就不会经代理走 QUIC，这个开关不影响。',
      (v) => setSetting({ blockQuic: v })),
    switchRow('IPv6', '默认关。打开后代理也转发 IPv6 流量。', set.ipv6,
      '大多数机场节点不支持 IPv6。打开后若出现部分网站打不开、连接时断时续，请关掉。',
      (v) => setSetting({ ipv6: v })),
    switchRow('使用订阅自带的 DNS 设置', '默认关，用本软件内置的 DNS（国内 DoH）。', set.useSubscriptionDns,
      '机场订阅里通常也带一段 DNS 配置。如果你信任机场的配置或者遇到解析问题想对比一下，可以打开。若订阅里没有 DNS 配置，此开关无效。',
      (v) => setSetting({ useSubscriptionDns: v })),
    settingRow('日志级别', '排查问题时改成 debug，平时 info 即可。', null, null),
    h('div', { class: 'chips' }, ['silent', 'error', 'warning', 'info', 'debug'].map((l) => chip(l, set.logLevel === l, () => setSetting({ logLevel: l })))),
    h('label', { class: 'field mt12' }, '本机 HTTP/SOCKS 代理端口'),
    h('div', { class: 'row' }, h('div', { class: 'grow' }, portInput), btn('保存端口', () => {
      const p = parseInt(drafts.port || portInput.value, 10);
      if (!(p >= 1024 && p <= 65535)) { toast('端口要在 1024～65535 之间'); return; }
      delete drafts.port;
      setSetting({ mixedPort: p }, '端口已保存');
    }, 'outlined')),
    helpText('系统代理指向的就是这个端口；其他软件也可以手动填 127.0.0.1:端口 使用（HTTP 和 SOCKS5 都支持）。默认 17890，避开其他 Clash 常用的 7890。', 'mt4'),
    switchRow('微软服务走国内（含必应国内版）', 'Office、Outlook、OneDrive、Windows 更新、必应等直连；Copilot、GitHub 仍走代理。默认开。', set.bingDirect,
      '微软的服务在国内都能直连，而且直连通常更快更稳。开启后这些服务一律直连，不随代理出口切换地区：\n• 必应（访问到的就是国内版 cn.bing.com）\n• Office / Microsoft 365、Outlook / Hotmail、OneDrive、Teams、Skype\n• Windows 更新、微软商店、Xbox、微软账号登录、MSN 等\n\n例外（照常按订阅规则走代理）：\n• Copilot：国内不提供服务，直连会用不了\n• GitHub：虽属微软，但国内直连经常很慢、时断时续\n\n想用国际版必应，或想让微软服务也走代理，关掉它即可。',
      (v) => setSetting({ bingDirect: v }))));

  // ---- 自动选节点：优先 / 备用 ----
  const ivInput = h('input', { type: 'number', min: 1, max: 1440, value: draft('testIv', String(set.testIntervalMin || 2)), oninput: (e) => { drafts.testIv = e.target.value; } });
  const kwEffect = helpText('', 'mt4');
  const showKwEffect = (raw) => { kwEffect.textContent = backupKeywordEffect(raw); };
  const kwInput = h('input', { type: 'text', maxlength: 20, placeholder: '留空 = 不分级，所有节点只按延迟选', value: draft('backupKw', set.backupKeyword || ''), oninput: (e) => { drafts.backupKw = e.target.value; showKwEffect(e.target.value); } });
  showKwEffect(kwInput.value);
  const penInput = h('input', { type: 'number', min: 0, max: 5000, value: draft('backupPen', String(set.backupPenaltyMs ?? 100)), oninput: (e) => { drafts.backupPen = e.target.value; } });
  out.push(card('自动测速与选节点',
    '【自动测速间隔】锁定地区后，每隔这么多分钟把该地区的节点测一遍，自动换到最快的（分级时同时判断用优先还是备用）。一次测速每个节点只发一个很小的请求，21 个节点约 10 KB 流量，可以放心设短一些；建议 1～10 分钟。\n\n' +
    '【优先 / 备用】有些节点测延迟很快，真用起来却经常卡（典型的是经 CDN「中转」的节点：到 CDN 很近，但 CDN 再回机场源站那一段不稳）。纯按延迟挑，总是会选中它们。\n\n' +
    '开启分级后，每个地区的「·自动」组会拆成两级：\n' +
    '• 优先：名字不含关键字的节点，平时一律在这里面挑最快的\n' +
    '• 备用：名字含关键字的节点\n\n' +
    '每个测速周期把两级都测一遍：只有当「优先」里最快的节点比「备用」里最快的还慢超过设定毫秒数时，才改用备用；优先节点恢复后自动切回。优先级全部超时也会自动用备用。\n\n' +
    '关键字留空 = 不分级，纯按延迟选。如果你的机场「中转」节点质量很好，就把关键字清掉。',
    h('label', { class: 'field' }, '自动测速间隔（分钟）'),
    ivInput,
    helpText('默认 2。锁定地区时每隔这么久自动重测并切到最快节点。', 'mb8'),
    helpText('名字含关键字的节点降为备用，只在其他节点明显更慢时才用。', 'mb8'),
    h('label', { class: 'field mt12' }, '备用节点关键字'),
    kwInput,
    kwEffect,
    h('label', { class: 'field mt12' }, '优先比备用慢多少毫秒才用备用'),
    h('div', { class: 'row' }, h('div', { class: 'grow' }, penInput), btn('保存', () => {
      const p = parseInt(drafts.backupPen ?? penInput.value, 10);
      if (!(p >= 0 && p <= 5000)) { toast('毫秒数要在 0～5000 之间'); return; }
      const iv = parseInt(drafts.testIv ?? ivInput.value, 10);
      if (!(iv >= 1 && iv <= 1440)) { toast('测速间隔要在 1～1440 分钟之间'); return; }
      const kw = (drafts.backupKw ?? kwInput.value).trim();
      delete drafts.backupKw; delete drafts.backupPen; delete drafts.testIv;
      setSetting({ backupKeyword: kw, backupPenaltyMs: p, testIntervalMin: iv }, '已保存');
    }, 'outlined')),
    helpText('默认 100：优先节点 250ms、备用 100ms → 慢了 150ms，超过 100 → 用备用；优先 180ms、备用 100ms → 只慢 80ms → 仍用优先。', 'mt4')));

  // ---- 附加规则 ----
  const rules = h('textarea', { class: 'mono', placeholder: 'DOMAIN-SUFFIX,example.com,DIRECT', oninput: (e) => { drafts.rules = e.target.value; } });
  rules.value = draft('rules', set.extraRules);
  out.push(card('附加规则',
    '每行一条，插在订阅规则的最前面（优先级最高）。格式：类型,内容,去向\n\n' +
    '常用类型：\n' +
    'DOMAIN-SUFFIX,example.com,DIRECT    域名后缀匹配\n' +
    'DOMAIN-KEYWORD,google,PROXY        域名包含关键词\n' +
    'DOMAIN,www.a.com,REJECT            完整域名\n' +
    'IP-CIDR,8.8.8.0/24,DIRECT          IP 段\n' +
    'GEOIP,CN,DIRECT                    按 IP 所属国家\n' +
    'GEOSITE,cn,DIRECT                  按网站分类（内置数据库）\n' +
    'PROCESS-NAME,' + P('chrome.exe', 'WeChat') + ',DIRECT     按程序名\n\n' +
    '去向可以是 DIRECT（直连）、REJECT（拒绝）或你订阅里任何一个策略组的名字。\n\n' +
    '以 # 开头的行是注释，会被忽略。',
    helpText('想让某个网站强制直连/走代理，在这里加一行。', 'mb8'), rules,
    h('div', { class: 'row mt8' },
      btn('保存规则', () => { const v = drafts.rules ?? rules.value; delete drafts.rules; setSetting({ extraRules: v }, '规则已保存'); }),
      btn('填入常用示例', () => { rules.value = presetRules(); drafts.rules = presetRules(); }, 'outlined'))));

  // ---- 覆写 ----
  const ov = h('textarea', { class: 'mono', placeholder: '+rules:\n  - DOMAIN-SUFFIX,example.com,DIRECT', oninput: (e) => { drafts.ov = e.target.value; } });
  ov.value = draft('ov', set.overrideYaml);
  out.push(card('YAML 覆写（高级）',
    '直接修改最终配置，写法与 Clash Verge / Clash Party 一致：\n\n' +
    '+rules:            插到规则最前面\n' +
    'rules+:            追加到规则最后面\n' +
    '+proxy-groups:     在策略组前面插入\n' +
    'dns:               整段替换/合并 DNS 设置\n' +
    '其他任何顶层字段：直接覆盖订阅里的同名字段\n\n' +
    '注意：tun、external-controller、端口等字段由软件接管，写了也无效。写错会导致连接失败，报错信息会显示在首页。',
    helpText('不懂 YAML 就别碰。附加规则和地区分组已经能满足绝大多数需求。', 'mb8'), ov,
    h('div', { class: 'mt8' }, btn('保存覆写', () => { const v = drafts.ov ?? ov.value; delete drafts.ov; setSetting({ overrideYaml: v }, '覆写已保存'); }))));

  // ---- 启动与托盘 ----
  out.push(card(P('启动与托盘', '启动与菜单栏'),
    P('关闭窗口不会退出，软件在屏幕右下角的托盘里继续运行：单击托盘图标打开窗口，右键可以连接 / 断开 / 切换出站模式 / 退出。',
      'EasyClash 常驻在屏幕顶部的菜单栏（双环图标）：点图标可以连接 / 断开 / 切换出站模式 / 打开控制面板 / 退出。控制面板就是这个浏览器页面，关掉不影响代理。') +
    '\n\n退出软件时会自动断开并恢复原来的系统代理设置。',
    switchRow('开机自动启动', P('登录 Windows 后自动在托盘里启动（不弹窗口）。默认关。', '登录 Mac 后自动在菜单栏启动（不打开控制面板）。默认关。'), set.autoStart, null, (v) => setSetting({ autoStart: v })),
    switchRow('启动后自动连接', '软件一打开就自动连接正在用的订阅。默认关。', set.autoConnect, null, (v) => setSetting({ autoConnect: v })),
    h('div', { class: 'mt8' }, btn('设为 clash:// 一键导入的打开方式', () => act('claim-protocol', {}), 'outlined')),
    helpText('机场网页上的「一键导入 Clash」按钮会打开 clash:// 链接。点了没反应或打开的是别的软件时，点这个按钮让它交给本软件。', 'mt4')));

  // ---- 诊断 ----
  const diag = card('诊断', '日志：内核运行时的记录，连接出问题时看这里。\n连接：当前所有网络连接走了哪条规则、哪个节点，可以用来确认某个程序有没有走代理。',
    h('div', { class: 'row wrap' },
      btn('查看日志', logsDialog, 'outlined'),
      btn('查看连接', connsDialog, 'outlined', !connected()),
      btn('打开数据文件夹', () => act('open-data-dir', {}), 'outlined')),
    connected() ? null : helpText('「查看连接」连接后可用。', 'mt4'));
  if (st.crash) {
    diag.append(h('h3', { class: 'err' }, '上次异常退出记录'),
      h('div', { class: 'logbox mono' }, st.crash.split('\n').slice(0, 14).join('\n')),
      h('div', { class: 'row mt8' },
        btn('复制崩溃记录', () => navigator.clipboard.writeText(st.crash).then(() => toast('已复制')), 'outlined'),
        btn('清除', () => act('clear-crash', {}), 'text')),
      helpText('软件上次不是正常退出的，把这段复制发给 Claude 可以定位原因。'));
  }
  out.push(diag);

  // ---- 关于 ----
  out.push(card('关于', null,
    kv('软件', 'EasyClash ' + P('Windows', 'Mac') + ' 版 ' + st.version),
    kv('内核', 'mihomo ' + st.coreVersion),
    kv('数据位置', st.dataDir),
    helpText('本软件只是 mihomo 内核的一个简化界面，节点和线路都来自你自己的订阅。', 'mt4')));
  return out;
}

function rulesCard() {
  const st = S.st, set = st.settings, ri = S.rinfo;
  const c = card('分流规则与地理数据库', RULES_HELP,
    helpText('分流规则和 GeoIP 等地理数据库都已内置，无需另外下载或设置。点右上角 ⓘ 看详细说明。', 'mb8'),
    h('h3', null, '规则'));
  if (connected() && ri && ri.connected) {
    const list = ri.providers || [];
    c.append(kv('当前生效规则', ri.ruleCount + ' 条'));
    if (!list.length) c.append(kv('订阅引用的规则集', '无'));
    else {
      const loaded = list.filter((p) => !p.neverLoaded).length;
      c.append(kv('订阅引用的规则集', `${loaded} / ${list.length} 个已加载`), kv('规则集里共有', list.reduce((s, p) => s + p.ruleCount, 0) + ' 条'));
      if (loaded < list.length) c.append(helpText('有规则集还没下载成功，点「更新规则集」重试；仍然失败可以在「诊断 → 查看日志」里看原因。', 'mt4'));
    }
    c.append(h('div', { class: 'row mt8' },
      btn(st.busy ? '更新中…' : '更新规则集', () => act('rules/update', {}).then(() => loadRulesInfo(true)).then(() => render(true)), 'outlined', st.busy || !list.some((p) => (p.vehicle || '').toUpperCase() === 'HTTP')),
      list.length ? btn('查看明细', () => providersDialog(list), 'text') : null));
  } else {
    c.append(helpText('连接后这里会显示当前生效的规则数和规则集下载情况。'));
  }
  c.append(h('h3', { class: 'mt12' }, '地理数据库'));
  const inUse = ri && ri.geoInUse ? ri.geoInUse : null;
  for (const f of (ri && ri.geo) || []) {
    const tail = !connected() || !inUse ? '' : inUse.includes(f.key) ? ' · 使用中' : ' · 当前规则未用到';
    c.append(kv(f.label, (f.date || '已内置，首次连接时启用') + tail));
  }
  c.append(kv('上次检查更新', set.geoLastCheck > 0 ? fmtTime(set.geoLastCheck) : '还没有'),
    switchRow('自动更新地理数据库', '每 7 天检查一次，连接后在后台进行。默认开。', set.geoAutoUpdate, GEO_AUTO_HELP, (v) => setSetting({ geoAutoUpdate: v })),
    h('div', null,
      btn(st.busy ? '正在更新…（约 1 分钟）' : '立即更新数据库', () => act('geo/update', {}).then(() => loadRulesInfo(true)).then(() => render(true)), 'outlined', !connected() || st.busy),
      connected() ? null : helpText('连接后可用（更新时经代理下载）。', 'mt4')));
  return c;
}

function providersDialog(list) {
  openDialog(`规则集明细（${list.length} 个）`, h('div', { class: 'body' },
    helpText('规则集由订阅指定，内核自动下载，并按订阅设定的间隔更新。', 'mb8'),
    list.map((p) => h('div', { class: 'kv' }, h('span', null, p.name),
      h('span', { class: p.neverLoaded ? 'err' : 'help', style: 'margin:0' }, p.neverLoaded ? '未下载' : `${p.ruleCount} 条 · ${(p.updatedAt || '').slice(0, 10)}`)))),
  [{ text: '关闭', onClick: (c) => c() }]);
}

function logsDialog() {
  const box = h('div', { class: 'logbox mono' });
  let seq = 0, lines = [], timer = 0;
  const title = h('span', null, '内核日志');
  const load = async () => {
    try {
      const j = await api('logs?since=' + seq);
      if (j.seq < seq) lines = [];
      seq = j.seq;
      if (j.lines.length) {
        lines = lines.concat(j.lines).slice(-1500);
        const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 30;
        box.textContent = lines.join('\n') || '暂无日志';
        if (atBottom) box.scrollTop = box.scrollHeight;
      } else if (!lines.length) box.textContent = '暂无日志';
      title.textContent = `内核日志（${lines.length} 行）`;
    } catch (e) { /* 忽略 */ }
  };
  const dlg = openDialog('内核日志', h('div', { class: 'body' }, helpText('软件启动后一直在收集，同时保存在数据文件夹的 logs\\easyclash.log 里（重启后也能查）。排查问题时点「复制」，粘贴发给 Claude。', 'mb8'), box), [
    { text: '打开日志文件夹', onClick: () => act('open-log-dir', {}) },
    { text: '清空', onClick: () => { api('logs/clear', {}); lines = []; box.textContent = '暂无日志'; } },
    { text: '复制', onClick: () => navigator.clipboard.writeText(lines.join('\n')).then(() => toast('已复制')) },
    { text: '关闭', onClick: (c) => c() },
  ], true);
  dlg.querySelector('h2').replaceChildren(title);
  load().then(() => { box.scrollTop = box.scrollHeight; });
  timer = setInterval(load, 1000);
  dlg._onclose = () => clearInterval(timer);
}

function connsDialog() {
  const box = h('div', { class: 'body' });
  const title = h('span', null, '当前连接');
  let timer = 0;
  const load = async () => {
    try {
      const j = await core('/connections');
      const conns = (j.connections || []).sort((a, b) => (b.start || '').localeCompare(a.start || ''));
      title.textContent = `当前连接（${conns.length}）`;
      box.replaceChildren(...(conns.length ? conns.map((c) => {
        const m = c.metadata || {};
        const chain = (c.chains || []).slice().reverse().map(displayGroup).join(' → ');
        return h('div', { style: 'padding:6px 0;border-bottom:1px solid var(--surface-high)' },
          h('div', { style: 'font-weight:600;overflow-wrap:anywhere' }, m.host || m.destinationIP),
          helpText(`${(m.network || '').toUpperCase()} ${m.destinationIP || ''}:${m.destinationPort || ''}` + (m.process ? ` · ${m.process}` : '')),
          helpText('规则：' + [c.rule, c.rulePayload].filter(Boolean).join(' ')),
          helpText('链路：' + chain),
          helpText(`↑${humanBytes(c.upload)} ↓${humanBytes(c.download)}`),
          btn('断开此连接', () => core('/connections/' + encodeURIComponent(c.id), 'DELETE').then(load), 'text'));
      }) : [helpText('暂无连接')]));
    } catch (e) { box.replaceChildren(helpText(e.message)); }
  };
  const dlg = openDialog('当前连接', box, [
    { text: '全部断开', onClick: () => core('/connections', 'DELETE').then(load) },
    { text: '关闭', onClick: (c) => c() },
  ], true);
  dlg.querySelector('h2').replaceChildren(title);
  load();
  timer = setInterval(load, 2000);
  dlg._onclose = () => clearInterval(timer);
}

// ======================= 启动 =======================
buildRail();
updateRail();
refresh(true);
setInterval(() => refresh(false), 1500);
