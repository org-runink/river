// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT
//
// The Runink River installer UI. It renders the backend's state (GET /api/state, pushed over
// /api/events) and turns clicks into API calls; it keeps nothing the backend does not, so a
// kiosk that reloads, or a backend that restarts, lands on the same screen. No framework, no
// build step: plain DOM and one small catalog per language (i18n/*.json).
"use strict";

const LANGS = ["en", "es", "fr", "pt"];
const INSTALL_SCREENS = ["welcome", "edition", "network", "machine", "account", "recovery", "install", "done"];
const cat = {};          // language -> strings
let S = null;            // the last state from the backend
let rendered = "";       // the screen (and page) currently in the DOM
const drafts = {};       // form input values, by element id (never sent until submitted)
let fieldErrors = {};    // account form errors from the last submit
let flash = "";          // the last action's error message key
let rec = null;          // {key, qr} while the recovery screen is up
let usb = null;          // removable drives on the recovery screen
let usbTimer = 0;
let logLines = [];
let showLog = false;
let wifiOpen = false;
let busy = false;

// ---- helpers --------------------------------------------------------------------------------
const $ = (sel) => document.querySelector(sel);
function esc(v) {
  return String(v == null ? "" : v).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function lang() { return (S && S.lang) || "en"; }
function t(key, vars) {
  let s = (cat[lang()] && cat[lang()][key]) || (cat.en && cat.en[key]) || key;
  if (vars) s = s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? vars[k] : m));
  return s;
}
function T(key, vars) { return esc(t(key, vars)); }
function has(key) { return !!((cat[lang()] && cat[lang()][key]) || (cat.en && cat.en[key])); }
function loc(m) { return m ? (m[lang()] || m.en || "") : ""; }
function gb(bytes) {
  const g = bytes / 1e9;
  return g >= 1000 ? (g / 1000).toFixed(g >= 10000 ? 0 : 1) + " TB" : Math.round(g) + " GB";
}
function selfEdition() {
  const l = (S && S.edition && S.edition.list) || [];
  return l.find((e) => e.id === S.edition.self) || l[0] || { title: "Runink River" };
}
function icon(status) { return `<span class="icon ${esc(status)}" aria-hidden="true"></span>`; }

async function api(path, body) {
  const opt = body === undefined ? {} : {
    method: "POST", headers: { "Content-Type": "application/json", "X-River": "1" }, body: JSON.stringify(body),
  };
  const r = await fetch(path, opt);
  let data = null;
  try { data = await r.json(); } catch (e) { data = null; }
  if (!r.ok) {
    const err = new Error((data && data.error) || "err.internal");
    err.data = data;
    throw err;
  }
  return data;
}

// act runs one user action; on success the returned state is rendered at once (the event
// stream sends the same shortly after).
async function act(path, body, opts) {
  if (busy) return;
  busy = true;
  flash = "";
  try {
    const st = await api(path, body === undefined ? {} : body);
    if (opts && opts.after) opts.after(st);
    if (st && st.mode) setState(st);
  } catch (e) {
    flash = e.message;
    if (e.data && e.data.fields) fieldErrors = e.data.fields;
    if (e.message === "err.state") refresh();
    render(true);
  } finally {
    busy = false;
  }
}

async function refresh() {
  try { setState(await api("/api/state")); } catch (e) { /* the event stream reconnects */ }
}

// ---- event stream, reconnect ---------------------------------------------------------------------
function connect() {
  const es = new EventSource("/api/events");
  es.addEventListener("state", (ev) => { $("#banner").hidden = true; setState(JSON.parse(ev.data)); });
  es.addEventListener("log", (ev) => {
    logLines.push(JSON.parse(ev.data));
    if (logLines.length > 400) logLines = logLines.slice(-400);
    const pre = $("#log");
    if (pre) { pre.textContent = logLines.join("\n"); pre.scrollTop = pre.scrollHeight; }
  });
  es.onerror = () => {
    $("#banner").hidden = false;
    $("#banner").textContent = t("app.reconnecting");
    if (es.readyState === EventSource.CLOSED) { setTimeout(connect, 1500); }
  };
}

function setState(st) {
  const langChanged = !S || S.lang !== st.lang;
  S = st;
  if (langChanged) document.documentElement.lang = st.lang;
  render(langChanged);
}

