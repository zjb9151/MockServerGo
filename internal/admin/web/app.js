const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

const titles = {
  overview: ["概览", "查看服务地址、接口数量和最近打到 Mock 端口的请求"],
  interfaces: ["接口", "先新建接口，再为每个接口添加规则。规则用来区分同一次调用的不同响应"],
  live: ["实时监控", "按接口查看输入和输出，也可以再选一条规则"],
  logs: ["请求日志", "每次请求都会记下来，方便对照实际收到的内容"],
  state: ["场景状态", "用来模拟登录前后等流程，规则可以读取并改写这些变量"],
  settings: ["配置与导入", "监听地址保存在本地数据库，重启后生效；兜底代理立即生效"],
};

const OPS = [
  ["eq", "等于"],
  ["ne", "不等于"],
  ["ieq", "等于，忽略大小写"],
  ["contains", "包含"],
  ["prefix", "前缀"],
  ["suffix", "后缀"],
  ["regex", "正则"],
  ["gt", "大于"],
  ["gte", "大于等于"],
  ["lt", "小于"],
  ["lte", "小于等于"],
  ["exists", "字段存在"],
  ["not_exists", "字段不存在"],
];

const state = {
  view: "overview",
  editing: null,
  editingIface: null,
  iface: null,
  interfaces: [],
  ifaceQ: "",
  rules: [],
  liveRules: [],
  ruleQ: "",
  logQ: "",
  logOffset: 0,
  meta: null,
  liveItems: [],
  livePaused: false,
  liveRunning: false,
  liveTarget: null,
};

const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];

let liveSource = null;

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[ch]));
}

async function api(path, opts = {}) {
  const res = await fetch(path, {
    ...opts,
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
  });
  const text = await res.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { data = { error: text }; }
  }
  if (!res.ok) throw new Error((data && data.error) || res.statusText || "请求失败");
  return data;
}

function toast(message, kind) {
  const box = $("#toasts");
  const item = document.createElement("div");
  item.className = "toast" + (kind === "err" ? " err" : "");
  item.textContent = message;
  box.appendChild(item);
  setTimeout(() => item.remove(), 3200);
}

function blankIface() {
  return { id: 0, name: "", enabled: true, method: "GET", path: "/", description: "" };
}

function blankRule() {
  const iface = state.iface || { id: 0, method: "GET", path: "/" };
  return {
    id: 0,
    interface_id: iface.id,
    name: "",
    enabled: true,
    priority: 0,
    method: iface.method,
    path: iface.path,
    query_match: [],
    header_match: [],
    body_match: [],
    state_match: [],
    status_code: 200,
    response_headers: { "Content-Type": "application/json; charset=utf-8" },
    response_body: '{\n  "ok": true\n}',
    delay_ms: 0,
    mode: "mock",
    proxy_url: "",
    record: false,
    state_set: {},
    description: "",
  };
}

function browseBase(addr) {
  let host = addr || "127.0.0.1:8080";
  host = host.replace("0.0.0.0", "127.0.0.1").replace("[::]", "127.0.0.1").replace(/^:::/, "127.0.0.1:");
  return "http://" + host;
}

function methodClass(method) {
  const name = String(method || "*").split(",")[0].trim().toLowerCase();
  if (["get", "post", "put", "patch", "delete"].includes(name)) return name;
  return "";
}

function boot() {
  $$("[data-nav]").forEach((btn) => {
    btn.addEventListener("click", () => {
      state.editing = null;
      state.editingIface = null;
      state.iface = null;
      const view = btn.dataset.nav;
      if (location.hash === "#" + view) render();
      else location.hash = view;
    });
  });
  $("#view").addEventListener("click", onViewClick);
  $("#view").addEventListener("submit", onViewSubmit);
  window.addEventListener("hashchange", render);
  if (!location.hash) location.hash = "overview";
  else render();
  refreshFoot();
}

async function refreshFoot() {
  try {
    state.meta = await api("/api/meta");
    $("#side-foot").textContent = "v" + state.meta.version + "\n" + state.meta.db_path;
  } catch {
    $("#side-foot").textContent = "无法连接管理接口";
  }
}

async function render() {
  let name = (location.hash || "#overview").replace("#", "");
  if (name === "rules") name = "interfaces";
  state.view = titles[name] ? name : "overview";
  if (state.view !== "interfaces") {
    state.editing = null;
    state.editingIface = null;
    state.iface = null;
  }
  if (state.view !== "live") stopLive();
  $$("[data-nav]").forEach((btn) => btn.classList.toggle("active", btn.dataset.nav === state.view));
  let title = titles[state.view][0];
  let sub = titles[state.view][1];
  if (state.view === "interfaces") {
    if (state.editingIface) {
      title = state.editingIface.id ? "编辑接口" : "新建接口";
      sub = "接口只确定方法和路径，匹配条件与响应写在规则里";
    } else if (state.editing) {
      title = state.editing.id ? "编辑规则" : "新建规则";
      sub = state.iface ? state.iface.method + " " + state.iface.path : "";
    } else if (state.iface) {
      title = state.iface.name || "接口规则";
      sub = state.iface.method + " " + state.iface.path + " · 用规则区分不同响应";
    }
  }
  $("#title").textContent = title;
  $("#subtitle").textContent = sub;
  $("#top-actions").innerHTML = "";
  if (state.view === "interfaces" && state.editingIface) {
    paintIfaceEditor();
    return;
  }
  if (state.view === "interfaces" && state.editing) {
    paintEditor();
    return;
  }
  $("#view").innerHTML = '<p class="muted">正在加载…</p>';
  try {
    if (state.view === "overview") await paintOverview();
    else if (state.view === "interfaces") await paintInterfaces();
    else if (state.view === "live") await paintLive();
    else if (state.view === "logs") await paintLogs();
    else if (state.view === "state") await paintState();
    else await paintSettings();
  } catch (err) {
    $("#view").innerHTML = `<div class="panel"><h2>加载失败</h2><p>${esc(err.message)}</p></div>`;
  }
}

