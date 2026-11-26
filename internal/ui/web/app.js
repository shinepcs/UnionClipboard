let state = null;
let historyItems = [];
let selectedHistoryId = null;
let folders = [];
let templates = [];
let selectedFolderId = "";
let selectedTemplateId = null;
let pinInterval = null;
let fileProgress = {};

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  if (!res.ok) {
    const msg = await res.text();
    throw new Error(msg || res.statusText);
  }
  const ct = res.headers.get("content-type") || "";
  if (ct.includes("application/json")) return res.json();
  return null;
}

function toast(msg) {
  const area = document.getElementById("toast-area");
  const el = document.createElement("div");
  el.className = "toast";
  el.textContent = msg;
  area.appendChild(el);
  setTimeout(() => el.remove(), 3200);
}

function fmtSize(bytes) {
  if (!bytes || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let idx = 0;
  let val = bytes;
  while (val >= 1024 && idx < units.length - 1) { val /= 1024; idx++; }
  return `${val.toFixed(idx === 0 ? 0 : 1)} ${units[idx]}`;
}

function fmtTime(ms) {
  if (!ms) return "";
  return new Date(ms).toLocaleString("ko-KR");
}

function osLabel(os) {
  if (os === "darwin") return "Mac";
  if (os === "windows") return "Win";
  return os || "";
}

async function loadState() {
  state = await api("/api/state");
  document.getElementById("device-subtitle").textContent = `${state.device.device_name} (${osLabel(state.os)})`;
  document.getElementById("quick-universal").checked = !!state.config.universal_clipboard;
  document.getElementById("quick-paused").checked = !!state.config.paused;
  document.getElementById("hotkey-hint").textContent = `단축키: ${state.config.hotkey}`;

  const onlineCount = (state.peers || []).filter((p) => p.online).length;
  document.getElementById("peer-badge").textContent = `${onlineCount}/${(state.peers || []).length}`;

  renderPeers();
  renderSettings();
}

async function loadHistory() {
  const q = encodeURIComponent(document.getElementById("history-search").value || "");
  const kind = encodeURIComponent(document.getElementById("history-kind").value || "");
  const os = encodeURIComponent(document.getElementById("history-os").value || "");
  historyItems = (await api(`/api/history?search=${q}&kind=${kind}&os=${os}`)) || [];
  if (historyItems.length && (!selectedHistoryId || !historyItems.some((x) => x.id === selectedHistoryId))) {
    selectedHistoryId = historyItems[0].id;
  }
  renderHistoryList();
  renderHistoryDetail();
}

function renderHistoryList() {
  const listEl = document.getElementById("history-list");
  if (!historyItems.length) {
    listEl.innerHTML = `<div class="empty-state" style="padding:24px;text-align:center;">표시할 히스토리가 없습니다.</div>`;
    return;
  }
  listEl.innerHTML = "";
  for (const it of historyItems) {
    const row = document.createElement("div");
    row.className = "row-item" + (it.id === selectedHistoryId ? " selected" : "");
    row.onclick = () => { selectedHistoryId = it.id; renderHistoryList(); renderHistoryDetail(); };

    if (it.kind === "image" && it.content_hash) {
      const img = document.createElement("img");
      img.className = "row-thumb";
      img.src = `/api/thumbs/${it.content_hash}`;
      row.appendChild(img);
    } else {
      const ico = document.createElement("div");
      ico.className = "row-icon";
      ico.textContent = it.kind === "files" ? "📁" : "📝";
      row.appendChild(ico);
    }

    const body = document.createElement("div");
    body.className = "row-body";
    const title = document.createElement("div");
    title.className = "row-title";
    title.textContent = it.title || "(제목 없음)";

    const meta = document.createElement("div");
    meta.className = "row-meta";
    const remoteTag = state && it.origin_device_id !== state.device.device_id ? `[${osLabel(it.origin_os)}] ` : "";
    const cloudTag = it.kind === "files" && state && it.origin_device_id !== state.device.device_id &&
      (it.files || []).some((f) => !f.local_cached_path) ? "☁ 원격 " : "";
    meta.textContent = `${remoteTag}${cloudTag}${new Date(it.updated_at).toLocaleTimeString("ko-KR")}`;

    body.appendChild(title);
    body.appendChild(meta);
    row.appendChild(body);
    listEl.appendChild(row);
  }
}

function renderHistoryDetail() {
  const pane = document.getElementById("history-detail");
  const it = historyItems.find((x) => x.id === selectedHistoryId);
  if (!it) {
    pane.innerHTML = `<div class="empty-state">항목을 선택하면 미리보기와 편집이 가능합니다.</div>`;
    return;
  }

  pane.innerHTML = "";
  const hdr = document.createElement("div");
  hdr.className = "detail-header";
  const info = document.createElement("div");
  info.innerHTML = `<strong>${escapeHtml(it.title)}</strong><div class="muted">${it.origin_device_name} (${osLabel(it.origin_os)}) · ${fmtTime(it.updated_at)}</div>`;
  const actions = document.createElement("div");
  actions.className = "detail-actions";

  const btnCopy = mkBtn("클립보드 복사", "btn primary", async () => {
    await api(`/api/history/${it.id}/select`, { method: "POST", body: JSON.stringify({ paste: false }) });
    toast("클립보드에 복사했습니다.");
    await loadHistory();
  });
  const btnTmpl = mkBtn("템플릿 등록", "btn", async () => {
    const title = prompt("템플릿 이름", it.title);
    if (!title) return;
    await api(`/api/history/${it.id}/to-template`, { method: "POST", body: JSON.stringify({ folder_id: selectedFolderId, title }) });
    toast("템플릿에 저장했습니다.");
    await loadTemplates();
  });
  const btnDel = mkBtn("삭제", "btn danger-ghost", async () => {
    await api(`/api/history/${it.id}`, { method: "DELETE" });
    selectedHistoryId = null;
    await loadHistory();
  });
  actions.append(btnCopy, btnTmpl, btnDel);
  hdr.append(info, actions);
  pane.appendChild(hdr);

  const box = document.createElement("div");
  box.className = "preview-box";
  if (it.kind === "text") {
    const ta = document.createElement("textarea");
    ta.className = "preview-text";
    ta.value = it.text_content || "";
    const saveRow = document.createElement("div");
    saveRow.style.marginTop = "10px";
    saveRow.appendChild(mkBtn("텍스트 수정 저장", "btn", async () => {
      await api(`/api/history/${it.id}`, { method: "PUT", body: JSON.stringify({ text_content: ta.value }) });
      toast("텍스트를 업데이트했습니다.");
      await loadHistory();
    }));
    box.append(ta, saveRow);
  } else if (it.kind === "image") {
    const img = document.createElement("img");
    img.className = "preview-image";
    img.src = `/api/blobs/${it.content_hash}`;
    img.onerror = () => { img.src = `/api/thumbs/${it.content_hash}`; };
    const meta = document.createElement("div");
    meta.className = "muted";
    meta.style.marginTop = "8px";
    meta.textContent = `${it.image_width} × ${it.image_height} px · ${fmtSize(it.byte_size)}`;
    box.append(img, meta);
  } else if (it.kind === "files") {
    const table = document.createElement("table");
    table.className = "file-table";
    table.innerHTML = `<thead><tr><th>이름</th><th>종류</th><th>크기</th><th>상태</th></tr></thead><tbody></tbody>`;
    const tb = table.querySelector("tbody");
    for (const f of it.files || []) {
      const tr = document.createElement("tr");
      const isLocal = (state && it.origin_device_id === state.device.device_id) || !!f.local_cached_path;
      tr.innerHTML = `<td>${escapeHtml(f.relative_name)}</td><td>${f.is_dir ? "폴더" : "파일"}</td><td>${f.is_dir ? "-" : fmtSize(f.size)}</td><td>${isLocal ? "준비됨" : "☁ 원격 (복사 시 다운로드)"}</td>`;
      tb.appendChild(tr);
    }
    box.appendChild(table);
    const prog = fileProgress[it.id];
    if (prog && !prog.done) {
      const pct = prog.bytes_total > 0 ? Math.min(100, Math.round((prog.bytes_done / prog.bytes_total) * 100)) : 50;
      const pWrap = document.createElement("div");
      pWrap.style.marginTop = "10px";
      pWrap.innerHTML = `<div class="muted">다운로드 중: ${escapeHtml(prog.file_name)} (${pct}%)</div><div class="progress-bar"><div class="progress-fill" style="width:${pct}%"></div></div>`;
      box.appendChild(pWrap);
    }
    if ((it.files || []).some((f) => f.local_cached_path || (state && it.origin_device_id === state.device.device_id))) {
      const revBtn = mkBtn("파일 위치 열기", "btn", async () => {
        await api(`/api/files/${it.id}/reveal`, { method: "POST" });
      });
      revBtn.style.marginTop = "10px";
      box.appendChild(revBtn);
    }
  }
  pane.appendChild(box);
}

async function loadTemplates() {
  const data = await api("/api/templates");
  folders = data.folders || [];
  templates = data.items || [];
  if (templates.length && (!selectedTemplateId || !templates.some((x) => x.id === selectedTemplateId))) {
    selectedTemplateId = templates[0].id;
  }
  renderFolderTree();
  renderTemplateList();
  renderTemplateDetail();
}

function renderFolderTree() {
  const el = document.getElementById("folder-tree");
  el.innerHTML = "";
  const root = document.createElement("div");
  root.className = "folder-node" + (selectedFolderId === "" ? " selected" : "");
  root.textContent = "📁 전체 / 루트";
  root.onclick = () => { selectedFolderId = ""; renderFolderTree(); renderTemplateList(); };
  el.appendChild(root);

  for (const f of folders) {
    const node = document.createElement("div");
    node.className = "folder-node" + (selectedFolderId === f.id ? " selected" : "");
    const label = document.createElement("span");
    label.textContent = "📂 " + f.name;
    const del = document.createElement("button");
    del.className = "btn danger-ghost";
    del.textContent = "×";
    del.style.padding = "2px 6px";
    del.onclick = async (e) => {
      e.stopPropagation();
      if (!confirm(`폴더 '${f.name}'를 삭제할까요? (내부 템플릿은 루트로 이동)`)) return;
      await api(`/api/templates/folders/${f.id}`, { method: "DELETE" });
      if (selectedFolderId === f.id) selectedFolderId = "";
      await loadTemplates();
    };
    node.onclick = () => { selectedFolderId = f.id; renderFolderTree(); renderTemplateList(); };
    node.append(label, del);
    el.appendChild(node);
  }
}

function renderTemplateList() {
  const el = document.getElementById("template-list");
  const filtered = templates.filter((t) => selectedFolderId === "" || t.folder_id === selectedFolderId);
  if (!filtered.length) {
    el.innerHTML = `<div class="empty-state" style="padding:20px;text-align:center;">템플릿이 없습니다.</div>`;
    return;
  }
  el.innerHTML = "";
  for (const t of filtered) {
    const row = document.createElement("div");
    row.className = "row-item" + (t.id === selectedTemplateId ? " selected" : "");
    row.onclick = () => { selectedTemplateId = t.id; renderTemplateList(); renderTemplateDetail(); };
    const body = document.createElement("div");
    body.className = "row-body";
    body.innerHTML = `<div class="row-title">${escapeHtml(t.title)}</div><div class="row-meta">${t.kind}</div>`;
    row.appendChild(body);
    el.appendChild(row);
  }
}

function renderTemplateDetail() {
  const pane = document.getElementById("template-detail");
  const t = templates.find((x) => x.id === selectedTemplateId);
  if (!t) {
    pane.innerHTML = `<div class="empty-state">템플릿을 선택하거나 새로 만드세요.</div>`;
    return;
  }
  pane.innerHTML = "";
  const hdr = document.createElement("div");
  hdr.className = "detail-header";
  const titleIn = document.createElement("input");
  titleIn.type = "text";
  titleIn.value = t.title;
  titleIn.style.flex = "1";

  const folderSel = document.createElement("select");
  folderSel.innerHTML = `<option value="">루트</option>` + folders.map((f) => `<option value="${f.id}">${escapeHtml(f.name)}</option>`).join("");
  folderSel.value = t.folder_id || "";

  const btnCopy = mkBtn("복사", "btn primary", async () => {
    await api(`/api/templates/${t.id}/select`, { method: "POST", body: JSON.stringify({ paste: false }) });
    toast("템플릿을 클립보드에 복사했습니다.");
  });
  const btnDel = mkBtn("삭제", "btn danger-ghost", async () => {
    await api(`/api/templates/${t.id}`, { method: "DELETE" });
    selectedTemplateId = null;
    await loadTemplates();
  });
  hdr.append(titleIn, folderSel, btnCopy, btnDel);
  pane.appendChild(hdr);

  const box = document.createElement("div");
  box.className = "preview-box";
  if (t.kind === "text") {
    const ta = document.createElement("textarea");
    ta.className = "preview-text";
    ta.value = t.text_content || "";
    const btnSave = mkBtn("저장", "btn primary", async () => {
      await api(`/api/templates/${t.id}`, {
        method: "PUT",
        body: JSON.stringify({ ...t, title: titleIn.value, folder_id: folderSel.value, text_content: ta.value }),
      });
      toast("템플릿을 저장했습니다.");
      await loadTemplates();
    });
    btnSave.style.marginTop = "10px";
    box.append(ta, btnSave);
  } else if (t.kind === "image") {
    const img = document.createElement("img");
    img.className = "preview-image";
    img.src = `/api/blobs/${t.content_hash}`;
    const btnSave = mkBtn("이름/폴더 저장", "btn primary", async () => {
      await api(`/api/templates/${t.id}`, {
        method: "PUT",
        body: JSON.stringify({ ...t, title: titleIn.value, folder_id: folderSel.value }),
      });
      toast("템플릿을 저장했습니다.");
      await loadTemplates();
    });
    btnSave.style.marginTop = "10px";
    box.append(img, document.createElement("br"), btnSave);
  }
  pane.appendChild(box);
}

function renderPeers() {
  if (!state) return;
  const discEl = document.getElementById("discovered-list");
  const disc = state.discovered || [];
  discEl.innerHTML = disc.length ? "" : `<div class="muted">탐지된 기기가 없습니다. (같은 Wi-Fi/LAN 또는 수동 IP 사용)</div>`;
  for (const d of disc) {
    const card = document.createElement("div");
    card.className = "peer-card";
    card.innerHTML = `<div><strong>${escapeHtml(d.device_name || d.device_id)}</strong> (${osLabel(d.os)})<div class="muted">${escapeHtml(d.addr)}</div></div>`;
    const btn = mkBtn(d.already_paired ? "연결됨" : "PIN 입력 연결", "btn", () => {
      document.getElementById("pair-addr").value = d.addr;
      document.getElementById("pair-pin").focus();
    });
    btn.disabled = d.already_paired;
    card.appendChild(btn);
    discEl.appendChild(card);
  }

  const peersEl = document.getElementById("peers-list");
  const peers = state.peers || [];
  peersEl.innerHTML = peers.length ? "" : `<div class="muted">페어링된 기기가 없습니다.</div>`;
  for (const p of peers) {
    const card = document.createElement("div");
    card.className = "peer-card";
    card.innerHTML = `<div><span class="dot ${p.online ? "online" : ""}"></span><strong>${escapeHtml(p.device_name)}</strong> (${osLabel(p.os)})<div class="muted">주소: ${escapeHtml(p.manual_addr || p.last_addr || "자동 탐지 대기")} · 핑거프린트: ${p.cert_fingerprint.slice(0, 12)}…</div></div>`;
    const btns = document.createElement("div");
    btns.className = "detail-actions";
    const btnAddr = mkBtn("IP 수정", "btn", async () => {
      const next = prompt("고정 IP:포트 (비우면 자동 탐지 사용)", p.manual_addr || p.last_addr || "");
      if (next === null) return;
      await api(`/api/peers/${p.device_id}/addr`, { method: "PUT", body: JSON.stringify({ manual_addr: next.trim() }) });
      await loadState();
    });
    const btnDel = mkBtn("연결 해제", "btn danger-ghost", async () => {
      if (!confirm(`${p.device_name} 연결을 해제할까요?`)) return;
      await api(`/api/peers/${p.device_id}`, { method: "DELETE" });
      await loadState();
    });
    btns.append(btnAddr, btnDel);
    card.appendChild(btns);
    peersEl.appendChild(card);
  }
}

function renderSettings() {
  if (!state) return;
  const c = state.config;
  document.getElementById("cfg-name").value = c.device_name || "";
  document.getElementById("cfg-hotkey").value = c.hotkey || "";
  document.getElementById("cfg-max-history").value = c.max_history || 200;
  document.getElementById("cfg-universal").checked = !!c.universal_clipboard;
  document.getElementById("cfg-autopaste").checked = !!c.auto_paste;
  document.getElementById("cfg-excluded").value = (c.excluded_apps || []).join("\n");
  document.getElementById("cache-size-text").textContent = `현재 캐시 사용량: ${fmtSize(state.cache_bytes)}`;

  const macCard = document.getElementById("mac-ax-card");
  if (state.os === "darwin") {
    macCard.classList.remove("hidden");
    document.getElementById("ax-status-text").textContent = state.accessibility_granted
      ? "손쉬운 사용 권한이 허용되어 자동 붙여넣기(Cmd+V)가 활성화되어 있습니다."
      : "자동 붙여넣기(Cmd+V)를 사용하려면 손쉬운 사용 권한이 필요합니다.";
  } else {
    macCard.classList.add("hidden");
  }
}

function mkBtn(label, cls, onClick) {
  const b = document.createElement("button");
  b.className = cls;
  b.textContent = label;
  b.onclick = onClick;
  return b;
}

function escapeHtml(s) {
  return (s || "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function connectWS() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(`${proto}//${location.host}/ws`);
  ws.onmessage = async (ev) => {
    try {
      const msg = JSON.parse(ev.data);
      if (msg.event === "history_changed") await loadHistory();
      else if (msg.event === "templates_changed") await loadTemplates();
      else if (msg.event === "peers_changed" || msg.event === "discovered_changed" || msg.event === "config_changed") await loadState();
      else if (msg.event === "download_progress") {
        fileProgress[msg.payload.item_id] = msg.payload;
        if (msg.payload.done) {
          delete fileProgress[msg.payload.item_id];
          await loadHistory();
        } else if (msg.payload.item_id === selectedHistoryId) {
          renderHistoryDetail();
        }
      } else if (msg.event === "toast" && msg.payload) {
        toast(msg.payload);
      }
    } catch {}
  };
  ws.onclose = () => setTimeout(connectWS, 1500);
}

function activateTab(tabName) {
  const target = document.getElementById(`tab-${tabName}`);
  const btn = document.querySelector(`.nav-btn[data-tab="${tabName}"]`);
  if (!target || !btn) return;
  document.querySelectorAll(".nav-btn").forEach((b) => b.classList.remove("active"));
  document.querySelectorAll(".tab-panel").forEach((p) => p.classList.remove("active"));
  btn.classList.add("active");
  target.classList.add("active");
}

// Navigation & Events
document.querySelectorAll(".nav-btn").forEach((btn) => {
  btn.onclick = () => {
    activateTab(btn.dataset.tab);
    history.replaceState(null, "", `#${btn.dataset.tab}`);
  };
});

window.addEventListener("hashchange", () => {
  const h = (location.hash || "").replace(/^#/, "");
  if (h) activateTab(h);
});

document.getElementById("history-search").oninput = loadHistory;
document.getElementById("history-kind").onchange = loadHistory;
document.getElementById("history-os").onchange = loadHistory;

document.getElementById("btn-clear-history").onclick = async () => {
  if (!confirm("모든 클립보드 히스토리를 삭제할까요?")) return;
  await api("/api/history/clear", { method: "POST" });
  selectedHistoryId = null;
  await loadHistory();
};

document.getElementById("btn-new-folder").onclick = async () => {
  const name = prompt("새 템플릿 폴더 이름");
  if (!name) return;
  await api("/api/templates/folders", { method: "POST", body: JSON.stringify({ name }) });
  await loadTemplates();
};

document.getElementById("btn-new-template").onclick = async () => {
  const title = prompt("새 텍스트 템플릿 이름");
  if (!title) return;
  const t = await api("/api/templates", {
    method: "POST",
    body: JSON.stringify({ folder_id: selectedFolderId, title, kind: "text", text_content: "" }),
  });
  selectedTemplateId = t.id;
  await loadTemplates();
};

document.getElementById("btn-start-pin").onclick = async () => {
  const res = await api("/api/pairing/start", { method: "POST" });
  document.getElementById("pin-box").classList.remove("hidden");
  document.getElementById("pin-code").textContent = res.pin;
  if (pinInterval) clearInterval(pinInterval);
  const updateTimer = () => {
    const rem = Math.max(0, Math.round((res.expires_at - Date.now()) / 1000));
    document.getElementById("pin-timer").textContent = rem > 0 ? `유효 시간: ${rem}초` : "만료됨";
    if (rem <= 0) clearInterval(pinInterval);
  };
  updateTimer();
  pinInterval = setInterval(updateTimer, 1000);
};

document.getElementById("btn-connect-pin").onclick = async () => {
  const addr = document.getElementById("pair-addr").value.trim();
  const pin = document.getElementById("pair-pin").value.trim();
  try {
    await api("/api/pairing/connect", { method: "POST", body: JSON.stringify({ addr, pin }) });
    toast("기기 연결에 성공했습니다!");
    document.getElementById("pair-pin").value = "";
    await loadState();
  } catch (e) {
    toast("연결 실패: " + e.message);
  }
};

async function saveQuickToggle() {
  if (!state) return;
  const next = {
    ...state.config,
    universal_clipboard: document.getElementById("quick-universal").checked,
    paused: document.getElementById("quick-paused").checked,
  };
  try {
    await api("/api/config", { method: "PUT", body: JSON.stringify(next) });
    await loadState();
  } catch (e) {
    toast("설정 변경 실패: " + e.message);
  }
}
document.getElementById("quick-universal").onchange = saveQuickToggle;
document.getElementById("quick-paused").onchange = saveQuickToggle;

let recordingHotkey = false;
const recBtn = document.getElementById("btn-record-hotkey");
const hotkeyInput = document.getElementById("cfg-hotkey");

function stopHotkeyRecording() {
  recordingHotkey = false;
  recBtn.textContent = "단축키 녹화";
  recBtn.classList.remove("primary");
}

function keyNameFromEvent(e) {
  const code = e.code || "";
  if (/^Key[A-Z]$/.test(code)) return code.slice(3);
  if (/^Digit[0-9]$/.test(code)) return code.slice(5);
  if (/^Numpad[0-9]$/.test(code)) return code.slice(6);
  if (code === "Space") return "Space";
  if (/^F([1-9]|1[0-2])$/.test(code)) return code;
  return "";
}

recBtn.onclick = () => {
  if (recordingHotkey) {
    stopHotkeyRecording();
    return;
  }
  recordingHotkey = true;
  recBtn.textContent = "새 단축키를 누르세요...";
  recBtn.classList.add("primary");
};

window.addEventListener("keydown", (e) => {
  if (!recordingHotkey) return;
  e.preventDefault();
  e.stopPropagation();
  if (e.key === "Escape") {
    stopHotkeyRecording();
    return;
  }
  const key = keyNameFromEvent(e);
  if (!key) return;
  const mods = [];
  const isMac = state && state.os === "darwin";
  if (e.ctrlKey) mods.push("Ctrl");
  if (e.altKey) mods.push(isMac ? "Option" : "Alt");
  if (e.shiftKey) mods.push("Shift");
  if (e.metaKey) mods.push(isMac ? "Cmd" : "Win");
  if (!mods.length) {
    toast("보조키(Option/Alt/Ctrl/Shift/Cmd)와 함께 눌러주세요.");
    return;
  }
  hotkeyInput.value = [...mods, key].join("+");
  stopHotkeyRecording();
}, true);

document.getElementById("btn-reset-hotkey").onclick = () => {
  stopHotkeyRecording();
  const isMac = state && state.os === "darwin";
  hotkeyInput.value = isMac ? "Option+V" : "Alt+V";
};

document.getElementById("btn-save-config").onclick = async () => {
  if (!state) return;
  const excluded = document.getElementById("cfg-excluded").value.split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
  const next = {
    ...state.config,
    device_name: document.getElementById("cfg-name").value.trim() || state.config.device_name,
    hotkey: document.getElementById("cfg-hotkey").value.trim() || state.config.hotkey,
    max_history: parseInt(document.getElementById("cfg-max-history").value, 10) || 200,
    universal_clipboard: document.getElementById("cfg-universal").checked,
    auto_paste: document.getElementById("cfg-autopaste").checked,
    excluded_apps: excluded,
  };
  try {
    await api("/api/config", { method: "PUT", body: JSON.stringify(next) });
    toast("설정을 저장했습니다.");
    await loadState();
  } catch (e) {
    toast("설정 저장 실패: " + e.message);
  }
};

document.getElementById("btn-clear-cache").onclick = async () => {
  await api("/api/cache/clear", { method: "POST" });
  toast("원격 파일 캐시를 비웠습니다.");
  await loadState();
  await loadHistory();
};

document.getElementById("btn-request-ax").onclick = async () => {
  await api("/api/system/accessibility", { method: "POST" });
};

(async () => {
  const initialTab = (location.hash || "").replace(/^#/, "");
  if (initialTab) activateTab(initialTab);
  await loadState();
  await loadHistory();
  await loadTemplates();
  connectWS();
})();