// ---- rendering ---------------------------------------------------------------------------------------
function header() {
  const sel = $("#lang");
  if (!sel.options.length) {
    sel.innerHTML = LANGS.map((l) => `<option value="${l}">${esc((cat[l] || {})["lang.name"] || l)}</option>`).join("");
    sel.addEventListener("change", () => act("/api/locale", { lang: sel.value, keyboard: "" }));
  }
  sel.value = lang();
  $("#langlabel").textContent = t("app.language");
  // The running edition names the product: "Install Runink River" on the Runink River medium,
  // a downstream edition's own title on its medium (descriptor "title"; first boot: S.title).
  const title = S.mode === "firstboot" ? (S.title || selfEdition().title) : t("app.title", { edition: selfEdition().title });
  $("#brand").textContent = title;
  const st = $("#stepper");
  if (S.mode === "install") {
    const i = INSTALL_SCREENS.indexOf(S.screen);
    st.innerHTML = INSTALL_SCREENS.map((_, k) => `<i class="${k < i ? "done" : k === i ? "on" : ""}"></i>`).join("");
  } else {
    st.innerHTML = "";
  }
  document.title = title;
}

function key() {
  if (!S) return "";
  if (S.mode === "firstboot") return "fb:" + S.screen + ":" + (S.current || "");
  return S.screen;
}

// render redraws the screen, keeping what the user typed and where the focus was.
function render(force) {
  if (!S) return;
  header();
  const k = key();
  const changed = k !== rendered;
  // A setup page's frame must never be reloaded by a state push.
  if (!changed && !force && S.mode === "firstboot" && S.screen === "page") return;
  const active = document.activeElement && document.activeElement.id;
  const selStart = active && document.activeElement.selectionStart;
  const fn = (S.mode === "firstboot" ? FB : SCREENS)[S.screen] || (() => `<p>${esc(S.screen)}</p>`);
  const html = fn();
  $("#app").innerHTML = html + (flash ? `<p class="error" role="alert">${T(flash)}</p>` : "");
  for (const id in drafts) {
    const el = document.getElementById(id);
    if (!el) continue;
    if (el.type === "checkbox" || el.type === "radio") el.checked = drafts[id];
    else el.value = drafts[id];
  }
  // Inline style attributes are blocked by the CSP; widths are set through the CSSOM.
  document.querySelectorAll("[data-pct]").forEach((el) => { el.style.width = el.dataset.pct + "%"; });
  rendered = k;
  if (changed) {
    flash = "";
    const f = $("#app [autofocus]") || $("#app .primary") || $("#app h1");
    if (f) f.focus();
    enter(k);
  } else if (active) {
    const el = document.getElementById(active);
    if (el) {
      el.focus();
      try { if (selStart != null) el.setSelectionRange(selStart, selStart); } catch (e) { /* not a text input */ }
    }
  }
}

// enter runs once when a screen appears.
function enter(k) {
  clearInterval(usbTimer);
  if (k === "recovery") {
    loadRecovery();
    loadUSB();
    usbTimer = setInterval(loadUSB, 4000);
  }
  if (k.startsWith("fb:page:")) loadPage(S.current);
}

document.addEventListener("input", (ev) => {
  const el = ev.target;
  if (!el.id) return;
  drafts[el.id] = (el.type === "checkbox" || el.type === "radio") ? el.checked : el.value;
  // The recovery screen's "start installing" button turns on only once every asked-for key
  // group is fully typed, so each keystroke in one has to re-render.
  if (el.id === "written" || el.id.startsWith("rkg-")) render(true);
});
document.addEventListener("change", (ev) => {
  const el = ev.target;
  if (el.name === "uilang") act("/api/locale", { lang: el.value, keyboard: "" });
  if (el.id === "keyboard") act("/api/locale", { lang: "", keyboard: el.value });
  if (el.name === "edition" || el.name === "role") { drafts[el.id] = true; render(true); }
});
document.addEventListener("click", (ev) => {
  const b = ev.target.closest("[data-act]");
  if (!b || b.disabled) return;
  ev.preventDefault();
  const fn = ACTIONS[b.dataset.act];
  if (fn) fn(b);
});
document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape" && S && S.machine && S.machine.erase_dialog) {
    act("/api/machine/erase-dialog", { open: false });
  }
  // Enter in a single-line field submits the screen's primary action.
  if (ev.key === "Enter" && ev.target.tagName === "INPUT" && ev.target.type !== "checkbox") {
    const scope = ev.target.closest(".dialog") || $("#app");
    const p = scope.querySelector(".primary:not(:disabled), .danger:not(:disabled)");
    if (p) { ev.preventDefault(); p.click(); }
  }
});

function actions(back, primary) {
  return `<div class="actions">${back ? `<button data-act="back" id="btn-back">${T("app.back")}</button>` : ""}<span class="spacer"></span>${primary}</div>`;
}

