// The two tabs (#concepts, #api), the theme, the chapters revealed as they come into view, and the API reference:
// RapiDoc on window.DJINN_OPENAPI, which openapi.js sets.
(function () {
  "use strict";
  var root = document.documentElement;
  root.classList.add("js");

  var tabs = Array.prototype.slice.call(document.querySelectorAll('[role="tab"]'));
  var ink = document.querySelector(".tab-ink");
  var reference = document.getElementById("reference");
  var loaded = false;

  // The colors of the reference, from the site's palette (site.css).
  var palettes = {
    dark: {
      "bg-color": "#0b1226",
      "text-color": "#dfe3f2",
      "primary-color": "#deb062",
      "nav-bg-color": "#070b1a",
      "nav-text-color": "#b4bbd6",
      "nav-hover-bg-color": "#121b3a",
      "nav-hover-text-color": "#f6dfa4",
      "nav-accent-color": "#deb062",
      "nav-accent-text-color": "#1a1305",
    },
    light: {
      "bg-color": "#fffaf0",
      "text-color": "#18203d",
      "primary-color": "#8a6420",
      "nav-bg-color": "#f2e9d4",
      "nav-text-color": "#3d4668",
      "nav-hover-bg-color": "#e9dcc0",
      "nav-hover-text-color": "#18203d",
      "nav-accent-color": "#a67a2e",
      "nav-accent-text-color": "#fffaf0",
    },
  };

  function paint() {
    var theme = root.dataset.theme === "light" ? "light" : "dark";
    var palette = palettes[theme];
    reference.setAttribute("theme", theme);
    Object.keys(palette).forEach(function (name) {
      reference.setAttribute(name, palette[name]);
    });
    var toggle = document.querySelector(".theme-toggle");
    toggle.setAttribute("aria-label", theme === "dark" ? "Switch to the light theme" : "Switch to the dark theme");
  }

  // RapiDoc 10.1 splits a const as a string: a number, as Connect-Protocol-Version's 1, breaks its rendering. The
  // same value, as an enum of one, reads the same.
  function constants(node) {
    if (Array.isArray(node)) {
      node.forEach(constants);
    } else if (node && typeof node === "object") {
      if ("const" in node && typeof node.const !== "string") {
        node.enum = [node.const];
        delete node.const;
      }
      Object.keys(node).forEach(function (key) {
        constants(node[key]);
      });
    }
    return node;
  }

  function load() {
    if (loaded) return;
    loaded = true;
    if (!window.DJINN_OPENAPI || !customElements.get("rapi-doc")) {
      document.querySelector(".api-missing").hidden = false;
      reference.hidden = true;
      return;
    }
    // Served by Djinn, the console calls the Djinn that serves the page; opened from a file, it reads only.
    if (location.protocol === "http:" || location.protocol === "https:") {
      reference.setAttribute("server-url", location.origin);
      reference.setAttribute("default-api-server", location.origin);
      reference.setAttribute("allow-try", "true");
    }
    reference.loadSpec(constants(window.DJINN_OPENAPI));
  }

  function place() {
    var current = tabs.filter(function (t) {
      return t.getAttribute("aria-selected") === "true";
    })[0];
    if (!current || !ink) return;
    ink.style.setProperty("--ink-left", current.offsetLeft + "px");
    ink.style.setProperty("--ink-width", current.offsetWidth + "px");
  }

  function show(name, focus) {
    tabs.forEach(function (tab) {
      var on = tab.getAttribute("aria-controls") === name;
      tab.setAttribute("aria-selected", on ? "true" : "false");
      tab.tabIndex = on ? 0 : -1;
      document.getElementById(tab.getAttribute("aria-controls")).hidden = !on;
      if (on && focus) tab.focus();
    });
    place();
    if (name === "api") load();
  }

  // A hash names a tab, or a place in the concepts.
  function route() {
    var id = location.hash.slice(1);
    if (id === "api") {
      show("api");
      window.scrollTo(0, 0);
      return;
    }
    show("concepts");
    var target = id && document.getElementById(id);
    if (target && target !== document.getElementById("concepts")) target.scrollIntoView();
    else if (id === "concepts") window.scrollTo(0, 0);
  }

  tabs.forEach(function (tab, i) {
    tab.addEventListener("click", function () {
      var name = tab.getAttribute("aria-controls");
      if (location.hash !== "#" + name) location.hash = name;
      else route();
    });
    tab.addEventListener("keydown", function (e) {
      var step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
      if (!step) return;
      e.preventDefault();
      var next = tabs[(i + step + tabs.length) % tabs.length];
      location.hash = next.getAttribute("aria-controls");
      next.focus();
    });
  });

  document.querySelector(".theme-toggle").addEventListener("click", function () {
    var next = root.dataset.theme === "light" ? "dark" : "light";
    root.dataset.theme = next;
    try {
      localStorage.setItem("djinn.docs.theme", next);
    } catch (e) {
      // No storage: the choice lasts as long as the page.
    }
    paint();
  });

  // The chapters rise into view once; all at once where the system asks for less motion.
  var chapters = document.querySelectorAll(".reveal");
  if ("IntersectionObserver" in window && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
    var watch = new IntersectionObserver(
      function (entries) {
        entries.forEach(function (entry) {
          if (!entry.isIntersecting) return;
          entry.target.classList.add("seen");
          watch.unobserve(entry.target);
        });
      },
      { rootMargin: "0px 0px -12% 0px" },
    );
    Array.prototype.forEach.call(chapters, function (c) {
      watch.observe(c);
    });
  } else {
    Array.prototype.forEach.call(chapters, function (c) {
      c.classList.add("seen");
    });
  }

  window.addEventListener("hashchange", route);
  window.addEventListener("resize", place);
  if (document.fonts) document.fonts.ready.then(place);
  paint();
  route();
})();
