/* ============================================================
   TempMail SPA — 主应用逻辑
   ============================================================ */

'use strict';

// ─── 配置 ───────────────────────────────────────────────────
const API_BASE = '/api';
const PUBLIC_BASE = '/public';

// ─── 状态 ───────────────────────────────────────────────────
const state = {
  apiKey:    localStorage.getItem('tm_apikey') || '',
  account:   JSON.parse(localStorage.getItem('tm_account') || 'null'),
  theme:     localStorage.getItem('tm_theme') || 'light',
  page:      'dashboard',
  /** 自助售号入口（来自 /public/claude-shop） */
  claudeShopEnabled: false,
  adminAccountsPage: 1,
  adminAccountsQ: '',
  adminAccountsStatus: 'all',    // v10：all|active|banned|svip
  // v10：优惠券
  myCouponStatus: 'available',   // available|used|expired|all
  adminCouponPage: 1,
  adminCouponQ: '',
  adminCouponStatus: '',         // ''=全部
  adminShopInventoryPage: 1,
  adminShopInventoryStatus: 'all',
  /** 库存列表批次筛选：''=全部 '__none__'=无批次 其它=批次号 */
  adminShopInventoryBatch: '',
  /** 库存列表商品筛选：''=全部 '__none__'=通用池 其它=商品 uuid */
  adminShopInventoryProduct: '',
  adminShopOrdersPage: 1,
  /** 管理端订单筛选：''=全部 awaiting_payment fulfilled */
  adminShopOrdersStatus: '',
  /** 管理端「在售 SKU」正在编辑的商品 id */
  adminShopProductEditId: null,
  claudeHighlightOrderId: null,
  /** 多 SKU 模式下当前选中的商品 id */
  claudeSelectedProductId: null,
  /** 无多 SKU 时，需用户点击默认商品卡片后才允许支付 */
  claudeShopDefaultAck: false,
  _claudeShopSummary: null,
  /** 支付宝当面付订单轮询定时器 */
  claudeAlipayPollTimer: null,
  /** 站点显示名（来自 /public/settings site_title，用于标题栏与登录页等） */
  siteTitle: 'TempMail',
  // 当前邮箱
  currentMailbox: null,
  currentEmail:   null,
  // 缓存
  mailboxes: [],
  emails:    [],
};

/** 导入库存时默认批次提示（月日，与留空时服务器默认规则接近） */
function shopDefaultBatchMMDD() {
  const d = new Date();
  return String(d.getMonth() + 1).padStart(2, '0') + String(d.getDate()).padStart(2, '0');
}

// ─── 工具函数 ───────────────────────────────────────────────
const $ = id => document.getElementById(id);
const el = (tag, cls, html) => {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html !== undefined) e.innerHTML = html;
  return e;
};

function toast(msg, type = 'info') {
  const icons = { success: '✓', error: '✗', warn: '⚠', info: 'ℹ' };
  const t = el('div', `toast ${type}`, `<span>${icons[type]||'ℹ'}</span><span>${escHtml(msg)}</span>`);
  const c = $('toast-container');
  c.appendChild(t);
  setTimeout(() => { t.style.opacity = '0'; t.style.transition = 'opacity 0.3s'; setTimeout(() => t.remove(), 300); }, 3500);
}

function escHtml(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

/**
 * 轻量 Markdown 渲染器（仅用于公告等可信来源文本）
 * 支持：粗体 **x** / 斜体 *x* / 链接 [text](url) / 行内代码 `x` / 自动换行
 * 先做 HTML 转义，再按 token 回填占位符，避免注入。
 */
function renderSimpleMarkdown(text) {
  if (!text) return '';
  let s = escHtml(text);
  // 行内代码 `xxx`（最先处理，避免里面 * 被当成强调）
  s = s.replace(/`([^`]+)`/g, '<code>$1</code>');
  // 链接 [text](http...)
  s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g,
    '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
  // 粗体 **x**
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  // 斜体 *x*（避免匹配已替换的 **）
  s = s.replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, '$1<em>$2</em>');
  // 换行
  s = s.replace(/\n/g, '<br />');
  return s;
}

/** 公告关闭记忆（按内容 hash 去重，用户可永久关闭已读公告；内容变了会重新弹） */
function announcementDismissKey(content, level, title) {
  try {
    const raw = String(level||'') + '|' + String(title||'') + '|' + String(content||'');
    let h = 0;
    for (let i = 0; i < raw.length; i++) {
      h = ((h << 5) - h) + raw.charCodeAt(i);
      h |= 0;
    }
    return 'tm_ann_dismissed_' + h.toString(36);
  } catch { return null; }
}
function isAnnouncementDismissed(content, level, title) {
  const k = announcementDismissKey(content, level, title);
  return k ? localStorage.getItem(k) === '1' : false;
}
window.dismissAnnouncement = function(content, level, title) {
  const k = announcementDismissKey(content, level, title);
  if (k) localStorage.setItem(k, '1');
  const card = document.getElementById('announcement-card');
  if (card) {
    card.style.transition = 'opacity .25s, transform .25s';
    card.style.opacity = '0';
    card.style.transform = 'translateY(-6px)';
    setTimeout(() => card.remove(), 260);
  }
};

/** 生成公告 HTML（Dashboard 使用）；content 为空返回空串 */
function buildAnnouncementHtml(content, level, title) {
  const c = (content || '').trim();
  if (!c) return '';
  if (isAnnouncementDismissed(c, level, title)) return '';
  const lvl = ['info', 'success', 'warn', 'danger'].includes(level) ? level : 'info';
  const iconMap = { info: '📣', success: '✓', warn: '⚠', danger: '!' };
  const icon = iconMap[lvl] || '📣';
  const titleHtml = title
    ? `<div class="announcement-title">${escHtml(title)}</div>`
    : '';
  const bodyHtml = renderSimpleMarkdown(c);
  const contentJs = JSON.stringify(c);
  const lvlJs = JSON.stringify(lvl);
  const titleJs = JSON.stringify(title || '');
  return `
    <div id="announcement-card" class="announcement-card level-${lvl}">
      <span class="announcement-icon">${escHtml(icon)}</span>
      ${titleHtml}
      <div class="announcement-body">${bodyHtml}</div>
      <button class="announcement-dismiss" onclick='dismissAnnouncement(${contentJs}, ${lvlJs}, ${titleJs})' title="关闭">×</button>
    </div>`;
}

/** HTML 邮件在 sandbox iframe 内展示；外链若在当前 frame 打开，整页会加载进 iframe，而 claude.ai 等站点禁止被嵌入（X-Frame-Options / CSP），浏览器即显示「拒绝连接」。统一改为新标签页打开。 */
function rewriteEmailIframeLinks(doc) {
  if (!doc || !doc.body) return;
  const nodes = doc.querySelectorAll('a[href], area[href]');
  nodes.forEach((el) => {
    const href = (el.getAttribute('href') || '').trim();
    if (!href || href === '#' || href.startsWith('#')) return;
    if (/^javascript:/i.test(href)) return;

    const t = (el.getAttribute('target') || '').toLowerCase();
    if (!t || t === '_self' || t === '_top' || t === '_parent') {
      el.setAttribute('target', '_blank');
    }
    const relSet = new Set((el.getAttribute('rel') || '').split(/\s+/).filter(Boolean));
    relSet.add('noopener');
    relSet.add('noreferrer');
    el.setAttribute('rel', [...relSet].join(' '));
  });
}

function formatDate(s) {
  if (!s) return '—';
  const d = new Date(s);
  return d.toLocaleString('zh-CN', { month:'2-digit', day:'2-digit', hour:'2-digit', minute:'2-digit'});
}

function timeAgo(s) {
  if (!s) return '—';
  const diff = Date.now() - new Date(s).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return '刚刚';
  if (mins < 60) return `${mins}分钟前`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}小时前`;
  return `${Math.floor(hrs/24)}天前`;
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast('已复制到剪贴板', 'success');
  } catch {
    toast('复制失败，请手动选择', 'warn');
  }
}

// ─── API 客户端 ─────────────────────────────────────────────
const INVENTORY_SPLIT_TOKENS = ['####', '----', '===='];
const INVENTORY_QR_EXTS = ['.png', '.jpg', '.jpeg', '.webp', '.gif', '.bmp', '.jfif'];
const INVENTORY_HEADER_EMAIL_TOKENS = ['邮箱账号', '邮箱', '邮箱地址', 'email', 'mail', 'mailbox', 'account'];
const INVENTORY_HEADER_KEY_TOKENS = ['邮箱apikey', 'apikey', 'api_key', 'api key', 'key', '登录key', '登录密钥', '密钥', 'token'];

function looksLikeInventoryEmail(value) {
  const s = String(value || '').trim();
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(s);
}

function classifyInventoryEmailKey(a, b) {
  const left = String(a || '').trim();
  const right = String(b || '').trim();
  const leftEmail = looksLikeInventoryEmail(left);
  const rightEmail = looksLikeInventoryEmail(right);
  if (leftEmail && !rightEmail) return { email: left, apiKey: right, ok: true };
  if (rightEmail && !leftEmail) return { email: right, apiKey: left, ok: true };
  if (left.includes('@') && !right.includes('@')) return { email: left, apiKey: right, ok: true };
  if (right.includes('@') && !left.includes('@')) return { email: right, apiKey: left, ok: true };
  return { email: '', apiKey: '', ok: false };
}

function parseSimpleCSVLine(line) {
  const out = [];
  let buf = '';
  let inQuotes = false;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (ch === '"') {
      if (inQuotes && line[i + 1] === '"') {
        buf += '"';
        i++;
      } else {
        inQuotes = !inQuotes;
      }
      continue;
    }
    if (ch === ',' && !inQuotes) {
      out.push(buf);
      buf = '';
      continue;
    }
    buf += ch;
  }
  out.push(buf);
  return out;
}

function normalizeInventoryHeaderToken(value) {
  return String(value || '')
    .trim()
    .replace(/^["']|["']$/g, '')
    .replace(/[\s_\-\u3000]+/g, '')
    .toLowerCase();
}

function firstTwoNonEmptyInventoryFields(fields) {
  const out = [];
  for (const field of fields || []) {
    if (!normalizeInventoryHeaderToken(field)) continue;
    out.push(String(field || '').trim().replace(/^["']|["']$/g, ''));
    if (out.length === 2) break;
  }
  return out;
}

function containsAnyInventoryToken(value, tokens) {
  const normalized = normalizeInventoryHeaderToken(value);
  return tokens.some(token => normalized === normalizeInventoryHeaderToken(token));
}

function isInventoryHeaderLine(line) {
  let cells = firstTwoNonEmptyInventoryFields(String(line || '').split('\t'));
  if (cells.length < 2) {
    cells = firstTwoNonEmptyInventoryFields(parseSimpleCSVLine(String(line || '')));
  }
  if (cells.length < 2) return false;
  const [a, b] = cells;
  return (
    containsAnyInventoryToken(a, INVENTORY_HEADER_EMAIL_TOKENS) &&
    containsAnyInventoryToken(b, INVENTORY_HEADER_KEY_TOKENS)
  ) || (
    containsAnyInventoryToken(b, INVENTORY_HEADER_EMAIL_TOKENS) &&
    containsAnyInventoryToken(a, INVENTORY_HEADER_KEY_TOKENS)
  );
}

function splitInventoryLineLocal(line) {
  for (const token of INVENTORY_SPLIT_TOKENS) {
    if (!line.includes(token)) continue;
    const idx = line.indexOf(token);
    return classifyInventoryEmailKey(line.slice(0, idx), line.slice(idx + token.length));
  }
  if (line.includes('\t')) {
    const rec = firstTwoNonEmptyInventoryFields(line.split('\t'));
    if (rec.length >= 2) return classifyInventoryEmailKey(rec[0], rec[1]);
  }
  const rec = firstTwoNonEmptyInventoryFields(parseSimpleCSVLine(line));
  if (rec.length < 2) return { email: '', apiKey: '', ok: false };
  return classifyInventoryEmailKey(rec[0], rec[1]);
}

function parseInventoryImportLocal(raw) {
  const pairs = [];
  const warnings = [];
  const seen = new Set();
  const lines = String(raw || '').replace(/\r\n/g, '\n').replace(/^\uFEFF/, '').split('\n');
  lines.forEach((rawLine, idx) => {
    const lineNo = idx + 1;
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) return;
    if (isInventoryHeaderLine(line)) return;
    const { email, apiKey, ok } = splitInventoryLineLocal(line);
    if (!ok) {
      warnings.push(`第 ${lineNo} 行：无法识别格式`);
      return;
    }
    if (!email || !apiKey) {
      warnings.push(`第 ${lineNo} 行：邮箱或 Key 为空`);
      return;
    }
    if (email.length > 320) {
      warnings.push(`第 ${lineNo} 行：邮箱长度超过 320`);
      return;
    }
    if (apiKey.length > 128) {
      warnings.push(`第 ${lineNo} 行：Key 长度超过 128`);
      return;
    }
    const dedupeKey = `${email.toLowerCase()}\u0000${apiKey}`;
    if (seen.has(dedupeKey)) {
      warnings.push(`第 ${lineNo} 行：重复数据已跳过`);
      return;
    }
    seen.add(dedupeKey);
    pairs.push({ email, apiKey });
  });
  return { pairs, warnings };
}

function updateShopImportStats(raw = '', sourceLabel = '') {
  const box = $('shop-import-stats');
  const parsed = parseInventoryImportLocal(raw);
  if (!box) return parsed;
  const parts = [];
  if (sourceLabel) parts.push(`来源：${escHtml(sourceLabel)}`);
  parts.push(`识别到 <strong>${parsed.pairs.length}</strong> 条`);
  parts.push(`跳过 <strong>${parsed.warnings.length}</strong> 条`);
  const warningsHtml = parsed.warnings.length
    ? `<div style="margin-top:0.35rem;color:var(--clr-warn)">${escHtml(parsed.warnings.slice(0, 3).join('；'))}${parsed.warnings.length > 3 ? ' ...' : ''}</div>`
    : '';
  box.innerHTML = `<div>${parts.join(' · ')}</div>${warningsHtml}`;
  return parsed;
}

function validateShopQRFile(file) {
  if (!file) return null;
  const name = String(file.name || '');
  const ext = name.includes('.') ? `.${name.split('.').pop().toLowerCase()}` : '';
  if (!INVENTORY_QR_EXTS.includes(ext)) {
    return '仅支持 png / jpg / jpeg / webp / gif / bmp / jfif';
  }
  if (file.size > (8 << 20)) {
    return '单张图片不能超过 8MB';
  }
  return null;
}

async function apiFetch(path, opts = {}) {
  const headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) };
  if (state.apiKey) headers['Authorization'] = `Bearer ${state.apiKey}`;
  const res = await fetch(path, { ...opts, headers });
  let data;
  try { data = await res.json(); } catch { data = {}; }
  if (!res.ok) {
    const errMsg = data.error || data.message || `HTTP ${res.status}`;
    const e = new Error(errMsg);
    e.status = res.status;
    if (data.code) e.code = data.code;
    throw e;
  }
  return data;
}

const api = {
  // 公共
  publicSettings: () => fetch(PUBLIC_BASE + '/settings').then(r => r.json()),
  publicClaudeShop: () => fetch(PUBLIC_BASE + '/claude-shop').then(r => r.json()),
  publicStats:     () => fetch(PUBLIC_BASE + '/stats').then(r => r.json()),
  register: body  => apiFetch(PUBLIC_BASE + '/register', { method: 'POST', body: JSON.stringify(body) }),

  // 账户
  me:              () => apiFetch(API_BASE + '/me'),
  rotateMyKey:     () => apiFetch(API_BASE + '/me/rotate-key', { method: 'POST', body: '{}' }),
  stats:           () => apiFetch(API_BASE + '/stats'),
  // 域名 → 解包 {domains:[...]} → 数组
  domains:         () => apiFetch(API_BASE + '/domains').then(d => Array.isArray(d) ? d : (d.domains || [])),
  // 任意已登录用户提交域名 MX 验证
  submitDomain:    body => apiFetch(API_BASE + '/domains/submit', { method: 'POST', body: JSON.stringify(body) }),
  // 轮询域名状态（任意已登录用户，不需要管理员权限）
  getDomainStatus: id => apiFetch(API_BASE + '/domains/' + id + '/status'),
  // 邮箱 → 解包 {data:[...]}
  createMailbox:   (body) => apiFetch(API_BASE + '/mailboxes', { method: 'POST', body: JSON.stringify(body || {}) }).then(d => d.mailbox || d),
  listMailboxes:   () => apiFetch(API_BASE + '/mailboxes').then(d => Array.isArray(d) ? d : (d.data || [])),
  deleteMailbox: id  => apiFetch(API_BASE + '/mailboxes/' + id, { method: 'DELETE' }),
  // 邮件 → 解包 {data:[...]}
  listEmails: mid    => apiFetch(API_BASE + '/mailboxes/' + mid + '/emails').then(d => Array.isArray(d) ? d : (d.data || [])),
  getEmail:   (mid, eid) => apiFetch(API_BASE + '/mailboxes/' + mid + '/emails/' + eid).then(d => d.email || d),
  deleteEmail:(mid, eid) => apiFetch(API_BASE + '/mailboxes/' + mid + '/emails/' + eid, { method: 'DELETE' }),
  // 管理
  shopListOrders: (page=1,size=20) => apiFetch(API_BASE + '/shop/orders?page='+page+'&size='+size),
  shopGetOrder: id => apiFetch(API_BASE + '/shop/orders/' + id),
  shopCreateOrder: body => apiFetch(API_BASE + '/shop/orders', { method: 'POST', body: JSON.stringify(body) }),
  // v10：优惠券（用户侧）
  couponMine:   (status='all') => apiFetch(API_BASE + '/coupons/mine?status=' + encodeURIComponent(status)),
  couponRedeem: code           => apiFetch(API_BASE + '/coupons/redeem', { method: 'POST', body: JSON.stringify({ code }) }),
  couponQuote:  (userCouponId, originalCents) => apiFetch(API_BASE + '/coupons/quote', {
    method: 'POST',
    body: JSON.stringify({ user_coupon_id: userCouponId, original_cents: originalCents }),
  }),
  admin: {
    // v10：账户列表支持 status 筛选（all/active/banned/svip）
    listAccounts:  (page=1,size=10,q='',status='') => {
      let u = API_BASE + '/admin/accounts?page='+page+'&size='+size;
      if (q) u += '&q=' + encodeURIComponent(q);
      if (status && status !== 'all') u += '&status=' + encodeURIComponent(status);
      return apiFetch(u);
    },
    patchAccount: (id, body) => apiFetch(API_BASE + '/admin/accounts/' + id, { method: 'PATCH', body: JSON.stringify(body) }),
    createAccount: body => apiFetch(API_BASE + '/admin/accounts', { method: 'POST', body: JSON.stringify(body) }),
    deleteAccount: id   => apiFetch(API_BASE + '/admin/accounts/' + id, { method: 'DELETE' }),
    rotateAccountKey: id => apiFetch(API_BASE + '/admin/accounts/' + id + '/rotate-key', { method: 'POST', body: '{}' }),
    // v10：SVIP 授权/撤销 + 配额
    grantSVIP:    (id, body) => apiFetch(API_BASE + '/admin/accounts/' + id + '/svip',  { method: 'POST', body: JSON.stringify(body || {}) }),
    setAccountQuota: (id, body) => apiFetch(API_BASE + '/admin/accounts/' + id + '/quota', { method: 'POST', body: JSON.stringify(body || {}) }),
    // v10：优惠券管理
    couponList:   (status='', q='', page=1, size=20) => {
      let u = API_BASE + '/admin/coupons?page='+page+'&size='+size;
      if (status) u += '&status=' + encodeURIComponent(status);
      if (q) u += '&q=' + encodeURIComponent(q);
      return apiFetch(u);
    },
    couponCreate: body => apiFetch(API_BASE + '/admin/coupons', { method: 'POST', body: JSON.stringify(body) }),
    couponUpdate: (id, body) => apiFetch(API_BASE + '/admin/coupons/' + id, { method: 'PUT', body: JSON.stringify(body) }),
    couponToggle: (id, enabled) => apiFetch(API_BASE + '/admin/coupons/' + id + '/toggle', { method: 'PATCH', body: JSON.stringify({ enabled }) }),
    couponDelete: id => apiFetch(API_BASE + '/admin/coupons/' + id, { method: 'DELETE' }),
    couponGrant:  (id, accountIds) => apiFetch(API_BASE + '/admin/coupons/' + id + '/grant', { method: 'POST', body: JSON.stringify({ account_ids: accountIds }) }),
    shopGetConfig: () => apiFetch(API_BASE + '/admin/shop/config'),
    shopPutConfig: body => apiFetch(API_BASE + '/admin/shop/config', { method: 'PUT', body: JSON.stringify(body) }),
    shopListProducts: () => apiFetch(API_BASE + '/admin/shop/products'),
    shopCreateProduct: body => apiFetch(API_BASE + '/admin/shop/products', { method: 'POST', body: JSON.stringify(body) }),
    shopUpdateProduct: (id, body) => apiFetch(API_BASE + '/admin/shop/products/' + id, { method: 'PUT', body: JSON.stringify(body) }),
    shopDeleteProduct: id => apiFetch(API_BASE + '/admin/shop/products/' + id, { method: 'DELETE' }),
    shopImportInventory: (text, batch, productId) => {
      let u = API_BASE + '/admin/shop/inventory/import';
      const qs = [];
      if (batch) qs.push('batch=' + encodeURIComponent(batch));
      if (productId) qs.push('product_id=' + encodeURIComponent(productId));
      if (qs.length) u += '?' + qs.join('&');
      return fetch(u, {
      method: 'POST',
      headers: { 'Authorization': 'Bearer ' + state.apiKey, 'Content-Type': 'text/plain; charset=utf-8' },
      body: text,
    }).then(async res => {
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || '导入失败');
      return data;
    });
    },
    // v10：导入 text / custom_kv 模式的库存（JSON）
    shopImportInventoryJSON: (payload, batch, productId) => {
      let u = API_BASE + '/admin/shop/inventory/import';
      const qs = [];
      if (batch) qs.push('batch=' + encodeURIComponent(batch));
      if (productId) qs.push('product_id=' + encodeURIComponent(productId));
      if (qs.length) u += '?' + qs.join('&');
      return apiFetch(u, { method: 'POST', body: JSON.stringify(payload) });
    },
    shopUploadQR: (formData) => fetch(API_BASE + '/admin/shop/qrcodes', {
      method: 'POST',
      headers: { 'Authorization': 'Bearer ' + state.apiKey },
      body: formData,
    }).then(async res => {
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || '上传失败');
      return data;
    }),
    shopListInventory: (status='all', page=1, size=30, batch='', productId='') => {
      let u = API_BASE + '/admin/shop/inventory?page=' + page + '&size=' + size;
      if (status && status !== 'all') u += '&status=' + encodeURIComponent(status);
      if (batch) u += '&batch=' + encodeURIComponent(batch);
      if (productId) u += '&product_id=' + encodeURIComponent(productId);
      return apiFetch(u);
    },
    shopListInventoryBatches: () => apiFetch(API_BASE + '/admin/shop/inventory/batches'),
    shopPurgeInventoryBatch: batchLabel => apiFetch(API_BASE + '/admin/shop/inventory/purge-batch', {
      method: 'POST',
      body: JSON.stringify({ batch_label: batchLabel }),
    }),
    shopPurgeAllAvailable: () => apiFetch(API_BASE + '/admin/shop/inventory/purge-available', { method: 'POST', body: '{}' }),
    shopDeleteInventory: id => apiFetch(API_BASE + '/admin/shop/inventory/' + id, { method: 'DELETE' }),
    shopListOrders: (status='', page=1, size=20) => {
      let u = API_BASE + '/admin/shop/orders?page='+page+'&size='+size;
      if (status) u += '&status=' + encodeURIComponent(status);
      return apiFetch(u);
    },
    shopConfirmOrder: id => apiFetch(API_BASE + '/admin/shop/orders/' + id + '/confirm', { method: 'POST' }),
    shopGetOrderAdmin: id => apiFetch(API_BASE + '/admin/shop/orders/' + id),
    addDomain:   body => apiFetch(API_BASE + '/admin/domains', { method: 'POST', body: JSON.stringify(body) }),
    deleteDomain:  id => apiFetch(API_BASE + '/admin/domains/' + id, { method: 'DELETE' }),
    toggleDomain:  (id, active) => apiFetch(API_BASE + '/admin/domains/' + id + '/toggle', { method: 'PUT', body: JSON.stringify({ active }) }),
    getSettings:    () => apiFetch(API_BASE + '/admin/settings'),
    saveSettings: body => apiFetch(API_BASE + '/admin/settings', { method: 'PUT', body: JSON.stringify(body) }),
    mxImport:    body => apiFetch(API_BASE + '/admin/domains/mx-import', { method: 'POST', body: JSON.stringify(body) }),
    mxRegister:  body => apiFetch(API_BASE + '/admin/domains/mx-register', { method: 'POST', body: JSON.stringify(body) }),
    getDomainStatus: id => apiFetch(API_BASE + '/admin/domains/' + id + '/status'),
  },
};

/** 从公开接口同步站点名称（无需登录） */
async function loadPublicSiteTitle() {
  try {
    const pub = await api.publicSettings();
    const raw = pub.site_title;
    state.siteTitle = (raw != null && String(raw).trim()) ? String(raw).trim() : 'TempMail';
  } catch {
    state.siteTitle = 'TempMail';
  }
}

/** 将 state.siteTitle 应用到 document.title、登录页、侧栏（若节点已存在） */
function applySiteBranding() {
  const t = state.siteTitle || 'TempMail';
  document.title = `${t} — 临时邮箱平台`;
  const authH = $('brand-site-title');
  if (authH) authH.textContent = t;
  const side = $('sidebar-site-title');
  if (side) side.textContent = t;
}

// ─── 主题 ────────────────────────────────────────────────────
function applyTheme(t) {
  document.documentElement.dataset.theme = t;
  state.theme = t;
  localStorage.setItem('tm_theme', t);
  const btn = $('btn-theme');
  if (btn) btn.textContent = t === 'dark' ? '☀ 浅色' : '☾ 深色';
}

// ─── 认证 ─────────────────────────────────────────────────────
async function tryLogin(key) {
  state.apiKey = key;
  try {
    const acct = await apiFetch(API_BASE + '/me');
    state.account = acct;
    localStorage.setItem('tm_apikey', key);
    localStorage.setItem('tm_account', JSON.stringify(acct));
    await showMainLayout();
    navigate('dashboard');
    toast(`欢迎回来，${acct.username || '用户'}`, 'success');
  } catch (e) {
    state.apiKey = '';
    if (e.status === 403 && e.code === 'account_banned') {
      toast(e.message || '由于违反邮箱服务协议，您的账户已被封禁，无法登录。', 'error');
    } else {
      toast('API Key 无效: ' + e.message, 'error');
    }
  }
}

function logout() {
  state.apiKey = '';
  state.account = null;
  localStorage.removeItem('tm_apikey');
  localStorage.removeItem('tm_account');
  showAuthPage();
}

// ─── 路由 ─────────────────────────────────────────────────────
function navigate(page, params = {}) {
  closeSidebar();
  // 离开收件箱时停止自动刷新
  if (page !== 'inbox') clearInboxPoller();
  state.page = page;
  Object.assign(state, params);
  renderPage(page);
  if (state.account) refreshClaudeShopNav();
  // 更新侧导航高亮
  document.querySelectorAll('.nav-item').forEach(n => {
    n.classList.toggle('active', n.dataset.page === page);
  });
}

// ─── 布局渲染 ──────────────────────────────────────────────────
function showAuthPage() {
  $('app').innerHTML = '';
  $('app').appendChild(buildAuthPage());
  renderLoginForm();
}

async function showMainLayout() {
  $('app').innerHTML = '';
  $('app').appendChild(buildMainLayout());
  applyTheme(state.theme);
  await refreshClaudeShopNav();
}

async function refreshClaudeShopNav() {
  try {
    const s = await api.publicClaudeShop();
    state.claudeShopEnabled = !!(s && s.enabled);
  } catch {
    state.claudeShopEnabled = false;
  }
  const wrap = $('nav-claude-shop-slot');
  if (!wrap) return;
  if (state.claudeShopEnabled) {
    wrap.innerHTML = `<div class="nav-section">商城</div>
      <button class="nav-item" data-page="claude-shop" onclick="navigate('claude-shop')"><span class="nav-icon">🛒</span><span>自助 Claude 账号</span></button>
      <button class="nav-item" data-page="claude-shop-orders" onclick="navigate('claude-shop-orders')"><span class="nav-icon">📋</span><span>我的订单</span></button>`;
  } else {
    wrap.innerHTML = '';
  }
}

