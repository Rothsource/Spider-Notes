// Mynotes UI behaviour. No inline scripts (strict CSP), so everything is wired here.
(function () {
  "use strict";

  var csrf = document.querySelector('meta[name="csrf-token"]');
  var token = csrf ? csrf.getAttribute("content") : "";

  // Send the CSRF token with every htmx request.
  document.body.addEventListener("htmx:configRequest", function (e) {
    if (token) e.detail.headers["X-CSRF-Token"] = token;
  });

  // Show server error text for failed htmx requests.
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
    t._timer = setTimeout(function () { t.hidden = true; }, 4000);
  }

  // Theme toggle: light -> dark -> system.
  var themeBtn = document.getElementById("theme-toggle");
  if (themeBtn) {
    themeBtn.addEventListener("click", function () {
      var root = document.documentElement;
      var cur = root.getAttribute("data-theme");
      var next = cur === "light" ? "dark" : cur === "dark" ? "" : "light";
      if (next) root.setAttribute("data-theme", next); else root.removeAttribute("data-theme");
      try { if (next) localStorage.setItem("theme", next); else localStorage.removeItem("theme"); } catch (e) { /* ignore */ }
      toast(next ? "Theme: " + next : "Theme: system");
    });
  }

  // Confirm dialogs for destructive plain forms.
  document.addEventListener("submit", function (e) {
    var f = e.target;
    if (f.matches && f.matches("form[data-confirm]") && !window.confirm(f.getAttribute("data-confirm"))) {
      e.preventDefault();
    }
  });

  // Memo capture: Enter submits, Shift+Enter inserts a newline.
  document.addEventListener("keydown", function (e) {
    var el = e.target;
    if (!el.matches || !el.matches("textarea[data-enter-submit]")) return;
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing && !e.ctrlKey && !e.altKey && !e.metaKey) {
      e.preventDefault();
      if (el.value.trim() !== "" && el.form) el.form.requestSubmit();
    }
  });
  // Clear the capture box after a successful save.
  document.body.addEventListener("htmx:afterRequest", function (e) {
    var f = e.detail.elt;
    if (f && f.id === "capture-form" && e.detail.successful) {
      f.reset();
      var ta = f.querySelector("textarea");
      if (ta) ta.focus();
      var empty = document.querySelector("#timeline > .empty");
      if (empty) empty.remove();
    }
  });

  // Tick Markdown checkboxes inside rendered memos.
  document.addEventListener("click", function (e) {
    var cb = e.target;
    if (!cb.matches || !cb.matches(".memo-body input[type=checkbox][data-cb]")) return;
    var card = cb.closest("[data-memo]");
    if (!card || !window.htmx) return;
    e.preventDefault();
    htmx.ajax("POST", "/memos/" + card.getAttribute("data-memo") + "/toggle?idx=" + cb.getAttribute("data-cb"),
      { target: "#" + card.id, swap: "outerHTML" });
  });

  // Snooze dropdown menus.
  document.addEventListener("click", function (e) {
    var btn = e.target.closest ? e.target.closest("[data-toggle-menu]") : null;
    document.querySelectorAll(".dropdown .menu").forEach(function (m) {
      if (!btn || m !== btn.nextElementSibling) m.hidden = true;
    });
    if (btn) btn.nextElementSibling.hidden = !btn.nextElementSibling.hidden;
  });

  // Copy-to-clipboard buttons.
  document.addEventListener("click", function (e) {
    var b = e.target.closest ? e.target.closest("[data-copy]") : null;
    if (!b) return;
    var src = document.querySelector(b.getAttribute("data-copy"));
    if (src && navigator.clipboard) navigator.clipboard.writeText(src.textContent.trim()).then(function () { toast("Copied"); });
  });

  // Calendar side panel close + keyboard activation of day cells.
  document.addEventListener("click", function (e) {
    if (e.target.closest && e.target.closest("[data-close-panel]")) {
      var p = document.getElementById("day-panel");
      if (p) p.innerHTML = '<p class="muted">Click a day to see everything on it.</p>';
    }
  });
  document.addEventListener("keydown", function (e) {
    if ((e.key === "Enter" || e.key === " ") && e.target.matches && e.target.matches("[role=button][hx-get]")) {
      e.preventDefault();
      e.target.click();
    }
  });
  // Clicking an item inside a day cell should navigate, not open the panel.
  document.addEventListener("click", function (e) {
    if (e.target.closest && e.target.closest(".cal-day a, .wv-cell a")) e.stopPropagation();
  }, true);

  // Week view: start scrolled to 07:00.
  var wv = document.getElementById("wv-body");
  var seven = document.getElementById("wv-7");
  if (wv && seven) wv.scrollTop = seven.offsetTop - wv.offsetTop;

  // Show/hide form sections: [data-show="a b"] is visible when the nearest
  // [data-switch] container's [data-switch-source] value is one of a, b.
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
      var allowed = el.getAttribute("data-show").split(/\s+/);
      el.hidden = allowed.indexOf(sourceValue(container)) < 0;
    });
  }
  document.addEventListener("change", function (e) {
    if (e.target.matches && e.target.matches("[data-switch-source]")) applySwitches();
  });
  applySwitches();
})();