// ---- install screens -----------------------------------------------------------------------------------
const SCREENS = {
  welcome() {
    const langs = LANGS.map((l) => `<label class="choice"><input type="radio" name="uilang" value="${l}" ${l === lang() ? "checked" : ""}>
      <b>${esc((cat[l] || {})["lang.name"] || l)}</b></label>`).join("");
    const kbs = (S.options.keyboards || []).map((k) => `<option value="${esc(k)}" ${k === S.keyboard ? "selected" : ""}>${T("kb." + k)}</option>`).join("");
    return `<h1 tabindex="-1">${T("welcome.title")}</h1>
      <p class="lead">${T("welcome.lead", { edition: selfEdition().title })}</p>
      <h2>${T("welcome.language")}</h2><div class="choices cols" role="radiogroup">${langs}</div>
      <label class="field"><span>${T("welcome.keyboard")}</span><select id="keyboard">${kbs}</select></label>
      <label class="field"><span class="help">${T("welcome.keyboard.test")}</span><input type="text" id="kbtest" autocomplete="off"></label>
      ${actions(false, `<button class="primary" data-act="welcome">${T("app.next")}</button>`)}`;
  },

  edition() {
    const list = S.edition.list || [];
    const chosen = pickedEdition();
    const eds = list.map((e) => `<label class="choice"><input type="radio" name="edition" id="ed-${esc(e.id)}" value="${esc(e.id)}" ${e.id === chosen ? "checked" : ""}>
      <span><b>${esc(e.title)}</b><small>${esc(loc(e.description))}</small>${e.self ? `<small>${T("edition.running")}</small>` : ""}</span></label>`).join("");
    const self = list.find((e) => e.id === chosen && e.self);
    let roles = "";
    if (self && self.roles && self.roles.length) {
      const pr = pickedRole(self);
      roles = `<h2>${T("edition.role")}</h2><div class="choices">` + self.roles.map((r) => `<label class="choice">
        <input type="radio" name="role" id="role-${esc(r.id)}" value="${esc(r.id)}" ${r.id === pr ? "checked" : ""}>
        <span><b>${esc(loc(r.title))}</b><small>${esc(loc(r.description))}</small></span></label>`).join("") + "</div>";
    }
    let sw = "";
    if (S.edition.switch_to) {
      const other = list.find((e) => e.id === S.edition.switch_to) || { title: S.edition.switch_to };
      sw = `<div class="note"><b>${T("edition.switch.title")}</b><p>${T("edition.switch.text", { edition: other.title })}</p></div>`;
      return `<h1 tabindex="-1">${T("edition.title")}</h1><div class="choices">${eds}</div>${sw}
        ${actions(true, `<button class="primary" data-act="reboot">${T("edition.switch.restart")}</button>`)}`;
    }
    return `<h1 tabindex="-1">${T("edition.title")}</h1><p class="lead">${T("edition.lead")}</p>
      <div class="choices" role="radiogroup">${eds}</div>${roles}
      ${actions(true, `<button class="primary" data-act="edition">${T("app.next")}</button>`)}`;
  },

  network() {
    const n = S.net || {};
    const st = n.state || {};
    let status;
    if (n.status === "running" || !n.status || n.status === "idle") {
      status = `<div class="status">${icon("running")}${T("net.running")}</div>`;
    } else if (st.state === "online") {
      status = `<div class="status">${icon("ok")}${T("net.online")}: ${esc(netKind(st))}</div>`;
    } else if (st.state === "lan-only") {
      status = `<div class="status">${icon("warn")}${T("net.lanonly")}</div><p class="help">${esc(netKind(st))}</p>`;
    } else {
      status = `<div class="status">${icon("pending")}${T("net.offline")}</div><p>${T("net.offline.help")}</p>`;
    }
    let wifi = "";
    const running = n.status === "running";
    if (!running && st.has_wifi && st.state !== "online") {
      if (!wifiOpen) {
        wifi = `<button data-act="wifi-open" id="btn-wifi">${T("net.wifi.choose")}</button>`;
      } else if (!n.wifi) {
        wifi = `<p>${icon("running")} ${T("net.wifi.scanning")}</p>`;
      } else if (!n.wifi.length) {
        wifi = `<p>${T("net.wifi.none")}</p><button data-act="wifi-open">${T("net.retry")}</button>`;
      } else {
        const pick = n.wifi.map((w, i) => `<label class="choice"><input type="radio" name="ssid" id="ssid-${i}" value="${esc(w.ssid)}">
          <span><b>${esc(w.ssid)}</b><small>${w.secure ? "🔒" : T("net.wifi.open")} · ${w.signal}%</small></span></label>`).join("");
        wifi = `<h2>${T("net.wifi.network")}</h2><div class="choices wifilist">${pick}</div>
          <label class="field"><span>${T("net.wifi.passphrase")}</span><input type="password" id="wifipsk" autocomplete="off"></label>
          <button data-act="wifi-connect" id="btn-wifi-connect">${T("net.wifi.connect")}</button>`;
      }
    }
    const details = st.state ? `<details><summary>${T("app.details")}</summary><dl class="kv">
      <dt>${T("net.detail.iface")}</dt><dd>${esc(st.iface || "-")}</dd>
      <dt>${T("net.detail.addrs")}</dt><dd class="mono">${esc((st.addrs || []).join("  ") || "-")}</dd>
      <dt>${T("net.detail.method")}</dt><dd>${esc(st.method || "-")}</dd>
      <dt>${T("net.detail.internet")}</dt><dd>IPv4 ${T(st.internet4 ? "net.detail.yes" : "net.detail.no")}, IPv6 ${T(st.internet6 ? "net.detail.yes" : "net.detail.no")}</dd>
      </dl></details>` : "";
    const offline = !st.state || st.state === "offline";
    const primary = `<button class="primary" data-act="net-continue" ${running ? "disabled" : ""}>${T(offline ? "net.continue.offline" : "app.next")}</button>`;
    return `<h1 tabindex="-1">${T("net.title")}</h1><p class="lead">${T("net.lead")}</p>
      <div class="card">${status}${n.error ? `<p class="error">${T(n.error)}</p>` : ""}</div>${wifi}${details}
      ${actions(true, `<button data-act="net-retry" ${running ? "disabled" : ""}>${T("net.retry")}</button>${primary}`)}`;
  },

  machine() {
    const m = S.machine || {};
    const ed = selfEdition();
    if (m.status === "running" || !m.status || m.status === "idle") {
      return `<h1 tabindex="-1">${T("machine.title")}</h1><div class="card"><div class="status">${icon("running")}${T("machine.running")}</div></div>${actions(true, "")}`;
    }
    if (m.status === "failed") {
      return `<h1 tabindex="-1">${T("machine.title")}</h1><div class="alert">${T(m.error || "err.machine.probe")}</div>
        ${actions(true, `<button class="primary" data-act="probe">${T("machine.retry")}</button>`)}`;
    }
    const facts = `<div class="card"><dl class="kv">
      <dt>${T("machine.cpu")}</dt><dd>${esc(m.cpu || "-")} <small>(${T("machine.cores", { cores: m.cores, threads: m.threads })})</small></dd>
      <dt>${T("machine.memory")}</dt><dd>${esc(Math.round(m.memory_gib))} GB</dd></dl></div>`;
    const details = `<details><summary>${T("app.details")}</summary><dl class="kv">
      <dt>${T("machine.layout")}</dt><dd>${esc(m.layout || "-")}</dd>
      <dt>${T("machine.usable")}</dt><dd>${m.usable_bytes ? gb(m.usable_bytes) : "-"}</dd>
      <dt>x86-64</dt><dd>${esc(m.psabi || "-")}${m.virtual ? " · VM" : ""}${m.uefi ? " · UEFI" : ""}</dd></dl>
      ${(m.warnings || []).length ? `<h2>${T("machine.warnings")}</h2><ul>${m.warnings.map((w) => `<li class="help">${esc(w)}</li>`).join("")}</ul>` : ""}
      ${(m.unused || []).length ? `<h2>${T("machine.unused")}</h2><ul>${m.unused.map((u) => `<li class="help">${esc(u.name)}: ${esc(u.reason)}</li>`).join("")}</ul>` : ""}
      ${m.verdict === "refused" ? `<p><button data-act="lab" id="btn-lab">${T("machine.lab")}</button></p><p class="help">${T("machine.lab.help")}</p>` : ""}
      </details>`;
    if (m.verdict === "refused") {
      return `<h1 tabindex="-1">${T("machine.refused.title")}</h1>${facts}
        <div class="alert"><p>${T("machine.refused.text", { edition: ed.title })}</p><ul>${(m.refusals || []).map((r) => `<li>${esc(r)}</li>`).join("")}</ul></div>
        ${details}${actions(true, `<button class="primary" data-act="probe">${T("machine.retry")}</button>`)}`;
    }
    const disks = m.disks || [];
    if (!disks.length) {
      return `<h1 tabindex="-1">${T("machine.title")}</h1>${facts}<div class="alert">${T("machine.nodisk")}</div>${details}
        ${actions(true, `<button class="primary" data-act="probe">${T("machine.retry")}</button>`)}`;
    }
    const dl = disks.map((d) => `<div class="disk"><div><div class="big">${esc(d.model || d.name)} · ${gb(d.size_bytes)}</div>
      <small>${esc(d.name)} · ${T("machine.serial", { serial: d.serial })}</small></div></div>`).join("");
    const layout = disks.length > 1 && has("machine.layout." + m.layout) ? `<p>${T("machine.layout." + m.layout, { n: disks.length })}</p>` : "";
    return `<h1 tabindex="-1">${T("machine.title")}</h1>${facts}
      ${m.lab ? `<div class="note">${T("machine.lab.on")}</div>` : ""}
      <h2>${T(disks.length > 1 ? "machine.eraseN" : "machine.erase1")}</h2>${dl}${layout}
      <p>${T("machine.everything", { them: t(disks.length > 1 ? "machine.them" : "machine.it") })}</p>${details}
      ${actions(true, `<button class="danger primary" data-act="erase-open" id="btn-erase">${T("machine.erase.button")}</button>`)}
      ${m.erase_dialog ? eraseDialog(disks) : ""}`;
  },

  account() {
    const a = S.account || {};
    const fe = (k) => fieldErrors[k] ? `<div class="error">${T(fieldErrors[k])}</div>` : "";
    const cls = (k) => fieldErrors[k] ? "field bad" : "field";
    if (!("acct-host" in drafts)) drafts["acct-host"] = a.hostname || "";
    if (!("acct-user" in drafts)) drafts["acct-user"] = a.username || "";
    return `<h1 tabindex="-1">${T("account.title")}</h1><p class="lead">${T("account.lead")}</p>
      <label class="${cls("hostname")}"><span>${T("account.hostname")}</span><input type="text" id="acct-host" autocomplete="off" autocapitalize="none" spellcheck="false">
        <div class="help">${T("account.hostname.help")}</div>${fe("hostname")}</label>
      <label class="${cls("username")}"><span>${T("account.username")}</span><input type="text" id="acct-user" autocomplete="off" autocapitalize="none" spellcheck="false">${fe("username")}</label>
      <label class="${cls("password")}"><span>${T("account.password")}</span><input type="password" id="acct-pass" autocomplete="new-password" ${a.has_password ? "" : "autofocus"}>
        ${a.has_password ? `<div class="help">${T("account.password.keep")}</div>` : ""}${fe("password")}</label>
      <label class="field"><span>${T("account.password2")}</span><input type="password" id="acct-pass2" autocomplete="new-password"></label>
      <details ${fieldErrors.ssh_keys ? "open" : ""}><summary>${T("account.ssh")}</summary>
        <label class="${cls("ssh_keys")}"><span class="help">${T("account.ssh.help")}</span><textarea id="acct-ssh" spellcheck="false"></textarea>${fe("ssh_keys")}</label></details>
      ${a.payloads ? `<details><summary>${T("account.passphrase")}</summary><label class="${cls("passphrase")}"><span class="help">${T("account.passphrase.help")}</span>
        <input type="password" id="acct-medium" autocomplete="off">${fe("passphrase")}</label></details>` : ""}
      <p class="help">${T("account.root")}</p>
      ${actions(true, `<button class="primary" data-act="account">${T("app.next")}</button>`)}`;
  },

  recovery() {
    const r = S.recovery || {};
    const keyBox = rec ? `<div class="keyrow"><div class="key" id="reckey" aria-label="${esc(rec.key)}">${esc(rec.key).replace(/ /g, (m, i) => (i === 35 ? "<br>" : " "))}</div>
      <div><div class="qr" role="img" aria-label="${T("recovery.qr")}">${qrSVG(rec.qr)}</div><div class="help">${T("recovery.qr")}</div></div></div>` : `<p>${icon("running")}</p>`;
    const drives = (usb || []).map((d) => `<button data-act="usb" data-dev="${esc(d.dev)}">${T("recovery.usb", { drive: (d.model || d.dev) + " (" + d.size_gb + " GB)" })}</button>`).join(" ");
    // Type back the groups the backend asked for. A tickbox used to stand here and was
    // satisfied by a glance; this key is shown once, for a disk it is the only way to
    // decrypt, so the copy that leaves the room has to be proved readable. It is asked for
    // even when the key was saved to a USB drive: a stick is one more thing that can be
    // lost, and the operator can read the groups straight off this screen.
    const want = r.confirm || [];
    const typed = (n) => (drafts["rkg-" + n] || "").replace(/\s/g, "");
    const filled = want.length > 0 && want.every((n) => typed(n).length === 8);
    const boxes = want.map((n) => `<label class="field group"><span>${T("recovery.group", { n })}</span>
      <input id="rkg-${n}" size="10" maxlength="12" autocomplete="off" autocapitalize="off" spellcheck="false"></label>`).join("");
    return `<h1 tabindex="-1">${T("recovery.title")}</h1>
      ${r.lost ? `<div class="note">${T("recovery.lost")}</div>` : ""}
      <p class="lead">${T("recovery.lead")}</p>${keyBox}
      <div class="note">${T("recovery.warn")}</div>
      <p>${drives}${r.saved_to ? ` <span class="help">${T("recovery.usb.saved", { drive: r.saved_to })}</span>` : ""}</p>
      <p class="lead">${T("recovery.confirm")}</p>
      <div class="keygroups">${boxes}</div>
      ${actions(true, `<button class="primary" data-act="ack" ${filled ? "" : "disabled"}>${T("recovery.start")}</button>`)}`;
  },

  install() {
    const i = S.install || {};
    const steps = (i.steps || []).map((s) => `<li class="${esc(s.status)}">${icon(s.status === "done" ? "ok" : s.status === "failed" ? "fail" : s.status)}
      ${T(has("step." + s.id) ? "step." + s.id : s.id)}${s.status === "running" && s.progress ? ` <small>(${s.progress}%)</small>` : ""}</li>`).join("");
    const eta = i.eta_seconds > 60 ? T("install.left", { min: Math.ceil(i.eta_seconds / 60) }) : T("install.leftless");
    if (i.status === "failed") {
      const fs = i.failed_step ? t(has("step." + i.failed_step) ? "step." + i.failed_step : i.failed_step) : "";
      return `<h1 tabindex="-1">${T("install.failed.title")}</h1>
        <div class="alert"><p>${T("install.failed.text", { step: fs })}</p>${i.error ? `<p class="help">${T(i.error)}</p>` : ""}</div>
        <ul class="steps">${steps}</ul>
        <p><button data-act="log" id="btn-log">${T("install.log")}</button></p>${showLog ? `<pre class="log" id="log">${esc(logLines.join("\n"))}</pre>` : ""}
        ${actions(false, `<button class="primary" data-act="retry">${T("install.retry")}</button>`)}`;
    }
    return `<h1 tabindex="-1">${T("install.title")}</h1><p class="lead">${T("install.lead")}</p>
      <div class="progress" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${i.percent || 0}"><div data-pct="${i.percent || 0}"></div></div>
      <p><b>${i.percent || 0}%</b> · ${eta}</p>
      <ul class="steps">${steps}</ul>
      <details ${showLog ? "open" : ""}><summary data-act="log">${T("install.log")}</summary>${showLog ? `<pre class="log" id="log">${esc(logLines.join("\n"))}</pre>` : ""}</details>`;
  },

  done() {
    return `<h1 tabindex="-1">${T("done.title")}</h1><div class="card"><div class="status">${icon("ok")}${T("done.text", { edition: selfEdition().title })}</div></div>
      ${actions(false, `<button class="primary" data-act="reboot">${T("done.restart")}</button>`)}`;
  },
};