async function paintOverview() {
  const meta = await api("/api/meta");
  state.meta = meta;
  const logs = await api("/api/logs?limit=8");
  const base = browseBase(meta.mock_addr);
  $("#top-actions").innerHTML = `<button type="button" class="btn" id="copy-mock">复制 Mock 地址</button>`;
  $("#copy-mock").onclick = () => copyText(base);
  const rows = (logs.logs || []).map((item) => `<tr>
    <td class="mono">${esc((item.created_at || "").replace("T", " ").replace("Z", ""))}</td>
    <td><span class="badge ${methodClass(item.method)}">${esc(item.method)}</span></td>
    <td class="mono">${esc(item.path)}</td>
    <td>${item.status_code || "-"}</td>
    <td>${esc(item.rule_name || "未匹配")}</td>
  </tr>`).join("");
  $("#view").innerHTML = `
    <div class="cards">
      <div class="card"><span>Mock 服务</span><strong class="mono">${esc(base)}</strong><em>绑定 ${esc(meta.mock_addr)}</em></div>
      <div class="card"><span>配置页面</span><strong class="mono">${esc(browseBase(meta.admin_addr))}</strong><em>绑定 ${esc(meta.admin_addr)}</em></div>
      <div class="card"><span>接口 / 规则</span><strong>${meta.interface_count || 0} / ${meta.rule_count}</strong><em>规则启用 ${meta.enabled_count}</em></div>
      <div class="card"><span>请求日志</span><strong>${meta.log_count}</strong><em>最多保留 2000 条</em></div>
    </div>
    <section class="panel">
      <h2>数据库</h2>
      <p class="mono">${esc(meta.db_path)}</p>
      <p class="hint">数据库文件在程序同级的 data 目录里，换机器时把 data 目录一起拷贝即可。</p>
    </section>
    <section class="panel">
      <h2>可以先试这几个内置接口</h2>
      <pre>curl ${esc(base)}/hello
curl ${esc(base)}/user/42?verbose=yes
curl -X POST ${esc(base)}/login -H "Content-Type: application/json" -d '{"username":"demo"}'
curl ${esc(base)}/me</pre>
      <p class="hint">登录成功后会写入场景状态，再请求 /me 会返回当前用户。这些规则只在第一次创建数据库时写入。</p>
    </section>
    <section class="panel">
      <h2>最近请求</h2>
      ${rows ? `<div class="table-wrap"><table><thead><tr><th>时间</th><th>方法</th><th>路径</th><th>状态</th><th>规则</th></tr></thead><tbody>${rows}</tbody></table></div>` : `<p class="empty muted">还没有请求。</p>`}
    </section>`;
}

async function paintInterfaces() {
  if (state.iface) {
    await paintIfaceRules();
    return;
  }
  const data = await api("/api/interfaces");
  state.interfaces = data.interfaces || [];
  $("#top-actions").innerHTML = `<button type="button" class="btn primary" id="new-iface">新建接口</button>`;
  $("#new-iface").onclick = () => { state.editingIface = blankIface(); render(); };
  $("#view").innerHTML = `
    <div class="toolbar">
      <input class="search" id="iface-q" placeholder="搜索名称、路径或方法" value="${esc(state.ifaceQ)}">
      <span class="muted" id="iface-count"></span>
    </div>
    <div class="table-wrap">
      <table>
        <thead><tr><th></th><th>方法</th><th>路径</th><th>名称</th><th>规则</th><th></th></tr></thead>
        <tbody id="iface-rows"></tbody>
      </table>
    </div>`;
  const input = $("#iface-q");
  input.addEventListener("input", () => {
    state.ifaceQ = input.value;
    renderIfaceRows();
  });
  renderIfaceRows();
}

function renderIfaceRows() {
  const q = state.ifaceQ.trim().toLowerCase();
  const items = state.interfaces.filter((item) => {
    if (!q) return true;
    return `${item.name} ${item.path} ${item.method} ${item.description || ""}`.toLowerCase().includes(q);
  });
  $("#iface-count").textContent = items.length + " 个";
  $("#iface-rows").innerHTML = items.map(ifaceRow).join("") || `<tr><td colspan="6"><div class="empty muted">还没有接口。点右上角「新建接口」，然后再为它添加规则。</div></td></tr>`;
}

function ifaceRow(item) {
  return `<tr class="${item.enabled ? "" : "off"}">
    <td><button type="button" class="switch ${item.enabled ? "on" : ""}" data-iface-toggle="${item.id}" aria-label="切换启用"></button></td>
    <td><span class="badge ${methodClass(item.method)}">${esc(item.method)}</span></td>
    <td class="mono">${esc(item.path)}</td>
    <td>${esc(item.name)}${item.description ? `<div class="muted">${esc(item.description)}</div>` : ""}</td>
    <td>${item.rule_count || 0}</td>
    <td class="row-actions">
      <button type="button" class="btn tiny" data-open="${item.id}">规则</button>
      <button type="button" class="btn tiny" data-iface-edit="${item.id}">编辑</button>
      <button type="button" class="btn tiny danger" data-iface-delete="${item.id}">删除</button>
    </td>
  </tr>`;
}

function paintIfaceEditor() {
  const iface = state.editingIface;
  const methods = HTTP_METHODS.slice();
  if (iface.method && !methods.includes(iface.method)) methods.unshift(iface.method);
  const options = methods.map((method) => `<option value="${esc(method)}"${method === iface.method ? " selected" : ""}>${esc(method)}</option>`).join("");
  $("#top-actions").innerHTML = `<button type="button" class="btn" id="back-ifaces">返回接口</button>`;
  $("#back-ifaces").onclick = () => { state.editingIface = null; render(); };
  $("#view").innerHTML = `
    <form id="iface-form" data-id="${iface.id || 0}">
      <section class="panel">
        <h2>接口</h2>
        <div class="grid-2">
          <div class="field"><label>名称</label><input name="name" value="${esc(iface.name)}" placeholder="例如 登录"></div>
          <div class="field"><label>方法</label><select name="method">${options}</select></div>
          <div class="field span-2"><label>路径</label><input name="path" class="mono" value="${esc(iface.path)}" placeholder="/login 或 /user/:id"></div>
          <div class="field span-2"><label>说明</label><input name="description" value="${esc(iface.description || "")}"></div>
          <div class="field"><label class="check"><input type="checkbox" name="enabled" ${iface.enabled ? "checked" : ""}> 启用</label></div>
        </div>
        <p class="hint">停用接口后，它下面的规则都不会再被匹配。</p>
      </section>
      <div class="form-actions">
        <button class="btn primary" type="submit">保存接口</button>
      </div>
    </form>`;
}