function buildAuthPage() {
  const wrap = el('div', null);
  wrap.id = 'auth-page';

  const card = el('div', 'auth-card');
  card.innerHTML = `
    <div class="auth-logo">
      <div class="logo-icon">✉</div>
      <h1 id="brand-site-title">${escHtml(state.siteTitle)}</h1>
      <p>临时邮箱服务 · 安全隔离 · 按需分配</p>
    </div>
    <div class="auth-tabs">
      <button class="auth-tab active" id="tab-login" onclick="switchAuthTab('login')">使用 API Key 登录</button>
      <button class="auth-tab" id="tab-reg" onclick="switchAuthTab('reg')">注册账户</button>
    </div>
    <div id="auth-form-area"></div>
  `;
  wrap.appendChild(card);

  // 检查是否允许注册
  api.publicSettings().then(d => {
    const open = d.registration_open === 'true' || d.registration_open === true;
    if (!open) {
      const regTab = card.querySelector('#tab-reg');
      if (regTab) { regTab.disabled = true; regTab.title = '管理员已关闭注册'; }
    }
  }).catch(() => {});

  return wrap;
}

window.switchAuthTab = function(t) {
  document.querySelectorAll('.auth-tab').forEach(b => b.classList.remove('active'));
  if (t === 'login') {
    $('tab-login').classList.add('active');
    renderLoginForm();
  } else {
    $('tab-reg').classList.add('active');
    renderRegForm();
  }
};

function renderLoginForm() {
  const area = $('auth-form-area');
  if (!area) return;
  area.innerHTML = `
    <div class="form-group">
      <label class="form-label">API Key</label>
      <div style="position:relative">
        <input class="form-input" id="login-key" type="password" placeholder="tm_xxxxxxxxxxxx" autocomplete="current-password" style="padding-right:3.5rem" />
        <button type="button" id="login-key-toggle" onclick="toggleLoginKeyVisibility()" title="显示/隐藏 API Key"
          style="position:absolute;right:0.5rem;top:50%;transform:translateY(-50%);background:transparent;border:0;cursor:pointer;font-size:0.78rem;color:var(--text-muted);padding:0.25rem 0.45rem">显示</button>
      </div>
      <div class="form-hint">使用账户的 API Key 登录（本项目通过 API Key 代替传统密码；若怀疑泄露可登录后自助重置）</div>
    </div>
    <button class="btn btn-primary" style="width:100%" onclick="doLogin()">登 录</button>
    <div class="divider"></div>
    <div style="text-align:center;font-size:0.78rem;color:var(--text-muted)">
      没有账户？联系管理员创建，或点击上方"注册账户"
    </div>
  `;
  const inp = $('login-key');
  if (inp) inp.addEventListener('keydown', e => { if (e.key === 'Enter') doLogin(); });
}

window.toggleLoginKeyVisibility = function() {
  const inp = $('login-key');
  const btn = $('login-key-toggle');
  if (!inp || !btn) return;
  if (inp.type === 'password') {
    inp.type = 'text';
    btn.textContent = '隐藏';
  } else {
    inp.type = 'password';
    btn.textContent = '显示';
  }
};

function renderRegForm() {
  const area = $('auth-form-area');
  if (!area) return;
  area.innerHTML = `
    <div class="form-group">
      <label class="form-label">用户名</label>
      <input class="form-input" id="reg-username" type="text" placeholder="your_name" />
      <div class="form-hint">注册成功后系统会生成专属 API Key，请妥善保存。</div>
    </div>
    <button class="btn btn-primary" style="width:100%" onclick="doRegister()">注 册</button>
  `;
}

window.doLogin = async function() {
  const key = ($('login-key')?.value || '').trim();
  if (!key) { toast('请输入 API Key', 'warn'); return; }
  await tryLogin(key);
};

window.doRegister = async function() {
  const username = ($('reg-username')?.value || '').trim();
  if (!username) { toast('请输入用户名', 'warn'); return; }
  try {
    const result = await api.register({ username });
    // 显示成功
    const area = $('auth-form-area');
    area.innerHTML = `
      <div class="apikey-hero">
        <span class="big-icon">🎉</span>
        <h2>注册成功！</h2>
        <p>请保存您的 API Key，它不会再次显示。</p>
        <div class="code-box">
          <span id="new-key">${escHtml(result.api_key)}</span>
          <button class="copy-btn" onclick="copyText('${escHtml(result.api_key)}')" title="复制">⎘</button>
        </div>
        <button class="btn btn-success" style="margin-top:1.2rem;width:100%" onclick="tryLogin('${escHtml(result.api_key)}')">立即登录</button>
      </div>
    `;
  } catch(e) {
    toast('注册失败: ' + e.message, 'error');
  }
};

// ─── 主布局 ────────────────────────────────────────────────────
function buildMainLayout() {
  const layout = el('div', null);
  layout.id = 'main-layout';
  layout.style.display = 'flex';
  layout.style.flex = '1';

  const isAdmin = state.account?.is_admin;
  const username = state.account?.username || '用户';
  const isSVIP = (state.account?.svip_level || 0) > 0 &&
    (!state.account?.svip_expires_at || new Date(state.account.svip_expires_at) > new Date());

  // sidebar
  layout.innerHTML = `
    <div class="sidebar-backdrop" id="sidebar-backdrop" onclick="closeSidebar()"></div>
    <nav class="sidebar" id="main-sidebar">
      <div class="sidebar-logo">
        <div class="logo-mark">✉</div>
        <div>
          <span id="sidebar-site-title">${escHtml(state.siteTitle)}</span>
          <small>临时邮箱服务</small>
        </div>
      </div>
      <div class="sidebar-nav">
        <div class="nav-section">邮件</div>
        <button class="nav-item active" data-page="dashboard" onclick="navigate('dashboard')">
          <span class="nav-icon">⊞</span><span>邮箱总览</span>
        </button>
        <button class="nav-item" data-page="domains-guide" onclick="navigate('domains-guide')">
          <span class="nav-icon">◎</span><span>域名列表</span>
        </button>
        <button class="nav-item" data-page="api-docs" onclick="navigate('api-docs')">
          <span class="nav-icon">📖</span><span>API 文档</span>
        </button>
        <div id="nav-claude-shop-slot"></div>
        <button class="nav-item" data-page="my-coupons" onclick="navigate('my-coupons')">
          <span class="nav-icon">🎟</span><span>我的优惠券</span>
        </button>
        ${isAdmin ? `
        <div class="nav-section">管理</div>
        <button class="nav-item" data-page="admin-accounts" onclick="navigate('admin-accounts')">
          <span class="nav-icon">👥</span><span>账户管理</span>
        </button>
        <button class="nav-item" data-page="admin-coupons" onclick="navigate('admin-coupons')">
          <span class="nav-icon">🎫</span><span>优惠券管理</span>
        </button>
        <button class="nav-item" data-page="admin-domains" onclick="navigate('admin-domains')">
          <span class="nav-icon">🌐</span><span>域名管理</span>
        </button>
        <button class="nav-item" data-page="admin-shop-settings" onclick="navigate('admin-shop-settings')">
          <span class="nav-icon">🏷</span><span>商品与收款</span>
        </button>
        <button class="nav-item" data-page="admin-shop-products" onclick="navigate('admin-shop-products')">
          <span class="nav-icon">🛒</span><span>在售 SKU</span>
        </button>
        <button class="nav-item" data-page="admin-shop-inventory" onclick="navigate('admin-shop-inventory')">
          <span class="nav-icon">📦</span><span>库存与货物</span>
        </button>
        <button class="nav-item" data-page="admin-shop-orders" onclick="navigate('admin-shop-orders')">
          <span class="nav-icon">🧾</span><span>订单与发货</span>
        </button>
        <button class="nav-item" data-page="admin-settings" onclick="navigate('admin-settings')">
          <span class="nav-icon">⚙</span><span>系统设置</span>
        </button>
        ` : ''}
      </div>
      <div class="sidebar-bottom">
        <div class="user-chip">
          <div class="user-avatar ${isSVIP ? 'svip-avatar-ring' : ''}">${username.charAt(0).toUpperCase()}</div>
          <div class="user-chip-info">
            <div class="user-chip-name">
              ${escHtml(username)}
              ${isSVIP ? '<span class="svip-badge svip-badge-sm" style="margin-left:4px">SVIP</span>' : ''}
            </div>
            <div class="user-chip-role">${isAdmin ? '管理员' : (isSVIP ? 'SVIP 会员' : '普通用户')}</div>
          </div>
        </div>
        <button class="btn-logout" onclick="logout()">⏏ 退出登录</button>
        <button class="btn-theme" id="btn-theme" onclick="toggleTheme()">${state.theme==='dark'?'☀ 浅色':'☾ 深色'}</button>
      </div>
    </nav>
    <div class="content" id="content-area">
      <div class="topbar">
        <div>
          <button class="hamburger-btn" id="hamburger-btn" onclick="toggleSidebar()" aria-label="菜单">☰</button>
          <div>
            <div class="topbar-title" id="topbar-title">邮箱总览</div>
            <div class="topbar-subtitle" id="topbar-subtitle"></div>
          </div>
        </div>
        <div id="topbar-actions"></div>
      </div>
      <div id="page-content" class="page"></div>
    </div>
  `;
  return layout;
}

window.toggleTheme = function() {
  applyTheme(state.theme === 'dark' ? 'light' : 'dark');
};
window.navigate = navigate;
window.logout   = logout;
window.copyText = copyText;
window.tryLogin = tryLogin;

window.toggleSidebar = function() {
  const sidebar  = document.getElementById('main-sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  if (!sidebar) return;
  const isOpen = sidebar.classList.contains('mob-open');
  if (isOpen) {
    sidebar.classList.remove('mob-open');
    if (backdrop) backdrop.classList.remove('show');
  } else {
    sidebar.classList.add('mob-open');
    if (backdrop) backdrop.classList.add('show');
  }
};

window.closeSidebar = function() {
  const sidebar  = document.getElementById('main-sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  if (sidebar)  sidebar.classList.remove('mob-open');
  if (backdrop) backdrop.classList.remove('show');
};

// ─── 页面渲染路由 ───────────────────────────────────────────
async function renderPage(page) {
  const container = $('page-content');
  if (!container) return;
  container.innerHTML = '<div style="padding:2rem;text-align:center"><span class="spinner"></span></div>';

  const titles = {
    'dashboard':      ['邮箱总览', '管理您的临时邮箱'],
    'inbox':          ['邮件列表', ''],
    'email-view':     ['邮件内容', ''],
    'domains-guide':  ['域名列表 & 添加指南', '查看可用域名并了解如何添加新域名'],
    'admin-accounts': ['账户管理', '创建和管理用户账户'],
    'admin-domains':  ['域名管理', '管理域名池'],
    'admin-settings': ['系统设置', ''],
    'claude-shop':    ['自助 Claude 账号', '选购商品'],
    'claude-shop-orders': ['我的订单', '记录与发货信息'],
    'admin-shop-settings': ['商品与收款', '文案、价格、收款码'],
    'admin-shop-products': ['在售 SKU', '多商品与标签'],
    'admin-shop-inventory': ['库存与货物', '导入与货物列表'],
    'admin-shop-orders': ['订单与发货', '待确认与历史订单'],
    'apikey-show':    ['API Key', ''],
    'api-docs':       ['API 接口文档', '查看所有可用 API 及调用示例'],
    'my-coupons':     ['我的优惠券', '查看、领取与使用您的优惠券'],
    'admin-coupons':  ['优惠券管理', '创建、派发、审计营销活动'],
  };
  const [t, s] = titles[page] || ['—', ''];
  const title = $('topbar-title'); if (title) title.textContent = t;
  const sub   = $('topbar-subtitle'); if (sub) sub.textContent = s;
  const actions = $('topbar-actions'); if (actions) actions.innerHTML = '';

  try {
    switch(page) {
      case 'dashboard':      await renderDashboard(container); break;
      case 'inbox':          await renderInbox(container); break;
      case 'email-view':     await renderEmailView(container); break;
      case 'domains-guide':  await renderDomainsGuide(container); break;
      case 'admin-accounts': await renderAdminAccounts(container); break;
      case 'admin-domains':  await renderAdminDomains(container); break;
      case 'admin-settings': await renderAdminSettings(container); break;
      case 'claude-shop':    await renderClaudeShop(container); break;
      case 'claude-shop-orders': await renderClaudeShopOrders(container); break;
      case 'admin-shop-settings': await renderAdminShopSettings(container); break;
      case 'admin-shop-products': await renderAdminShopProducts(container); break;
      case 'admin-shop-inventory': await renderAdminShopInventory(container); break;
      case 'admin-shop-orders': await renderAdminShopOrders(container); break;
      case 'apikey-show':    renderApiKeyShow(container); break;
      case 'api-docs':       renderApiDocs(container); break;
      case 'my-coupons':     await renderMyCoupons(container); break;
      case 'admin-coupons':  await renderAdminCoupons(container); break;
      default: container.innerHTML = '<div class="page"><p>页面未找到</p></div>';
    }
  } catch(e) {
    container.innerHTML = `<div style="padding:2rem;color:var(--clr-danger)">加载失败：${escHtml(e.message)}</div>`;
  }
}

// ─── Dashboard ─────────────────────────────────────────────
async function renderDashboard(container) {
  const isAdmin = state.account?.is_admin;
  const [mailboxes, domains, statsData] = await Promise.all([
    api.listMailboxes(),
    api.domains(),
    api.stats().catch(() => null),
  ]);
  state.mailboxes = mailboxes || [];

  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `
      <button class="btn btn-primary btn-sm" onclick="createMailbox()">+ 新建邮箱</button>
      <button class="btn btn-ghost btn-sm" onclick="navigate('apikey-show')" style="margin-left:0.4rem">⚿ 我的 API Key</button>
    `;
  }

  const boxes  = state.mailboxes;
  const st     = statsData || {};
  const activeDomains  = (domains||[]).filter(d => d.is_active).length;
  const pendingDomains = (domains||[]).filter(d => d.status === 'pending').length;

  const statCards = [
    { label: '我的邮箱', value: boxes.length,                   note: '当前有效' },
    { label: '可用域名', value: activeDomains,                  note: `共 ${(domains||[]).length} 个` },
    { label: '收到邮件', value: st.total_emails ?? '—',         note: '全平台累计' },
    { label: '邮箱总量', value: st.total_mailboxes ?? '—',      note: `活跃 ${st.active_mailboxes ?? '—'} 个` },
    ...(isAdmin ? [
      { label: '账户总数', value: st.total_accounts ?? '—',       note: '注册用户' },
      { label: '待验证域名', value: st.pending_domains ?? pendingDomains, note: pendingDomains > 0 ? '🔄 验证中' : '无' },
    ] : []),
  ];

  // v10：公告栏（玻璃拟态 + Markdown + 级别图标 + 可关闭）
  const pub = await api.publicSettings().catch(() => ({}));
  const annContent = (pub.announcement || '').trim();
  const annLevel   = (pub.announcement_level || 'info').trim();
  const annTitle   = (pub.announcement_title || '').trim();
  const annHtml    = buildAnnouncementHtml(annContent, annLevel, annTitle);

  container.innerHTML = `
    ${annHtml}
    <div class="stat-grid" style="grid-template-columns:repeat(auto-fill,minmax(140px,1fr))">
      ${statCards.map(s => `
        <div class="stat-card">
          <div class="stat-label">${escHtml(s.label)}</div>
          <div class="stat-value">${typeof s.value === 'number' ? s.value.toLocaleString() : s.value}</div>
          <div class="stat-note">${escHtml(s.note)}</div>
        </div>
      `).join('')}
    </div>
    ${pendingDomains > 0 ? `
      <div class="card" style="margin-top:0.8rem;border-left:3px solid var(--clr-warn,#e6a817)">
        <div style="font-size:0.82rem">🔄 有 ${pendingDomains} 个域名正在 MX 验证中，通过后将自动加入域名池</div>
      </div>
    ` : ''}
    ${boxes.length === 0 ? `
      <div class="card" style="margin-top:0.8rem">
        <div class="empty-state">
          <span class="empty-icon">✉</span>
          <p>还没有邮箱，点击右上角"新建邮箱"创建第一个</p>
        </div>
      </div>
    ` : `
      <div class="mailbox-grid" id="mailbox-grid" style="margin-top:0.8rem">
        ${boxes.map(mb => buildMailboxCard(mb)).join('')}
      </div>
    `}
  `;
}

function buildMailboxCard(mb) {
  const expiresAt = mb.expires_at ? new Date(mb.expires_at) : null;
  const now = new Date();
  let expiryHtml = '';
  if (expiresAt) {
    const diffMs = expiresAt - now;
    if (diffMs <= 0) {
      expiryHtml = '<span style="color:var(--clr-danger);font-size:0.75rem">⏱ 已过期</span>';
    } else {
      const mins = Math.ceil(diffMs / 60000);
      const color = mins <= 5 ? 'var(--clr-danger)' : mins <= 15 ? 'var(--clr-warn,#e6a817)' : 'var(--text-muted)';
      expiryHtml = `<span style="color:${color};font-size:0.75rem">⏱ ${mins}分钟后删除</span>`;
    }
  } else {
    expiryHtml = '<span style="color:var(--text-muted);font-size:0.75rem">♾ 永不过期</span>';
  }
  return `
    <div class="mailbox-card" onclick="openInbox('${mb.id}','${escHtml(mb.full_address)}')">
      <div class="mailbox-address">${escHtml(mb.full_address)}</div>
      <div class="mailbox-stats" style="display:flex;gap:0.7rem;align-items:center">
        <span>创建于 ${formatDate(mb.created_at)}</span>
        ${expiryHtml}
      </div>
      <div class="mailbox-actions">
        <button class="btn btn-ghost btn-sm" onclick="event.stopPropagation();openInbox('${mb.id}','${escHtml(mb.full_address)}')">📬 查看邮件</button>
        <button class="btn btn-ghost btn-sm" onclick="event.stopPropagation();copyText('${escHtml(mb.full_address)}')" title="复制地址">⎘</button>
        <button class="btn btn-danger btn-sm" onclick="event.stopPropagation();confirmDeleteMailbox('${mb.id}','${escHtml(mb.full_address)}')">✕</button>
      </div>
    </div>
  `;
}

window.openInbox = function(id, addr) {
  state.currentMailbox = { id, full_address: addr };
  navigate('inbox');
};

window.createMailbox = async function() {
  // 拉取活跃域名列表，构建选择弹窗
  let activeDomains = [];
  try {
    const all = await api.domains();
    activeDomains = (all || []).filter(d => d.is_active);
  } catch(e) { /* 获取失败时退化为随机域名 */ }

  const old = document.querySelector('.modal-overlay');
  if (old) old.remove();
  const overlay = el('div', 'modal-overlay');

  const domainOptions = activeDomains.map(d =>
    `<option value="${escHtml(d.domain)}">${escHtml(d.domain)}</option>`
  ).join('');

  overlay.innerHTML = `
    <div class="modal" style="max-width:420px">
      <div class="modal-title">+ 新建临时邮箱</div>
      <button class="modal-close" onclick="this.closest('.modal-overlay').remove()">✕</button>
      <div class="form-group" style="margin-top:0.8rem">
        <label class="form-label">本地部分（@ 之前）</label>
        <input class="form-input" id="mb-address" placeholder="留空则随机生成" autocomplete="off" />
        <div class="form-hint">只允许字母、数字、连字符、下划线</div>
      </div>
      <div class="form-group">
        <label class="form-label">域名</label>
        <select class="form-input" id="mb-domain">
          <option value="">随机选取</option>
          ${domainOptions}
        </select>
      </div>
      <div class="modal-actions">
        <button class="btn btn-ghost" onclick="this.closest('.modal-overlay').remove()">取消</button>
        <button class="btn btn-primary" id="mb-confirm-btn">创建</button>
      </div>
    </div>
  `;
  document.body.appendChild(overlay);
  overlay.addEventListener('click', e => { if (e.target === overlay) overlay.remove(); });

  // 回车确认
  overlay.querySelector('#mb-address').addEventListener('keydown', e => {
    if (e.key === 'Enter') overlay.querySelector('#mb-confirm-btn').click();
  });

  overlay.querySelector('#mb-confirm-btn').addEventListener('click', async () => {
    const btn     = overlay.querySelector('#mb-confirm-btn');
    const address = overlay.querySelector('#mb-address').value.trim();
    const domain  = overlay.querySelector('#mb-domain').value;
    btn.disabled  = true;
    btn.textContent = '创建中...';
    try {
      const body = {};
      if (address) body.address = address;
      if (domain)  body.domain  = domain;
      const mb = await api.createMailbox(body);
      overlay.remove();
      toast(`已创建：${mb.full_address}`, 'success');
      navigate('dashboard');
    } catch(e) {
      btn.disabled = false;
      btn.textContent = '创建';
      toast('创建失败：' + e.message, 'error');
    }
  });
};

window.confirmDeleteMailbox = function(id, addr) {
  showModal(`删除邮箱`, `<p>确定删除 <strong>${escHtml(addr)}</strong>？<br/><span style="font-size:0.8rem;color:var(--clr-danger)">所有邮件将被永久删除。</span></p>`,
    async () => {
      try {
        await api.deleteMailbox(id);
        toast('邮箱已删除', 'success');
        navigate('dashboard');
      } catch(e) { toast('删除失败: ' + e.message, 'error'); }
    }
  );
};

// ─── API Key 展示 ──────────────────────────────────────────
function renderApiKeyShow(container) {
  const key = state.apiKey || '—';
  const keyJs = JSON.stringify(key);
  container.innerHTML = `
    <div class="card" style="max-width:540px">
      <div class="card-header"><div class="card-title">⚿ 我的 API Key</div></div>
      <div class="card-body">
        <p style="font-size:0.84rem;color:var(--text-secondary);margin-bottom:1rem">
          API Key 用于认证所有 API 请求。请勿泄露。
        </p>
        <div class="form-label">当前 API Key</div>
        <div class="code-box" style="margin-bottom:1rem">
          <span style="filter:blur(4px);cursor:pointer" id="key-blur" onclick="this.style.filter='none'">${escHtml(key)}</span>
          <button class="copy-btn" onclick='copyText(${keyJs})' title="复制">⎘</button>
        </div>
        <p style="font-size:0.76rem;color:var(--text-muted)">点击 Key 可显示明文。若怀疑泄露，可点下方按钮自助重置，旧 Key 会立刻失效。</p>
        <div style="margin-top:0.9rem">
          <button class="btn btn-danger btn-sm" onclick="confirmRotateMyKey()">⟳ 重置 API Key</button>
        </div>
        <div class="divider"></div>
        <div class="form-label">HTTP 请求示例</div>
        <div class="code-box" style="font-size:0.75rem">curl -H "Authorization: Bearer &lt;api_key&gt;" http://server:8080/api/mailboxes</div>
      </div>
    </div>
  `;
}

// ─── 重置 API Key（用户自助 / 管理员代操作共用一个结果弹窗）──
/** 展示"一次性新 Key"模态：大号明文 + 复制 + 仅一个关闭按钮 */
function showNewApiKeyModal(title, newKey, noteHtml) {
  const old = document.querySelector('.modal-overlay');
  if (old) old.remove();
  const keyJs = JSON.stringify(newKey);
  const overlay = el('div', 'modal-overlay');
  overlay.innerHTML = `
    <div class="modal" style="max-width:560px">
      <div class="modal-title">${escHtml(title)}</div>
      <button class="modal-close" onclick="this.closest('.modal-overlay').remove()">✕</button>
      <div style="background:var(--clr-warning-soft, #fff3cd);border:1px solid var(--clr-warning, #f0ad4e);padding:0.7rem 0.85rem;border-radius:8px;font-size:0.84rem;margin-bottom:0.9rem;color:var(--text-primary)">
        ${noteHtml}
      </div>
      <div class="form-label">新 API Key（仅此一次显示）</div>
      <div class="code-box" style="user-select:all;word-break:break-all;font-size:0.86rem;padding:0.6rem 0.7rem">
        <span style="flex:1">${escHtml(newKey)}</span>
        <button class="copy-btn" onclick='copyText(${keyJs})' title="复制">⎘</button>
      </div>
      <div class="modal-actions">
        <button class="btn btn-primary" onclick="this.closest('.modal-overlay').remove()">我已保存</button>
      </div>
    </div>
  `;
  document.body.appendChild(overlay);
}

window.confirmRotateMyKey = function() {
  showModal('重置我的 API Key', `
    <p>重置后，<strong>旧 Key 将立即失效</strong>，所有使用旧 Key 的脚本 / 程序将返回 401。</p>
    <p style="font-size:0.85rem;color:var(--text-muted);margin-top:0.4rem">请确认已准备好更新所有调用方。</p>
  `, async () => {
    try {
      const res = await api.rotateMyKey();
      const newKey = res.api_key;
      if (!newKey) throw new Error('未返回新 Key');
      state.apiKey = newKey;
      localStorage.setItem('tm_apikey', newKey);
      toast('API Key 已轮换', 'success');
      showNewApiKeyModal('新的 API Key',
        newKey,
        '<strong>旧 Key 已失效。</strong>请立即复制保存新 Key；关闭此窗口后系统不会再次展示。');
      const c = $('page-content');
      if (c && state.page === 'apikey-show') renderApiKeyShow(c);
    } catch (e) {
      toast('重置失败: ' + (e.message || '未知错误'), 'error');
      return false;
    }
  });
};

window.adminRotateAccountKey = function(id, username) {
  showModal('为该账户重置 API Key', `
    <p>将为账户 <strong>${escHtml(username)}</strong> 生成新 API Key。</p>
    <p><strong>旧 Key 立即失效</strong>，该用户当前登录会失效，需要用新 Key 重新登录。</p>
    <p style="font-size:0.85rem;color:var(--text-muted);margin-top:0.4rem">新 Key 仅在下一步弹窗中显示一次，请及时通知该用户。</p>
  `, async () => {
    try {
      const res = await api.admin.rotateAccountKey(id);
      const newKey = res.api_key;
      if (!newKey) throw new Error('未返回新 Key');
      toast('已重置该账户的 API Key', 'success');
      showNewApiKeyModal(`【${username}】的新 API Key`,
        newKey,
        '<strong>仅此一次显示。</strong>请立即复制并通过安全渠道通知该用户；关闭后无法再查看原始值（列表页会显示同值，但不要以此为唯一依据）。');
      navigate('admin-accounts');
    } catch (e) {
      toast('重置失败: ' + (e.message || '未知错误'), 'error');
      return false;
    }
  });
};

// ─── Inbox ────────────────────────────────────────────────
async function renderInbox(container) {
  const mb = state.currentMailbox;
  if (!mb) { navigate('dashboard'); return; }

  const title = $('topbar-title'); if (title) title.textContent = mb.full_address;
  const sub   = $('topbar-subtitle'); if (sub) sub.textContent = '邮件列表';
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `
      <button class="btn btn-ghost btn-sm" onclick="copyText('${escHtml(mb.full_address)}')">⎘ 复制地址</button>
      <button class="btn btn-primary btn-sm" onclick="refreshInbox()" style="margin-left:0.4rem">↻ 刷新</button>
      <button class="btn btn-ghost btn-sm" onclick="navigate('dashboard')" style="margin-left:0.4rem">← 返回</button>
    `;
  }

  const emails = await api.listEmails(mb.id);
  state.emails = emails || [];

  // 启动自动刷新（每 8 秒）
  clearInboxPoller();
  _inboxPollerTimer = setInterval(async () => {
    if (state.page !== 'inbox') { clearInboxPoller(); return; }
    try {
      const fresh = await api.listEmails(mb.id);
      if (!fresh) return;
      // 有新邮件才重新渲染，避免闪烁
      if (fresh.length !== (state.emails || []).length ||
          (fresh[0]?.id !== state.emails?.[0]?.id)) {
        state.emails = fresh;
        const c = $('page-content');
        if (c) renderInbox(c);
      }
    } catch(e) { /* 静默失败 */ }
  }, 8000);

  if (!state.emails.length) {
    container.innerHTML = `
      <div class="card">
        <div class="empty-state">
          <span class="empty-icon">📭</span>
          <p>暂无邮件</p>
          <p style="margin-top:0.5rem;font-size:0.8rem">向 <strong>${escHtml(mb.full_address)}</strong> 发送邮件后，邮件将显示在此处</p>
        </div>
      </div>
    `;
    return;
  }

  container.innerHTML = `
    <div class="card" style="padding:0">
      ${state.emails.map(e => buildEmailItem(mb.id, e)).join('')}
    </div>
  `;
}

