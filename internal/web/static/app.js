// Spider Notes UI behaviour. No inline scripts (strict CSP), so everything is wired here.
(function () {
  "use strict";

  var csrf = document.querySelector('meta[name="csrf-token"]');
  var token = csrf ? csrf.getAttribute("content") : "";

  // Send the CSRF token with every htmx request.
  document.body.addEventListener("htmx:configRequest", function (e) {
    if (token) e.detail.headers["X-CSRF-Token"] = token;
  });
  document.body.addEventListener("htmx:responseError", function (e) {
    var xhr = e.detail.xhr;
    toast((xhr && xhr.responseText) || "Request failed", true);
  });
  document.body.addEventListener("htmx:sendError", function () {
    toast("Network error, please retry", true);
  });

  function toast(msg, isErr) {
    var t = document.getElementById("toast");
    if (!t) return;
    t.textContent = String(msg).slice(0, 300);
    t.className = "toast" + (isErr ? " err" : "");
    t.hidden = false;
    clearTimeout(t._timer);
    t._timer = setTimeout(function () { t.hidden = true; }, 3500);
  }

  // ---- sidebar drawer (mobile) ----
  function closeSidebar() { document.body.classList.remove("sb-open"); }
  document.addEventListener("click", function (e) {
    if (!e.target.closest) return;
    if (e.target.closest("[data-open-sidebar]")) document.body.classList.add("sb-open");
    else if (e.target.closest("[data-close-sidebar]")) closeSidebar();
  });

  // ---- theme: system -> light -> dark ----
  var themeBtn = document.getElementById("theme-toggle");
  var themeLabel = document.getElementById("theme-label");
  function currentTheme() { return document.documentElement.getAttribute("data-theme") || ""; }
  function showTheme() { if (themeLabel) themeLabel.textContent = currentTheme() || "system"; }
  showTheme();
  if (themeBtn) {
    themeBtn.addEventListener("click", function () {
      var cur = currentTheme();
      var next = cur === "" ? "light" : cur === "light" ? "dark" : "";
      if (next) document.documentElement.setAttribute("data-theme", next);
      else document.documentElement.removeAttribute("data-theme");
      try { if (next) localStorage.setItem("theme", next); else localStorage.removeItem("theme"); } catch (err) { /* ignore */ }
      showTheme();
    });
  }

  // ---- confirm dialogs for plain forms ----
  document.addEventListener("submit", function (e) {
    var f = e.target;
    if (f.matches && f.matches("form[data-confirm]") && !window.confirm(f.getAttribute("data-confirm"))) e.preventDefault();
  });

  // ---- auto-submit filter controls ----
  document.addEventListener("change", function (e) {
    var el = e.target;
    if (el.matches && el.matches("[data-autosubmit]") && el.form) el.form.requestSubmit();
  });

  // ---- textareas: autosize, Enter to save (capture), Ctrl+Enter to save (edit) ----
  function autosize(el) {
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight + 2, window.innerHeight * 0.6) + "px";
  }
  function refreshSave(form) {
    if (!form) return;
    var ta = form.querySelector("textarea");
    var btn = form.querySelector("[data-requires-text]");
    if (ta && btn) btn.disabled = ta.value.trim() === "";
  }
  function initTextareas(root) {
    root.querySelectorAll("textarea[data-autosize]").forEach(function (el) { autosize(el); });
    root.querySelectorAll("[data-requires-text]").forEach(function (b) { refreshSave(b.form); });
  }
  document.addEventListener("input", function (e) {
    var el = e.target;
    if (!el.matches) return;
    if (el.matches("textarea[data-autosize]")) autosize(el);
    if (el.form) refreshSave(el.form);
  });
  document.addEventListener("keydown", function (e) {
    var el = e.target;
    if (!el.matches || e.key !== "Enter" || e.isComposing) return;
    var plainEnter = !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey;
    var ctrlEnter = e.ctrlKey || e.metaKey;
    if ((el.matches("textarea[data-enter-submit]") && plainEnter) || (el.matches("textarea[data-ctrl-enter]") && ctrlEnter)) {
      e.preventDefault();
      if (el.value.trim() !== "" && el.form) el.form.requestSubmit();
    }
  });
  document.body.addEventListener("htmx:afterRequest", function (e) {
    var f = e.detail.elt;
    if (f && f.id === "capture-form" && e.detail.successful) {
      f.reset();
      var ta = f.querySelector("textarea");
      if (ta) { autosize(ta); ta.focus(); }
      refreshSave(f);
      var empty = document.querySelector("#timeline > .empty");
      if (empty) empty.remove();
    }
  });
  document.body.addEventListener("htmx:afterSwap", function (e) {
    initTextareas(e.detail.target.parentNode || document);
    // Focus a memo that was just switched into edit mode.
    var editing = document.querySelector("form.editing textarea");
    if (editing && document.activeElement !== editing) { editing.focus(); editing.setSelectionRange(editing.value.length, editing.value.length); }
  });

  // ---- editor toolbar: insert #, checkbox, code ----
  document.addEventListener("click", function (e) {
    var b = e.target.closest ? e.target.closest("[data-insert]") : null;
    if (!b) return;
    var form = b.closest("form");
    var ta = form && form.querySelector("textarea");
    if (!ta) return;
    var ins = b.getAttribute("data-insert");
    var s = ta.selectionStart, en = ta.selectionEnd, v = ta.value;
    var text, caret;
    if (b.hasAttribute("data-wrap")) {
      text = ins + v.slice(s, en) + ins;
      caret = en === s ? s + ins.length : s + text.length;
    } else if (b.hasAttribute("data-line")) {
      var atLineStart = s === 0 || v[s - 1] === "\n";
      text = (atLineStart ? "" : "\n") + ins;
      caret = s + text.length;
    } else {
      var needSpace = s > 0 && !/\s/.test(v[s - 1]);
      text = (needSpace ? " " : "") + ins;
      caret = s + text.length;
    }
    ta.value = v.slice(0, s) + text + v.slice(en);
    ta.focus();
    ta.setSelectionRange(caret, caret);
    ta.dispatchEvent(new Event("input", { bubbles: true }));
  });

  // ---- tick Markdown checkboxes inside rendered memos ----
  document.addEventListener("click", function (e) {
    var cb = e.target;
    if (!cb.matches || !cb.matches(".memo-body input[type=checkbox][data-cb]")) return;
    var card = cb.closest("[data-memo]");
    if (!card || !window.htmx) return;
    e.preventDefault();
    htmx.ajax("POST", "/memos/" + card.getAttribute("data-memo") + "/toggle?idx=" + cb.getAttribute("data-cb"),
      { target: "#" + card.id, swap: "outerHTML" });
  });

  // ---- dropdown menus ----
  function closeMenus(except) {
    document.querySelectorAll(".dropdown .menu").forEach(function (m) { if (m !== except) m.hidden = true; });
  }
  document.addEventListener("click", function (e) {
    var btn = e.target.closest ? e.target.closest("[data-toggle-menu]") : null;
    var item = e.target.closest ? e.target.closest(".menu-item") : null;
    if (btn) {
      var menu = btn.nextElementSibling;
      closeMenus(menu);
      menu.hidden = !menu.hidden;
      return;
    }
    if (item || !(e.target.closest && e.target.closest(".menu"))) closeMenus(null);
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") { closeMenus(null); closeSidebar(); }
  });

  // ---- copy buttons ----
  document.addEventListener("click", function (e) {
    var b = e.target.closest ? e.target.closest("[data-copy]") : null;
    if (!b) return;
    var src = document.querySelector(b.getAttribute("data-copy"));
    if (src && navigator.clipboard) navigator.clipboard.writeText(src.textContent.trim()).then(function () { toast("Copied"); });
  });

  // ---- calendar ----
  document.addEventListener("click", function (e) {
    if (e.target.closest && e.target.closest("[data-close-panel]")) {
      var p = document.getElementById("day-panel");
      if (p) p.innerHTML = '<div class="empty small"><p>Select a day to see everything on it.</p></div>';
    }
  });
  document.addEventListener("keydown", function (e) {
    if ((e.key === "Enter" || e.key === " ") && e.target.matches && e.target.matches("[role=button][hx-get]")) {
      e.preventDefault();
      e.target.click();
    }
  });
  // Clicking an item inside a day cell navigates instead of opening the panel.
  document.addEventListener("click", function (e) {
    if (e.target.closest && e.target.closest(".cal-day a, .wv-cell a")) e.stopPropagation();
  }, true);
  // On small screens, bring the day panel into view after it loads.
  document.body.addEventListener("htmx:afterSwap", function (e) {
    if (e.detail.target.id === "day-panel" && window.innerWidth < 1100) e.detail.target.scrollIntoView({ behavior: "smooth", block: "start" });
  });
  var wv = document.getElementById("wv-body");
  var seven = document.getElementById("wv-7");
  if (wv && seven) wv.scrollTop = seven.offsetTop - wv.offsetTop;

  // ---- show/hide form sections ----
  // [data-show="a b"] is visible when the nearest [data-switch] ancestor's
  // [data-switch-source] value (select or checked radio) is one of a, b.
  function sourceValue(container) {
    var srcs = Array.prototype.filter.call(container.querySelectorAll("[data-switch-source]"), function (s) {
      return s.closest("[data-switch]") === container;
    });
    for (var i = 0; i < srcs.length; i++) {
      var s = srcs[i];
      if (s.type === "radio") { if (s.checked) return s.value; } else return s.value;
    }
    return "";
  }
  function applySwitches() {
    document.querySelectorAll("[data-show]").forEach(function (el) {
      var container = el.parentElement && el.parentElement.closest("[data-switch]");
      if (!container) return;
      el.hidden = el.getAttribute("data-show").split(/\s+/).indexOf(sourceValue(container)) < 0;
    });
  }
  document.addEventListener("change", function (e) {
    if (e.target.matches && e.target.matches("[data-switch-source]")) applySwitches();
  });

  applySwitches();
  initTextareas(document);
})();