function netKind(st) {
  const fam = [st.ipv4 ? "IPv4" : "", st.ipv6 ? "IPv6" : ""].filter(Boolean).join(" + ");
  return t(st.wifi ? "net.kind.wifi" : "net.kind.wired") + (fam ? ", " + fam : "");
}

function pickedEdition() {
  for (const e of (S.edition.list || [])) if (drafts["ed-" + e.id]) {
    // the last radio the user clicked (radios keep one checked; drafts may hold stale trues)
    const el = document.getElementById("ed-" + e.id);
    if (el ? el.checked : true) return e.id;
  }
  return S.edition.switch_to || S.edition.choice || S.edition.self;
}
function pickedRole(self) {
  for (const r of self.roles) {
    const el = document.getElementById("role-" + r.id);
    if (el && el.checked) return r.id;
  }
  return S.edition.role || self.default_role;
}

function eraseDialog(disks) {
  const word = (S.options && S.options.erase_word) || "ERASE";
  const names = disks.map((d) => `${d.model || d.name}, ${gb(d.size_bytes)} (${t("machine.serial", { serial: d.serial })})`).join("; ");
  return `<div class="dialog-back"><div class="dialog" role="alertdialog" aria-modal="true" aria-labelledby="erase-h">
    <h1 id="erase-h">${T("erase.title")}</h1><p>${T("erase.text", { disk: names })}</p>
    <label class="field"><span>${T("erase.type", { word })}</span><input type="text" id="erase-word" autocomplete="off" autocapitalize="characters" spellcheck="false" autofocus></label>
    <div class="actions"><button data-act="erase-cancel">${T("erase.cancel")}</button><span class="spacer"></span>
    <button class="danger primary" data-act="erase-confirm">${T("erase.confirm")}</button></div></div></div>`;
}