async function saveIface(form) {
  const iface = {
    id: Number(form.dataset.id || 0),
    name: field(form, "name").value.trim(),
    method: field(form, "method").value,
    path: field(form, "path").value.trim(),
    description: field(form, "description").value.trim(),
    enabled: field(form, "enabled").checked,
  };
  if (!iface.path.startsWith("/")) {
    toast("路径要以 / 开头", "err");
    return;
  }
  try {
    const saved = iface.id
      ? await api("/api/interfaces/" + iface.id, { method: "PUT", body: JSON.stringify(iface) })
      : await api("/api/interfaces", { method: "POST", body: JSON.stringify(iface) });
    toast("接口已保存");
    state.editingIface = null;
    state.iface = saved;
    state.ruleQ = "";
    await refreshFoot();
    render();
  } catch (err) {
    toast(err.message, "err");
  }
}

async function paintIfaceRules() {
  const data = await api("/api/interfaces/" + state.iface.id + "/rules");
  state.rules = data.rules || [];
  $("#top-actions").innerHTML = `
    <button type="button" class="btn" id="back-ifaces">返回接口</button>
    <button type="button" class="btn primary" id="new-rule">新建规则</button>`;
  $("#back-ifaces").onclick = () => { state.iface = null; state.editing = null; render(); };
  $("#new-rule").onclick = () => { state.editing = blankRule(); render(); };
  $("#view").innerHTML = `
    <div class="toolbar">
      <input class="search" id="rule-q" placeholder="搜索规则名称" value="${esc(state.ruleQ)}">
      <span class="muted" id="rule-count"></span>
    </div>
    <div class="table-wrap">
      <table>
        <thead><tr><th></th><th>优先级</th><th>名称</th><th>匹配</th><th>状态码</th><th>延迟</th><th>模式</th><th></th></tr></thead>
        <tbody id="rule-rows"></tbody>
      </table>
    </div>`;
  const input = $("#rule-q");
  input.addEventListener("input", () => {
    state.ruleQ = input.value;
    renderRuleRows();
  });
  renderRuleRows();
}

function renderRuleRows() {
  const q = state.ruleQ.trim().toLowerCase();
  const rules = state.rules.filter((rule) => {
    if (!q) return true;
    return `${rule.name} ${rule.description || ""}`.toLowerCase().includes(q);
  });
  $("#rule-count").textContent = rules.length + " 条";
  $("#rule-rows").innerHTML = rules.map(ruleRow).join("") || `<tr><td colspan="8"><div class="empty muted">这个接口还没有规则。点右上角「新建规则」。</div></td></tr>`;
}

function ruleRow(rule) {
  const conds = rule.query_match.length + rule.header_match.length + rule.body_match.length + rule.state_match.length;
  return `<tr class="${rule.enabled ? "" : "off"}">
    <td><button type="button" class="switch ${rule.enabled ? "on" : ""}" data-toggle="${rule.id}" aria-label="切换启用"></button></td>
    <td>${rule.priority}</td>
    <td>${esc(rule.name)}</td>
    <td>${conds ? conds + " 个条件" : "全部请求"}</td>
    <td>${rule.status_code}</td>
    <td>${rule.delay_ms ? rule.delay_ms + " ms" : "-"}</td>
    <td>${rule.mode === "proxy" ? "代理" : "Mock"}</td>
    <td class="row-actions">
      <button type="button" class="btn tiny" data-edit="${rule.id}">编辑</button>
      <button type="button" class="btn tiny" data-copy="${rule.id}">复制</button>
      <button type="button" class="btn tiny danger" data-delete="${rule.id}">删除</button>
    </td>
  </tr>`;
}

function paintEditor() {
  const rule = state.editing;
  $("#top-actions").innerHTML = `<button type="button" class="btn" id="back-rules">返回规则</button>`;
  $("#back-rules").onclick = () => { state.editing = null; render(); };
  $("#view").innerHTML = `
    <form id="rule-form" data-id="${rule.id || 0}">
      <div class="editor-grid">
        <section class="panel">
          <h2>基本信息</h2>
          <div class="grid-2">
            <div class="field span-2"><label>所属接口</label><input value="${esc(state.iface ? state.iface.method + " " + state.iface.path + " · " + state.iface.name : rule.method + " " + rule.path)}" disabled></div>
            <input type="hidden" name="method" value="${esc(state.iface ? state.iface.method : rule.method)}">
            <input type="hidden" name="path" value="${esc(state.iface ? state.iface.path : rule.path)}">
            <div class="field"><label>名称</label><input name="name" value="${esc(rule.name)}" placeholder="例如 登录成功"></div>
            <div class="field"><label>优先级</label><input name="priority" type="number" value="${esc(rule.priority)}" title="数字越大越优先"></div>
            <div class="field span-2"><label>说明</label><input name="description" value="${esc(rule.description || "")}"></div>
            <div class="field"><label class="check"><input type="checkbox" name="enabled" ${rule.enabled ? "checked" : ""}> 启用</label></div>
          </div>
          ${condGroup("Query 参数", "例如 key=id，操作=等于，值=1001。多个条件同时满足才命中。", "query", rule.query_match)}
          ${condGroup("Header", "Header 名不区分大小写。", "header", rule.header_match)}
          ${condGroup("Body", "JSON 用点路径，例如 user.name 或 items.0.id。原始正文用 $raw。", "body", rule.body_match)}
          ${condGroup("场景状态", "先在「场景状态」或命中规则时写入，再在这里读取。", "state", rule.state_match)}
        </section>
        <div>
          <section class="panel">
            <h2>响应</h2>
            <div class="grid-2">
              <div class="field"><label>状态码</label><input name="status_code" type="number" value="${esc(rule.status_code)}"></div>
              <div class="field"><label>延迟（毫秒）</label><input name="delay_ms" type="number" min="0" max="300000" value="${esc(rule.delay_ms)}"></div>
            </div>
            <div class="match-head"><strong>响应 Header</strong><button type="button" class="btn tiny" data-add-kv="headers">添加</button></div>
            <div data-list="headers">${Object.entries(rule.response_headers || {}).map(([k, v]) => kvRow(k, v)).join("")}</div>
            <div class="field" style="margin-top:12px"><label>响应 Body</label>
              <textarea name="response_body" rows="12">${esc(rule.response_body || "")}</textarea>
            </div>
            <p class="hint">模板：<code>{{params.id}}</code> <code>{{query.id}}</code> <code>{{header.X-Token}}</code> <code>{{body.user.name}}</code> <code>{{state.user}}</code> <code>{{method}}</code> <code>{{path}}</code> <code>{{rawquery}}</code> <code>{{uuid}}</code> <code>{{now}}</code></p>
          </section>
          <section class="panel">
            <h2>转发</h2>
            <label class="radio"><input type="radio" name="mode" value="mock" ${rule.mode !== "proxy" ? "checked" : ""}> 直接返回上面的响应</label>
            <label class="radio"><input type="radio" name="mode" value="proxy" ${rule.mode === "proxy" ? "checked" : ""}> 转发到真实服务</label>
            <div class="proxy-fields ${rule.mode === "proxy" ? "" : "hidden"}">
              <div class="field" style="margin-top:12px"><label>代理地址</label><input name="proxy_url" value="${esc(rule.proxy_url || "")}" placeholder="https://api.example.com"></div>
              <label class="check"><input type="checkbox" name="record" ${rule.record ? "checked" : ""}> 把上游响应录制进这条规则</label>
              <p class="hint">录制后仍会继续转发。要改成固定 Mock，把模式改回「直接返回」，或在请求日志里点「采用为 Mock 规则」。</p>
            </div>
          </section>
          <section class="panel">
            <h2>命中后写入状态</h2>
            <div class="match-head"><span class="hint">值里可以使用和响应相同的模板。</span><button type="button" class="btn tiny" data-add-kv="stateset">添加</button></div>
            <div data-list="stateset">${Object.entries(rule.state_set || {}).map(([k, v]) => kvRow(k, v)).join("")}</div>
          </section>
        </div>
      </div>
      <div class="form-actions">
        <button class="btn primary" type="submit">保存规则</button>
      </div>
    </form>
    <section class="panel" id="try-panel">
      <h2>试匹配</h2>
      <p class="hint">按当前表单试一次。不会写请求日志，也不会修改场景状态。</p>
      <div class="grid-2">
        <div class="field"><label>方法</label><input id="try-method" value="${esc((state.iface && state.iface.method) || rule.method || "GET")}"></div>
        <div class="field"><label>路径</label><input id="try-url" class="mono" value="${esc((state.iface && state.iface.path) || rule.path || "/")}"></div>
        <div class="field"><label>Header，每行 Key: Value</label><textarea id="try-headers" rows="4" placeholder="X-Token: secret"></textarea></div>
        <div class="field"><label>Body</label><textarea id="try-body" rows="4" placeholder='{"username":"demo"}'></textarea></div>
      </div>
      <button type="button" class="btn" id="try-run">试匹配</button>
      <div id="try-result"></div>
    </section>`;
  const form = $("#rule-form");
  form.addEventListener("click", onEditorClick);
  form.addEventListener("change", onEditorChange);
  $("#try-run").onclick = () => runTry(form);
}