function buildEmailItem(mbId, e) {
  const from = e.sender || e.from_addr || '(无发件人)';
  const initials = from.charAt(0).toUpperCase();
  const preview = (e.body_text || e.text_body || '').slice(0, 80).replace(/\n/g, ' ');
  return `
    <div class="email-item" onclick="openEmail('${mbId}','${e.id}')">
      <div class="email-avatar">${escHtml(initials)}</div>
      <div class="email-meta">
        <div class="email-from">${escHtml(from)}</div>
        <div class="email-subject">${escHtml(e.subject || '(无主题)')}</div>
        <div class="email-preview">${escHtml(preview)}</div>
      </div>
      <div>
        <div class="email-time">${timeAgo(e.received_at)}</div>
        <button class="btn btn-ghost btn-sm" style="margin-top:0.3rem" onclick="event.stopPropagation();deleteEmail('${mbId}','${e.id}')">✕</button>
      </div>
    </div>
  `;
}

window.openEmail = function(mbId, eid) {
  state.currentMailbox = state.currentMailbox || { id: mbId };
  state.currentEmailId = eid;
  navigate('email-view');
};

window.refreshInbox = function() {
  clearInboxPoller();
  renderPage('inbox');
};

window.deleteEmail = async function(mbId, eid) {
  try {
    await api.deleteEmail(mbId, eid);
    toast('邮件已删除', 'success');
    navigate('inbox');
  } catch(e) { toast('删除失败: ' + e.message, 'error'); }
};

// ─── Email View ────────────────────────────────────────────
async function renderEmailView(container) {
  const mb = state.currentMailbox;
  const eid = state.currentEmailId;
  if (!mb || !eid) { navigate('dashboard'); return; }

  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `
      <button class="btn btn-ghost btn-sm" onclick="navigate('inbox')">← 返回列表</button>
      <button class="btn btn-danger btn-sm" onclick="deleteEmail('${mb.id}','${eid}');navigate('inbox')" style="margin-left:0.4rem">删除</button>
    `;
  }

  const e = await api.getEmail(mb.id, eid);
  const fromAddr = e.sender || e.from_addr || '—';
  const toAddr   = mb.full_address || state.currentMailbox?.full_address || '—';
  const htmlBody  = e.body_html || e.html_body || '';
  const textBody  = e.body_text || e.text_body || '';
  const title = $('topbar-title'); if (title) title.textContent = e.subject || '(无主题)';
  const sub   = $('topbar-subtitle'); if (sub) sub.textContent = `来自：${fromAddr}`;

  // 先渲染完整 HTML（含 iframe 占位），再向 iframe 写入内容
  container.innerHTML = `
    <div class="card" style="padding:0;max-width:860px">
      <div class="email-detail-header">
        <div class="email-subject-big">${escHtml(e.subject || '(无主题)')}</div>
        <div class="email-info-row">
          <span>发件人：<strong>${escHtml(fromAddr)}</strong></span>
          <span style="margin:0 0.3rem">·</span>
          <span>收件人：<strong>${escHtml(toAddr)}</strong></span>
          <span style="margin:0 0.3rem">·</span>
          <span>${formatDate(e.received_at)}</span>
        </div>
      </div>
      ${htmlBody
        ? `<iframe class="email-body-frame" id="email-frame" sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox"></iframe>`
        : `<div class="email-body-text" style="white-space:pre-wrap">${escHtml(textBody || '(邮件内容为空)')}</div>`
      }
    </div>
  `;

  // innerHTML 中的 <script> 不会执行；在 DOM 就绪后直接向 iframe 写内容
  if (htmlBody) {
    const frame = container.querySelector('#email-frame');
    if (frame) {
      frame.contentDocument.open();
      frame.contentDocument.write(htmlBody);
      frame.contentDocument.close();
      rewriteEmailIframeLinks(frame.contentDocument);
      const setH = () => {
        try { frame.style.height = frame.contentDocument.body.scrollHeight + 20 + 'px'; } catch (_) {}
      };
      frame.addEventListener('load', setH);
      setTimeout(setH, 300);
    }
  }
}

// ─── 域名列表 & 指南 ─────────────────────────────────────────
async function renderDomainsGuide(container) {
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `<button class="btn btn-success btn-sm" onclick="showMXRegisterModal()">⚡ 提交域名自动验证</button>`;
  }

  const [domains, pub] = await Promise.all([
    api.domains(),
    api.publicSettings().catch(() => ({})),
  ]);
  const smtpIP  = pub.smtp_server_ip || '';
  const smtpHostname = pub.smtp_hostname || '';
  const ipLabel = smtpIP || '&lt;服务器 IP&gt;';
  const mxTarget = smtpHostname || '&lt;服务器邮件主机名&gt;';
  const needsARec = !smtpHostname;

  const pending = (domains||[]).filter(d => d.status === 'pending');
  const active  = (domains||[]).filter(d => d.status !== 'pending');

  const pendingHtml = pending.length > 0 ? `
    <div class="card" style="border-left:3px solid var(--clr-warn,#e6a817);margin-bottom:1rem">
      <div class="card-header">
        <div class="card-title">🔄 待 MX 验证 (${pending.length})</div>
        <div style="font-size:0.78rem;color:var(--text-muted)">后台每 30 秒自动检测，验证通过后自动激活</div>
      </div>
      <div class="table-wrap">
        <table>
          <thead><tr><th>域名</th><th>上次检测</th><th>状态</th></tr></thead>
          <tbody>
            ${pending.map(d => `
              <tr id="pending-row-${d.id}">
                <td style="font-family:var(--font-mono);font-size:0.82rem">${escHtml(d.domain)}</td>
                <td style="font-size:0.78rem">${d.mx_checked_at ? timeAgo(d.mx_checked_at) : '待首次检测'}</td>
                <td><span class="badge badge-gold" id="pending-status-${d.id}">⏳ 检测中</span></td>
              </tr>
            `).join('')}
          </tbody>
        </table>
      </div>
    </div>
  ` : '';

  container.innerHTML = `
    ${pendingHtml}
    <div class="domain-guide-grid" style="display:grid;grid-template-columns:1fr 1fr;gap:1.2rem;max-width:1000px">
      <div>
        <div class="card">
          <div class="card-header"><div class="card-title">◎ 可用域名池</div></div>
          <div class="table-wrap">
            <table>
              <thead><tr><th>域名</th><th>状态</th></tr></thead>
              <tbody>
                ${active.length === 0
                  ? `<tr><td colspan="2" style="text-align:center;color:var(--text-muted)">暂无域名</td></tr>`
                  : active.map(d => `
                    <tr>
                      <td style="font-family:var(--font-mono);font-size:0.82rem">${escHtml(d.domain)}</td>
                      <td>${d.is_active
                        ? '<span class="badge badge-green">● 启用</span>'
                        : '<span class="badge badge-gray">○ 停用</span>'}</td>
                    </tr>
                  `).join('')}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <div>
        <div class="card">
          <div class="card-header"><div class="card-title">📖 添加域名指南</div></div>
          <div class="card-body">
            <div class="guide-step">
              <div class="step-num">1</div>
              <div class="step-body">
                <div class="step-title">准备域名</div>
                <div class="step-desc">在域名注册商处购买一个域名，例如 <code>example.com</code>，并获取 DNS 管理权限。</div>
              </div>
            </div>
            <div class="guide-step">
              <div class="step-num">2</div>
              <div class="step-body">
                <div class="step-title">配置 MX 记录（仅需一条）</div>
                <div class="step-desc">在 DNS 面板添加以下记录，让 SMTP 邮件投递到本服务器：</div>
                <table class="dns-table" style="margin-top:0.5rem">
                  <thead><tr><th>类型</th><th>主机名</th><th>内容</th><th>优先级</th></tr></thead>
                  <tbody>
                    <tr><td>MX</td><td>@</td><td style="font-family:monospace">${mxTarget}</td><td>10</td></tr>
                    ${needsARec ? `<tr><td>A</td><td style="font-family:monospace">mail.yourdomain.com</td><td style="font-family:monospace">${ipLabel}</td><td>—</td></tr>` : ''}
                    <tr><td>TXT</td><td>@</td><td style="font-family:monospace">v=spf1 ip4:${ipLabel} ~all</td><td>—</td></tr>
                  </tbody>
                </table>
              </div>
            </div>
            <div class="guide-step">
              <div class="step-num">3</div>
              <div class="step-body">
                <div class="step-title">提交域名自动验证</div>
                <div class="step-desc">
                  DNS 广播后（通常 5–30 分钟），点击右上角「⚡ 提交域名自动验证」按钮。<br>
                  <ul style="margin:0.4rem 0 0 1rem;font-size:0.82rem">
                    <li>MX 已生效 → <b>立即激活</b>加入域名池</li>
                    <li>MX 未生效 → 进入<b>待验证队列</b>，后台每 30 秒自动重试</li>
                  </ul>
                </div>
                <button class="btn btn-success btn-sm" style="margin-top:0.5rem" onclick="showMXRegisterModal()">⚡ 提交域名</button>
              </div>
            </div>
            <div class="guide-step">
              <div class="step-num">4</div>
              <div class="step-body">
                <div class="step-title">验证收信</div>
                <div class="step-desc">域名激活后，创建该域名下的邮箱，用其他邮件客户端发送测试邮件，30 秒内应能收到。</div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  `;

  if (pending.length > 0) {
    startPendingDomainPoller(pending.map(d => d.id));
  }
}

// ─── Admin: 账户管理 ─────────────────────────────────────────
function formatLastSeen(t) {
  if (!t) return '—';
  return formatDate(t);
}

window.adminAccSearch = function() {
  state.adminAccountsQ = ($('admin-acc-q')?.value || '').trim();
  state.adminAccountsPage = 1;
  navigate('admin-accounts');
};

window.adminAccGoPage = function(p) {
  state.adminAccountsPage = Math.max(1, p);
  navigate('admin-accounts');
};

// v10：按状态筛选（all/active/banned/svip）
window.adminAccSetStatus = function(status) {
  state.adminAccountsStatus = status || 'all';
  state.adminAccountsPage = 1;
  navigate('admin-accounts');
};

window.toggleBanAccount = function(id, username, ban) {
  const act = ban ? '封禁' : '解除封禁';
  showModal(act + '账户', `<p>确定对 <strong>${escHtml(username)}</strong> ${act}？封禁后该用户无法登录。</p>`, async () => {
    try {
      await api.admin.patchAccount(id, { is_active: !ban });
      toast('已更新', 'success');
      navigate('admin-accounts');
    } catch (e) {
      toast(e.message || '失败', 'error');
    }
  });
};

// v10：授予 / 撤销 SVIP
window.showGrantSVIPModal = function(id, username, curLevel, curExpires) {
  const isSVIP = (curLevel || 0) > 0;
  const defaultDays = 30;
  showModal(isSVIP ? '管理 SVIP' : '授予 SVIP', `
    <div style="font-size:0.88rem;margin-bottom:0.8rem">为 <strong>${escHtml(username)}</strong> 设置 SVIP 身份</div>
    <div class="form-group">
      <label class="form-label">有效期</label>
      <select class="form-input" id="svip-duration" onchange="document.getElementById('svip-custom-wrap').style.display=this.value==='custom'?'block':'none'">
        <option value="30">30 天</option>
        <option value="90">90 天</option>
        <option value="180">180 天</option>
        <option value="365">365 天</option>
        <option value="forever">永久</option>
        <option value="custom">自定义天数…</option>
      </select>
    </div>
    <div id="svip-custom-wrap" class="form-group" style="display:none">
      <label class="form-label">自定义天数</label>
      <input type="number" min="1" class="form-input" id="svip-custom-days" placeholder="如 60" />
    </div>
    ${isSVIP && curExpires ? `<div class="form-hint">当前到期：${formatDate(curExpires)}</div>` : ''}
    ${isSVIP && !curExpires && curLevel>0 ? `<div class="form-hint">当前为永久 SVIP</div>` : ''}
    ${isSVIP ? `<div style="margin-top:1rem"><button class="btn btn-danger btn-sm" onclick="revokeSVIP('${id}', ${JSON.stringify(username)})">撤销 SVIP</button></div>` : ''}
  `, async () => {
    const sel = $('svip-duration')?.value || '30';
    let days = null;
    let forever = false;
    if (sel === 'forever') { forever = true; }
    else if (sel === 'custom') {
      const v = parseInt($('svip-custom-days')?.value || '0', 10);
      if (!v || v < 1) { toast('请输入有效天数', 'warn'); return false; }
      days = v;
    } else { days = parseInt(sel, 10) || defaultDays; }
    try {
      const body = { level: 1 };
      if (forever) {} else if (days) body.duration_days = days;
      await api.admin.grantSVIP(id, body);
      toast('SVIP 已更新', 'success');
      navigate('admin-accounts');
    } catch(e) { toast('失败：' + (e.message||''), 'error'); return false; }
  });
};

window.revokeSVIP = async function(id, username) {
  if (!confirm('确定撤销 ' + username + ' 的 SVIP 身份？')) return;
  try {
    await api.admin.grantSVIP(id, { level: 0 });
    toast('SVIP 已撤销', 'success');
    closeModal();
    navigate('admin-accounts');
  } catch(e) { toast('撤销失败：' + (e.message||''), 'error'); }
};

// v10：配额管理
window.showQuotaModal = function(id, username, curQuota, curTTL) {
  const qStr = curQuota === -1 ? 'unlimited' : (curQuota === 0 ? 'default' : 'custom');
  const ttlStr = curTTL === null || curTTL === undefined ? 'default' : (curTTL === 0 ? 'forever' : 'custom');
  showModal('配额管理', `
    <div style="font-size:0.88rem;margin-bottom:0.6rem">为 <strong>${escHtml(username)}</strong> 设置专属配额（0=跟随全局设置）</div>
    <div class="form-group">
      <label class="form-label">邮箱数量上限</label>
      <select class="form-input" id="quota-mode" onchange="document.getElementById('quota-custom').style.display=this.value==='custom'?'block':'none'">
        <option value="default" ${qStr==='default'?'selected':''}>跟随全局设置</option>
        <option value="unlimited" ${qStr==='unlimited'?'selected':''}>无限（SVIP 推荐）</option>
        <option value="custom" ${qStr==='custom'?'selected':''}>自定义…</option>
      </select>
    </div>
    <div id="quota-custom" class="form-group" style="display:${qStr==='custom'?'block':'none'}">
      <input type="number" min="1" class="form-input" id="quota-val" placeholder="如 50" value="${qStr==='custom'?curQuota:''}" />
    </div>
    <div class="form-group">
      <label class="form-label">邮箱保留时长</label>
      <select class="form-input" id="ttl-mode" onchange="document.getElementById('ttl-custom').style.display=this.value==='custom'?'block':'none'">
        <option value="default" ${ttlStr==='default'?'selected':''}>跟随全局（默认 30 分钟）</option>
        <option value="forever" ${ttlStr==='forever'?'selected':''}>永不过期</option>
        <option value="custom" ${ttlStr==='custom'?'selected':''}>自定义分钟数…</option>
      </select>
    </div>
    <div id="ttl-custom" class="form-group" style="display:${ttlStr==='custom'?'block':'none'}">
      <input type="number" min="1" class="form-input" id="ttl-val" placeholder="如 120（=2 小时）" value="${ttlStr==='custom'?curTTL:''}" />
    </div>
  `, async () => {
    const body = {};
    const qm = $('quota-mode').value;
    if (qm === 'default') body.mailbox_quota = 0;
    else if (qm === 'unlimited') body.mailbox_quota = -1;
    else {
      const v = parseInt($('quota-val').value || '0', 10);
      if (!v || v < 1) { toast('配额无效', 'warn'); return false; }
      body.mailbox_quota = v;
    }
    const tm = $('ttl-mode').value;
    if (tm === 'default') body.use_default_ttl = true;
    else if (tm === 'forever') body.mailbox_ttl_minutes = 0;
    else {
      const v = parseInt($('ttl-val').value || '0', 10);
      if (!v || v < 1) { toast('TTL 无效', 'warn'); return false; }
      body.mailbox_ttl_minutes = v;
    }
    try {
      await api.admin.setAccountQuota(id, body);
      toast('配额已更新', 'success');
      navigate('admin-accounts');
    } catch(e) { toast('失败：' + (e.message||''), 'error'); return false; }
  });
};

/** 根据账号数据渲染徽章（管理员/SVIP/正常/封禁） */
function renderAccountBadges(a) {
  const parts = [];
  if (a.is_admin) {
    parts.push('<span class="badge badge-gold">管理员</span>');
  } else {
    parts.push('<span class="badge badge-gray">普通用户</span>');
  }
  if ((a.svip_level || 0) > 0) {
    const expHint = a.svip_expires_at
      ? ` title="到期 ${formatDate(a.svip_expires_at)}"`
      : ' title="永久 SVIP"';
    parts.push(`<span class="svip-badge svip-badge-sm"${expHint}>SVIP</span>`);
  }
  return parts.join(' ');
}

async function renderAdminAccounts(container) {
  const page = state.adminAccountsPage || 1;
  const q = state.adminAccountsQ || '';
  const status = state.adminAccountsStatus || 'all';
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `
      <input class="form-input" id="admin-acc-q" placeholder="搜用户名或 Key" value="${escHtml(q)}"
        style="max-width:200px;padding:0.35rem 0.5rem;font-size:0.82rem" onkeydown="if(event.key==='Enter')adminAccSearch()" />
      <button class="btn btn-ghost btn-sm" onclick="adminAccSearch()">搜索</button>
      <button class="btn btn-primary btn-sm" onclick="showCreateAccountModal()" style="margin-left:0.3rem">+ 创建账户</button>
    `;
  }

  const res = await api.admin.listAccounts(page, 10, q, status);
  const accounts = res.data || [];
  const total = res.total ?? 0;
  const size = res.size || 10;
  const maxPage = Math.max(1, Math.ceil(total / size) || 1);

  const pills = [
    { k: 'all',    label: '全部' },
    { k: 'active', label: '正常' },
    { k: 'banned', label: '已封禁' },
    { k: 'svip',   label: 'SVIP' },
  ].map(o => `<button class="filter-pill ${status===o.k?'active':''}" onclick="adminAccSetStatus('${o.k}')">${o.label}</button>`).join('');

  container.innerHTML = `
    <div class="card" style="max-width:980px">
      <div class="card-header">
        <div class="card-title">👥 账户列表</div>
        <div style="font-size:0.78rem;color:var(--text-muted)">共 ${total} 个账户 · 每页 ${size} 条 · 第 ${page}/${maxPage} 页</div>
      </div>
      <div style="padding:0.7rem 1.3rem 0.4rem;border-bottom:1px solid var(--border-light)">
        <div class="filter-pills">${pills}</div>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr><th>用户名 / Key</th><th>角色 / 等级</th><th>状态</th><th>创建</th><th>最近活跃</th><th>操作</th></tr>
          </thead>
          <tbody>
            ${accounts.map(a => {
              const keyJs = JSON.stringify(a.api_key || '');
              const uJs = JSON.stringify(a.username||'');
              const svip = (a.svip_level || 0) > 0;
              const svipLbl = svip ? '管理 SVIP' : '授予 SVIP';
              return `
              <tr>
                <td>
                  <div style="font-weight:600">${escHtml(a.username || '—')}</div>
                  <div class="code-box" style="margin-top:0.3rem;font-size:0.72rem">
                    <span>${escHtml(a.api_key || '—')}</span>
                    <button type="button" class="copy-btn" onclick='copyText(${keyJs})'>⎘</button>
                  </div>
                </td>
                <td style="white-space:nowrap">${renderAccountBadges(a)}</td>
                <td>${a.is_active === false
                  ? '<span class="badge" style="background:var(--clr-danger);color:#fff">已封禁</span>'
                  : '<span class="badge badge-green">正常</span>'}</td>
                <td style="font-size:0.8rem">${formatDate(a.created_at)}</td>
                <td style="font-size:0.8rem">${formatLastSeen(a.last_seen_at)}</td>
                <td style="white-space:nowrap">
                  ${!a.is_admin && a.is_active ? `<button class="btn btn-ghost btn-sm" onclick='toggleBanAccount("${a.id}", ${uJs}, true)'>封禁</button>` : ''}
                  ${!a.is_admin && a.is_active === false ? `<button class="btn btn-success btn-sm" onclick='toggleBanAccount("${a.id}", ${uJs}, false)'>解除</button>` : ''}
                  ${!a.is_admin ? `<button class="btn ${svip?'btn-primary':'btn-ghost'} btn-sm" onclick='showGrantSVIPModal("${a.id}", ${uJs}, ${a.svip_level||0}, ${JSON.stringify(a.svip_expires_at||'')})'>✦ ${svipLbl}</button>` : ''}
                  ${!a.is_admin ? `<button class="btn btn-ghost btn-sm" onclick='showQuotaModal("${a.id}", ${uJs}, ${a.mailbox_quota||0}, ${a.mailbox_ttl_minutes === null || a.mailbox_ttl_minutes === undefined ? 'null' : a.mailbox_ttl_minutes})'>配额</button>` : ''}
                  ${!a.is_admin ? `<button class="btn btn-ghost btn-sm" onclick='adminRotateAccountKey("${a.id}", ${uJs})' title="重置该账户的 API Key，旧 Key 立即失效">⟳ Key</button>` : ''}
                  ${!a.is_admin ? `<button class="btn btn-danger btn-sm" onclick='confirmDeleteAccount("${a.id}", ${uJs})'>删除</button>` : ''}
                </td>
              </tr>`;
            }).join('')}
          </tbody>
        </table>
      </div>
      <div style="display:flex;gap:0.5rem;align-items:center;margin:1rem 1.3rem;flex-wrap:wrap">
        <button class="btn btn-ghost btn-sm" ${page <= 1 ? 'disabled' : ''} onclick="adminAccGoPage(${page - 1})">上一页</button>
        <span style="font-size:0.85rem;color:var(--text-muted)">第 ${page} / ${maxPage} 页</span>
        <button class="btn btn-ghost btn-sm" ${page >= maxPage ? 'disabled' : ''} onclick="adminAccGoPage(${page + 1})">下一页</button>
      </div>
    </div>
  `;
}

window.showCreateAccountModal = function() {
  showModal('创建账户', `
    <div class="form-group">
      <label class="form-label">用户名</label>
      <input class="form-input" id="new-acc-username" placeholder="username" />
    </div>
    <div class="form-group">
      <label class="form-label">
        <input type="checkbox" id="new-acc-admin" style="margin-right:0.4rem">
        设为管理员
      </label>
    </div>
  `, async () => {
    const username = ($('new-acc-username')?.value || '').trim();
    if (!username) { toast('请输入用户名', 'warn'); return false; }
    const is_admin = $('new-acc-admin')?.checked || false;
    try {
      await api.admin.createAccount({ username, is_admin });
      toast('账户已创建', 'success');
      navigate('admin-accounts');
    } catch(e) { toast('创建失败: ' + e.message, 'error'); return false; }
  });
};

window.confirmDeleteAccount = function(id, name) {
  showModal('删除账户', `<p>确定删除账户 <strong>${escHtml(name)}</strong>？</p>`, async () => {
    try {
      await api.admin.deleteAccount(id);
      toast('账户已删除', 'success');
      navigate('admin-accounts');
    } catch(e) { toast('删除失败: ' + e.message, 'error'); }
  });
};

// ─── Admin: 域名管理 ─────────────────────────────────────────
async function renderAdminDomains(container) {
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `
      <button class="btn btn-primary btn-sm" onclick="showAddDomainModal()">+ 手动添加</button>
      <button class="btn btn-success btn-sm" onclick="showMXRegisterModal()" style="margin-left:0.4rem">⚡ MX 自动注册</button>
    `;
  }

  const domains = await api.domains();
  const pending  = (domains||[]).filter(d => d.status === 'pending');
  const active   = (domains||[]).filter(d => d.status !== 'pending');

  container.innerHTML = `
    <div style="max-width:760px;display:flex;flex-direction:column;gap:1rem">
      ${pending.length > 0 ? `
        <div class="card" style="border-left:3px solid var(--clr-warn,#e6a817)">
          <div class="card-header">
            <div class="card-title">🔄 待 MX 验证 (${pending.length})</div>
            <div style="font-size:0.78rem;color:var(--text-muted)">后台每 30 秒自动检测，验证通过后自动加入域名池</div>
          </div>
          <div class="table-wrap">
            <table>
              <thead><tr><th>域名</th><th>上次检测</th><th>操作</th></tr></thead>
              <tbody id="pending-domains-tbody">
                ${pending.map(d => `
                  <tr id="pending-row-${d.id}">
                    <td style="font-family:var(--font-mono)">${escHtml(d.domain)}</td>
                    <td style="font-size:0.78rem">${d.mx_checked_at ? timeAgo(d.mx_checked_at) : '从未'}</td>
                    <td>
                      <span class="badge badge-gold" id="pending-status-${d.id}">⏳ 检测中</span>
                      <button class="btn btn-danger btn-sm" style="margin-left:0.4rem" onclick="confirmDeleteDomain(${d.id},'${escHtml(d.domain)}')">✕</button>
                    </td>
                  </tr>
                `).join('')}
              </tbody>
            </table>
          </div>
        </div>
      ` : ''}

      <div class="card">
        <div class="card-header">
          <div class="card-title">🌐 域名列表</div>
          <div style="font-size:0.78rem;color:var(--text-muted)">共 ${active.length} 个</div>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>域名</th><th>状态</th><th>操作</th></tr></thead>
            <tbody>
              ${active.length === 0 ? `<tr><td colspan="3" style="text-align:center;color:var(--text-muted)">暂无域名</td></tr>` :
                active.map(d => `
                  <tr>
                    <td style="font-family:var(--font-mono)">${escHtml(d.domain)}</td>
                    <td>${d.is_active
                      ? '<span class="badge badge-green">● 启用</span>'
                      : '<span class="badge badge-gray">○ 停用</span>'}</td>
                    <td style="display:flex;gap:0.5rem;align-items:center">
                      <button class="btn btn-ghost btn-sm" onclick="toggleDomain(${d.id},${!d.is_active})">${d.is_active ? '停用' : '启用'}</button>
                      <button class="btn btn-danger btn-sm" onclick="confirmDeleteDomain(${d.id},'${escHtml(d.domain)}')">删除</button>
                    </td>
                  </tr>
                `).join('')}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  `;

  // 如果有 pending 域名，开始轮询
  if (pending.length > 0) {
    startPendingDomainPoller(pending.map(d => d.id));
  }
}

window.showAddDomainModal = function() {
  const old = document.querySelector('.modal-overlay');
  if (old) old.remove();

  let serverIP = '';
  let serverHostname = '';
  api.publicSettings().then(s => {
    serverIP = s.smtp_server_ip || '';
    serverHostname = s.smtp_hostname || '';
    updateDnsHint();
  }).catch(() => {});

  const overlay = el('div', 'modal-overlay');
  overlay.innerHTML = `
    <div class="modal" style="max-width:580px">
      <div class="modal-title">添加域名</div>
      <button class="modal-close" onclick="this.closest('.modal-overlay').remove()">✕</button>

      <div id="add-step1">
        <div class="form-group" style="margin-bottom:0.5rem">
          <label class="form-label">域名</label>
          <input class="form-input" id="add-domain-inp" placeholder="example.com" autofocus />
          <div class="form-hint">输入将用于接收邮件的顶级域名</div>
        </div>
        <div id="add-dns-hint" style="background:var(--bg-secondary);border-radius:6px;padding:0.7rem 0.9rem;margin-bottom:0.8rem;font-size:0.8rem">
          <b>需要配置的 DNS 记录：</b>
          <table style="margin-top:0.5rem;width:100%;border-collapse:collapse;font-size:0.76rem">
            <thead><tr><th style="text-align:left;padding:2px 5px">类型</th><th style="text-align:left;padding:2px 5px">主机名</th><th style="text-align:left;padding:2px 5px">内容</th><th style="text-align:left;padding:2px 5px">优先级</th></tr></thead>
            <tbody id="add-dns-rows"></tbody>
          </table>
        </div>
        <div id="add-mx-result" style="display:none;margin-bottom:0.7rem"></div>
        <div class="modal-actions" id="add-actions">
          <button class="btn btn-ghost" onclick="this.closest('.modal-overlay').remove()">取消</button>
          <button class="btn btn-secondary" id="add-check-btn" onclick="doAddDomainCheck(false)">🔍 检测 MX</button>
          <button class="btn btn-primary"  id="add-force-btn" style="display:none" onclick="doAddDomainCheck(true)">⚡ 强制添加</button>
        </div>
      </div>

      <div id="add-step2" style="display:none"></div>
    </div>
  `;
  document.body.appendChild(overlay);
  overlay.addEventListener('click', e => { if (e.target === overlay) overlay.remove(); });

  const inp = overlay.querySelector('#add-domain-inp');
  inp?.addEventListener('keydown', e => { if (e.key === 'Enter') window.doAddDomainCheck(false); });
  inp?.addEventListener('input', updateDnsHint);

  function updateDnsHint() {
    const d = (inp?.value || '').trim() || 'example.com';
    const ip = serverIP || '&lt;服务器IP&gt;';
    const hn = serverHostname || 'mail.' + d;
    const hasHostname = !!serverHostname;
    const tbody = document.getElementById('add-dns-rows');
    if (!tbody) return;
    tbody.innerHTML = `
      <tr><td style="padding:2px 5px">MX</td><td style="padding:2px 5px;font-family:monospace">@</td><td style="padding:2px 5px;font-family:monospace">${escHtml(hn)}</td><td style="padding:2px 5px">10</td></tr>
      ${hasHostname ? '' : `<tr><td style="padding:2px 5px">A</td><td style="padding:2px 5px;font-family:monospace">mail.${escHtml(d)}</td><td style="padding:2px 5px;font-family:monospace">${escHtml(ip)}</td><td style="padding:2px 5px">—</td></tr>`}
      <tr><td style="padding:2px 5px">TXT</td><td style="padding:2px 5px;font-family:monospace">@</td><td style="padding:2px 5px;font-family:monospace">v=spf1 ip4:${escHtml(ip)} ~all</td><td style="padding:2px 5px">—</td></tr>
    `;
  }
  updateDnsHint();

  window.doAddDomainCheck = async function(force) {
    const domain = (inp?.value || '').trim().toLowerCase();
    if (!domain) { toast('请输入域名', 'warn'); return; }
    const checkBtn = document.getElementById('add-check-btn');
    const forceBtn = document.getElementById('add-force-btn');
    const resEl    = document.getElementById('add-mx-result');
    if (checkBtn) { checkBtn.disabled = true; checkBtn.textContent = '检测中...'; }

    try {
      if (force) {
        // 强制直接添加（跳过 MX 检测）
        const r = await api.admin.addDomain({ domain });
        showDnsInstructions(domain, r);
        overlay.remove();
        return;
      }

      // 先做 MX 检测（force:false）
      let r;
      try {
        r = await api.admin.mxImport({ domain, force: false });
        // MX 通过 → 已添加
        const step1 = document.getElementById('add-step1');
        const step2 = document.getElementById('add-step2');
        if (step1) step1.style.display = 'none';
        if (step2) {
          step2.style.display = 'block';
          step2.innerHTML = `
            <div style="text-align:center;padding:1.2rem 0">
              <div style="font-size:2rem">✅</div>
              <h3 style="margin:0.5rem 0">MX 验证通过</h3>
              <p style="font-size:0.84rem;color:var(--text-secondary)">域名 <strong>${escHtml(domain)}</strong> 已立即加入域名池</p>
              <button class="btn btn-primary" style="margin-top:1rem" onclick="this.closest('.modal-overlay').remove();navigate('admin-domains')">查看域名列表</button>
            </div>`;
        }
        toast('✓ ' + domain + ' MX 验证通过，已加入域名池', 'success');
      } catch(err) {
        // MX 未通过 → 提示强制添加选项
        if (checkBtn) { checkBtn.disabled = false; checkBtn.textContent = '🔍 检测 MX'; }
        if (forceBtn) forceBtn.style.display = '';
        if (resEl) {
          resEl.style.display = 'block';
          resEl.innerHTML = `
            <div style="background:var(--clr-warn-bg,#fff8e1);border:1px solid var(--clr-warn,#e6a817);border-radius:6px;padding:0.6rem 0.9rem;font-size:0.82rem">
              ⚠️ <b>MX 记录未检测到</b>：${escHtml(err.message)}<br>
              <span style="color:var(--text-muted)">请先配置上方 DNS 记录后重新检测，或点击「强制添加」跳过检测直接加入域名池</span>
            </div>`;
        }
      }
    } catch(e) {
      if (checkBtn) { checkBtn.disabled = false; checkBtn.textContent = '🔍 检测 MX'; }
      toast('操作失败: ' + e.message, 'error');
    }
  };
};

// \u5c55\u793a\u6dfb\u52a0\u57df\u540d\u540e\u7684 DNS \u914d\u7f6e\u6307\u5f15
function showDnsInstructions(domain, result) {
  const dns = result.dns_records || [];
  const rows = dns.map(r => `
    <tr>
      <td style="padding:3px 8px;font-weight:600">${escHtml(r.type)}</td>
      <td style="padding:3px 8px">${escHtml(r.host)}</td>
      <td style="padding:3px 8px;font-family:monospace;font-size:0.78rem">${escHtml(r.value)}</td>
      <td style="padding:3px 8px">${r.priority || '\u2014'}</td>
    </tr>`).join('');
  const old = document.querySelector('.modal-overlay');
  if (old) old.remove();
  const overlay = el('div', 'modal-overlay');
  overlay.innerHTML = `
    <div class="modal" style="max-width:600px">
      <div class="modal-title">\u2705 \u57df\u540d\u5df2\u6dfb\u52a0\uff1a${escHtml(domain)}</div>
      <p style="font-size:0.84rem;color:var(--text-secondary);margin:0.5rem 0 0.8rem">
        \u8bf7\u5728 DNS \u7ba1\u7406\u9762\u677f\u6dfb\u52a0\u4ee5\u4e0b\u8bb0\u5f55\uff0c\u4e00\u822c 5\u201330 \u5206\u949f\u751f\u6548\uff1a
      </p>
      <div class="table-wrap">
        <table>
          <thead><tr><th>\u7c7b\u578b</th><th>\u4e3b\u673a\u540d</th><th>\u5185\u5bb9</th><th>\u4f18\u5148\u7ea7</th></tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
      <p style="font-size:0.78rem;color:var(--text-muted);margin-top:0.6rem">\u2139\ufe0f ${escHtml(result.instructions || '')}</p>
      <div class="modal-actions">
        <button class="btn btn-primary" onclick="this.closest('.modal-overlay').remove();navigate('admin-domains')">
          \u5b8c\u6210\uff0c\u67e5\u770b\u57df\u540d\u5217\u8868
        </button>
      </div>
    </div>
  `;
  document.body.appendChild(overlay);
  overlay.addEventListener('click', e => { if (e.target === overlay) { overlay.remove(); navigate('admin-domains'); }});
}

window.toggleDomain = async function(id, newActive) {
  try {
    await api.admin.toggleDomain(id, newActive);
    toast('状态已切换', 'success');
    navigate('admin-domains');
  } catch(e) { toast('操作失败: ' + e.message, 'error'); }
};

window.confirmDeleteDomain = function(id, name) {
  showModal('删除域名', `<p>确定删除域名 <strong>${escHtml(name)}</strong>？</p>`, async () => {
    try {
      await api.admin.deleteDomain(id);
      toast('域名已删除', 'success');
      navigate('admin-domains');
    } catch(e) { toast('删除失败: ' + e.message, 'error'); }
  });
};

// ─── Admin: 系统设置 ─────────────────────────────────────────
async function renderAdminSettings(container) {
  let settings = {};
  try { settings = await api.admin.getSettings(); } catch {}

  const regOpen    = settings.registration_open === 'true' || settings.registration_open === true;
  const smtpIp      = settings.smtp_server_ip       || '';
  const smtpHostname = settings.smtp_hostname         || '';
  const siteTitle  = settings.site_title            || 'TempMail';
  const defDomain  = settings.default_domain        || '';
  const ttlMins    = settings.mailbox_ttl_minutes   || '30';
  const announce   = settings.announcement          || '';
  const annTitle   = settings.announcement_title    || '';
  const annLevel   = settings.announcement_level    || 'info';
  const maxMb      = settings.max_mailboxes_per_user|| '5';

  function inputRow(id, label, value, hint, placeholder = '', settingKey = '') {
    const key = settingKey || id.replace(/^input-/, '').replace(/-/g, '_');
    return `
      <div class="form-group">
        <label class="form-label">${label}</label>
        <div style="display:flex;gap:0.5rem">
          <input class="form-input" id="${id}" value="${escHtml(value)}" placeholder="${escHtml(placeholder)}" style="flex:1" />
          <button class="btn btn-primary btn-sm" onclick="saveSetting('${id}','${key}')">✓ 保存</button>
        </div>
        ${hint ? `<div class="form-hint">${hint}</div>` : ''}
      </div>`;
  }

  container.innerHTML = `
    <div class="card" style="max-width:640px">
      <div class="card-header"><div class="card-title">⚙ 系统设置</div></div>
      <div class="card-body" style="display:flex;flex-direction:column;gap:0.1rem">

        <!-- 注册开关 -->
        <div class="toggle-wrap" style="margin-bottom:0.5rem">
          <label class="toggle">
            <input type="checkbox" id="toggle-reg" ${regOpen ? 'checked' : ''} onchange="saveRegistrationSetting(this.checked)">
            <span class="toggle-slider"></span>
          </label>
          <div>
            <div class="toggle-label">开放自行注册</div>
            <span class="toggle-desc">开启后未登录用户可在登录页自行注册账户</span>
          </div>
        </div>
        <div class="divider"></div>

        <!-- 站点名称 -->
        ${inputRow('input-site-title', '站点名称', siteTitle, '保存后：浏览器标签标题、登录页大标题、侧栏 Logo 文案均来自 /public/settings 的 site_title；未设置时显示 TempMail', 'TempMail')}
        <div class="divider"></div>

        <!-- 公告 v10：标题 + 级别 + Markdown 正文 -->
        <div class="form-group">
          <label class="form-label">公告标题（可选）</label>
          <div style="display:flex;gap:0.5rem">
            <input class="form-input" id="input-announcement-title" value="${escHtml(annTitle)}" placeholder="如「12 月系统维护公告」" style="flex:1" />
            <button class="btn btn-primary btn-sm" onclick="saveSetting('input-announcement-title','announcement_title')">✓ 保存</button>
          </div>
        </div>
        <div class="form-group">
          <label class="form-label">公告级别</label>
          <div style="display:flex;gap:0.5rem">
            <select class="form-input" id="input-announcement-level" style="flex:1">
              <option value="info"    ${annLevel==='info'?'selected':''}>info（提示 · 蓝金色调）</option>
              <option value="success" ${annLevel==='success'?'selected':''}>success（成功 · 翠绿）</option>
              <option value="warn"    ${annLevel==='warn'?'selected':''}>warn（注意 · 琥珀）</option>
              <option value="danger"  ${annLevel==='danger'?'selected':''}>danger（警示 · 砖红）</option>
            </select>
            <button class="btn btn-primary btn-sm" onclick="saveSetting('input-announcement-level','announcement_level')">✓ 保存</button>
          </div>
          <div class="form-hint">不同级别显示不同的配色与图标动画</div>
        </div>
        <div class="form-group">
          <label class="form-label">公告正文（支持 Markdown）</label>
          <div style="display:flex;gap:0.5rem">
            <textarea class="form-input" id="input-announcement" rows="4" placeholder="留空则不显示公告。支持 **粗体** *斜体* \`行内代码\` [链接](https://example.com) 和换行" style="flex:1;resize:vertical">${escHtml(announce)}</textarea>
            <button class="btn btn-primary btn-sm" onclick="saveSetting('input-announcement','announcement')" style="align-self:flex-start">✓ 保存</button>
          </div>
          <div class="form-hint">展示在 Dashboard 顶部 · 可点击 × 关闭（内容变更后会重新弹出）</div>
        </div>
        <div class="divider"></div>

        <!-- SMTP IP -->
        ${inputRow('input-smtp-ip', 'SMTP 服务器公网 IP', smtpIp, '用于生成 SPF DNS 配置提示', '0.0.0.0', 'smtp_server_ip')}
        <div class="divider"></div>

        <!-- SMTP Hostname -->
        ${inputRow('input-smtp-hostname', '邮件服务器主机名', smtpHostname, '用作 MX 记录目标（如 mail.yourdomain.com）。设置后用户添加域名只需一条 MX 记录，无需额外 A 记录。', 'mail.yourdomain.com', 'smtp_hostname')}
        <div class="divider"></div>

        <!-- 默认邮箱域名 -->
        ${inputRow('input-default-domain', '默认邮箱域名', defDomain, '创建邮箱时下拉框优先选中的域名', 'mail.example.com')}
        <div class="divider"></div>

        <!-- 邮箱 TTL -->
        ${inputRow('input-mailbox-ttl-minutes', '邮箱有效期（分钟）', ttlMins, '新建邮箱的默认存活时间，0 = 永不过期', '30')}
        <div class="divider"></div>

        <!-- 每用户邮箱上限 -->
        ${inputRow('input-max-mailboxes-per-user', '每账户邮箱上限', maxMb, '每个账户同时存在的邮箱数量上限', '5')}
        <div class="divider"></div>

        <!-- 服务信息 -->
        <div style="font-size:0.82rem;color:var(--text-secondary)">
          <strong>服务信息</strong>
          <p style="margin-top:0.5rem;line-height:2">
            SMTP IP:&nbsp;<code>${escHtml(smtpIp||'<未设置>')}</code><br>
            邮件主机名:&nbsp;<code>${escHtml(smtpHostname||'<未设置>')}</code><br>
            API:&nbsp;<code>${window.location.origin}/api</code><br>
            前端:&nbsp;<code>${window.location.origin}</code>
          </p>
        </div>
        <div class="divider"></div>

        <!-- 管理员 Key -->
        <div>
          <div class="form-label">管理员 API Key</div>
          <div class="code-box" style="font-size:0.78rem">
            <span style="filter:blur(4px);cursor:pointer" onclick="this.style.filter='none'">${escHtml(state.apiKey)}</span>
            <button class="copy-btn" onclick="copyText('${escHtml(state.apiKey)}')">⎘</button>
          </div>
          <div class="form-hint">Key 文件位置：<code>/data/admin.key</code>（API 服务容器内）</div>
        </div>

      </div>
    </div>
  `;
}

// 通用保存
window.saveSetting = async function(inputId, settingKey) {
  const el2 = document.getElementById(inputId);
  const val = el2 ? (el2.tagName === 'TEXTAREA' ? el2.value : el2.value.trim()) : '';
  try {
    await api.admin.saveSettings({ [settingKey]: val });
    // server_ip / hostname 由后台 goroutine 的 DB 优先逻辑热更新，30 秒内对下一轮 MX 巡检生效
    if (settingKey === 'smtp_server_ip' || settingKey === 'smtp_hostname') {
      toast('已保存，下次 MX 巡检（30 秒内）自动使用新值', 'success');
    } else {
      toast('已保存', 'success');
    }
    if (settingKey === 'site_title') {
      state.siteTitle = val.trim() ? val.trim() : 'TempMail';
      applySiteBranding();
    }
  } catch(e) { toast('保存失败: ' + e.message, 'error'); }
};

// 兼容旧调用
window.saveSmtpIp = async function() { await window.saveSetting('input-smtp-ip', 'smtp_server_ip'); };

window.saveRegistrationSetting = async function(enabled) {
  try {
    await api.admin.saveSettings({ registration_open: enabled ? 'true' : 'false' });
    toast(`注册已${enabled ? '开启' : '关闭'}`, 'success');
  } catch(e) {
    toast('保存失败: ' + e.message, 'error');
    const cb = $('toggle-reg');
    if (cb) cb.checked = !enabled;
  }
};

// ─── Modal ────────────────────────────────────────────────
function showModal(title, bodyHtml, onConfirm) {
  const old = document.querySelector('.modal-overlay');
  if (old) old.remove();

  const overlay = el('div', 'modal-overlay');
  overlay.innerHTML = `
    <div class="modal">
      <div class="modal-title">${escHtml(title)}</div>
      <button class="modal-close" onclick="this.closest('.modal-overlay').remove()">✕</button>
      ${bodyHtml}
      <div class="modal-actions">
        <button class="btn btn-ghost" onclick="this.closest('.modal-overlay').remove()">取消</button>
        <button class="btn btn-primary" id="modal-confirm-btn">确认</button>
      </div>
    </div>
  `;
  document.body.appendChild(overlay);
  overlay.addEventListener('click', e => { if (e.target === overlay) overlay.remove(); });

  const confirmBtn = overlay.querySelector('#modal-confirm-btn');
  confirmBtn.addEventListener('click', async () => {
    confirmBtn.disabled = true;
    const result = await onConfirm();
    if (result !== false) overlay.remove();
    else confirmBtn.disabled = false;
  });
}

window.closeModal = function() {
  document.querySelectorAll('.modal-overlay').forEach(o => o.remove());
};

// ─── MX 自动注册（全自动验证流程）──────────────────────────
// 轮询待验证域名状态
let _pendingPollerTimer = null;
let _inboxPollerTimer   = null;
function clearInboxPoller() {
  if (_inboxPollerTimer) { clearInterval(_inboxPollerTimer); _inboxPollerTimer = null; }
}
function startPendingDomainPoller(ids) {
  if (!ids || ids.length === 0) return;
  clearInterval(_pendingPollerTimer);
  const remaining = new Set(ids);
  _pendingPollerTimer = setInterval(async () => {
    for (const id of [...remaining]) {
      try {
        const d = await api.getDomainStatus(id); // 使用非管理员接口
        const statusEl = document.getElementById('pending-status-' + id);
        const rowEl    = document.getElementById('pending-row-'   + id);
        if (d.status === 'active') {
          if (statusEl) statusEl.innerHTML = '<span class="badge badge-green">✓ 已激活</span>';
          remaining.delete(id);
          toast(`✓ 域名 ${d.domain} MX验证通过，已加入域名池`, 'success');
          setTimeout(() => { if (rowEl) rowEl.remove(); }, 3000);
        } else if (statusEl) {
          const ago = d.mx_checked_at ? timeAgo(d.mx_checked_at) : '从未';
          statusEl.innerHTML = `<span class="badge badge-gold">⏳ 检测中（上次${ago}）</span>`;
        }
      } catch {}
    }
    if (remaining.size === 0) clearInterval(_pendingPollerTimer);
  }, 5000);
}

window.showMXRegisterModal = function() {
  const old = document.querySelector('.modal-overlay');
  if (old) old.remove();
  const overlay = el('div', 'modal-overlay');
  overlay.innerHTML = `
    <div class="modal" style="max-width:560px">
      <div class="modal-title">⚡ MX 自动注册域名</div>
      <button class="modal-close" onclick="this.closest('.modal-overlay').remove()">✕</button>
      <p style="font-size:0.82rem;color:var(--text-secondary);margin:0.5rem 0 0.8rem">
        提交域名后系统立即检测 MX 记录。若已配置则直接激活；
        否则进入待验证队列，后台每 <b>30 秒</b>自动重试，无需手动确认。
      </p>
      <div class="form-group">
        <label class="form-label">域名（如 example.com）</label>
        <input class="form-input" id="mxr-domain" placeholder="example.com" autofocus />
      </div>
      <div id="mxr-dns-hint" style="display:none;background:var(--bg-secondary);border-radius:6px;padding:0.7rem 0.9rem;margin-bottom:0.6rem;font-size:0.8rem">
        <b>请在 DNS 管理面板添加以下记录：</b>
        <table style="margin-top:0.5rem;width:100%;border-collapse:collapse;font-size:0.76rem">
          <thead><tr><th style="text-align:left">类型</th><th style="text-align:left">主机名</th><th style="text-align:left">内容</th><th style="text-align:left">优先级</th></tr></thead>
          <tbody id="mxr-dns-rows"></tbody>
        </table>
      </div>
      <div id="mxr-status" style="display:none;margin-bottom:0.7rem"></div>
      <div class="modal-actions" id="mxr-actions">
        <button class="btn btn-ghost" onclick="this.closest('.modal-overlay').remove()">取消</button>
        <button class="btn btn-primary" id="mxr-submit">提交检测</button>
      </div>
    </div>
  `;
  document.body.appendChild(overlay);
  overlay.addEventListener('click', e => { if (e.target === overlay) overlay.remove(); });

  // 实时更新 DNS 提示
  const inp = overlay.querySelector('#mxr-domain');
  inp?.addEventListener('keydown', e => { if (e.key === 'Enter') submitMXRegister(); });

  overlay.querySelector('#mxr-submit').addEventListener('click', submitMXRegister);

  async function submitMXRegister() {
    const domain = (inp?.value || '').trim().toLowerCase();
    if (!domain) { toast('请输入域名', 'warn'); return; }
    const btn    = overlay.querySelector('#mxr-submit');
    const status = overlay.querySelector('#mxr-status');
    const hint   = overlay.querySelector('#mxr-dns-hint');
    btn.disabled = true;
    btn.textContent = '检测中...';
    status.style.display = 'none';

    const domainListPage = state.account?.is_admin ? 'admin-domains' : 'domains-guide';
    try {
      const r = await api.submitDomain({ domain }); // 任意已登录用户可用
      if (r.status === 'active') {
        overlay.innerHTML = `
          <div class="modal" style="text-align:center;padding:2rem">
            <div style="font-size:2rem">✅</div>
            <h3 style="margin:0.5rem 0">MX 验证通过</h3>
            <p style="font-size:0.84rem;color:var(--text-secondary)">域名 <strong>${escHtml(domain)}</strong> 已立即加入域名池</p>
            <button class="btn btn-primary" style="margin-top:1rem" onclick="this.closest('.modal-overlay').remove();navigate('${domainListPage}')">查看域名列表</button>
          </div>
        `;
        toast(`✓ ${domain} 已激活`, 'success');
      } else {
        // pending — 显示 DNS 配置 + 等待提示
        const rows = (r.dns_required || []).map(rec =>
          `<tr><td>${escHtml(rec.type)}</td><td style="font-family:monospace">${escHtml(rec.host)}</td><td style="font-family:monospace">${escHtml(rec.value)}</td><td>${rec.priority || '—'}</td></tr>`
        ).join('');
        overlay.querySelector('#mxr-dns-rows').innerHTML = rows;
        hint.style.display = 'block';

        status.style.display = 'block';
        status.innerHTML = `
          <div style="background:var(--clr-warn-bg,#fff8e1);border:1px solid var(--clr-warn,#e6a817);border-radius:6px;padding:0.6rem 0.9rem;font-size:0.81rem">
            ⏳ <b>域名已加入验证队列（ID ${r.domain.id}）</b><br>
            MX 记录配置生效后（通常 5-30 分钟），系统将自动激活。<br>
            <span style="color:var(--text-muted)">此窗口关闭后可在「域名列表」页查看验证进度</span>
          </div>
        `;
        const actionsEl = overlay.querySelector('#mxr-actions');
        actionsEl.innerHTML = `<button class="btn btn-primary" onclick="this.closest('.modal-overlay').remove();navigate('${domainListPage}')">前往域名列表查看进度</button>`;

        // 开始在当前 overlay 内轮询
        startInlinePoller(r.domain.id, domain, overlay);
      }
    } catch(e) {
      btn.disabled = false;
      btn.textContent = '重新提交';
      status.style.display = 'block';
      status.innerHTML = `<div style="color:var(--clr-danger);font-size:0.82rem">❌ ${escHtml(e.message)}</div>`;
    }
  }

  async function startInlinePoller(domainId, domainName, modal) {
    const statusEl = modal.querySelector('#mxr-status');
    let attempts = 0;
    const timer = setInterval(async () => {
      attempts++;
      if (!document.body.contains(modal)) { clearInterval(timer); return; }
      try {
        const d = await api.getDomainStatus(domainId); // 非管理员接口
        if (d.status === 'active') {
          clearInterval(timer);
          if (statusEl) statusEl.innerHTML = `
            <div style="background:#e8f5e9;border:1px solid #4caf50;border-radius:6px;padding:0.6rem 0.9rem;font-size:0.81rem">
              ✅ <b>MX 验证通过！域名 ${escHtml(domainName)} 已自动激活。</b>
            </div>`;
          toast(`✓ ${domainName} 已自动激活`, 'success');
          setTimeout(() => { modal.remove(); navigate(state.account?.is_admin ? 'admin-domains' : 'domains-guide'); }, 2500);
        } else if (statusEl) {
          const ago = d.mx_checked_at ? timeAgo(d.mx_checked_at) : '从未';
          statusEl.innerHTML = `
            <div style="background:var(--clr-warn-bg,#fff8e1);border:1px solid var(--clr-warn,#e6a817);border-radius:6px;padding:0.6rem 0.9rem;font-size:0.81rem">
              ⏳ 等待中（第 ${attempts} 次检测，上次 ${ago}）…
            </div>`;
        }
      } catch {}
    }, 5000);
  }
};

// ─── API 文档 ─────────────────────────────────────────
function renderApiDocs(container) {
  const key = state.apiKey || 'YOUR_API_KEY';
  const base = window.location.origin;
  const sections = [
    {
      title: '🔐 认证方式',
      desc: '仅 /api/* 下的接口需要 API Key（管理员接口须使用管理员 Key）。不支持 X-API-Key 头，请用下面两种方式之一：',
      code: `# Bearer（推荐）
