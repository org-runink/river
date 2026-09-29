// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT
//
// The mouse pointer of the server kiosk. The kiosk (river-kiosk) is WPE WebKit drawing straight
// to KMS/DRM, which delivers the mouse but draws no pointer on most displays (not on the legacy
// KMS path, not without a hardware cursor plane, not without a cursor theme): the mouse worked
// and nobody could see where it was. So the page draws one. It is active only when the page's
// URL carries the fragment #kiosk (river-kiosk adds it; the UI passes it on to a first-boot
// setup page's frame), so a desktop browser keeps its own pointer. A setup page served on
// another loopback port can load this same file (http://[::1]:47660/kiosk-pointer.js) to get a
// pointer inside its frame; a page's events never reach the frame around it. It also opens
// drop-down lists on a click, which this web view does not.
"use strict";
(function kioskPointer() {
  if (!/(^|[#&])kiosk(&|$)/.test(location.hash.slice(1))) return;
  const root = document.documentElement;
  root.classList.add("kiosk");
  // One arrow, white with a dark edge so it shows on every colour; its tip is the hot spot. The
  // fill is #FEFEFE, a white nothing else on the page uses, so build/qemu-gui-test.sh can find it.
  const p = document.createElement("div");
  p.id = "kiosk-pointer";
  p.setAttribute("aria-hidden", "true");
  p.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="30" viewBox="0 0 20 30">' +
    '<path d="M1 1 L1 24 L6.5 18.5 L10.5 28 L14 26.5 L10 17.5 L18 17.5 Z" fill="#FEFEFE" stroke="#000000" ' +
    'stroke-width="1.5" stroke-linejoin="round"/></svg>';
  const s = p.style;
  s.position = "fixed"; s.left = "0"; s.top = "0"; s.zIndex = "2147483647";
  s.pointerEvents = "none"; s.display = "none"; s.willChange = "transform";
  // The platform pointer, wherever one exists, is hidden, so there is never a second arrow (the
  // UI has the same rule in app.css, since its policy refuses inline styles).
  const hide = document.createElement("style");
  hide.textContent = "html.kiosk, html.kiosk * { cursor: none !important; }";
  const put = (e) => {
    if (e.pointerType === "touch") { s.display = "none"; return; }
    s.display = "block";
    s.transform = `translate(${e.clientX}px, ${e.clientY}px)`;
  };
  const mount = () => {
    document.head.appendChild(hide);
    document.body.appendChild(p);
  };
  if (document.body) mount(); else document.addEventListener("DOMContentLoaded", mount);
  // Mouse events too: a web view without pointer events still sends these.
  for (const t of ["pointermove", "pointerdown", "pointerup", "mousemove", "mousedown"]) {
    window.addEventListener(t, put, { capture: true, passive: true });
  }
  // Into a setup page's frame: its events are that page's, which draws its own pointer.
  window.addEventListener("pointerover", (e) => { if (e.target && e.target.tagName === "IFRAME") s.display = "none"; }, { capture: true, passive: true });
  // A drop-down list: this web view opens no pop-up for a <select>, so a click on a closed one
  // turns it into an open list in place; picking an entry (or leaving it) closes it again.
  document.addEventListener("mousedown", (e) => {
    const sel = e.target && e.target.closest ? e.target.closest("select") : null;
    if (!sel || sel.multiple || sel.size > 1 || sel.disabled) return;
    e.preventDefault();
    sel.size = Math.max(2, Math.min(sel.options.length, 8));
    sel.focus();
    const close = () => {
      sel.removeAttribute("size");
      sel.removeEventListener("change", close);
      sel.removeEventListener("blur", close);
    };
    sel.addEventListener("change", close);
    sel.addEventListener("blur", close);
  }, true);
})();