function condGroup(title, hint, name, items) {
  const rows = (items || []).map((item) => condRow(item)).join("");
  return `<div class="match-block">
    <div class="match-head"><strong>${title}</strong><button type="button" class="btn tiny" data-add-cond="${name}">添加</button></div>
    <p class="hint">${hint}</p>
    <div data-list="${name}">${rows || '<p class="muted empty-cond">不限制</p>'}</div>
  </div>`;
}

function condRow(item) {
  const op = item.op || "eq";
  const options = OPS.map(([value, label]) => `<option value="${value}"${value === op ? " selected" : ""}>${label}</option>`).join("");
  const disabled = op === "exists" || op === "not_exists" ? " disabled" : "";
  return `<div class="cond">
    <input data-k="key" value="${esc(item.key || "")}" placeholder="字段名">
    <select data-k="op">${options}</select>
    <input data-k="value" value="${esc(item.value || "")}" placeholder="期望值"${disabled}>
    <button type="button" class="btn tiny danger" data-remove>删除</button>
  </div>`;
}

function kvRow(key, value) {
  return `<div class="kv">
    <input data-k="key" value="${esc(key)}" placeholder="名称">
    <input data-k="value" value="${esc(value)}" placeholder="值">
    <button type="button" class="btn tiny danger" data-remove>删除</button>
  </div>`;
}

function onEditorClick(e) {
  const addCond = e.target.closest("[data-add-cond]");
  if (addCond) {
    const box = e.currentTarget.querySelector(`[data-list="${addCond.dataset.addCond}"]`);
    const empty = box.querySelector(".empty-cond");
    if (empty) empty.remove();
    box.insertAdjacentHTML("beforeend", condRow({ op: "eq" }));
  }
  const addKV = e.target.closest("[data-add-kv]");
  if (addKV) {
    const box = e.currentTarget.querySelector(`[data-list="${addKV.dataset.addKv}"]`);
    box.insertAdjacentHTML("beforeend", kvRow("", ""));
  }
  const remove = e.target.closest("[data-remove]");
  if (remove) remove.closest(".cond, .kv").remove();
}

function onEditorChange(e) {
  if (e.target.matches("[data-k=op]")) {
    const input = e.target.closest(".cond").querySelector("[data-k=value]");
    const hide = e.target.value === "exists" || e.target.value === "not_exists";
    input.disabled = hide;
  }
  if (e.target.name === "mode") {
    e.currentTarget.querySelector(".proxy-fields").classList.toggle("hidden", e.target.value !== "proxy");
  }
}

function field(form, name) {
  return form.elements.namedItem(name);
}

function readForm(form) {
  const iface = state.iface;
  return {
    id: Number(form.dataset.id || 0),
    interface_id: iface ? iface.id : 0,
    name: field(form, "name").value.trim(),
    enabled: field(form, "enabled").checked,
    priority: Number(field(form, "priority").value || 0),
    method: iface ? iface.method : field(form, "method").value.trim(),
    path: iface ? iface.path : field(form, "path").value.trim(),
    description: field(form, "description").value.trim(),
    status_code: Number(field(form, "status_code").value || 200),
    delay_ms: Number(field(form, "delay_ms").value || 0),
    mode: field(form, "mode").value,
    proxy_url: field(form, "proxy_url").value.trim(),
    record: field(form, "record").checked,
    response_body: field(form, "response_body").value,
    query_match: readConds(form, "query"),
    header_match: readConds(form, "header"),
    body_match: readConds(form, "body"),
    state_match: readConds(form, "state"),
    response_headers: readMap(form, "headers"),
    state_set: readMap(form, "stateset"),
  };
}