curl -H "Authorization: Bearer ${key}" ${base}/api/me

# Query
curl "${base}/api/me?api_key=${key}"`,
    },
    {
      title: '🌐 GET /public/settings（无需登录）',
      desc: '返回站点名称 site_title、是否开放注册 registration_open、公告 announcement、SMTP 提示字段等。前端启动时用它刷新浏览器标题与登录页/侧栏站点名；后台「系统设置」里修改站点名称并保存后会通过此接口生效。',
      code: `curl -s ${base}/public/settings | python3 -m json.tool

# 字段说明：
#   site_title      — 站点显示名（空则前端默认 TempMail）
#   registration_open — 是否允许 POST /public/register
#   announcement    — 登录后 Dashboard 顶部公告`,
    },
    {
      title: '📝 POST /public/register（无需登录）',
      desc: '仅创建平台账号并返回 api_key，不会自动创建邮箱。须先在后台开启「开放自行注册」。注册成功后用户需自行 POST /api/mailboxes 创建邮箱，或由管理员代为建号。',
      code: `curl -s -X POST ${base}/public/register \\
  -H "Content-Type: application/json" \\
  -d '{"username":"new_user_01"}'

# 成功 201：id, username, api_key, message
# 失败：403 注册已关闭 / 409 用户名已存在`,
    },
    {
      title: '🛡 POST /api/admin/accounts（须管理员 Key）',
      desc: '管理员创建用户。可选字段 mailbox_domain：填写已激活的域名（如 yourdomain.com）时，会在该域名下自动创建一个「随机本地部分@域名」邮箱；若地址与全局已有邮箱冲突会自动换随机串重试。不传 mailbox_domain 则只建账号，不建邮箱。',
      code: `# 仅账号
curl -s -X POST ${base}/api/admin/accounts \\
  -H "Authorization: Bearer <管理员API_Key>" \\
  -H "Content-Type: application/json" \\
  -d '{"username":"buyer_001"}'

# 账号 + 自动分配邮箱（随机前缀 @ 指定域名）
curl -s -X POST ${base}/api/admin/accounts \\
  -H "Authorization: Bearer <管理员API_Key>" \\
  -H "Content-Type: application/json" \\
  -d '{"username":"buyer_002","mailbox_domain":"yahoohh.chat"}'

# 成功 201：id, username, api_key；若带了 mailbox_domain 且成功则另有 mailbox 对象（含 full_address 等）
# 域名无效 400；用户名冲突 409`,
    },
    {
      title: '📫 1. 创建临时邮箱',
      desc: 'POST /api/mailboxes — address 和 domain 均为可选字段',
      code: `# 随机地址 + 随机域名
curl -s -X POST ${base}/api/mailboxes \\
  -H "Authorization: Bearer ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{}'

# 指定本地部分（@ 之前），域名随机
curl -s -X POST ${base}/api/mailboxes \\
  -H "Authorization: Bearer ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{"address": "mytestbox"}'

# 指定域名，地址随机（domain 须是已激活域名）
curl -s -X POST ${base}/api/mailboxes \\
  -H "Authorization: Bearer ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{"domain": "example.com"}'

# 同时指定地址和域名
curl -s -X POST ${base}/api/mailboxes \\
  -H "Authorization: Bearer ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{"address": "mytestbox", "domain": "example.com"}'

# 错误码：
#   400 → domain 不存在或未激活
#   409 → 地址已被占用（换一个 address 或留空让系统随机生成）
#   503 → 系统内无可用域名`,
    },
    {
      title: '📌 2. 获取邮箱列表',
      desc: 'GET /api/mailboxes — 获取当前账号下所有邮箱',
      code: `curl -s ${base}/api/mailboxes \\
  -H "Authorization: Bearer ${key}"

# 分页
 curl -s "${base}/api/mailboxes?page=1&size=20" \\
  -H "Authorization: Bearer ${key}"`,
    },
    {
      title: '📥 3. 获取邮箱收件箱（邮件列表）',
      desc: 'GET /api/mailboxes/:id/emails — 按收件时间倒序列出邮件摘要',
      code: `MAILBOX_ID="你的邮箱UUID"
curl -s ${base}/api/mailboxes/$MAILBOX_ID/emails \\
  -H "Authorization: Bearer ${key}"

# 分页
curl -s "${base}/api/mailboxes/$MAILBOX_ID/emails?page=1&size=20" \\
  -H "Authorization: Bearer ${key}"`,
    },
    {
      title: '📝 4. 读取单封邮件',
      desc: 'GET /api/mailboxes/:id/emails/:email_id — 获取邮件完整内容（含 HTML/纯文本和原始数据）',
      code: `MAILBOX_ID="你的邮箱UUID"
EMAIL_ID="你的邮件UUID"
curl -s ${base}/api/mailboxes/$MAILBOX_ID/emails/$EMAIL_ID \\
  -H "Authorization: Bearer ${key}"`,
    },
    {
      title: '🗑 5. 删除邮箱',
      desc: 'DELETE /api/mailboxes/:id — 立即删除邮箱及其所有邮件',
      code: `MAILBOX_ID="你的邮箱UUID"
curl -s -X DELETE ${base}/api/mailboxes/$MAILBOX_ID \\
  -H "Authorization: Bearer ${key}"`,
    },
    {
      title: '🗑 6. 删除单封邮件',
      desc: 'DELETE /api/mailboxes/:id/emails/:email_id',
      code: `curl -s -X DELETE ${base}/api/mailboxes/$MAILBOX_ID/emails/$EMAIL_ID \\
  -H "Authorization: Bearer ${key}"`,
    },
    {
      title: '🧪 7. 完整自动化示例（Shell 脚本）',
      desc: '创建邮箱 → 等待 5 秒 → 读取邮件 → 清理',
      code: `#!/bin/bash
BASE="${base}"
KEY="${key}"

# 1. 创建临时邮箱
MB=$(curl -s -X POST $BASE/api/mailboxes \\
  -H "Authorization: Bearer $KEY" \\
  -H "Content-Type: application/json" \\
  -d '{}')
MB_ID=$(echo $MB | python3 -c "import sys,json; print(json.load(sys.stdin)['mailbox']['id'])")
MB_ADDR=$(echo $MB | python3 -c "import sys,json; print(json.load(sys.stdin)['mailbox']['full_address'])")
echo "✓ 邮箱: $MB_ADDR (主键: $MB_ID)"

# 2. 向邮箱发送邮件...
echo "将测试邮件发到: $MB_ADDR"
sleep 5

# 3. 查看收件箱
EMAILS=$(curl -s $BASE/api/mailboxes/$MB_ID/emails \\
  -H "Authorization: Bearer $KEY")
echo "取到邮件: $EMAILS" | python3 -m json.tool

# 4. 读取第一封邮件（收件箱）
EMAIL_ID=$(echo $EMAILS | python3 -c "import sys,json;d=json.load(sys.stdin);print(d['data'][0]['id']) if d.get('data') else print('')" 2>/dev/null)
if [ -n "$EMAIL_ID" ]; then
  curl -s $BASE/api/mailboxes/$MB_ID/emails/$EMAIL_ID \\
    -H "Authorization: Bearer $KEY" | python3 -m json.tool
fi

# 5. 删除邮箱
curl -s -X DELETE $BASE/api/mailboxes/$MB_ID \\
  -H "Authorization: Bearer $KEY"
echo "✓ 邮箱已删除"`,
    },
    {
      title: '📈 8. 并发压测示例（wrk）',
      desc: '对注册接口进行高并发压测，500 并发，持续 30 秒',
      code: `# 安装 wrk: apt install wrk

# 导出注册脚本
cat > /tmp/register.lua << 'EOF'
wrk.method = "POST"
wrk.body   = '{"username": "user_' .. math.random(100000,999999) .. '"}'
wrk.headers["Content-Type"] = "application/json"
EOF

# 运行压测
wrk -t 10 -c 500 -d 30s --script /tmp/register.lua \\
  ${base}/public/register

# 或使用 k6
cat > /tmp/test.js << 'EOF'
import http from 'k6/http';
import { check } from 'k6';
export const options = { vus: 500, duration: '30s' };
const KEY = '${key}';
export default function() {
  const r = http.post(
    '${base}/api/mailboxes',
    '{}',
    { headers: { 'Authorization': 'Bearer ' + KEY, 'Content-Type': 'application/json' }}
  );
  check(r, { '创建成功': r => r.status === 201 });
}
EOF
k6 run /tmp/test.js`,
    },
  ];

  container.innerHTML = `
    <div style="max-width:860px">
      <div style="margin-bottom:1.2rem;padding:0.8rem 1rem;background:var(--bg-secondary);border-radius:8px;font-size:0.82rem">
        🔑 当前 API Key：
        <code style="margin-left:0.5rem;filter:blur(3px);cursor:pointer" onclick="this.style.filter='none'">${escHtml(key)}</code>
        <button class="copy-btn" onclick="copyText('${escHtml(key)}')" title="复制">⎘</button>
      </div>
      ${sections.map((s,i) => `
        <div class="card" style="margin-bottom:1rem">
          <div class="card-header"><div class="card-title">${escHtml(s.title)}</div></div>
          <div class="card-body">
            <p style="font-size:0.82rem;color:var(--text-secondary);margin-bottom:0.6rem">${escHtml(s.desc)}</p>
            <div class="code-box" style="white-space:pre;overflow-x:auto;font-size:0.75rem;line-height:1.6;position:relative">
              <button class="copy-btn" style="position:absolute;top:6px;right:6px" onclick="copyText(${JSON.stringify(s.code)})" title="复制">⎘</button>
              ${escHtml(s.code)}
            </div>
          </div>
        </div>
      `).join('')}
    </div>
  `;
}