// qrSVG draws the matrix the backend encoded (rows of "0"/"1") with a 4-module quiet zone.
function qrSVG(rows) {
  if (!rows || !rows.length) return "";
  const n = rows.length, q = 4;
  let d = "";
  rows.forEach((row, y) => {
    for (let x = 0; x < n; x++) if (row[x] === "1") d += `M${x + q} ${y + q}h1v1h-1z`;
  });
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${n + 2 * q} ${n + 2 * q}" shape-rendering="crispEdges"><rect width="100%" height="100%" fill="#fff"/><path d="${d}" fill="#000"/></svg>`;
}

async function loadRecovery() {
  try { rec = await api("/api/recovery"); } catch (e) { rec = null; }
  render(true);
}
async function loadUSB() {
  try { usb = (await api("/api/recovery/usb")).drives; } catch (e) { usb = []; }
  if (S && S.screen === "recovery") render(true);
}

const ACTIONS = {
  back: () => act("/api/back"),
  welcome: () => act("/api/welcome"),
  edition: () => {
    const ed = pickedEdition();
    const self = (S.edition.list || []).find((e) => e.id === ed && e.self);
    act("/api/edition", { edition: ed, role: self && self.roles && self.roles.length ? pickedRole(self) : "" });
  },
  reboot: () => act("/api/reboot"),
  "net-retry": () => { wifiOpen = false; act("/api/network/auto"); },
  "net-continue": () => act("/api/network/continue", { offline: !(S.net.state && S.net.state.state !== "offline") }),
  "wifi-open": () => { wifiOpen = true; render(true); act("/api/network/scan"); },
  "wifi-connect": () => {
    const sel = document.querySelector("input[name=ssid]:checked");
    act("/api/network/wifi", { ssid: sel ? sel.value : "", passphrase: ($("#wifipsk") || {}).value || "" },
      { after: () => { delete drafts.wifipsk; wifiOpen = false; } });
  },
  probe: () => act("/api/machine/probe", { lab: !!(S.machine && S.machine.lab) }),
  lab: () => act("/api/machine/probe", { lab: true }),
  "erase-open": () => { delete drafts["erase-word"]; act("/api/machine/erase-dialog", { open: true }); },
  "erase-cancel": () => act("/api/machine/erase-dialog", { open: false }),
  "erase-confirm": () => act("/api/machine/confirm", {
    word: ($("#erase-word") || {}).value || "", disks: (S.machine.disks || []).map((d) => d.serial),
  }),
  account: () => {
    fieldErrors = {};
    const v = (id) => ($("#" + id) || {}).value || "";
    act("/api/account", {
      hostname: v("acct-host"), username: v("acct-user"), password: v("acct-pass"), password2: v("acct-pass2"),
      ssh_keys: v("acct-ssh"), passphrase: v("acct-medium"),
    }, { after: () => { delete drafts["acct-pass"]; delete drafts["acct-pass2"]; delete drafts["acct-medium"]; } });
  },
  usb: (b) => act("/api/recovery/usb", { dev: b.dataset.dev }),
  ack: () => {
    const groups = {};
    ((S.recovery || {}).confirm || []).forEach((n) => { groups[n] = ($("#rkg-" + n) || {}).value || ""; });
    act("/api/recovery/ack", { groups }, {
      after: () => {
        rec = null;
        Object.keys(drafts).forEach((k) => { if (k.startsWith("rkg-")) delete drafts[k]; });
      },
    });
  },
  retry: () => { showLog = false; act("/api/install/retry"); },
  log: async () => {
    showLog = !showLog;
    if (showLog) { try { logLines = (await api("/api/install/log")).lines || []; } catch (e) { /* keep streamed lines */ } }
    render(true);
  },
  // first boot
  "fb-retry": () => act("/api/firstboot/retry"),
  "fb-skip": () => act("/api/firstboot/skip", { name: S.current }),
  "fb-finish": () => act("/api/firstboot/finish"),
};