function readConds(form, name) {
  return $$(`[data-list="${name}"] .cond`, form).map((row) => ({
    key: row.querySelector("[data-k=key]").value.trim(),
    op: row.querySelector("[data-k=op]").value,
    value: row.querySelector("[data-k=value]").value,
  })).filter((item) => item.key);
}

function readMap(form, name) {
  const out = {};
  $$(`[data-list="${name}"] .kv`, form).forEach((row) => {
    const key = row.querySelector("[data-k=key]").value.trim();
    if (key) out[key] = row.querySelector("[data-k=value]").value;
  });
  return out;
}

async function saveRule(form) {
  const rule = readForm(form);
  if (!rule.path.startsWith("/")) {
    toast("路径要以 / 开头", "err");
    return;
  }
  if (!Number.isFinite(rule.priority) || !Number.isFinite(rule.status_code) || !Number.isFinite(rule.delay_ms)) {
    toast("优先级、状态码和延迟必须是数字", "err");
    return;
  }
  try {
    if (rule.id) await api("/api/rules/" + rule.id, { method: "PUT", body: JSON.stringify(rule) });
    else await api("/api/rules", { method: "POST", body: JSON.stringify(rule) });
    toast("规则已保存");
    state.editing = null;
    await refreshFoot();
    render();
  } catch (err) {
    toast(err.message, "err");
  }
}