// ─── Claude 自助购号（用户）──────────────────────────────────
function shopBuildQrBlockFromUrls(wechatUrl, alipayUrl) {
  let h = '';
  if (wechatUrl) {
    h += `<div><div class="form-hint" style="margin-bottom:0.3rem">微信收款</div>
      <img class="shop-qr-img" src="${escHtml(wechatUrl)}" alt="微信收款码" loading="lazy" /></div>`;
  }
  if (alipayUrl) {
    h += `<div><div class="form-hint" style="margin-bottom:0.3rem">支付宝收款</div>
      <img class="shop-qr-img" src="${escHtml(alipayUrl)}" alt="支付宝收款码" loading="lazy" /></div>`;
  }
  if (!wechatUrl && !alipayUrl) {
    h += `<p style="color:var(--clr-warn);font-size:0.85rem">管理员尚未上传收款码，请联系店主。</p>`;
  }
  return h;
}

/**
 * v10：按 delivery_type 渲染发货凭证卡片
 *   card_key  → 邮箱 + Key
 *   text      → 大号长文本
 *   custom_kv → 多字段（payload 对象）
 */
function renderShopDeliveryCard(ln) {
  const dt = ln.delivery_type || 'card_key';
  if (dt === 'card_key') {
    const em = String(ln.email || '');
    const ky = String(ln.api_key || '');
    return `<div class="shop-credential-card">
      <span class="shop-credential-label">邮箱</span>
      <div class="shop-credential-value-row">
        <span class="shop-credential-value" dir="ltr">${escHtml(em)}</span>
        <button type="button" class="btn btn-ghost btn-sm shop-credential-copy" onclick='copyText(${JSON.stringify(em)})'>复制</button>
      </div>
      <span class="shop-credential-label" style="margin-top:0.9rem;display:block">登录 Key</span>
      <div class="shop-credential-value-row">
        <span class="shop-credential-value" dir="ltr">${escHtml(ky)}</span>
        <button type="button" class="btn btn-ghost btn-sm shop-credential-copy" onclick='copyText(${JSON.stringify(ky)})'>复制</button>
      </div>
    </div>`;
  }
  if (dt === 'text') {
    const text = (ln.payload && typeof ln.payload.text === 'string') ? ln.payload.text : '';
    return `<div class="shop-credential-card">
      <span class="shop-credential-label">发货内容</span>
      <div class="shop-credential-value-row">
        <span class="shop-credential-value multi" style="white-space:pre-wrap">${escHtml(text)}</span>
        <button type="button" class="btn btn-ghost btn-sm shop-credential-copy" onclick='copyText(${JSON.stringify(text)})'>复制</button>
      </div>
    </div>`;
  }
  if (dt === 'custom_kv') {
    const p = ln.payload || {};
    const keys = Object.keys(p);
    const items = keys.map(k => {
      const v = String(p[k] ?? '');
      const multi = v.includes('\n');
      return `<div class="payload-item">
        <span class="payload-label">${escHtml(k)}</span>
        <div class="payload-value ${multi?'multi':''}">
          <span>${escHtml(v)}</span>
          <button type="button" class="btn btn-ghost btn-sm shop-credential-copy" onclick='copyText(${JSON.stringify(v)})'>复制</button>
        </div>
      </div>`;
    }).join('');
    return `<div class="shop-credential-card">
      <span class="shop-credential-label">发货内容</span>
      <div class="shop-payload-kv">${items || '<em style="color:var(--text-muted)">（空）</em>'}</div>
    </div>`;
  }
  return '';
}

async function buildClaudeShopHighlightHtml() {
  if (!state.claudeHighlightOrderId) return '';
  try {
    const d = await api.shopGetOrder(state.claudeHighlightOrderId);
    const o = d.order;
    const pay = d.payment || {};
    const totalY = (o.total_cents / 100).toFixed(2);
    let body = '';
    if (o.status === 'fulfilled' && (o.lines || []).length) {
      body += `<div class="shop-deliver-banner" role="status">⚠️ 请立即复制保存以下内容；页面关闭后仍可在「我的订单」中查看。</div>`;
      body += (o.lines || []).map(ln => renderShopDeliveryCard(ln)).join('');
    } else if (o.payment_channel === 'alipay_precreate') {
      body += `<p style="font-size:0.95rem;margin-bottom:0.5rem;font-weight:700">订单号 <code style="font-size:0.85rem">${escHtml(o.id)}</code></p>`;
      body += `<p style="font-size:0.95rem;margin-bottom:0.6rem">订单应付：<strong style="color:var(--clr-primary)">¥${totalY}</strong>（数量 ${o.quantity}）</p>`;
      body += `<p style="font-size:0.8rem;color:var(--text-muted);margin-bottom:0.8rem">本单为 <strong>支付宝当面付</strong>：请打开支付宝完成支付。支付成功后系统将<strong>自动发货</strong>（若本页未刷新，请到「我的订单」查看）。</p>`;
      if (pay.hint) {
        body += `<p style="font-size:0.78rem;color:var(--clr-warn);margin-bottom:0.6rem">${escHtml(pay.hint)}</p>`;
      }
      if (pay.tutorial_url) {
        body += `<p style="margin-top:0.5rem"><a class="shop-tutorial-link" href="${escHtml(pay.tutorial_url)}" target="_blank" rel="noopener">📘 使用教程点我！！</a></p>`;
      }
    } else {
      body += `<p style="font-size:0.95rem;margin-bottom:0.5rem;font-weight:700">订单号 <code style="font-size:0.85rem">${escHtml(o.id)}</code></p>`;
      body += `<p style="font-size:0.95rem;margin-bottom:0.6rem">订单应付：<strong style="color:var(--clr-primary)">¥${totalY}</strong>（数量 ${o.quantity}）</p>`;
      body += `<p style="font-size:0.8rem;color:var(--text-muted);margin-bottom:0.8rem">请使用微信或支付宝扫描下方静态收款码支付对应金额，支付完成后等待管理员确认。</p>`;
      body += `<div class="shop-qr-row">`;
      body += shopBuildQrBlockFromUrls(pay.wechat_qr_url, pay.alipay_qr_url);
      body += `</div>`;
      if (pay.tutorial_url) {
        body += `<p style="margin-top:0.5rem"><a class="shop-tutorial-link" href="${escHtml(pay.tutorial_url)}" target="_blank" rel="noopener">📘 使用教程点我！！</a></p>`;
      }
    }
    return `
      <div class="card shop-order-highlight" style="max-width:720px;margin-bottom:1rem;border-left:4px solid var(--clr-accent)">
        <div class="card-header" style="align-items:flex-start">
          <div>
            <div class="card-title">订单详情</div>
            <div style="font-size:0.75rem;color:var(--text-muted);margin-top:0.2rem">#${escHtml(o.id)}</div>
          </div>
          <button type="button" class="btn btn-ghost btn-sm" onclick="claudeShopDismissHighlight()">收起</button>
        </div>
        <div class="card-body">${body}</div>
      </div>`;
  } catch {
    return '';
  }
}

function claudeShopGetSelectedProduct(summary) {
  const prods = summary.products || [];
  if (!summary.product_pick_required || !prods.length) return null;
  const id = state.claudeSelectedProductId;
  return prods.find(p => p.id === id) || null;
}

window.claudeShopAckDefaultProduct = function() {
  state.claudeShopDefaultAck = true;
  document.querySelectorAll('.shop-product-card[data-default-product="1"]').forEach(card => {
    card.classList.add('shop-product-card--selected');
  });
  claudeShopPriceRefresh();
};

window.claudeShopSelectProduct = function(id) {
  state.claudeSelectedProductId = id;
  document.querySelectorAll('.shop-product-card[data-product-id]').forEach(card => {
    card.classList.toggle('shop-product-card--selected', card.getAttribute('data-product-id') === id);
  });
  claudeShopPriceRefresh();
};

/** v10：当前账户是否 SVIP（用于 SVIP 专享价展示） */
function currentAccountIsSVIP() {
  const a = state.account;
  if (!a || (a.svip_level || 0) <= 0) return false;
  if (a.svip_expires_at && new Date(a.svip_expires_at) < new Date()) return false;
  return true;
}

window.claudeShopPriceRefresh = function() {
  const s = state._claudeShopSummary;
  if (!s) return;
  const qty = Math.max(1, Math.min(999, parseInt($('claude-shop-qty')?.value || '1', 10) || 1));
  const picked = claudeShopGetSelectedProduct(s);
  const isSVIP = currentAccountIsSVIP();
  let minW, retailY, wholesaleY, svipY;
  if (picked) {
    minW = picked.wholesale_min_qty || 5;
    retailY = Number(picked.retail_price_yuan);
    wholesaleY = Number(picked.wholesale_price_yuan);
    svipY = (picked.svip_price_yuan !== undefined && picked.svip_price_yuan !== null)
      ? Number(picked.svip_price_yuan) : null;
  } else {
    minW = s.wholesale_min_qty || 5;
    retailY = Number(s.retail_price_yuan);
    wholesaleY = Number(s.wholesale_price_yuan);
    svipY = null;
  }
  const isWs = qty >= minW;
  let unit = isWs ? wholesaleY : retailY;
  let discountSource = null; // 'svip' | 'wholesale' | null
  if (isSVIP && svipY !== null && svipY >= 0 && svipY < unit) {
    unit = svipY;
    discountSource = 'svip';
  } else if (isWs) {
    discountSource = 'wholesale';
  }
  const total = unit * qty;

  // 应用优惠券（若已选）
  let couponDiscount = 0;
  const ucid = $('claude-shop-coupon-select')?.value || '';
  const availCoupons = state._myAvailableCoupons || [];
  let pickedCoupon = null;
  if (ucid) {
    pickedCoupon = availCoupons.find(c => c.id === ucid) || null;
  }
  if (pickedCoupon) {
    const totalCents = Math.round(total * 100);
    if (totalCents >= (pickedCoupon.snapshot_min_order_cents || 0)) {
      let d = 0;
      if (pickedCoupon.snapshot_discount_type === 'percentage') {
        d = Math.floor(totalCents * (pickedCoupon.snapshot_discount_value / 100));
        if (pickedCoupon.snapshot_max_discount_cents > 0 && d > pickedCoupon.snapshot_max_discount_cents) {
          d = pickedCoupon.snapshot_max_discount_cents;
        }
      } else if (pickedCoupon.snapshot_discount_type === 'fixed') {
        d = pickedCoupon.snapshot_discount_value;
      }
      if (d > totalCents) d = totalCents;
      couponDiscount = d / 100;
    }
  }
  const payable = Math.max(0, total - couponDiscount);
  const totalTxt = '¥' + payable.toFixed(2);
  const el = $('claude-shop-pay-total');
  if (el) {
    if (couponDiscount > 0) {
      el.innerHTML = `<span class="price-strike">¥${total.toFixed(2)}</span><strong style="color:var(--clr-danger)">${totalTxt}</strong>`;
    } else {
      el.textContent = totalTxt;
    }
  }
  const mel = $('claude-shop-modal-total');
  if (mel) mel.textContent = totalTxt;
  const el2 = $('claude-shop-unit-hint');
  if (el2) {
    if (discountSource === 'svip') {
      el2.innerHTML = `<span class="svip-badge svip-badge-sm">SVIP</span> 已享 SVIP 专享价 ¥${unit.toFixed(2)} / 件 · 原价 <span style="text-decoration:line-through;color:var(--text-muted)">¥${retailY.toFixed(2)}</span>`;
    } else if (isWs) {
      el2.textContent = `已享批发价（满 ${minW} 件）· 单价 ¥${unit.toFixed(2)}`;
    } else {
      const svipHint = svipY !== null && svipY >= 0 && svipY < retailY
        ? ` · SVIP 专享 ¥${svipY.toFixed(2)} / 件`
        : '';
      el2.textContent = `零售单价 ¥${unit.toFixed(2)} · 满 ${minW} 件可享批发 ¥${wholesaleY.toFixed(2)} / 件${svipHint}`;
    }
  }
  // 优惠券说明区
  const couponEl = $('claude-shop-coupon-hint');
  if (couponEl) {
    if (pickedCoupon && couponDiscount > 0) {
      couponEl.innerHTML = `已应用「${escHtml(pickedCoupon.snapshot_name)}」，优惠 <span class="picker-discount">-¥${couponDiscount.toFixed(2)}</span>`;
    } else if (pickedCoupon && couponDiscount === 0 && Math.round(total*100) < (pickedCoupon.snapshot_min_order_cents || 0)) {
      couponEl.textContent = `差 ¥${((pickedCoupon.snapshot_min_order_cents - Math.round(total*100))/100).toFixed(2)} 即可使用该券`;
    } else {
      couponEl.textContent = '';
    }
  }

  const payBtn = document.querySelector('[data-claude-shop-pay]');
  if (payBtn) {
    const showStaticPay = s.static_qr_enabled && !!(s.wechat_qr_url || s.alipay_qr_url);
    const canPay = (s.alipay_precreate_available || showStaticPay);
    const needPickMulti = s.product_pick_required && (s.products || []).length > 0 && !picked;
    const needAckDefault = !s.product_pick_required && !state.claudeShopDefaultAck;
    payBtn.disabled = !canPay || needPickMulti || needAckDefault;
  }
  syncClaudeShopModalGate();
};

window.claudeShopCouponRefresh = function() {
  claudeShopPriceRefresh();
};

window.syncClaudeShopModalGate = function() {
  const btn = $('claude-shop-modal-submit');
  const cb = $('claude-shop-modal-pay-done');
  if (!btn) return;
  btn.disabled = !(cb && cb.checked);
};

function stopClaudeShopAlipayPoll() {
  if (state.claudeAlipayPollTimer) {
    clearInterval(state.claudeAlipayPollTimer);
    state.claudeAlipayPollTimer = null;
  }
}

window.closeClaudeShopPayModal = function() {
  stopClaudeShopAlipayPoll();
  document.querySelector('.shop-pay-flow-overlay')?.remove();
};

/** 根据选购页的支付方式打开静态码弹窗或支付宝当面付 */
window.openClaudeShopPayFlow = function() {
  const s = state._claudeShopSummary;
  if (!s) return;
  const showStaticPay = s.static_qr_enabled && !!(s.wechat_qr_url || s.alipay_qr_url);
  if (!s.alipay_precreate_available && !showStaticPay) {
    toast('未配置可用支付方式', 'warn');
    return;
  }
  const pickReq = s.product_pick_required && (s.products || []).length > 0;
  if (pickReq && !state.claudeSelectedProductId) {
    toast('请先点击选择一款商品', 'warn');
    return;
  }
  if (!pickReq && !state.claudeShopDefaultAck) {
    toast('请先点击商品卡片确认选购，再支付', 'warn');
    return;
  }
  let useAlipay = !!s.alipay_precreate_available;
  if (showStaticPay && s.alipay_precreate_available) {
    useAlipay = $('claude-shop-pay-mode-alipay')?.checked !== false;
  } else if (showStaticPay && !s.alipay_precreate_available) {
    useAlipay = false;
  }
  if (useAlipay) {
    openClaudeShopAlipayPayModal();
  } else {
    openClaudeShopPayModal();
  }
};

/** 支付宝 precreate：直接下单并展示动态码，轮询直到发货 */
window.openClaudeShopAlipayPayModal = async function() {
  const s = state._claudeShopSummary;
  if (!s || !s.alipay_precreate_available) {
    toast('支付宝当面付未启用', 'warn');
    return;
  }
  const stock = s.stock_available ?? 0;
  if (stock < 1) {
    toast('暂时缺货', 'warn');
    return;
  }
  const pickReq = s.product_pick_required && (s.products || []).length > 0;
  if (pickReq && !state.claudeSelectedProductId) {
    toast('请先点击选择一款商品', 'warn');
    return;
  }
  if (!pickReq && !state.claudeShopDefaultAck) {
    toast('请先点击商品卡片确认选购，再支付', 'warn');
    return;
  }
  const qty = Math.max(1, Math.min(999, parseInt($('claude-shop-qty')?.value || '1', 10) || 1));
  if (qty > stock) {
    toast('购买数量超过当前库存', 'warn');
    return;
  }
  closeClaudeShopPayModal();
  const overlay = el('div', 'modal-overlay shop-pay-flow-overlay');
  overlay.innerHTML = `
    <div class="modal shop-pay-modal" style="max-width:480px">
      <div class="modal-title">支付宝当面付</div>
      <button type="button" class="modal-close" onclick="closeClaudeShopPayModal()">✕</button>
      <p class="shop-pay-modal-amount">应付金额：<strong id="claude-shop-modal-total">—</strong></p>
      <p id="claude-shop-alipay-status" style="font-size:0.88rem;color:var(--text-secondary);line-height:1.55">正在创建订单…</p>
      <div id="claude-shop-alipay-qr-slot" style="min-height:120px;display:flex;align-items:center;justify-content:center;margin-top:0.75rem"></div>
      <div class="modal-actions" style="margin-top:1rem;justify-content:flex-end">
        <button type="button" class="btn btn-ghost" onclick="closeClaudeShopPayModal()">关闭</button>
      </div>
    </div>`;
  document.body.appendChild(overlay);
  overlay.addEventListener('click', e => { if (e.target === overlay) closeClaudeShopPayModal(); });
  claudeShopPriceRefresh();
  try {
    const body = { quantity: qty, payment_method: 'alipay' };
    if (state.claudeSelectedProductId) body.product_id = state.claudeSelectedProductId;
    const ucid = $('claude-shop-coupon-select')?.value;
    if (ucid) body.user_coupon_id = ucid;
    const r = await api.shopCreateOrder(body);
    const qr = r.payment && r.payment.alipay_qr_code;
    const hint = (r.payment && r.payment.hint) || '请使用支付宝扫码支付。';
    if (!qr) {
      $('claude-shop-alipay-status').textContent = '未返回收款码，请稍后重试或改用静态收款码。';
      toast('支付宝下单异常', 'error');
      return;
    }
    const st = $('claude-shop-alipay-status');
    if (st) st.textContent = hint;
    const slot = $('claude-shop-alipay-qr-slot');
    if (slot) {
      const src = 'https://api.qrserver.com/v1/create-qr-code/?size=220x220&data=' + encodeURIComponent(qr);
      slot.innerHTML = `<img class="shop-qr-img" src="${escHtml(src)}" alt="支付宝收款码" style="max-width:220px;height:auto" />`;
    }
    state.claudeHighlightOrderId = r.order.id;
    state.claudeAlipayPollTimer = setInterval(async () => {
      try {
        const d = await api.shopGetOrder(r.order.id);
        if (d.order && d.order.status === 'fulfilled') {
          stopClaudeShopAlipayPoll();
          toast('支付成功，已自动发货', 'success');
          closeClaudeShopPayModal();
          navigate(state.page === 'claude-shop-orders' ? 'claude-shop-orders' : 'claude-shop');
        }
      } catch (_) { /* 轮询忽略单次失败 */ }
    }, 2500);
  } catch (e) {
    const elSt = $('claude-shop-alipay-status');
    if (elSt) elSt.textContent = e.message || '下单失败';
    if (e.status === 409) {
      toast(e.message || '已有待支付订单', 'warn');
      closeClaudeShopPayModal();
      navigate('claude-shop-orders');
    } else {
      toast(e.message || '下单失败', 'error');
    }
  }
};

window.openClaudeShopPayModal = function() {
  const s = state._claudeShopSummary;
  if (!s) return;
  const stock = s.stock_available ?? 0;
  if (stock < 1) {
    toast('暂时缺货', 'warn');
    return;
  }
  const qty = Math.max(1, Math.min(999, parseInt($('claude-shop-qty')?.value || '1', 10) || 1));
  if (qty > stock) {
    toast('购买数量超过当前库存', 'warn');
    return;
  }
  const pickReq = s.product_pick_required && (s.products || []).length > 0;
  if (pickReq && !state.claudeSelectedProductId) {
    toast('请先点击选择一款商品', 'warn');
    return;
  }
  if (!pickReq && !state.claudeShopDefaultAck) {
    toast('请先点击商品卡片确认选购，再支付', 'warn');
    return;
  }
  if (!s.wechat_qr_url && !s.alipay_qr_url) {
    toast('管理员尚未上传收款码，请联系店主', 'warn');
    return;
  }
  closeClaudeShopPayModal();
  const qrBlock = shopBuildQrBlockFromUrls(s.wechat_qr_url, s.alipay_qr_url);
  const tut = s.tutorial_url
    ? `<p style="margin-top:0.5rem;text-align:center"><a class="shop-tutorial-link" href="${escHtml(s.tutorial_url)}" target="_blank" rel="noopener">📘 使用教程点我！！</a></p>`
    : '';
  const overlay = el('div', 'modal-overlay shop-pay-flow-overlay');
  overlay.innerHTML = `
    <div class="modal shop-pay-modal" style="max-width:520px">
      <div class="modal-title">扫码支付</div>
      <button type="button" class="modal-close" onclick="closeClaudeShopPayModal()">✕</button>
      <p class="shop-pay-modal-amount">应付金额：<strong id="claude-shop-modal-total">—</strong></p>
      <p style="font-size:0.82rem;color:var(--text-secondary);line-height:1.5;margin-bottom:0.5rem">
        请使用微信或支付宝扫描下方二维码，支付金额须与上方一致。付款完成后勾选确认，系统将关闭本窗口并<strong>生成订单号</strong>（待管理员核对到账后发货）。
      </p>
      <div class="shop-qr-row">${qrBlock}</div>
      ${tut}
      <label style="display:flex;align-items:flex-start;gap:0.5rem;cursor:pointer;font-size:0.88rem;line-height:1.45;margin-top:1rem">
        <input type="checkbox" id="claude-shop-modal-pay-done" style="margin-top:0.2rem" onchange="syncClaudeShopModalGate()" />
        <span>我确认已按上述金额完成扫码支付</span>
      </label>
      <div class="modal-actions" style="margin-top:1rem;justify-content:flex-end">
        <button type="button" class="btn btn-ghost" onclick="closeClaudeShopPayModal()">取消</button>
        <button type="button" class="btn btn-primary" id="claude-shop-modal-submit" disabled onclick="submitClaudeShopOrder()">确认并生成订单</button>
      </div>
      <p style="font-size:0.72rem;color:var(--text-muted);margin-top:0.65rem;line-height:1.45">使用静态收款码时，同一账号仅允许一笔待管理员确认的订单；支付宝当面付可继续下新单。</p>
    </div>
  `;
  document.body.appendChild(overlay);
  overlay.addEventListener('click', e => { if (e.target === overlay) closeClaudeShopPayModal(); });
  claudeShopPriceRefresh();
  syncClaudeShopModalGate();
};