// ---- first boot screens ---------------------------------------------------------------------------------
const FB = {
  settle() {
    const checks = (S.checks || []).map((c) => {
      const d = c.detail ? (has("fb.detail." + c.detail) ? t("fb.detail." + c.detail) : c.detail) : "";
      return `<li>${icon(c.status)}<b>${T("fb.check." + c.id)}</b><span>${esc(d)}</span></li>`;
    }).join("");
    const hooks = S.hooks || [];
    let hk = "";
    if (hooks.length) {
      const busyH = hooks.some((h) => h.outcome === "pending" || h.outcome === "running");
      const failed = hooks.filter((h) => h.outcome === "failed");
      hk = `<li>${icon(failed.length ? "fail" : busyH ? "running" : "ok")}<b>${T("fb.hooks")}</b><span></span></li>`;
      if (failed.length) {
        hk += `</ul>` + failed.map((h) => `<div class="alert"><b>${T("fb.hook.failed", { name: h.name })}</b>
          <details><summary>${T("app.details")}</summary><p class="help">exit ${esc(h.rc)} · ${T("fb.hook.failed.help", { name: h.name })}</p></details></div>`).join("") + `<ul class="checks">`;
      }
    }
    const failedAny = hooks.some((h) => h.outcome === "failed");
    return `<h1 tabindex="-1">${T("fb.title", { edition: S.title || "Runink River" })}</h1><p class="lead">${T("fb.lead")}</p>
      <ul class="checks">${checks}${hk}</ul>
      ${pageErrors()}
      ${failedAny ? actions(false, `<button class="primary" data-act="fb-retry">${T("fb.hook.retry")}</button>`) : ""}`;
  },
  page() {
    const p = (S.pages || []).find((x) => x.name === S.current) || { title: "" };
    return `<h1 tabindex="-1">${T("fb.page.step")}: ${esc(p.title)}</h1>
      <iframe class="frame" id="pageframe" title="${esc(p.title)}" referrerpolicy="no-referrer"></iframe>
      <details><summary>${T("app.details")}</summary><p><button data-act="fb-skip">${T("fb.page.skip")}</button></p>
      <p class="help">${T("fb.page.skip.help")}</p></details>`;
  },
  ready() {
    const r = S.ready || {};
    const addrs = (r.addresses || []);
    const ssh = addrs.map((a) => `<div class="mono">ssh ${esc(r.admin_user || "runink")}@${esc(a)}</div>`).join("");
    return `<h1 tabindex="-1">${T("fb.ready.title", { edition: r.title || S.title || "Runink River" })}</h1>
      <div class="card"><div class="status">${icon("ok")}${esc(r.hostname)}</div>
      <dl class="kv"><dt>${T("fb.ready.addresses")}</dt><dd class="mono">${esc(addrs.join("  ") || t("fb.ready.none"))}</dd></dl></div>
      ${ssh ? `<h2>${T("fb.ready.ssh")}</h2><div class="card">${ssh}</div>` : ""}
      <details><summary>${T("fb.ready.fingerprints")}</summary><p class="help">${T("fb.ready.fingerprints.help")}</p>
      ${(r.fingerprints || []).map((f) => `<div class="mono">${esc(f)}</div>`).join("")}</details>
      ${pageErrors()}
      ${actions(false, `<button class="primary" data-act="fb-finish">${T("fb.finish")}</button>`)}`;
  },
  finished() {
    return `<h1 tabindex="-1">${T("fb.ready.title", { edition: S.title || "Runink River" })}</h1>
      <div class="card"><div class="status">${icon("ok")}${T("fb.finished")}</div></div>`;
  },
};