async function runTry(form) {
  const headers = {};
  $("#try-headers").value.split(/\r?\n/).forEach((line) => {
    const idx = line.indexOf(":");
    if (idx > 0) headers[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
  });
  try {
    const result = await api("/api/try", {
      method: "POST",
      body: JSON.stringify({
        method: $("#try-method").value.trim() || "GET",
        url: $("#try-url").value.trim(),
        headers,
        body: $("#try-body").value,
        draft: readForm(form),
      }),
    });
    const names = (result.matches || []).map((item) => esc(item.name)).join("、") || "无";
    if (!result.matched) {
      $("#try-result").innerHTML = `<p>没有命中。候选：${names}</p>`;
      return;
    }
    const headerText = result.headers ? Object.entries(result.headers).map(([k, v]) => k + ": " + v).join("\n") : "";
    $("#try-result").innerHTML = `
      <p>命中 <strong>${esc(result.rule.name)}</strong>，状态码 ${esc(result.status_code || "-")}。同时满足的规则：${names}</p>
      <p class="muted">路径参数 ${esc(JSON.stringify(result.params || {}))}</p>
      ${headerText ? `<pre>${esc(headerText)}</pre>` : ""}
      <pre>${esc(result.body || "")}</pre>`;
  } catch (err) {
    toast(err.message, "err");
  }
}

async function onViewClick(e) {
  const ifaceToggle = e.target.closest("[data-iface-toggle]");
  if (ifaceToggle) {
    try {
      const iface = await api("/api/interfaces/" + ifaceToggle.dataset.ifaceToggle + "/toggle", { method: "POST" });
      const idx = state.interfaces.findIndex((item) => item.id === iface.id);
      if (idx >= 0) state.interfaces[idx] = iface;
      renderIfaceRows();
    } catch (err) { toast(err.message, "err"); }
    return;
  }
  const open = e.target.closest("[data-open]");
  if (open) {
    state.iface = state.interfaces.find((item) => String(item.id) === open.dataset.open) || null;
    state.ruleQ = "";
    render();
    return;
  }
  const ifaceEdit = e.target.closest("[data-iface-edit]");
  if (ifaceEdit) {
    state.editingIface = state.interfaces.find((item) => String(item.id) === ifaceEdit.dataset.ifaceEdit) || null;
    render();
    return;
  }
  const ifaceDel = e.target.closest("[data-iface-delete]");
  if (ifaceDel) {
    const iface = state.interfaces.find((item) => String(item.id) === ifaceDel.dataset.ifaceDelete);
    if (!confirm("删除接口「" + (iface ? iface.name : "") + "」及其全部规则？")) return;
    try {
      await api("/api/interfaces/" + ifaceDel.dataset.ifaceDelete, { method: "DELETE" });
      toast("已删除");
      render();
    } catch (err) { toast(err.message, "err"); }
    return;
  }
  const toggle = e.target.closest("[data-toggle]");
  if (toggle) {
    try {
      const rule = await api("/api/rules/" + toggle.dataset.toggle + "/toggle", { method: "POST" });
      const idx = state.rules.findIndex((item) => item.id === rule.id);
      if (idx >= 0) state.rules[idx] = rule;
      renderRuleRows();
    } catch (err) { toast(err.message, "err"); }
    return;
  }
  const edit = e.target.closest("[data-edit]");
  if (edit) {
    state.editing = state.rules.find((item) => String(item.id) === edit.dataset.edit);
    render();
    return;
  }
  const copy = e.target.closest("[data-copy]");
  if (copy) {
    const rule = state.rules.find((item) => String(item.id) === copy.dataset.copy);
    state.editing = { ...rule, id: 0, name: rule.name + " 副本" };
    render();
    return;
  }
  const del = e.target.closest("[data-delete]");
  if (del) {
    const rule = state.rules.find((item) => String(item.id) === del.dataset.delete);
    if (!confirm("删除规则「" + (rule ? rule.name : "") + "」？")) return;
    try {
      await api("/api/rules/" + del.dataset.delete, { method: "DELETE" });
      toast("已删除");
      render();
    } catch (err) { toast(err.message, "err"); }
  }
}

function onViewSubmit(e) {
  if (e.target.id === "iface-form") {
    e.preventDefault();
    saveIface(e.target);
  } else if (e.target.id === "rule-form") {
    e.preventDefault();
    saveRule(e.target);
  } else if (e.target.id === "log-search") {
    e.preventDefault();
    state.logQ = e.target.elements.namedItem("q").value.trim();
    state.logOffset = 0;
    render();
  } else if (e.target.id === "state-form") {
    e.preventDefault();
    saveState(e.target);
  } else if (e.target.id === "settings-form") {
    e.preventDefault();
    saveSettings(e.target);
  }
}

function stopLive() {
  if (liveSource) {
    liveSource.close();
    liveSource = null;
  }
}

async function paintLive() {
  stopLive();
  state.livePaused = false;
  state.liveRunning = false;
  state.liveTarget = null;
  state.liveRules = [];
  let interfaces = [];
  try {
    const data = await api("/api/interfaces");
    interfaces = data.interfaces || [];
    state.interfaces = interfaces;
  } catch (err) {
    if (state.view === "live") toast(err.message, "err");
  }
  if (state.view !== "live") return;
  const options = ['<option value="">全部接口</option>'].concat(interfaces.map((item) =>
    `<option value="${item.id}">${esc(item.method)} ${esc(item.path)} · ${esc(item.name)}</option>`
  )).join("");
  $("#top-actions").innerHTML = `
    <span id="live-status" class="muted"><i class="dot"></i> 未开始</span>
    <button type="button" class="btn" id="live-pause" disabled>暂停</button>
    <button type="button" class="btn" id="live-clear">清空</button>`;
  $("#view").innerHTML = `
    <div class="toolbar">
      <div class="row-actions">
        <select id="live-iface" class="live-pick">${options}</select>
        <select id="live-rule" class="live-pick" disabled><option value="">全部规则</option></select>
        <button type="button" class="btn primary" id="live-start">开始监控</button>
      </div>
      <span class="muted" id="live-count"></span>
    </div>
    <div id="live-list"></div>`;
  $("#live-iface").onchange = () => fillLiveRules($("#live-iface").value);
  $("#live-start").onclick = toggleLive;
  $("#live-pause").onclick = () => {
    if (!state.liveRunning) return;
    state.livePaused = !state.livePaused;
    $("#live-pause").textContent = state.livePaused ? "继续" : "暂停";
    setLiveStatus();
  };
  $("#live-clear").onclick = () => {
    state.liveItems = [];
    renderLiveList();
  };
  renderLiveList();
}

async function fillLiveRules(id) {
  const pick = $("#live-rule");
  if (!pick || state.liveRunning) return;
  if (!id) {
    state.liveRules = [];
    pick.disabled = true;
    pick.innerHTML = '<option value="">全部规则</option>';
    return;
  }
  try {
    const data = await api("/api/interfaces/" + id + "/rules");
    state.liveRules = data.rules || [];
  } catch (err) {
    state.liveRules = [];
    toast(err.message, "err");
  }
  pick.disabled = false;
  pick.innerHTML = '<option value="">全部规则</option>' + state.liveRules.map((rule) =>
    `<option value="${rule.id}">${esc(rule.name)}</option>`
  ).join("");
}

function toggleLive() {
  if (state.liveRunning) {
    stopLive();
    state.liveRunning = false;
    state.livePaused = false;
    const start = $("#live-start");
    const ifacePick = $("#live-iface");
    const rulePick = $("#live-rule");
    const pause = $("#live-pause");
    if (start) start.textContent = "开始监控";
    if (ifacePick) ifacePick.disabled = false;
    if (rulePick) rulePick.disabled = !ifacePick || !ifacePick.value;
    if (pause) {
      pause.disabled = true;
      pause.textContent = "暂停";
    }
    setLiveStatus();
    return;
  }
  const ifacePick = $("#live-iface");
  const rulePick = $("#live-rule");
  const ifaceId = ifacePick ? ifacePick.value : "";
  const ruleId = rulePick ? rulePick.value : "";
  if (ifaceId) {
    const iface = state.interfaces.find((item) => String(item.id) === ifaceId);
    const rule = state.liveRules.find((item) => String(item.id) === ruleId);
    state.liveTarget = iface ? {
      id: iface.id,
      method: iface.method,
      path: iface.path,
      name: iface.name,
      ruleId: rule ? rule.id : 0,
      ruleName: rule ? rule.name : "",
    } : null;
  } else {
    state.liveTarget = null;
  }
  state.liveItems = [];
  state.livePaused = false;
  state.liveRunning = true;
  if (ifacePick) ifacePick.disabled = true;
  if (rulePick) rulePick.disabled = true;
  const start = $("#live-start");
  if (start) start.textContent = "停止监控";
  const pause = $("#live-pause");
  if (pause) {
    pause.disabled = false;
    pause.textContent = "暂停";
  }
  renderLiveList();
  setLiveStatus();
  const source = new EventSource("/api/live");
  liveSource = source;
  source.addEventListener("hello", () => {
    if (state.view === "live" && state.liveRunning) setLiveStatus();
  });
  source.addEventListener("exchange", (ev) => {
    if (state.view !== "live" || !state.liveRunning || state.livePaused) return;
    let item;
    try { item = JSON.parse(ev.data); } catch { return; }
    if (!matchesLiveTarget(item)) return;
    state.liveItems.unshift(item);
    if (state.liveItems.length > 100) state.liveItems.length = 100;
    renderLiveList();
  });
  source.onerror = () => {
    if (state.view === "live" && state.liveRunning) {
      const status = $("#live-status");
      if (status) status.innerHTML = '<i class="dot wait"></i> 连接中断，正在重连…';
    }
  };
}

function setLiveStatus() {
  const status = $("#live-status");
  if (!status) return;
  if (!state.liveRunning) {
    status.innerHTML = '<i class="dot"></i> 未开始';
    return;
  }
  if (state.livePaused) {
    status.innerHTML = '<i class="dot wait"></i> 已暂停';
    return;
  }
  const target = state.liveTarget;
  let label = "正在监控全部接口";
  if (target) {
    label = "正在监控 " + target.name + " " + target.method + " " + target.path;
    if (target.ruleName) label += " · " + target.ruleName;
  }
  status.innerHTML = '<i class="dot on"></i> ' + esc(label);
}

function matchesLiveTarget(item) {
  const target = state.liveTarget;
  if (!target) return true;
  if (target.ruleId) return String(item.rule_id || "") === String(target.ruleId);
  return methodMatches(target.method, item.method) && pathMatches(target.path, item.path);
}

function methodMatches(ruleMethod, reqMethod) {
  const method = String(ruleMethod || "*").toUpperCase();
  if (method === "*" || method === "ANY" || method === "") return true;
  const got = String(reqMethod || "").toUpperCase();
  return method.split(",").map((part) => part.trim()).includes(got);
}

function pathMatches(pattern, actual) {
  const parts = (value) => {
    let path = value || "/";
    if (path.length > 1 && path.endsWith("/")) path = path.slice(0, -1);
    if (path === "/") return [];
    return path.replace(/^\//, "").split("/");
  };
  const expected = parts(pattern);
  const got = parts(actual);
  if (expected.length !== got.length) return false;
  for (let i = 0; i < expected.length; i++) {
    const seg = expected[i];
    const param = seg.startsWith(":") || (seg.startsWith("{") && seg.endsWith("}") && seg.length > 2);
    if (!param && seg !== got[i]) return false;
  }
  return true;
}

function renderLiveList() {
  const box = $("#live-list");
  if (!box) return;
  const count = $("#live-count");
  if (count) count.textContent = state.liveItems.length + " 条";
  if (!state.liveItems.length) {
    const hint = state.liveRunning
      ? "正在等待请求。"
      : "先选择接口，也可以再选一条规则，然后点「开始监控」。都不选则监听全部接口。";
    box.innerHTML = `<p class="empty muted">${hint}</p>`;
    return;
  }
  box.innerHTML = state.liveItems.map(liveCard).join("");
}

function liveCard(item) {
  const when = item.created_at ? new Date(item.created_at).toLocaleString() : "";
  const rule = item.matched ? item.rule_name : "未匹配";
  return `<article class="panel live-card">
    <header>
      <span class="badge ${methodClass(item.method)}">${esc(item.method)}</span>
      <strong class="mono">${esc(item.path)}${item.query ? "?" + esc(item.query) : ""}</strong>
      <span>${esc(item.status_code || "-")}</span>
      <span class="muted">${esc(rule)} · ${esc(item.duration_ms || 0)} ms</span>
      <span class="muted">${esc(when)}</span>
    </header>
    <div class="live-split">
      <section>
        <h3>输入</h3>
        <pre>${esc(formatExchange(item.headers, item.body))}</pre>
      </section>
      <section>
        <h3>输出</h3>
        <pre>${esc(formatExchange(item.response_headers, item.response_body))}</pre>
        ${item.error ? `<p class="hint">${esc(item.error)}</p>` : ""}
      </section>
    </div>
  </article>`;
}

function formatExchange(headers, body) {
  const parts = [];
  const head = prettyText(headers);
  if (head && head !== "{}" && head !== "(空)") parts.push("Headers\n" + head);
  parts.push("Body\n" + prettyText(body));
  return parts.join("\n\n");
}

function prettyText(text) {
  if (!text) return "(空)";
  try { return JSON.stringify(JSON.parse(text), null, 2); } catch { return text; }
}

async function paintLogs() {
  const limit = 20;
  const data = await api("/api/logs?limit=" + limit + "&offset=" + state.logOffset + "&q=" + encodeURIComponent(state.logQ));
  const total = data.total || 0;
  $("#top-actions").innerHTML = `<button type="button" class="btn danger" id="clear-logs">清空日志</button>`;
  $("#clear-logs").onclick = async () => {
    if (!confirm("清空全部请求日志？")) return;
    await api("/api/logs", { method: "DELETE" });
    state.logOffset = 0;
    toast("日志已清空");
    render();
  };
  const rows = (data.logs || []).map((item) => `<tr>
    <td class="mono">${esc((item.created_at || "").replace("T", " ").replace("Z", ""))}</td>
    <td><span class="badge ${methodClass(item.method)}">${esc(item.method)}</span></td>
    <td class="mono">${esc(item.path)}${item.query ? `<div class="muted">?${esc(item.query)}</div>` : ""}</td>
    <td>${item.status_code || "-"}</td>
    <td>${esc(item.matched ? item.rule_name : "未匹配")}</td>
    <td>${item.duration_ms} ms</td>
    <td><button type="button" class="btn tiny" data-log="${item.id}">详情</button></td>
  </tr>`).join("");
  const page = Math.floor(state.logOffset / limit) + 1;
  const pages = Math.max(1, Math.ceil(total / limit));
  $("#view").innerHTML = `
    <form id="log-search" class="toolbar">
      <input class="search" name="q" value="${esc(state.logQ)}" placeholder="搜索路径、方法或规则名">
      <button class="btn" type="submit">搜索</button>
    </form>
    <div class="table-wrap">
      <table>
        <thead><tr><th>时间 (UTC)</th><th>方法</th><th>路径</th><th>状态</th><th>规则</th><th>耗时</th><th></th></tr></thead>
        <tbody>${rows || `<tr><td colspan="7"><div class="empty muted">没有日志。</div></td></tr>`}</tbody>
      </table>
    </div>
    <div class="pager">
      <span>第 ${page} / ${pages} 页，共 ${total} 条</span>
      <span>
        <button type="button" class="btn tiny" data-page="prev" ${state.logOffset <= 0 ? "disabled" : ""}>上一页</button>
        <button type="button" class="btn tiny" data-page="next" ${state.logOffset + limit >= total ? "disabled" : ""}>下一页</button>
      </span>
    </div>`;
  $("#view").querySelector("[data-page=prev]")?.addEventListener("click", () => { state.logOffset = Math.max(0, state.logOffset - limit); render(); });
  $("#view").querySelector("[data-page=next]")?.addEventListener("click", () => { state.logOffset += limit; render(); });
  $$("[data-log]").forEach((btn) => btn.addEventListener("click", () => openLog(btn.dataset.log)));
}

async function openLog(id) {
  const item = await api("/api/logs/" + id);
  let headers = item.headers || "";
  try { headers = JSON.stringify(JSON.parse(item.headers || "{}"), null, 2); } catch { /* 保持原文 */ }
  openDrawer("请求 #" + item.id, `
    <div class="kv-view">
      <span>方法</span><b>${esc(item.method)}</b>
      <span>路径</span><b class="mono">${esc(item.path)}${item.query ? "?" + esc(item.query) : ""}</b>
      <span>来源</span><b>${esc(item.remote_addr || "-")}</b>
      <span>规则</span><b>${esc(item.matched ? item.rule_name : "未匹配")}</b>
      <span>状态码</span><b>${esc(item.status_code || "-")}</b>
      <span>耗时</span><b>${esc(item.duration_ms)} ms</b>
    </div>
    ${item.error ? `<p class="hint">${esc(item.error)}</p>` : ""}
    <h3>请求 Header</h3><pre>${esc(headers)}</pre>
    <h3>请求 Body</h3><pre>${esc(item.body || "")}</pre>
    <h3>响应 Header</h3><pre>${esc(item.response_headers || "")}</pre>
    <h3>响应 Body</h3><pre>${esc(item.response_body || "")}</pre>
    <div class="form-actions">
      <button type="button" class="btn primary" id="adopt-log">采用为 Mock 规则</button>
      <button type="button" class="btn danger" id="delete-log">删除这条日志</button>
    </div>`);
  $("#adopt-log").onclick = async () => {
    try {
      const rule = await api("/api/logs/" + id + "/adopt", { method: "POST" });
      toast("已保存为规则「" + rule.name + "」");
      closeDrawer();
    } catch (err) { toast(err.message, "err"); }
  };
  $("#delete-log").onclick = async () => {
    await api("/api/logs/" + id, { method: "DELETE" });
    closeDrawer();
    toast("已删除");
    render();
  };
}

function openDrawer(title, html) {
  let drawer = $("#drawer");
  if (!drawer) {
    drawer = document.createElement("div");
    drawer.id = "drawer";
    drawer.innerHTML = `<div class="drawer-back"></div><aside class="drawer"><header><h2></h2><button type="button" class="btn tiny" data-close>关闭</button></header><div class="drawer-body"></div></aside>`;
    document.body.appendChild(drawer);
    drawer.addEventListener("click", (e) => {
      if (e.target.matches(".drawer-back, [data-close]")) closeDrawer();
    });
  }
  drawer.querySelector("h2").textContent = title;
  drawer.querySelector(".drawer-body").innerHTML = html;
  drawer.classList.add("open");
}

function closeDrawer() {
  $("#drawer")?.classList.remove("open");
}

async function paintState() {
  const data = await api("/api/state");
  $("#top-actions").innerHTML = `<button type="button" class="btn danger" id="clear-state">清空状态</button>`;
  $("#clear-state").onclick = async () => {
    if (!confirm("清空全部场景状态？")) return;
    await api("/api/state", { method: "DELETE" });
    toast("状态已清空");
    render();
  };
  const rows = (data.states || []).map((item) => `<tr>
    <td class="mono">${esc(item.key)}</td>
    <td class="mono">${esc(item.value)}</td>
    <td>${esc((item.updated_at || "").replace("T", " ").replace("Z", ""))}</td>
    <td><button type="button" class="btn tiny danger" data-state="${esc(item.key)}">删除</button></td>
  </tr>`).join("");
  $("#view").innerHTML = `
    <form id="state-form" class="panel">
      <h2>写入变量</h2>
      <div class="grid-2">
        <div class="field"><label>键</label><input name="key" placeholder="auth" required></div>
        <div class="field"><label>值</label><input name="value" placeholder="logged_in"></div>
      </div>
      <button class="btn primary" type="submit">保存</button>
    </form>
    <div class="table-wrap">
      <table>
        <thead><tr><th>键</th><th>值</th><th>更新时间 (UTC)</th><th></th></tr></thead>
        <tbody>${rows || `<tr><td colspan="4"><div class="empty muted">还没有状态。可以调用内置的 POST /login 自动写入。</div></td></tr>`}</tbody>
      </table>
    </div>`;
  $$("[data-state]").forEach((btn) => btn.addEventListener("click", async () => {
    await api("/api/state/" + encodeURIComponent(btn.dataset.state), { method: "DELETE" });
    toast("已删除");
    render();
  }));
}

async function saveState(form) {
  try {
    await api("/api/state", { method: "PUT", body: JSON.stringify({ key: field(form, "key").value.trim(), value: field(form, "value").value }) });
    toast("状态已保存");
    render();
  } catch (err) { toast(err.message, "err"); }
}

async function paintSettings() {
  const settings = await api("/api/settings");
  const meta = state.meta || await api("/api/meta");
  $("#view").innerHTML = `
    <form id="settings-form" class="panel">
      <h2>监听与代理</h2>
      <div class="grid-2">
        <div class="field"><label>Mock 监听地址</label><input name="mock_addr" value="${esc(settings.mock_addr)}" placeholder="0.0.0.0:8080"><p class="hint">当前生效：${esc(settings.mock_addr_active)}，修改后需重启。</p></div>
        <div class="field"><label>配置页监听地址</label><input name="admin_addr" value="${esc(settings.admin_addr)}" placeholder="127.0.0.1:9090"><p class="hint">当前生效：${esc(settings.admin_addr_active)}，修改后需重启。</p></div>
        <div class="field span-2"><label>未命中时的兜底代理</label><input name="proxy_fallback" value="${esc(settings.proxy_fallback || "")}" placeholder="留空表示未命中返回 404"><p class="hint">没有规则命中时，把请求原样转发到这个地址，并记入日志。立即生效。</p></div>
      </div>
      <button class="btn primary" type="submit">保存设置</button>
    </form>
    <section class="panel">
      <h2>导入 / 导出</h2>
      <p class="hint">支持 JSON 和 YAML。文件里 <code>replace: true</code> 会先清空再导入；页面上的「覆盖现有规则」优先于文件。</p>
      ${meta.rules_file ? `<p class="hint">本次启动加载的规则文件：<code>${esc(meta.rules_file)}</code></p>` : ""}
      <div class="form-actions">
        <a class="btn" href="/api/export">导出 JSON</a>
        <a class="btn" href="/api/export?format=yaml">导出 YAML</a>
      </div>
      <div class="field" style="margin-top:16px"><label>选择文件</label><input id="import-file" type="file" accept=".json,.yaml,.yml,application/json,text/yaml"></div>
      <label class="check"><input id="import-replace" type="checkbox"> 覆盖现有规则</label>
      <div class="form-actions"><button type="button" class="btn primary" id="import-btn">导入</button></div>
    </section>`;
  $("#import-btn").onclick = importFile;
}

async function saveSettings(form) {
  try {
    await api("/api/settings", {
      method: "PUT",
      body: JSON.stringify({
        mock_addr: field(form, "mock_addr").value.trim(),
        admin_addr: field(form, "admin_addr").value.trim(),
        proxy_fallback: field(form, "proxy_fallback").value.trim(),
      }),
    });
    toast("已保存。监听地址在下次启动时生效");
  } catch (err) { toast(err.message, "err"); }
}

async function importFile() {
  const file = $("#import-file").files[0];
  if (!file) { toast("请先选择文件", "err"); return; }
  const replace = $("#import-replace").checked;
  if (replace && !confirm("覆盖会删除当前全部规则，再导入文件内容。继续？")) return;
  try {
    const text = await file.text();
    const result = await api("/api/import?replace=" + (replace ? "1" : "0"), { method: "POST", body: text });
    toast(`导入完成：新增 ${result.created}，更新 ${result.updated}`);
    refreshFoot();
  } catch (err) { toast(err.message, "err"); }
}

function copyText(text) {
  if (navigator.clipboard) {
    navigator.clipboard.writeText(text).then(() => toast("已复制")).catch(() => toast(text));
  } else toast(text);
}

boot();