window.submitClaudeShopOrder = async function() {
  const overlay = document.querySelector('.shop-pay-flow-overlay');
  if (!overlay || !$('claude-shop-modal-pay-done')?.checked) {
    toast('请先完成扫码支付，并在弹窗中勾选确认', 'warn');
    return;
  }
  const qty = Math.max(1, Math.min(999, parseInt($('claude-shop-qty')?.value || '1', 10) || 1));
  try {
    const body = { quantity: qty, payment_method: 'static' };
    if (state.claudeSelectedProductId) body.product_id = state.claudeSelectedProductId;
    const ucid = $('claude-shop-coupon-select')?.value;
    if (ucid) body.user_coupon_id = ucid;
    const r = await api.shopCreateOrder(body);
    closeClaudeShopPayModal();
    state.claudeHighlightOrderId = r.order.id;
    const done = r.order && r.order.status === 'fulfilled';
    toast(done ? '订单已生成并已自动发货' : '订单已生成，请等待管理员核对到账后发货', 'success');
    navigate('claude-shop-orders');
  } catch (e) {
    if (e.status === 409) {
      closeClaudeShopPayModal();
      toast(e.message || '已有待确认订单', 'warn');
      navigate('claude-shop-orders');
      return;
    }
    toast(e.message || '下单失败', 'error');
  }
};

window.claudeShopViewOrder = function(id) {
  state.claudeHighlightOrderId = id;
  navigate('claude-shop-orders');
};

window.claudeShopDismissHighlight = function() {
  state.claudeHighlightOrderId = null;
  navigate(state.page === 'claude-shop-orders' ? 'claude-shop-orders' : 'claude-shop');
};

async function renderClaudeShop(container) {
  const actions = $('topbar-actions');
  if (actions) actions.innerHTML = '';
  const summary = await api.publicClaudeShop();
  state._claudeShopSummary = summary;
  if (!summary.enabled) {
    container.innerHTML = `<div class="card" style="max-width:560px"><p>店主暂未开放自助购号。</p></div>`;
    return;
  }

  const highlightHtml = await buildClaudeShopHighlightHtml();

  const tags = [];
  if (summary.tag_hot) tags.push('<span class="shop-tag shop-tag-hot">🔥 爆火</span>');
  if (summary.show_tag_wholesale) tags.push(`<span class="shop-tag">批发 ${summary.wholesale_min_qty || 5} 件起</span>`);
  if (summary.tag_fan_welfare) tags.push(`<span class="shop-tag shop-tag-fan">${escHtml(summary.tag_fan_welfare)}</span>`);
  if (summary.max_per_user > 0) tags.push(`<span class="shop-tag">限购 · 每用户 ${summary.max_per_user} 件</span>`);

  const prods = summary.products || [];
  const pickReq = summary.product_pick_required && prods.length > 0;
  if (pickReq) {
    const ids = new Set(prods.map(p => p.id));
    if (state.claudeSelectedProductId && !ids.has(state.claudeSelectedProductId)) state.claudeSelectedProductId = null;
  } else {
    state.claudeSelectedProductId = null;
    state.claudeShopDefaultAck = false;
  }

  const myOrders = await api.shopListOrders(1, 20).catch(() => ({ data: [] }));
  const orders = myOrders.data || [];
  const pendingStaticOrder = orders.find(o => o.status === 'awaiting_payment' && o.payment_channel !== 'alipay_precreate');
  // v10：加载我的可用优惠券供下单页选择
  const myCouponsRes = await api.couponMine('available').catch(() => ({ data: [] }));
  state._myAvailableCoupons = (myCouponsRes.data || []).filter(uc => uc.status === 'available');
  const showStaticPay = summary.static_qr_enabled && !!(summary.wechat_qr_url || summary.alipay_qr_url);
  const alipayOk = !!summary.alipay_precreate_available;
  const blockCheckoutEntirely = !!pendingStaticOrder && !alipayOk;
  const restrictStaticDueToPending = !!pendingStaticOrder && alipayOk;

  let productGridHtml = '';
  if (pickReq) {
    productGridHtml = prods.map(p => {
      const sel = state.claudeSelectedProductId === p.id;
      const tagHtml = p.tag ? `<span class="shop-product-card-tag">${escHtml(p.tag)}</span>` : '';
      // p.stock_available = 专属池 + 通用池兜底，能实际下单的数量；低于 1 时标红并禁用
      const available = Number(p.stock_available ?? 0);
      const outOfStock = available <= 0;
      const stockStyle = outOfStock ? 'color:var(--clr-danger, #c0392b)' : 'color:var(--text-muted)';
      const stockText = outOfStock ? '暂无库存' : `剩余 <strong>${available}</strong> 件`;
      const hasSVIPPrice = (p.svip_price_yuan !== undefined && p.svip_price_yuan !== null);
      const svipPriceTag = hasSVIPPrice
        ? `<span class="svip-price-tag">SVIP ¥${Number(p.svip_price_yuan).toFixed(2)}</span>`
        : '';
      return `<button type="button" class="shop-product-card${sel ? ' shop-product-card--selected' : ''}${outOfStock ? ' shop-product-card--disabled' : ''}" data-product-id="${escHtml(p.id)}" ${outOfStock ? 'disabled aria-disabled="true"' : ''} onclick="${outOfStock ? '' : `claudeShopSelectProduct('${escHtml(p.id)}')`}">
        <div class="shop-product-card-head">${tagHtml}${svipPriceTag}<span class="shop-product-card-title">${escHtml(p.title)}</span></div>
        <p class="shop-product-card-desc">${escHtml(p.description || '')}</p>
        <div class="shop-product-card-prices">
          <span class="shop-product-card-retail">¥${Number(p.retail_price_yuan).toFixed(2)}<small>/件</small></span>
          <span class="shop-product-card-ws">满 ${p.wholesale_min_qty || 5} 件 · ¥${Number(p.wholesale_price_yuan).toFixed(2)}/件</span>
        </div>
        <div class="shop-product-card-foot"><span class="shop-product-card-picked">已选中</span><span class="shop-product-card-hint" style="${stockStyle}">${stockText}</span></div>
      </button>`;
    }).join('');
  } else {
    const selDef = state.claudeShopDefaultAck;
    productGridHtml = `<button type="button" class="shop-product-card shop-product-card--default${selDef ? ' shop-product-card--selected' : ''}" data-default-product="1" onclick="claudeShopAckDefaultProduct()" aria-label="店铺默认商品，点击确认选购">
      <div class="shop-product-card-head"><span class="shop-product-card-title">${escHtml(summary.title || 'Claude 账号')}</span></div>
      <p class="shop-product-card-desc">${escHtml(summary.description || '')}</p>
      <div class="shop-product-card-prices">
        <span class="shop-product-card-retail">¥${Number(summary.retail_price_yuan).toFixed(2)}<small>/件</small></span>
        <span class="shop-product-card-ws">满 ${summary.wholesale_min_qty || 5} 件 · ¥${Number(summary.wholesale_price_yuan).toFixed(2)}/件</span>
      </div>
      <div class="shop-product-card-foot"><span class="shop-product-card-picked">已确认 · 可支付</span><span class="shop-product-card-hint">请点击卡片确认选购</span></div>
    </button>`;
  }

  let payHint = '';
  if (summary.alipay_precreate_available && showStaticPay) {
    payHint = '选「当面付」将直接生成订单并展示支付宝二维码；选「静态码」请先扫码付款，再勾选确认生成订单。';
  } else if (summary.alipay_precreate_available && !showStaticPay) {
    payHint = '使用支付宝当面付：支付成功后系统自动发货。';
  } else if (showStaticPay) {
    payHint = '先弹出静态码完成支付，勾选确认后生成订单；发货后请在「我的订单」查看。';
  } else {
    payHint = '当前未配置可用支付方式，请联系店主。';
  }

  let payBtnLabel = '去支付';
  if (summary.alipay_precreate_available && showStaticPay) payBtnLabel = '继续支付';
  else if (summary.alipay_precreate_available && !showStaticPay) payBtnLabel = '支付宝支付';
  else if (showStaticPay) payBtnLabel = '打开收款码并生成订单';

  const staticRadioAttrs = restrictStaticDueToPending ? 'disabled' : '';
  const payModeBlock = (summary.alipay_precreate_available && showStaticPay) ? `
          <div style="margin:0.65rem 0;font-size:0.86rem;line-height:1.55">
            <span style="color:var(--text-muted);display:block;margin-bottom:0.35rem">支付方式</span>
            <label style="display:flex;align-items:center;gap:0.45rem;cursor:pointer;margin-bottom:0.25rem">
              <input type="radio" name="claude-shop-pay-mode" id="claude-shop-pay-mode-alipay" value="alipay" checked onchange="claudeShopPriceRefresh()" />
              <span><strong>支付宝当面付</strong>（官方动态码，付完自动发货）</span>
            </label>
            <label style="display:flex;align-items:flex-start;gap:0.45rem;cursor:${restrictStaticDueToPending ? 'not-allowed' : 'pointer'};opacity:${restrictStaticDueToPending ? '0.72' : '1'}">
              <input type="radio" name="claude-shop-pay-mode" id="claude-shop-pay-mode-static" value="static" onchange="claudeShopPriceRefresh()" ${staticRadioAttrs} />
              <span>微信 / 支付宝静态收款码（需管理员确认）${restrictStaticDueToPending ? '<span style="display:block;font-size:0.78rem;color:var(--clr-warn);margin-top:0.2rem">您有待管理员确认的静态收款订单，请先处理或改用当面付；静态码暂不可再选。</span>' : ''}</span>
            </label>
          </div>` : '';

  const purchaseSection = blockCheckoutEntirely ? `
        <div class="shop-checkout-panel" style="margin-top:0.5rem;padding:1rem;background:rgba(230,126,34,0.12);border-radius:var(--radius);border:1px solid var(--border-light)">
          <p style="font-size:0.9rem;line-height:1.55;margin-bottom:0.65rem">您有 <strong>一笔</strong> 待管理员确认的静态收款订单，且当前仅支持静态码支付。请先等待该笔订单处理完成后再继续购买。</p>
          <button type="button" class="btn btn-primary btn-sm" onclick="claudeShopViewOrder('${pendingStaticOrder.id}')">前往我的订单</button>
        </div>
        ` : `
        <div class="shop-checkout-panel" style="margin-top:0.5rem;padding:1rem 0 0">
          ${restrictStaticDueToPending ? `<div class="shop-pending-static-banner" role="status"><strong>提示：</strong>您有待确认的静态收款订单，仍可继续使用<strong>支付宝当面付</strong>下单；静态码路径需等上一单处理完毕。</div>` : ''}
          <label class="form-label">购买数量</label>
          <input type="number" class="form-input" id="claude-shop-qty" min="1" max="999" value="1" style="max-width:120px" oninput="claudeShopPriceRefresh()" />
          <p id="claude-shop-unit-hint" style="font-size:0.8rem;color:var(--text-secondary);margin:0.5rem 0"></p>
          ${state._myAvailableCoupons && state._myAvailableCoupons.length ? `
          <div class="coupon-picker" style="margin:0.5rem 0 0.75rem">
            <span class="picker-tag">🎟 优惠券</span>
            <select class="form-input" id="claude-shop-coupon-select" onchange="claudeShopCouponRefresh()">
              <option value="">不使用优惠券</option>
              ${state._myAvailableCoupons.map(uc => {
                const dv = uc.snapshot_discount_type === 'percentage'
                  ? (uc.snapshot_discount_value + '% off')
                  : ('-¥' + (uc.snapshot_discount_value/100).toFixed(2));
                return `<option value="${escHtml(uc.id)}">${escHtml(uc.snapshot_name)}（${dv}）</option>`;
              }).join('')}
            </select>
            <span id="claude-shop-coupon-hint" style="font-size:0.8rem"></span>
            <a href="#" onclick="event.preventDefault();navigate('my-coupons')" style="font-size:0.8rem">我的优惠券 →</a>
          </div>` : `
          <div style="font-size:0.8rem;color:var(--text-muted);margin:0.3rem 0 0.7rem">暂无可用优惠券 · <a href="#" onclick="event.preventDefault();navigate('my-coupons')">去领取</a></div>
          `}
          <p class="shop-checkout-total-line">应付金额：<span id="claude-shop-pay-total">—</span></p>
          ${payModeBlock}
          <button type="button" class="btn btn-primary" data-claude-shop-pay style="margin-top:0.25rem" onclick="openClaudeShopPayFlow()">${escHtml(payBtnLabel)}</button>
          <p class="shop-checkout-hint">${escHtml(payHint)}</p>
          ${pickReq ? '<p class="shop-checkout-hint" style="color:var(--clr-warn)">请先点击上方卡片<strong>选中</strong>一款商品，再支付。</p>' : '<p class="shop-checkout-hint" style="color:var(--clr-warn)">请先点击上方商品卡片<strong>确认选购</strong>，再支付。</p>'}
        </div>
        `;

  container.innerHTML = `
    ${highlightHtml}
    <div class="card" style="max-width:720px">
      <div class="card-header">
        <div>
          <div class="card-title">${escHtml(summary.title || 'Claude 账号')}</div>
          <div style="font-size:0.8rem;color:var(--text-muted);margin-top:0.2rem">${escHtml(summary.subtitle || '')}</div>
        </div>
      </div>
      <div class="card-body">
        <section class="shop-product-panel" aria-label="商品信息">
          ${!pickReq ? `<div class="shop-tags-row" style="display:flex;flex-wrap:wrap;gap:0.45rem;margin-bottom:0.85rem">${tags.join('')}</div>` : `<p class="form-label" style="margin-bottom:0.65rem">选择商品 <span style="font-weight:400;color:var(--text-muted);font-size:0.82rem">（必选 · 点击卡片选中）</span></p>`}
          <div class="shop-product-grid">${productGridHtml}</div>
          <div class="shop-stock-banner"><span class="text-muted">全站总可售</span> <strong>${summary.stock_available ?? 0}</strong> 件${(summary.stock_unassigned ?? 0) > 0 ? ` <span class="text-muted" style="font-size:0.78rem">（其中通用池 ${summary.stock_unassigned} 件，可作为任一 SKU 订单兜底）</span>` : ''}
            ${summary.tutorial_url ? `<span class="shop-stock-banner-tutorial"><a class="shop-tutorial-link" href="${escHtml(summary.tutorial_url)}" target="_blank" rel="noopener">📘 使用教程</a></span>` : ''}
          </div>
        </section>
        <section class="shop-checkout-panel" aria-label="购买">
          ${purchaseSection}
        </section>
        <div style="margin-top:1rem;padding-top:1rem;border-top:1px solid var(--border-light)">
          <button type="button" class="btn btn-ghost btn-sm" onclick="navigate('claude-shop-orders')">📋 我的订单（记录与发货信息）</button>
        </div>
      </div>
    </div>
  `;
  claudeShopPriceRefresh();
}

async function renderClaudeShopOrders(container) {
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `<button type="button" class="btn btn-ghost btn-sm" onclick="navigate('claude-shop')">← 返回选购</button>`;
  }
  const summary = await api.publicClaudeShop().catch(() => ({}));
  state._claudeShopSummary = summary && summary.enabled ? summary : state._claudeShopSummary;
  if (!summary || !summary.enabled) {
    container.innerHTML = `<div class="card" style="max-width:560px"><p>店主暂未开放自助购号。</p></div>`;
    return;
  }
  const highlightHtml = await buildClaudeShopHighlightHtml();
  const myOrders = await api.shopListOrders(1, 50).catch(() => ({ data: [] }));
  const orders = myOrders.data || [];
  container.innerHTML = `
    ${highlightHtml}
    <div class="card" style="max-width:720px">
      <div class="card-header"><div class="card-title">我的订单</div></div>
      <div class="table-wrap">
        <table>
          <thead><tr><th>时间</th><th>商品</th><th>数量</th><th>应付</th><th>状态</th><th></th></tr></thead>
          <tbody>
            ${orders.length ? orders.map(o => {
              const pt = (o.product_title_snapshot && String(o.product_title_snapshot).trim()) || '—';
              return `
              <tr>
                <td style="font-size:0.78rem">${formatDate(o.created_at)}</td>
                <td style="font-size:0.78rem;max-width:140px">${escHtml(pt)}</td>
                <td>${o.quantity}</td>
                <td>¥${(o.total_cents / 100).toFixed(2)}</td>
                <td>${o.status === 'fulfilled' ? '<span class="badge badge-green">已发货</span>' : (o.payment_channel === 'alipay_precreate' ? '<span class="badge badge-gray">待支付·支付宝</span>' : '<span class="badge badge-gray">待确认收款</span>')}</td>
                <td><button type="button" class="btn btn-ghost btn-sm" onclick="claudeShopViewOrder('${o.id}')">查看详情</button></td>
              </tr>`;
            }).join('') : '<tr><td colspan="6" style="text-align:center;color:var(--text-muted);padding:1rem">暂无订单</td></tr>'}
          </tbody>
        </table>
      </div>
    </div>
  `;
}

// ─── Claude 店铺（管理员）────────────────────────────────────
window.saveAdminShopConfig = async function() {
  const body = {
    enabled: $('shop-cfg-enabled')?.checked || false,
    title: ($('shop-cfg-title')?.value || '').trim(),
    subtitle: ($('shop-cfg-subtitle')?.value || '').trim(),
    description: ($('shop-cfg-desc')?.value || '').trim(),
    tutorial_url: ($('shop-cfg-tutorial')?.value || '').trim(),
    retail_price_yuan: parseFloat($('shop-cfg-retail')?.value || '0') || 0,
    wholesale_min_qty: parseInt($('shop-cfg-wsqty')?.value || '5', 10) || 5,
    wholesale_price_yuan: parseFloat($('shop-cfg-wholesale')?.value || '0') || 0,
    tag_hot: $('shop-cfg-hot')?.checked || false,
    show_tag_wholesale: $('shop-cfg-showws')?.checked !== false,
    tag_fan_welfare: ($('shop-cfg-fan')?.value || '').trim(),
    max_per_user: parseInt($('shop-cfg-maxuser')?.value || '0', 10) || 0,
    static_payment_manual_confirm: $('shop-cfg-static-manual')?.checked !== false,
    static_qr_enabled: $('shop-cfg-static-qr')?.checked !== false,
  };
  try {
    await api.admin.shopPutConfig(body);
    toast('已保存', 'success');
    await refreshClaudeShopNav();
    navigate('admin-shop-settings');
  } catch (e) {
    toast(e.message || '保存失败', 'error');
  }
};

window.uploadShopQR = async function() {
  const fd = new FormData();
  const w = $('shop-qr-wechat')?.files?.[0];
  const a = $('shop-qr-alipay')?.files?.[0];
  const wErr = validateShopQRFile(w);
  const aErr = validateShopQRFile(a);
  if (w) fd.append('wechat', w);
  if (a) fd.append('alipay', a);
  if (!w && !a) {
    toast('请选择至少一张图片', 'warn');
    return;
  }
  if (wErr || aErr) {
    toast(wErr || aErr, 'warn');
    return;
  }
  try {
    const res = await api.admin.shopUploadQR(fd);
    const updated = Array.isArray(res.updated) && res.updated.length
      ? res.updated.map(v => v === 'wechat' ? '微信' : (v === 'alipay' ? '支付宝' : v)).join('、')
      : '收款码';
    toast(`${updated} 收款码已更新`, 'success');
    navigate('admin-shop-settings');
  } catch (e) {
    toast(e.message || '上传失败', 'error');
  }
};

window.handleShopImportInput = function() {
  updateShopImportStats($('shop-import-ta')?.value || '');
};

window.loadShopImportFile = async function() {
  const file = $('shop-import-file')?.files?.[0];
  if (!file) {
    updateShopImportStats($('shop-import-ta')?.value || '');
    return;
  }
  try {
    const text = await file.text();
    const ta = $('shop-import-ta');
    if (ta) ta.value = text;
    updateShopImportStats(text, file.name);
    toast(`已载入 ${file.name}`, 'success');
  } catch (e) {
    toast(e.message || '读取文件失败', 'error');
  }
};

