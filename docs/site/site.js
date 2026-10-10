// The two tabs (#concepts, #commands), the theme, the chapters revealed as they come into view, and the search of the
// command line, which only hides what does not match: the reference itself is plain HTML.
(function () {
  "use strict";
  var root = document.documentElement;
  root.classList.add("js");

  var tabs = Array.prototype.slice.call(document.querySelectorAll('[role="tab"]'));
  var ink = document.querySelector(".tab-ink");
  var commands = document.getElementById("commands");

  function paint() {
    var theme = root.dataset.theme === "light" ? "light" : "dark";
    var toggle = document.querySelector(".theme-toggle");
    toggle.setAttribute("aria-label", theme === "dark" ? "Switch to the light theme" : "Switch to the dark theme");
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
  }

  // A hash names a tab, or a place in one: a chapter of the concepts, a command (#cmd-wish-make).
  function route() {
    var id = location.hash.slice(1);
    var target = id && document.getElementById(id);
    var name = target && commands.contains(target) ? "commands" : "concepts";
    show(name);
    // A command is reached at once: the reference is long, and nothing in it moves.
    if (target && target.getAttribute("role") !== "tabpanel") {
      target.scrollIntoView(name === "commands" ? { behavior: "instant" } : undefined);
    } else window.scrollTo(0, 0);
  }

  // The search keeps the commands whose text holds every word typed, and the groups that keep one.
  var search = document.querySelector(".cli-search input");
  function filter() {
    var words = search.value.toLowerCase().split(/\s+/).filter(Boolean);
    var shown = 0;
    Array.prototype.forEach.call(commands.querySelectorAll(".cli-group[data-group]"), function (group) {
      var kept = 0;
      Array.prototype.forEach.call(group.querySelectorAll(".cli-cmd"), function (cmd) {
        var text = (group.dataset.group + " " + cmd.textContent).toLowerCase();
        var on = words.every(function (w) {
          return text.indexOf(w) >= 0;
        });
        cmd.hidden = !on;
        var entry = commands.querySelector('.cli-toc li[data-command="' + cmd.dataset.command + '"]');
        if (entry) entry.hidden = !on;
        if (on) kept++;
      });
      group.hidden = kept === 0;
      var toc = commands.querySelector('.cli-toc-group[data-group="' + group.id.replace(/^group-/, "") + '"]');
      if (toc) toc.hidden = kept === 0;
      shown += kept;
    });
    commands.querySelector("#global-flags").hidden = words.length > 0;
    commands.querySelector(".cli-toc-global").hidden = words.length > 0;
    var none = commands.querySelector(".cli-none");
    if (none) none.hidden = shown > 0;
  }
  if (search) search.addEventListener("input", filter);

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