function pageErrors() {
  return (S.pages || []).filter((p) => p.error).map((p) => `<div class="note">${T("fb.page.refused", { why: p.name + ": " + p.error })}</div>`).join("");
}

async function loadPage(name) {
  try {
    const r = await api("/api/firstboot/page?name=" + encodeURIComponent(name));
    const f = $("#pageframe");
    // In the kiosk the frame is told so too (kiosk-pointer.js draws the pointer inside it).
    const k = document.documentElement.classList.contains("kiosk") && !r.url.includes("#") ? "#kiosk" : "";
    if (f && S.current === name) f.src = r.url + k;
  } catch (e) { /* the next state push retries */ }
}

// ---- start ---------------------------------------------------------------------------------------
(async function start() {
  await Promise.all(LANGS.map(async (l) => {
    try { cat[l] = await (await fetch("i18n/" + l + ".json")).json(); } catch (e) { cat[l] = {}; }
  }));
  await refresh();
  connect();
})();

// The edition's own mark (descriptor "icon", served at /edition-icon) replaces the generic one.
(function editionIcon() {
  const img = document.getElementById("logo");
  const show = () => { img.hidden = false; document.getElementById("mark").hidden = true; };
  if (img.complete && img.naturalWidth > 0) show(); else img.addEventListener("load", show);
})();