window.runShopImport = async function() {
  const productSelect = $('shop-import-product');
  const productId = (productSelect?.value || '').trim();
  const batchTag = ($('shop-import-batch')?.value || '').trim();
  const deliveryType = productSelect?.selectedOptions[0]?.dataset?.deliveryType || 'card_key';

  if (deliveryType === 'text') {
    const raw = ($('shop-import-text-ta')?.value || '').trim();
    if (!raw) { toast('请粘贴发货内容', 'warn'); return; }
    const items = raw.split(/^####\s*$/m)
      .map(s => s.trim())
      .filter(Boolean)
      .map(text => ({ text }));
    if (!items.length) { toast('没有可导入条目', 'warn'); return; }
    try {
      const r = await api.admin.shopImportInventoryJSON({ delivery_type: 'text', items }, batchTag, productId);
      toast(`导入成功 ${r.inserted} 条`, 'success');
      navigate('admin-shop-inventory');
    } catch(e) { toast(e.message || '导入失败', 'error'); }
    return;
  }

  if (deliveryType === 'custom_kv') {
    const rows = document.querySelectorAll('#shop-import-kv-items .kv-item');
    const items = [];
    rows.forEach(r => {
      const obj = {};
      r.querySelectorAll('[data-field-key]').forEach(inp => {
        const k = inp.getAttribute('data-field-key');
        const v = inp.value;
        if (String(v).trim() !== '') obj[k] = v;
      });
      if (Object.keys(obj).length > 0) items.push(obj);
    });
    if (!items.length) { toast('请至少填写一条', 'warn'); return; }
    try {
      const r = await api.admin.shopImportInventoryJSON({ delivery_type: 'custom_kv', items }, batchTag, productId);
      toast(`导入成功 ${r.inserted} 条`, 'success');
      navigate('admin-shop-inventory');
    } catch(e) { toast(e.message || '导入失败', 'error'); }
    return;
  }

  // 默认 card_key：走 text/plain
  const t = ($('shop-import-ta')?.value || '').trim();
  if (!t) {
    toast('请粘贴 .txt / .csv 内容', 'warn');
    return;
  }
  const preview = updateShopImportStats(t);
  if (!preview.pairs.length) {
    toast('未识别到可导入数据', 'warn');
    return;
  }
  try {
    const r = await api.admin.shopImportInventory(t, batchTag, productId);
    const recognized = Number.isFinite(r.recognized) ? r.recognized : preview.pairs.length;
    const skipped = Number.isFinite(r.skipped) ? r.skipped : ((r.warnings || []).length);
    let msg = `识别 ${recognized} 条，导入成功 ${r.inserted} 条`;
    if (skipped > 0) msg += `，跳过 ${skipped} 条`;
    toast(msg, skipped > 0 ? 'warn' : 'success');
    navigate('admin-shop-inventory');
  } catch (e) {
    toast(e.message || '导入失败', 'error');
  }
};

/** v10：切换库存导入区域的 UI 模式（根据选中 SKU 的 delivery_type） */
window.updateImportUIForProduct = function() {
  const sel = $('shop-import-product');
  if (!sel) return;
  const opt = sel.selectedOptions[0];
  const dt = opt?.dataset?.deliveryType || 'card_key';
  ['cardkey', 'text', 'kv'].forEach(m => {
    const box = $(`shop-import-mode-${m}`);
    if (box) box.style.display = 'none';
  });
  if (dt === 'text') {
    $('shop-import-mode-text').style.display = 'block';
  } else if (dt === 'custom_kv') {
    $('shop-import-mode-kv').style.display = 'block';
    // 初始化至少一条
    const box = $('shop-import-kv-items');
    if (box && box.children.length === 0) addImportKVItem();
  } else {
    $('shop-import-mode-cardkey').style.display = 'block';
  }
};

/** 根据当前 SKU 的 schema 新增一行 custom_kv 输入 */
window.addImportKVItem = function() {
  const sel = $('shop-import-product');
  if (!sel) return;
  const opt = sel.selectedOptions[0];
  if (!opt) return;
  const schemaStr = opt.dataset.schema || '';
  let schema = {};
  try { schema = JSON.parse(schemaStr || '{}'); } catch {}
  const fields = (schema.fields || []).length ? schema.fields : [{ key: 'text', label: '内容', multiline: true }];

  const box = $('shop-import-kv-items');
  if (!box) return;
  const div = document.createElement('div');
  div.className = 'kv-item';
  div.style.cssText = 'border:1px solid var(--border-light);padding:0.65rem;border-radius:var(--radius-sm);position:relative';
  div.innerHTML = `
    <button type="button" class="btn-kv-del" onclick="this.parentElement.remove()" style="position:absolute;top:0.4rem;right:0.4rem">✕</button>
    ${fields.map(f => {
      const isMulti = !!f.multiline;
      const hint = f.hint ? `<div class="form-hint">${escHtml(f.hint)}</div>` : '';
      return `<div class="form-group" style="margin-bottom:0.5rem">
        <label class="form-label">${escHtml(f.label || f.key)}</label>
        ${isMulti
          ? `<textarea class="form-input" rows="2" data-field-key="${escHtml(f.key)}"></textarea>`
          : `<input class="form-input" type="text" data-field-key="${escHtml(f.key)}" />`}
        ${hint}
      </div>`;
    }).join('')}
  `;
  box.appendChild(div);
};

window.confirmShopOrderPaid = function(id) {
  showModal('确认收款', '<p>确认已收到该笔款项？系统将从库存自动扣减并发货给买家。</p>', async () => {
    try {
      await api.admin.shopConfirmOrder(id);
      toast('已确认并发货', 'success');
      navigate('admin-shop-orders');
    } catch (e) {
      toast(e.message || '操作失败', 'error');
    }
  });
};

window.adminShopInventorySetStatus = function(status) {
  state.adminShopInventoryStatus = status || 'all';
  state.adminShopInventoryPage = 1;
  navigate('admin-shop-inventory');
};

window.adminShopInventorySetBatch = function(batch) {
  state.adminShopInventoryBatch = batch || '';
  state.adminShopInventoryPage = 1;
  navigate('admin-shop-inventory');
};

window.adminShopInventorySetProduct = function(productId) {
  state.adminShopInventoryProduct = productId || '';
  state.adminShopInventoryPage = 1;
  navigate('admin-shop-inventory');
};

window.adminShopInventoryGoPage = function(page) {
  state.adminShopInventoryPage = Math.max(1, parseInt(page, 10) || 1);
  navigate('admin-shop-inventory');
};

window.confirmPurgeShopInventoryBatch = function() {
  const b = state.adminShopInventoryBatch;
  if (!b) {
    toast('请先在「批次」中选择具体批次（不能选「全部批次」）', 'warn');
    return;
  }
  const name = b === '__none__' ? '无批次（未打标签）' : b;
  showModal('确认删除该批次待售', `<p>将删除批次 <strong>${escHtml(name)}</strong> 下所有<strong>待售</strong>货物；已售出记录保留，不受影响。</p><p style="font-size:0.82rem;color:var(--clr-danger);margin-top:0.5rem">此操作不可恢复。</p>`, async () => {
    try {
      const r = await api.admin.shopPurgeInventoryBatch(b);
      const n = Number(r.deleted) || 0;
      toast(`已删除 ${n} 条待售`, n ? 'success' : 'warn');
      navigate('admin-shop-inventory');
    } catch (e) {
      toast(e.message || '删除失败', 'error');
      return false;
    }
  });
};

window.confirmPurgeAllShopAvailable = function() {
  showModal('确认清空全部待售库存', `<p>将删除系统中<strong>所有待售</strong>货物（不限批次）。已售出记录保留。</p><p style="font-size:0.82rem;color:var(--clr-danger);margin-top:0.5rem"><strong>不可恢复</strong>，请确认无待发货订单缺货风险。</p>`, async () => {
    try {
      const r = await api.admin.shopPurgeAllAvailable();
      const n = Number(r.deleted) || 0;
      toast(`已清空 ${n} 条待售`, n ? 'success' : 'warn');
      navigate('admin-shop-inventory');
    } catch (e) {
      toast(e.message || '操作失败', 'error');
      return false;
    }
  });
};

window.deleteShopInventory = function(id, label) {
  showModal('删除货物', `<p>确定删除货物 <strong>${escHtml(label || id)}</strong> 吗？已售出的记录也会从库存列表移除，但不会影响已发货订单内容。</p>`, async () => {
    try {
      await api.admin.shopDeleteInventory(id);
      const rows = document.querySelectorAll('[data-shop-inventory-row]').length;
      if (rows <= 1 && (state.adminShopInventoryPage || 1) > 1) {
        state.adminShopInventoryPage -= 1;
      }
      toast('货物已删除', 'success');
      navigate('admin-shop-inventory');
    } catch (e) {
      toast(e.message || '删除失败', 'error');
    }
  });
};

window.adminShopOrdersSetStatus = function(status) {
  state.adminShopOrdersStatus = status === 'all' ? '' : (status || '');
  state.adminShopOrdersPage = 1;
  navigate('admin-shop-orders');
};

window.adminShopOrdersGoPage = function(page) {
  state.adminShopOrdersPage = Math.max(1, parseInt(page, 10) || 1);
  navigate('admin-shop-orders');
};

window.showAdminShopOrderDetail = async function(id) {
  try {
    const d = await api.admin.shopGetOrderAdmin(id);
    const o = d.order;
    const stLabel = o.status === 'fulfilled' ? '已发货' : (o.status === 'awaiting_payment' ? '待确认收款' : o.status);
    let body = `<p style="font-size:0.85rem"><strong>状态</strong> ${stLabel}</p>`;
    body += `<p style="font-size:0.85rem"><strong>买家账号</strong> <code style="font-size:0.78rem">${escHtml(o.account_id)}</code></p>`;
    if (o.product_title_snapshot && String(o.product_title_snapshot).trim()) {
      body += `<p style="font-size:0.85rem"><strong>商品</strong> ${escHtml(o.product_title_snapshot)}</p>`;
    }
    body += `<p style="font-size:0.85rem"><strong>数量</strong> ${o.quantity} · <strong>应付</strong> ¥${(o.total_cents / 100).toFixed(2)}</p>`;
    if ((o.lines || []).length) {
      body += `<p class="form-label" style="margin-top:0.85rem">已发货账号</p>`;
      body += (o.lines || []).map(ln => renderShopDeliveryCard(ln)).join('');
    } else {
      body += `<p style="font-size:0.8rem;color:var(--text-muted);margin-top:0.65rem">暂无发货明细（订单待确认或未扣库存发货）。</p>`;
    }
    document.querySelector('.admin-shop-detail-overlay')?.remove();
    const overlay = el('div', 'modal-overlay admin-shop-detail-overlay');
    overlay.innerHTML = `
      <div class="modal" style="max-width:580px">
        <div class="modal-title">订单详情</div>
        <button type="button" class="modal-close" onclick="this.closest('.modal-overlay').remove()">✕</button>
        <div style="font-size:0.76rem;color:var(--text-muted);margin-bottom:0.65rem;font-family:var(--font-mono);word-break:break-all">#${escHtml(o.id)}</div>
        ${body}
        <div class="modal-actions" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="btn btn-primary" onclick="this.closest('.modal-overlay').remove()">关闭</button>
        </div>
      </div>`;
    document.body.appendChild(overlay);
    overlay.addEventListener('click', e => { if (e.target === overlay) overlay.remove(); });
  } catch (e) {
    toast(e.message || '加载失败', 'error');
  }
};

async function renderAdminShopSettings(container) {
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `<button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-products')">在售 SKU</button>
      <button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-inventory')">库存与货物</button>
      <button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-orders')">订单与发货</button>`;
  }
  const cfg = await api.admin.shopGetConfig();
  container.innerHTML = `
    <div style="max-width:1120px;display:flex;flex-direction:column;gap:1rem">
      <div class="card">
        <div class="card-header"><div class="card-title">店铺开关与文案</div></div>
        <div class="card-body">
          <label style="display:flex;align-items:center;gap:0.5rem;margin-bottom:0.8rem;cursor:pointer">
            <input type="checkbox" id="shop-cfg-enabled" ${cfg.enabled ? 'checked' : ''} />
            <span>开启自助购号（关闭后普通用户侧栏不显示入口）</span>
          </label>
          <div class="form-group"><label class="form-label">商品标题</label>
            <input class="form-input" id="shop-cfg-title" value="${escHtml(cfg.title || '')}" /></div>
          <div class="form-group"><label class="form-label">副标题</label>
            <input class="form-input" id="shop-cfg-subtitle" value="${escHtml(cfg.subtitle || '')}" /></div>
          <div class="form-group"><label class="form-label">商品说明</label>
            <textarea class="form-input" id="shop-cfg-desc" rows="4" style="resize:vertical">${escHtml(cfg.description || '')}</textarea></div>
          <div class="form-group"><label class="form-label">使用教程链接</label>
            <input class="form-input" id="shop-cfg-tutorial" placeholder="https://" value="${escHtml(cfg.tutorial_url || '')}" /></div>
        </div>
      </div>
      <div class="card">
        <div class="card-header"><div class="card-title">价格与标签</div></div>
        <div class="card-body">
          <div style="display:grid;grid-template-columns:1fr 1fr;gap:0.75rem">
            <div class="form-group"><label class="form-label">零售价（元/件）</label>
              <input type="number" step="0.01" class="form-input" id="shop-cfg-retail" value="${Number(cfg.retail_price_yuan).toFixed(2)}" /></div>
            <div class="form-group"><label class="form-label">批发价（元/件）</label>
              <input type="number" step="0.01" class="form-input" id="shop-cfg-wholesale" value="${Number(cfg.wholesale_price_yuan).toFixed(2)}" /></div>
            <div class="form-group"><label class="form-label">批发起订件数</label>
              <input type="number" class="form-input" id="shop-cfg-wsqty" value="${cfg.wholesale_min_qty ?? 5}" /></div>
            <div class="form-group"><label class="form-label">每用户限购（0=不限）</label>
              <input type="number" class="form-input" id="shop-cfg-maxuser" value="${cfg.max_per_user ?? 0}" /></div>
          </div>
          <label style="display:flex;align-items:center;gap:0.45rem;margin:0.6rem 0;cursor:pointer">
            <input type="checkbox" id="shop-cfg-hot" ${cfg.tag_hot ? 'checked' : ''} /><span>显示「爆火」标签</span>
          </label>
          <label style="display:flex;align-items:center;gap:0.45rem;margin:0.3rem 0;cursor:pointer">
            <input type="checkbox" id="shop-cfg-showws" ${cfg.show_tag_wholesale !== false ? 'checked' : ''} /><span>显示「批发」标签</span>
          </label>
          <div class="form-group"><label class="form-label">粉丝福利等自定义标签文案（留空不显示）</label>
            <input class="form-input" id="shop-cfg-fan" placeholder="如：粉丝福利" value="${escHtml(cfg.tag_fan_welfare || '')}" /></div>
          <label style="display:flex;align-items:flex-start;gap:0.45rem;margin:0.75rem 0;cursor:pointer;line-height:1.45">
            <input type="checkbox" id="shop-cfg-static-qr" style="margin-top:0.2rem" ${cfg.static_qr_enabled !== false ? 'checked' : ''} />
            <span><strong>启用静态收款码</strong>：开启后用户可选用微信/支付宝静态码；<strong>静态码</strong>路径下同一账号仅允许一笔待管理员确认。关闭后用户端不展示静态码；<strong>支付宝当面付</strong>始终可多笔待支付并行（若已配置）。</span>
          </label>
          <label style="display:flex;align-items:flex-start;gap:0.45rem;margin:0.75rem 0;cursor:pointer;line-height:1.45">
            <input type="checkbox" id="shop-cfg-static-manual" style="margin-top:0.2rem" ${cfg.static_payment_manual_confirm !== false ? 'checked' : ''} />
            <span><strong>静态收款码</strong>订单需管理员手动确认发货（推荐开启）。关闭后用户选静态码下单将<strong>立即自动发货</strong>，无法核实是否付款。</span>
          </label>
          <p style="font-size:0.78rem;color:var(--text-muted);margin:-0.35rem 0 0.6rem;line-height:1.45">
            支付宝当面付订单不受「手动确认」影响，支付成功后会始终自动发货。
            ${cfg.alipay_precreate_available ? '<span style="color:var(--clr-success)">当前：支付宝当面付已配置。</span>' : '<span>当面付：请在服务器配置 ALIPAY_* 环境变量。</span>'}
          </p>
          <p style="font-size:0.78rem;color:var(--text-muted);margin-bottom:0.6rem;line-height:1.45">多商品与独立标价请到侧栏「在售 SKU」维护；未添加任何 SKU 时，用户侧使用本页的默认标题与价格。</p>
          <button type="button" class="btn btn-primary" onclick="saveAdminShopConfig()">保存配置</button>
        </div>
      </div>
      <div class="card">
        <div class="card-header"><div class="card-title">收款二维码（静态码，需先勾选「启用静态收款码」）</div></div>
        <div class="card-body">
          ${cfg.static_qr_enabled === false ? `<p style="font-size:0.85rem;color:var(--clr-warn);margin-bottom:0.75rem">静态收款码已关闭：用户端不会展示静态码，也无法再发起静态码支付。需要时请先勾选上方「启用静态收款码」并保存。</p>` : ''}
          <p style="font-size:0.8rem;color:var(--text-muted);margin-bottom:0.6rem">支持 png / jpg / jpeg / webp / gif / bmp / jfif，单张最大 8MB。</p>
          <div style="display:flex;flex-wrap:wrap;gap:1rem;margin-bottom:0.8rem">
            ${cfg.wechat_qr_url ? `<div><div class="form-hint">当前微信</div><img class="shop-qr-img" src="${escHtml(cfg.wechat_qr_url)}" alt="" /></div>` : ''}
            ${cfg.alipay_qr_url ? `<div><div class="form-hint">当前支付宝</div><img class="shop-qr-img" src="${escHtml(cfg.alipay_qr_url)}" alt="" /></div>` : ''}
          </div>
          <div class="form-group"><label class="form-label">上传微信收款码</label><input type="file" id="shop-qr-wechat" accept="image/*" ${cfg.static_qr_enabled === false ? 'disabled' : ''} /></div>
          <div class="form-group"><label class="form-label">上传支付宝收款码</label><input type="file" id="shop-qr-alipay" accept="image/*" ${cfg.static_qr_enabled === false ? 'disabled' : ''} /></div>
          <button type="button" class="btn btn-success btn-sm" onclick="uploadShopQR()" ${cfg.static_qr_enabled === false ? 'disabled' : ''}>上传所选图片</button>
        </div>
      </div>
    </div>
  `;
}

async function renderAdminShopInventory(container) {
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `<button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-settings')">商品与收款</button>
      <button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-products')">在售 SKU</button>
      <button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-orders')">订单与发货</button>`;
  }
  const inventoryPage = state.adminShopInventoryPage || 1;
  const inventoryStatus = state.adminShopInventoryStatus || 'all';
  const batchFilter = state.adminShopInventoryBatch || '';
  const productFilter = state.adminShopInventoryProduct || '';
  const defBatch = shopDefaultBatchMMDD();
  const [cfg, inventoryRes, batchesRes, productsRes] = await Promise.all([
    api.admin.shopGetConfig(),
    api.admin.shopListInventory(inventoryStatus, inventoryPage, 30, batchFilter, productFilter).catch(() => ({
      data: [],
      total: 0,
      page: inventoryPage,
      size: 30,
      summary: {
        total: 0,
        available: 0,
        sold: 0,
      },
    })),
    api.admin.shopListInventoryBatches().catch(() => ({ data: [], unbatched_available: 0 })),
    api.admin.shopListProducts().catch(() => ({ data: [], stock_unassigned: 0 })),
  ]);
  const products = productsRes.data || [];
  const unassignedStock = Number(productsRes.stock_unassigned ?? cfg.stock_unassigned ?? 0);
  const productMap = new Map(products.map(p => [p.id, p]));
  const productOptions = products.map(p => {
    const dedicated = Number(p.stock_dedicated ?? 0);
    const dt = p.delivery_type || 'card_key';
    const schemaAttr = p.delivery_schema ? ` data-schema='${escHtml(JSON.stringify(p.delivery_schema))}'` : '';
    return `<option value="${escHtml(p.id)}" data-delivery-type="${escHtml(dt)}"${schemaAttr}>${escHtml(p.title || '—')}（专属待售 ${dedicated}）</option>`;
  }).join('');
  const productFilterOptions = products.map(p => {
    const sel = productFilter === p.id ? 'selected' : '';
    const dedicated = Number(p.stock_dedicated ?? 0);
    return `<option value="${escHtml(p.id)}" ${sel}>${escHtml(p.title || '—')}（专属 ${dedicated}）</option>`;
  }).join('');
  const inventoryRows = inventoryRes.data || [];
  const inventoryTotal = inventoryRes.total ?? 0;
  const inventorySize = inventoryRes.size || 30;
  const inventoryMaxPage = Math.max(1, Math.ceil(inventoryTotal / inventorySize) || 1);
  const inventorySummary = inventoryRes.summary || {};
  const totalStock = inventorySummary.total ?? cfg.stock_total ?? ((cfg.stock_available ?? 0) + (cfg.stock_sold ?? 0));
  const availableStock = inventorySummary.available ?? cfg.stock_available ?? 0;
  const soldStock = inventorySummary.sold ?? cfg.stock_sold ?? 0;
  const unbatchedAvail = Number(batchesRes.unbatched_available) || 0;
  const batchRows = batchesRes.data || [];
  const batchOpts = batchRows.map(b => {
    const sel = batchFilter === b.label ? 'selected' : '';
    return `<option value="${escHtml(b.label)}" ${sel}>${escHtml(b.label)}（待售 ${b.available} / 共 ${b.total}）</option>`;
  }).join('');
  const noneOpt = unbatchedAvail > 0
    ? `<option value="__none__" ${batchFilter === '__none__' ? 'selected' : ''}>无批次（待售 ${unbatchedAvail}）</option>`
    : '';

  container.innerHTML = `
    <div style="max-width:1120px;display:flex;flex-direction:column;gap:1rem">
      <div class="card">
        <div class="card-header"><div class="card-title">导入库存</div></div>
        <div class="card-body">
          <p style="font-size:0.8rem;color:var(--text-secondary);line-height:1.55;margin-bottom:0.5rem">
            每行一件：<code>邮箱####登录key</code>、<code>----</code> 或 <code>====</code> 分隔；也支持 CSV 两列（自动识别邮箱列）。
          </p>
          <div class="form-group">
            <label class="form-label">本批批次标识</label>
            <input class="form-input" id="shop-import-batch" placeholder="如 0404、1；留空则按服务器 Asia/Shanghai 当日 MMDD" style="max-width:280px" value="${escHtml(defBatch)}" />
            <div class="form-hint">本次导入的所有行共用此标签，便于按批筛选、删除待售</div>
          </div>
          <div class="form-group">
            <label class="form-label">关联商品（库存池归属）</label>
            <select class="form-input" id="shop-import-product" style="max-width:360px" onchange="updateImportUIForProduct()">
              <option value="" data-delivery-type="card_key">通用池（任意 SKU 订单均可兜底取用）</option>
              ${productOptions}
            </select>
            <div class="form-hint">选择某商品 = 本批只供该 SKU 订单使用；留空 = 通用池（所有 SKU 订单的兜底，仅支持卡密格式）</div>
          </div>

          <!-- card_key 模式（默认） -->
          <div id="shop-import-mode-cardkey">
            <div class="form-group">
              <label class="form-label">文件导入</label>
              <input type="file" id="shop-import-file" accept=".txt,.csv,text/plain,text/csv" onchange="loadShopImportFile()" />
              <div class="form-hint">支持 txt / csv，载入后会自动统计识别条数</div>
            </div>
            <textarea class="form-input" id="shop-import-ta" rows="8" style="resize:vertical;font-family:var(--font-mono);font-size:0.78rem" placeholder="user@mail.com####tm_xxx" oninput="handleShopImportInput()"></textarea>
            <div id="shop-import-stats" class="form-hint" style="margin-top:0.5rem"></div>
          </div>

          <!-- text 模式 -->
          <div id="shop-import-mode-text" style="display:none">
            <div class="form-hint" style="margin-bottom:0.5rem">该商品的发货模式为 <strong>长文本</strong>：每条发货内容以 <code>####</code> 分隔；单条可以是多行（如网盘链接 + 提取码 + 备注）。</div>
            <textarea class="form-input" id="shop-import-text-ta" rows="10" style="resize:vertical;font-size:0.82rem" placeholder="https://pan.example.com/xxx&#10;提取码：a1b2&#10;####&#10;https://pan.example.com/yyy&#10;提取码：c3d4"></textarea>
          </div>

          <!-- custom_kv 模式 -->
          <div id="shop-import-mode-kv" style="display:none">
            <div class="form-hint" style="margin-bottom:0.5rem">该商品的发货模式为 <strong>自定义字段</strong>：按下方表格每行一件添加；字段由 SKU 的 schema 决定。</div>
            <div id="shop-import-kv-items" style="display:flex;flex-direction:column;gap:0.7rem"></div>
            <button type="button" class="btn btn-ghost btn-sm" style="margin-top:0.5rem" onclick="addImportKVItem()">+ 再添加一条</button>
          </div>

          <div style="display:flex;flex-wrap:wrap;gap:0.6rem;align-items:center;margin-top:0.8rem">
            <button type="button" class="btn btn-primary btn-sm" onclick="runShopImport()">导入到待售池</button>
            <span style="font-size:0.75rem;color:var(--text-muted)">当前可售库存：<strong>${availableStock}</strong> 件（其中通用池 <strong>${unassignedStock}</strong>）/ 已售 <strong>${soldStock}</strong> 件 / 总计 <strong>${totalStock}</strong> 件</span>
          </div>
        </div>
      </div>
      <div class="card">
        <div class="card-header" style="align-items:flex-start;flex-wrap:wrap;gap:0.75rem">
          <div style="flex:1;min-width:200px">
            <div class="card-title">货物列表</div>
            <div style="font-size:0.75rem;color:var(--text-muted);margin-top:0.2rem">30 条/页 · 按状态与批次筛选 · 批量删除仅影响<strong>待售</strong></div>
          </div>
          <div style="display:flex;flex-wrap:wrap;gap:0.5rem;align-items:center">
            <span style="font-size:0.78rem;color:var(--text-muted)">状态</span>
            <select class="form-input" style="min-width:120px" onchange="adminShopInventorySetStatus(this.value)">
              <option value="all" ${inventoryStatus === 'all' ? 'selected' : ''}>全部</option>
              <option value="available" ${inventoryStatus === 'available' ? 'selected' : ''}>未售出</option>
              <option value="sold" ${inventoryStatus === 'sold' ? 'selected' : ''}>已售出</option>
            </select>
            <span style="font-size:0.78rem;color:var(--text-muted)">批次</span>
            <select class="form-input" style="min-width:200px" onchange="adminShopInventorySetBatch(this.value)">
              <option value="" ${batchFilter === '' ? 'selected' : ''}>全部批次</option>
              ${noneOpt}
              ${batchOpts}
            </select>
            <span style="font-size:0.78rem;color:var(--text-muted)">商品</span>
            <select class="form-input" style="min-width:200px" onchange="adminShopInventorySetProduct(this.value)">
              <option value="" ${productFilter === '' ? 'selected' : ''}>全部</option>
              <option value="__none__" ${productFilter === '__none__' ? 'selected' : ''}>通用池（未绑定 SKU · 可售 ${unassignedStock}）</option>
              ${productFilterOptions}
            </select>
          </div>
        </div>
        <div class="card-body" style="padding-top:0">
          <div style="display:flex;flex-wrap:wrap;gap:0.5rem;margin-bottom:0.85rem;align-items:center">
            <button type="button" class="btn btn-danger btn-sm" ${batchFilter === '' ? 'disabled' : ''} onclick="confirmPurgeShopInventoryBatch()">删除本批次·全部待售</button>
            <button type="button" class="btn btn-danger btn-sm" onclick="confirmPurgeAllShopAvailable()">一键清空·全部待售</button>
            <span style="font-size:0.74rem;color:var(--text-muted)">须先选择具体批次才可「删除本批次」；两项操作均有二次确认</span>
          </div>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>邮箱 / Key</th><th>批次</th><th>所属商品</th><th>状态</th><th>关联订单</th><th>导入时间</th><th></th></tr></thead>
            <tbody>
              ${inventoryRows.length ? inventoryRows.map(item => {
                const emailJs = JSON.stringify(item.email || '');
                const keyJs = JSON.stringify(item.api_key || '');
                const labelJs = JSON.stringify(item.email || item.id || '');
                const bl = (item.batch_label || '').trim();
                const blShow = bl ? escHtml(bl) : '<span style="color:var(--text-muted)">—</span>';
                const prod = item.product_id ? productMap.get(item.product_id) : null;
                const prodShow = item.product_id
                  ? (prod ? escHtml(prod.title || '—') : '<span style="color:var(--clr-warn)">已删除 SKU</span>')
                  : '<span class="badge badge-gray">通用池</span>';
                return `
                <tr data-shop-inventory-row>
                  <td>
                    <div class="code-box" style="font-size:0.72rem"><span>${escHtml(item.email || '')}</span><button type="button" class="copy-btn" onclick='copyText(${emailJs})'>⧉</button></div>
                    <div class="code-box" style="margin-top:0.35rem;font-size:0.72rem"><span>${escHtml(item.api_key || '')}</span><button type="button" class="copy-btn" onclick='copyText(${keyJs})'>⧉</button></div>
                  </td>
                  <td style="font-size:0.78rem;font-weight:600">${blShow}</td>
                  <td style="font-size:0.78rem">${prodShow}</td>
                  <td>${item.status === 'sold' ? '<span class="badge badge-gray">已售出</span>' : '<span class="badge badge-green">待售</span>'}</td>
                  <td style="font-size:0.72rem;font-family:var(--font-mono)">${item.order_id ? escHtml(item.order_id) : '<span style="color:var(--text-muted)">—</span>'}</td>
                  <td style="font-size:0.78rem">${formatDate(item.created_at)}</td>
                  <td><button type="button" class="btn btn-danger btn-sm" onclick='deleteShopInventory("${item.id}", ${labelJs})'>删除</button></td>
                </tr>`;
              }).join('') : '<tr><td colspan="7" style="text-align:center;color:var(--text-muted);padding:1rem">暂无货物</td></tr>'}
            </tbody>
          </table>
        </div>
        <div style="display:flex;gap:0.5rem;align-items:center;justify-content:space-between;margin-top:1rem;flex-wrap:wrap;padding:0 1.2rem 1.2rem">
          <div style="font-size:0.82rem;color:var(--text-muted)">共 ${inventoryTotal} 条 · 第 ${inventoryPage} / ${inventoryMaxPage} 页</div>
          <div style="display:flex;gap:0.5rem;align-items:center">
            <button class="btn btn-ghost btn-sm" ${inventoryPage <= 1 ? 'disabled' : ''} onclick="adminShopInventoryGoPage(${inventoryPage - 1})">上一页</button>
            <button class="btn btn-ghost btn-sm" ${inventoryPage >= inventoryMaxPage ? 'disabled' : ''} onclick="adminShopInventoryGoPage(${inventoryPage + 1})">下一页</button>
          </div>
        </div>
      </div>
    </div>
  `;
  updateShopImportStats($('shop-import-ta')?.value || '');
  updateImportUIForProduct();
}

async function renderAdminShopOrders(container) {
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `<button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-settings')">商品与收款</button>
      <button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-products')">在售 SKU</button>
      <button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-inventory')">库存与货物</button>`;
  }
  const status = state.adminShopOrdersStatus || '';
  const page = state.adminShopOrdersPage || 1;
  const size = 25;
  const res = await api.admin.shopListOrders(status, page, size).catch(() => ({ data: [], total: 0, page: 1, size }));
  const rows = res.data || [];
  const total = res.total ?? 0;
  const maxPage = Math.max(1, Math.ceil(total / size) || 1);
  const statusSel = !status ? 'all' : status;

  container.innerHTML = `
    <div style="max-width:1120px;display:flex;flex-direction:column;gap:1rem">
      <div class="card">
        <div class="card-header" style="align-items:flex-start;flex-wrap:wrap;gap:0.75rem">
          <div>
            <div class="card-title">订单列表</div>
            <div style="font-size:0.75rem;color:var(--text-muted);margin-top:0.2rem">筛选后可查看详情；待确认订单可在此确认收款并发货</div>
          </div>
          <div style="display:flex;gap:0.5rem;align-items:center;flex-wrap:wrap">
            <span style="font-size:0.78rem;color:var(--text-muted)">状态</span>
            <select class="form-input" style="min-width:160px" onchange="adminShopOrdersSetStatus(this.value)">
              <option value="all" ${statusSel === 'all' ? 'selected' : ''}>全部</option>
              <option value="awaiting_payment" ${statusSel === 'awaiting_payment' ? 'selected' : ''}>待确认收款</option>
              <option value="fulfilled" ${statusSel === 'fulfilled' ? 'selected' : ''}>已发货</option>
            </select>
          </div>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>订单号</th><th>商品</th><th>买家账号ID</th><th>数量</th><th>应付</th><th>状态</th><th>时间</th><th></th></tr></thead>
            <tbody>
              ${rows.length ? rows.map(o => {
                const st = o.status === 'fulfilled' ? '<span class="badge badge-green">已发货</span>' : '<span class="badge badge-gray">待确认</span>';
                const confirmBtn = o.status === 'awaiting_payment'
                  ? `<button type="button" class="btn btn-success btn-sm" onclick='confirmShopOrderPaid("${o.id}")'>确认发货</button>`
                  : '';
                const ptitle = (o.product_title_snapshot && String(o.product_title_snapshot).trim()) || '—';
                return `
                <tr>
                  <td style="font-size:0.7rem;font-family:var(--font-mono);max-width:120px;word-break:break-all">${escHtml(o.id)}</td>
                  <td style="font-size:0.78rem;max-width:140px">${escHtml(ptitle)}</td>
                  <td style="font-size:0.7rem;font-family:var(--font-mono);max-width:100px;word-break:break-all">${escHtml(o.account_id)}</td>
                  <td>${o.quantity}</td>
                  <td>¥${(o.total_cents / 100).toFixed(2)}</td>
                  <td>${st}</td>
                  <td style="font-size:0.78rem">${formatDate(o.created_at)}</td>
                  <td style="display:flex;flex-wrap:wrap;gap:0.35rem">
                    <button type="button" class="btn btn-ghost btn-sm" onclick='showAdminShopOrderDetail("${o.id}")'>详情</button>
                    ${confirmBtn}
                  </td>
                </tr>`;
              }).join('') : '<tr><td colspan="8" style="text-align:center;color:var(--text-muted);padding:1rem">暂无订单</td></tr>'}
            </tbody>
          </table>
        </div>
        <div style="display:flex;gap:0.5rem;align-items:center;justify-content:space-between;margin-top:1rem;flex-wrap:wrap">
          <div style="font-size:0.82rem;color:var(--text-muted)">共 ${total} 条 · 第 ${page} / ${maxPage} 页</div>
          <div style="display:flex;gap:0.5rem;align-items:center">
            <button class="btn btn-ghost btn-sm" ${page <= 1 ? 'disabled' : ''} onclick="adminShopOrdersGoPage(${page - 1})">上一页</button>
            <button class="btn btn-ghost btn-sm" ${page >= maxPage ? 'disabled' : ''} onclick="adminShopOrdersGoPage(${page + 1})">下一页</button>
          </div>
        </div>
      </div>
    </div>
  `;
}

window.adminShopProductStartEdit = function(id) {
  state.adminShopProductEditId = id || null;
  navigate('admin-shop-products');
};

window.adminShopProductCancelEdit = function() {
  state.adminShopProductEditId = null;
  navigate('admin-shop-products');
};

// v10：SKU 表单的 schema 行操作
window.addKVSchemaRow = function(key = '', label = '', hint = '', multiline = false) {
  const box = $('shop-prod-schema-rows');
  if (!box) return;
  const row = document.createElement('div');
  row.className = 'kv-row';
  row.innerHTML = `
    <input type="text" placeholder="字段 key（英文）" value="${escHtml(key)}" class="kv-key" />
    <input type="text" placeholder="展示名（如 网盘链接）" value="${escHtml(label)}" class="kv-label" />
    <button type="button" class="btn-kv-del" onclick="this.parentElement.remove()">✕</button>
    <input type="text" placeholder="可选提示" value="${escHtml(hint)}" class="kv-hint" style="grid-column:1 / -1;margin-top:-4px;padding:0.35rem 0.55rem;font-size:0.78rem" />
    <label style="grid-column:1 / -1;font-size:0.78rem;color:var(--text-muted);display:flex;gap:0.35rem;align-items:center;margin-bottom:0.3rem">
      <input type="checkbox" class="kv-multi" ${multiline?'checked':''} /> 多行文本
    </label>
  `;
  box.appendChild(row);
};

window.handleDeliveryTypeChange = function() {
  const v = $('shop-prod-delivery-type')?.value || 'card_key';
  const kvBox = $('shop-prod-schema-wrap');
  if (kvBox) kvBox.style.display = (v === 'custom_kv') ? 'block' : 'none';
};

window.submitAdminShopProductForm = async function() {
  const editId = ($('shop-prod-edit-id')?.value || '').trim();
  const body = {
    title: ($('shop-prod-title')?.value || '').trim(),
    description: ($('shop-prod-desc')?.value || '').trim(),
    tag: ($('shop-prod-tag')?.value || '').trim(),
    retail_price_yuan: parseFloat($('shop-prod-retail')?.value || '0') || 0,
    wholesale_price_yuan: parseFloat($('shop-prod-wholesale')?.value || '0') || 0,
    wholesale_min_qty: parseInt($('shop-prod-wsqty')?.value || '5', 10) || 5,
    sort_order: parseInt($('shop-prod-sort')?.value || '0', 10) || 0,
    enabled: $('shop-prod-enabled')?.checked !== false,
    // v10：发货模式 + SVIP 价
    delivery_type: $('shop-prod-delivery-type')?.value || 'card_key',
  };
  const svipYuan = ($('shop-prod-svip-price')?.value || '').trim();
  if (svipYuan === '') {
    body.clear_svip_price = true;
  } else {
    const n = parseFloat(svipYuan);
    if (!isFinite(n) || n < 0) { toast('SVIP 价格无效', 'warn'); return; }
    body.svip_price_yuan = n;
  }
  if (body.delivery_type === 'custom_kv') {
    const rows = document.querySelectorAll('#shop-prod-schema-rows .kv-row');
    const fields = [];
    rows.forEach(r => {
      const key = r.querySelector('.kv-key').value.trim();
      const label = r.querySelector('.kv-label').value.trim();
      const hint = r.querySelector('.kv-hint').value.trim();
      const multi = r.querySelector('.kv-multi').checked;
      if (key && label) fields.push({ key, label, hint, multiline: multi });
    });
    if (fields.length === 0) { toast('自定义 KV 至少需要 1 个字段', 'warn'); return; }
    body.delivery_schema = { fields };
  } else {
    body.delivery_schema = { fields: [] };
  }
  if (!body.title) {
    toast('请填写商品标题', 'warn');
    return;
  }
  try {
    if (editId) {
      await api.admin.shopUpdateProduct(editId, body);
      toast('已更新 SKU', 'success');
    } else {
      await api.admin.shopCreateProduct(body);
      toast('已新增 SKU', 'success');
    }
    state.adminShopProductEditId = null;
    navigate('admin-shop-products');
  } catch (e) {
    toast(e.message || '保存失败', 'error');
  }
};

window.adminShopProductDelete = function(id, title) {
  showModal('删除 SKU', `<p>确定删除 <strong>${escHtml(title || id)}</strong> 吗？历史订单仍保留快照。</p>`, async () => {
    try {
      await api.admin.shopDeleteProduct(id);
      toast('已删除', 'success');
      if (state.adminShopProductEditId === id) state.adminShopProductEditId = null;
      navigate('admin-shop-products');
    } catch (e) {
      toast(e.message || '删除失败', 'error');
    }
  });
};

async function renderAdminShopProducts(container) {
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `<button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-settings')">商品与收款</button>
      <button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-inventory')">库存与货物</button>
      <button type="button" class="btn btn-ghost btn-sm" onclick="navigate('admin-shop-orders')">订单与发货</button>`;
  }
  const res = await api.admin.shopListProducts().catch(() => ({ data: [], stock_unassigned: 0 }));
  const list = res.data || [];
  const unassignedStock = Number(res.stock_unassigned ?? 0);
  const editId = state.adminShopProductEditId;
  const editing = editId ? list.find(p => p.id === editId) : null;

  const formTitle = editing ? '编辑 SKU' : '新增 SKU';
  const v = editing || {};
  const hid = editing ? `<input type="hidden" id="shop-prod-edit-id" value="${escHtml(editing.id)}" />` : '<input type="hidden" id="shop-prod-edit-id" value="" />';

  container.innerHTML = `
    <div style="max-width:1120px;display:flex;flex-direction:column;gap:1rem">
      <div class="card">
        <div class="card-header" style="justify-content:space-between;align-items:center;flex-wrap:wrap;gap:0.75rem">
          <div class="card-title">${escHtml(formTitle)}</div>
          ${editing ? '<button type="button" class="btn btn-ghost btn-sm" onclick="adminShopProductCancelEdit()">取消编辑</button>' : ''}
        </div>
        <div class="card-body">
          ${hid}
          <div class="form-group"><label class="form-label">标题</label>
            <input class="form-input" id="shop-prod-title" placeholder="如：Claude 成品号" value="${escHtml(v.title || '')}" /></div>
          <div class="form-group"><label class="form-label">标签（展示在卡片角标，可空）</label>
            <input class="form-input" id="shop-prod-tag" placeholder="如：热销 / 粉丝价" value="${escHtml(v.tag || '')}" /></div>
          <div class="form-group"><label class="form-label">说明</label>
            <textarea class="form-input" id="shop-prod-desc" rows="3" style="resize:vertical">${escHtml(v.description || '')}</textarea></div>
          <div style="display:grid;grid-template-columns:1fr 1fr;gap:0.75rem">
            <div class="form-group"><label class="form-label">零售价（元/件）</label>
              <input type="number" step="0.01" class="form-input" id="shop-prod-retail" value="${v.retail_price_yuan != null ? Number(v.retail_price_yuan).toFixed(2) : ''}" /></div>
            <div class="form-group"><label class="form-label">批发价（元/件）</label>
              <input type="number" step="0.01" class="form-input" id="shop-prod-wholesale" value="${v.wholesale_price_yuan != null ? Number(v.wholesale_price_yuan).toFixed(2) : ''}" /></div>
            <div class="form-group"><label class="form-label">批发起订件数</label>
              <input type="number" class="form-input" id="shop-prod-wsqty" value="${v.wholesale_min_qty ?? 5}" /></div>
            <div class="form-group"><label class="form-label">排序（越小越靠前）</label>
              <input type="number" class="form-input" id="shop-prod-sort" value="${v.sort_order ?? 0}" /></div>
            <div class="form-group"><label class="form-label">✦ SVIP 专享价（元/件，留空=不设）</label>
              <input type="number" step="0.01" class="form-input" id="shop-prod-svip-price" value="${v.svip_price_yuan != null ? Number(v.svip_price_yuan).toFixed(2) : ''}" placeholder="例如 79.00" /></div>
            <div class="form-group"><label class="form-label">发货模式</label>
              <select class="form-input" id="shop-prod-delivery-type" onchange="handleDeliveryTypeChange()">
                <option value="card_key"  ${(v.delivery_type||'card_key')==='card_key'?'selected':''}>邮箱 + 卡密（默认）</option>
                <option value="text"      ${v.delivery_type==='text'?'selected':''}>长文本（适合网盘链接、激活码等）</option>
                <option value="custom_kv" ${v.delivery_type==='custom_kv'?'selected':''}>自定义字段（URL + 口令 + 备注…）</option>
              </select>
            </div>
          </div>
          <div id="shop-prod-schema-wrap" style="display:${v.delivery_type==='custom_kv'?'block':'none'}">
            <label class="form-label">自定义字段定义</label>
            <div class="kv-schema-editor">
              <div id="shop-prod-schema-rows"></div>
              <button type="button" class="btn btn-ghost btn-sm" onclick="addKVSchemaRow()">+ 添加字段</button>
              <p class="form-hint">例如「url=网盘链接 / code=提取码 / note=备注」；库存导入时管理员按这些字段填写。</p>
            </div>
          </div>
          <label style="display:flex;align-items:center;gap:0.45rem;margin:0.6rem 0;cursor:pointer">
            <input type="checkbox" id="shop-prod-enabled" ${editing && v.enabled === false ? '' : 'checked'} />
            <span>上架（关闭后用户侧不展示）</span>
          </label>
          <button type="button" class="btn btn-primary" onclick="submitAdminShopProductForm()">${editing ? '保存修改' : '添加 SKU'}</button>
        </div>
      </div>
      <div class="card">
        <div class="card-header" style="align-items:flex-start;flex-wrap:wrap;gap:0.6rem">
          <div class="card-title">已维护的 SKU</div>
          <div style="font-size:0.78rem;color:var(--text-muted)">通用池（未绑定 SKU 的库存）可售：<strong>${unassignedStock}</strong> 件；任何 SKU 订单在专属池不足时会自动从通用池兜底。</div>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>排序</th><th>标题</th><th>标签</th><th>零售</th><th>批发</th><th>专属库存</th><th>上架</th><th></th></tr></thead>
            <tbody>
              ${list.length ? list.map(p => {
                const dedicated = Number(p.stock_dedicated ?? 0);
                const total = Number(p.stock_with_unassigned ?? dedicated);
                return `
                <tr>
                  <td>${p.sort_order ?? 0}</td>
                  <td style="font-size:0.85rem">${escHtml(p.title || '')}</td>
                  <td style="font-size:0.8rem">${escHtml(p.tag || '—')}</td>
                  <td>¥${Number(p.retail_price_yuan).toFixed(2)}</td>
                  <td>¥${Number(p.wholesale_price_yuan).toFixed(2)} <span style="font-size:0.72rem;color:var(--text-muted)">(${p.wholesale_min_qty || 5}件起)</span></td>
                  <td style="font-size:0.82rem"><strong>${dedicated}</strong> <span style="color:var(--text-muted);font-size:0.72rem">（含兜底 ${total}）</span></td>
                  <td>${p.enabled !== false ? '是' : '否'}</td>
                  <td style="display:flex;flex-wrap:wrap;gap:0.35rem">
                    <button type="button" class="btn btn-ghost btn-sm" onclick="adminShopProductStartEdit('${escHtml(p.id)}')">编辑</button>
                    <button type="button" class="btn btn-ghost btn-sm" onclick="adminShopProductDelete(${JSON.stringify(p.id)}, ${JSON.stringify(p.title || '')})">删除</button>
                  </td>
                </tr>`;
              }).join('') : '<tr><td colspan="8" style="text-align:center;color:var(--text-muted);padding:1rem">暂无 SKU，用户侧将使用「商品与收款」页的默认单价</td></tr>'}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  `;

  // v10：若正在编辑 custom_kv 模式的商品，把现有 schema 字段回填
  if (editing && editing.delivery_type === 'custom_kv' && editing.delivery_schema && Array.isArray(editing.delivery_schema.fields)) {
    editing.delivery_schema.fields.forEach(f => {
      window.addKVSchemaRow(f.key || '', f.label || '', f.hint || '', !!f.multiline);
    });
  }
}

// ─── v10：我的优惠券（用户页）────────────────────────────────
window.myCouponsSetStatus = function(status) {
  state.myCouponStatus = status || 'available';
  navigate('my-coupons');
};

window.redeemMyCouponCode = async function() {
  const code = ($('my-coupon-code')?.value || '').trim();
  if (!code) { toast('请输入领取码', 'warn'); return; }
  try {
    await api.couponRedeem(code);
    toast('领取成功！', 'success');
    navigate('my-coupons');
  } catch(e) {
    toast('领取失败：' + (e.message || ''), 'error');
  }
};

/** 渲染单张用户优惠券卡片（参数是 user_coupon 对象） */
function renderUserCouponCard(uc, opts = {}) {
  const expired = uc.snapshot_expires_at && new Date(uc.snapshot_expires_at) < new Date();
  const disabled = uc.status !== 'available' || expired;
  const isPct = uc.snapshot_discount_type === 'percentage';
  const amount = isPct
    ? `<span class="coupon-card-amount">${uc.snapshot_discount_value}<small>%OFF</small></span>`
    : `<span class="coupon-card-amount"><small>¥</small>${(uc.snapshot_discount_value / 100).toFixed(0)}</span>`;
  const kind = isPct ? '折扣券' : '满减券';
  const minY = (uc.snapshot_min_order_cents || 0) / 100;
  const metaParts = [];
  if (minY > 0) metaParts.push(`<span class="tag">满 ¥${minY.toFixed(0)}</span>`);
  if (uc.snapshot_expires_at) metaParts.push(`过期：${formatDate(uc.snapshot_expires_at)}`);
  if (uc.status === 'used') metaParts.push('已使用');
  else if (uc.status === 'expired' || expired) metaParts.push('已过期');
  else if (uc.status === 'revoked') metaParts.push('已撤销');

  let actions = '';
  if (!disabled && opts.showShopLink) {
    actions = `<div class="coupon-card-actions"><button class="btn btn-primary btn-sm" onclick="navigate('claude-shop')">去使用</button></div>`;
  }

  return `<div class="coupon-card ${disabled?'is-disabled':''}">
    <div class="coupon-card-left">
      ${amount}
      <div class="coupon-card-kind">${kind}</div>
    </div>
    <div class="coupon-card-right">
      <div class="coupon-card-name">${escHtml(uc.snapshot_name || '优惠券')}</div>
      <div class="coupon-card-desc">领取于 ${formatDate(uc.acquired_at)}${uc.used_at?' · 使用于 '+formatDate(uc.used_at):''}</div>
      <div class="coupon-card-meta">${metaParts.join('')}</div>
      ${actions}
    </div>
  </div>`;
}

async function renderMyCoupons(container) {
  const status = state.myCouponStatus || 'available';
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `<button class="btn btn-ghost btn-sm" onclick="navigate('claude-shop')">🛒 自助商城</button>`;
  }

  const res = await api.couponMine(status).catch(() => ({ data: [] }));
  const list = res.data || [];

  const pills = [
    { k: 'available', label: '可用' },
    { k: 'used',      label: '已使用' },
    { k: 'expired',   label: '已过期' },
    { k: 'all',       label: '全部' },
  ].map(o => `<button class="filter-pill ${status===o.k?'active':''}" onclick="myCouponsSetStatus('${o.k}')">${o.label}</button>`).join('');

  const cards = list.map(uc => renderUserCouponCard(uc, { showShopLink: status === 'available' })).join('');

  container.innerHTML = `
    <div style="max-width:960px;display:flex;flex-direction:column;gap:1rem">
      <div class="card">
        <div class="card-header"><div class="card-title">🎟 输入优惠码领取</div></div>
        <div class="card-body">
          <div class="coupon-redeem-bar">
            <input type="text" class="form-input" id="my-coupon-code" placeholder="请输入优惠码" onkeydown="if(event.key==='Enter')redeemMyCouponCode()" />
            <button class="btn btn-primary" onclick="redeemMyCouponCode()">领取</button>
          </div>
          <p style="font-size:0.8rem;color:var(--text-muted);margin-top:0.2rem">领取成功后可在下方"可用"列表中查看；下单时选择使用。</p>
        </div>
      </div>
      <div class="card">
        <div class="card-header">
          <div class="card-title">我的优惠券</div>
          <div class="filter-pills">${pills}</div>
        </div>
        <div class="card-body">
          ${list.length ? `<div class="coupon-grid">${cards}</div>` : `<div class="empty-state"><span class="empty-icon">🎟</span><p>${status==='available'?'暂无可用券，输入优惠码领取吧！':'暂无记录'}</p></div>`}
        </div>
      </div>
    </div>
  `;
}

// ─── v10：优惠券管理（管理员页）────────────────────────────────
window.adminCouponSetStatus = function(status) {
  state.adminCouponStatus = status || '';
  state.adminCouponPage = 1;
  navigate('admin-coupons');
};

window.adminCouponSearch = function() {
  state.adminCouponQ = ($('admin-coupon-q')?.value || '').trim();
  state.adminCouponPage = 1;
  navigate('admin-coupons');
};

window.adminCouponGoPage = function(p) {
  state.adminCouponPage = Math.max(1, p);
  navigate('admin-coupons');
};

window.adminCouponToggle = async function(id, enabled) {
  try {
    await api.admin.couponToggle(id, enabled);
    toast('已更新', 'success');
    navigate('admin-coupons');
  } catch(e) { toast('操作失败：' + (e.message||''), 'error'); }
};

window.adminCouponDelete = function(id, name) {
  showModal('删除优惠券', `<p>确定删除优惠券 <strong>${escHtml(name)}</strong>？已领取的记录会级联删除。</p>`, async () => {
    try {
      await api.admin.couponDelete(id);
      toast('已删除', 'success');
      navigate('admin-coupons');
    } catch(e) { toast('删除失败：' + (e.message||''), 'error'); return false; }
  });
};

window.adminCouponGrant = function(id, name) {
  showModal('定向派发：' + name, `
    <div class="form-group">
      <label class="form-label">目标账户 UUID（每行一个）</label>
      <textarea class="form-input" id="grant-account-ids" rows="6" placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx&#10;xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"></textarea>
      <div class="form-hint">管理员在「账户管理」可复制 UUID；每张券按 per_user_limit 去重。</div>
    </div>
  `, async () => {
    const raw = ($('grant-account-ids')?.value || '').trim();
    if (!raw) { toast('请输入账户 UUID', 'warn'); return false; }
    const ids = raw.split(/\s+/).filter(Boolean);
    if (ids.length === 0) { toast('请输入账户 UUID', 'warn'); return false; }
    try {
      const r = await api.admin.couponGrant(id, ids);
      const failed = (r.results || []).filter(x => !x.ok);
      if (failed.length) {
        toast(`成功 ${r.granted}/${r.total}；失败：${failed[0].error}`, 'warn');
      } else {
        toast(`已派发 ${r.granted} 张`, 'success');
      }
      navigate('admin-coupons');
    } catch(e) { toast('派发失败：' + (e.message||''), 'error'); return false; }
  });
};

window.showCouponEditor = function(existing) {
  const c = existing || {
    name: '', description: '', code: '',
    discount_type: 'percentage', discount_value: 10,
    min_order_cents: 0, max_discount_cents: 0,
    total_quota: 0, per_user_limit: 1,
    starts_at: null, expires_at: null,
    svip_only: false, new_user_gift: false, svip_gift: false,
    enabled: true,
  };
  const isEdit = !!existing;
  const expires30 = new Date(Date.now() + 30*24*3600*1000).toISOString().slice(0, 16);
  const title = isEdit ? '编辑优惠券' : '新建优惠券';
  showModal(title, `
    <div class="form-group">
      <label class="form-label">名称 *</label>
      <input type="text" class="form-input" id="c-name" value="${escHtml(c.name)}" placeholder="如「新人 9 折券」" />
    </div>
    <div class="form-group">
      <label class="form-label">描述</label>
      <textarea class="form-input" id="c-desc" rows="2" placeholder="向用户展示的说明">${escHtml(c.description || '')}</textarea>
    </div>
    <div class="form-group">
      <label class="form-label">公开领取码（留空 = 仅管理员定向派发 / 自动赠送）</label>
      <input type="text" class="form-input" id="c-code" value="${escHtml(c.code || '')}" placeholder="NEWYEAR2026" style="font-family:var(--font-mono)" />
    </div>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:0.6rem">
      <div class="form-group">
        <label class="form-label">折扣类型</label>
        <select class="form-input" id="c-type">
          <option value="percentage" ${c.discount_type==='percentage'?'selected':''}>百分比折扣 (%)</option>
          <option value="fixed"      ${c.discount_type==='fixed'?'selected':''}>固定金额 (分)</option>
        </select>
      </div>
      <div class="form-group">
        <label class="form-label">折扣值</label>
        <input type="number" class="form-input" id="c-value" value="${c.discount_value || 10}" min="1" />
        <div class="form-hint">百分比：1-100；固定：分（1 元 = 100）</div>
      </div>
    </div>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:0.6rem">
      <div class="form-group">
        <label class="form-label">最低订单金额（分）</label>
        <input type="number" class="form-input" id="c-min" value="${c.min_order_cents || 0}" min="0" />
      </div>
      <div class="form-group">
        <label class="form-label">最高优惠封顶（分，0=不限）</label>
        <input type="number" class="form-input" id="c-max" value="${c.max_discount_cents || 0}" min="0" />
      </div>
    </div>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:0.6rem">
      <div class="form-group">
        <label class="form-label">总发放数量（0=无限）</label>
        <input type="number" class="form-input" id="c-quota" value="${c.total_quota || 0}" min="0" />
      </div>
      <div class="form-group">
        <label class="form-label">单用户可领次数</label>
        <input type="number" class="form-input" id="c-peruser" value="${c.per_user_limit || 1}" min="1" />
      </div>
    </div>
    <div class="form-group">
      <label class="form-label">有效期至</label>
      <input type="datetime-local" class="form-input" id="c-expires" value="${c.expires_at ? new Date(c.expires_at).toISOString().slice(0,16) : expires30}" />
      <div class="form-hint">留空表示永不过期</div>
    </div>
    <div class="form-group">
      <label style="display:flex;align-items:center;gap:0.5rem;font-size:0.88rem"><input type="checkbox" id="c-svip-only" ${c.svip_only?'checked':''} /> 仅 SVIP 可领/使用</label>
      <label style="display:flex;align-items:center;gap:0.5rem;font-size:0.88rem;margin-top:0.3rem"><input type="checkbox" id="c-new-user" ${c.new_user_gift?'checked':''} /> 新用户注册自动赠送</label>
      <label style="display:flex;align-items:center;gap:0.5rem;font-size:0.88rem;margin-top:0.3rem"><input type="checkbox" id="c-svip-gift" ${c.svip_gift?'checked':''} /> 授予 SVIP 时自动赠送</label>
      <label style="display:flex;align-items:center;gap:0.5rem;font-size:0.88rem;margin-top:0.3rem"><input type="checkbox" id="c-enabled" ${c.enabled!==false?'checked':''} /> 启用</label>
    </div>
  `, async () => {
    const body = {
      name: ($('c-name').value || '').trim(),
      description: ($('c-desc').value || '').trim(),
      code: ($('c-code').value || '').trim() || null,
      discount_type: $('c-type').value,
      discount_value: parseInt($('c-value').value || '0', 10) || 0,
      min_order_cents: parseInt($('c-min').value || '0', 10) || 0,
      max_discount_cents: parseInt($('c-max').value || '0', 10) || 0,
      total_quota: parseInt($('c-quota').value || '0', 10) || 0,
      per_user_limit: parseInt($('c-peruser').value || '1', 10) || 1,
      svip_only: $('c-svip-only').checked,
      new_user_gift: $('c-new-user').checked,
      svip_gift: $('c-svip-gift').checked,
      enabled: $('c-enabled').checked,
    };
    const exp = ($('c-expires').value || '').trim();
    if (exp) {
      const dt = new Date(exp);
      if (!isNaN(dt.getTime())) body.expires_at = dt.toISOString();
    }
    if (!body.name) { toast('请输入名称', 'warn'); return false; }
    if (body.discount_value <= 0) { toast('折扣值必须 > 0', 'warn'); return false; }
    if (body.discount_type === 'percentage' && body.discount_value > 100) { toast('百分比不得超过 100', 'warn'); return false; }
    try {
      if (isEdit) await api.admin.couponUpdate(existing.id, body);
      else await api.admin.couponCreate(body);
      toast('已保存', 'success');
      navigate('admin-coupons');
    } catch(e) { toast('保存失败：' + (e.message||''), 'error'); return false; }
  });
};

async function renderAdminCoupons(container) {
  const page = state.adminCouponPage || 1;
  const status = state.adminCouponStatus || '';
  const q = state.adminCouponQ || '';
  const actions = $('topbar-actions');
  if (actions) {
    actions.innerHTML = `
      <input class="form-input" id="admin-coupon-q" placeholder="搜名称 / 码" value="${escHtml(q)}"
        style="max-width:180px;padding:0.35rem 0.5rem;font-size:0.82rem" onkeydown="if(event.key==='Enter')adminCouponSearch()" />
      <button class="btn btn-ghost btn-sm" onclick="adminCouponSearch()">搜索</button>
      <button class="btn btn-primary btn-sm" onclick="showCouponEditor()" style="margin-left:0.3rem">+ 新建优惠券</button>
    `;
  }

  const res = await api.admin.couponList(status, q, page, 20).catch(() => ({ data: [], total: 0 }));
  const list = res.data || [];
  const total = res.total ?? list.length;
  const size = res.size || 20;
  const maxPage = Math.max(1, Math.ceil(total / size) || 1);

  const pills = [
    { k: '',          label: '全部' },
    { k: 'enabled',   label: '启用中' },
    { k: 'disabled',  label: '已停用' },
    { k: 'expired',   label: '已过期' },
  ].map(o => `<button class="filter-pill ${status===o.k?'active':''}" onclick="adminCouponSetStatus('${o.k}')">${o.label}</button>`).join('');

  const rows = list.map(c => {
    const isPct = c.discount_type === 'percentage';
    const amt = isPct ? `${c.discount_value}% off` : `-¥${(c.discount_value/100).toFixed(2)}`;
    const tags = [];
    if (c.svip_only) tags.push('<span class="svip-badge svip-badge-sm">SVIP 专享</span>');
    if (c.new_user_gift) tags.push('<span class="badge badge-green">新用户赠</span>');
    if (c.svip_gift) tags.push('<span class="badge badge-gold">SVIP 赠</span>');
    if (c.code) tags.push(`<span class="coupon-card-code">${escHtml(c.code)}</span>`);
    const used = c.used_count || 0;
    const quota = c.total_quota || 0;
    const quotaStr = quota > 0 ? `${used} / ${quota}` : `${used} / ∞`;
    const expStr = c.expires_at ? formatDate(c.expires_at) : '永久';
    const enabled = c.enabled;
    const nameJs = JSON.stringify(c.name || '');
    return `<tr ${!enabled?'style="opacity:0.5"':''}>
      <td>
        <div style="font-weight:600">${escHtml(c.name)}</div>
        <div style="font-size:0.76rem;color:var(--text-muted);margin-top:0.2rem;display:flex;gap:0.3rem;flex-wrap:wrap">${tags.join('')}</div>
      </td>
      <td><span class="badge ${isPct?'badge-green':'badge-gold'}">${escHtml(amt)}</span></td>
      <td style="font-size:0.8rem">${quotaStr}</td>
      <td style="font-size:0.8rem">${expStr}</td>
      <td>${enabled ? '<span class="badge badge-green">启用</span>' : '<span class="badge badge-gray">停用</span>'}</td>
      <td style="white-space:nowrap">
        <button class="btn btn-ghost btn-sm" onclick='showCouponEditor(${JSON.stringify(c).replace(/'/g, "&#39;")})'>编辑</button>
        <button class="btn btn-ghost btn-sm" onclick='adminCouponGrant("${c.id}", ${nameJs})'>📤 派发</button>
        <button class="btn btn-ghost btn-sm" onclick='adminCouponToggle("${c.id}", ${!enabled})'>${enabled?'停用':'启用'}</button>
        <button class="btn btn-danger btn-sm" onclick='adminCouponDelete("${c.id}", ${nameJs})'>删除</button>
      </td>
    </tr>`;
  }).join('');

  container.innerHTML = `
    <div class="card" style="max-width:1040px">
      <div class="card-header">
        <div class="card-title">🎫 优惠券列表</div>
        <div style="font-size:0.78rem;color:var(--text-muted)">共 ${total} 张</div>
      </div>
      <div style="padding:0.7rem 1.3rem 0.4rem;border-bottom:1px solid var(--border-light)">
        <div class="filter-pills">${pills}</div>
      </div>
      <div class="table-wrap">
        <table>
          <thead><tr><th>名称 / 标签</th><th>折扣</th><th>已领/总量</th><th>到期</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>${list.length ? rows : '<tr><td colspan="6" style="text-align:center;color:var(--text-muted);padding:2rem">暂无优惠券</td></tr>'}</tbody>
        </table>
      </div>
      <div style="display:flex;gap:0.5rem;align-items:center;margin:1rem 1.3rem;flex-wrap:wrap">
        <button class="btn btn-ghost btn-sm" ${page <= 1 ? 'disabled' : ''} onclick="adminCouponGoPage(${page - 1})">上一页</button>
        <span style="font-size:0.85rem;color:var(--text-muted)">第 ${page} / ${maxPage} 页</span>
        <button class="btn btn-ghost btn-sm" ${page >= maxPage ? 'disabled' : ''} onclick="adminCouponGoPage(${page + 1})">下一页</button>
      </div>
    </div>
  `;
}

// ─── 启动 ──────────────────────────────────────────────────
async function init() {
  applyTheme(state.theme);
  await loadPublicSiteTitle();
  applySiteBranding();

  if (state.apiKey && state.account) {
    await showMainLayout();
    applySiteBranding();
    navigate('dashboard');
  } else if (state.apiKey) {
    await tryLogin(state.apiKey);
    if (!state.apiKey) showAuthPage();
    applySiteBranding();
  } else {
    showAuthPage();
    applySiteBranding();
  }
}

document.addEventListener('DOMContentLoaded', () => { init().catch(() => { showAuthPage(); applySiteBranding(); }); });
