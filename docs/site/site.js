// The Grimoire: the splash page or the book, whichever the address names; in the book, the page it names, Introduction
// when it names none of them; the navigation's words in the reader's language; the theme; the splash's chapters
// revealed as they come into view; and the search of the command line, which only hides what does not match: the
// reference itself is plain HTML.
(function () {
  "use strict";
  var root = document.documentElement;
  root.classList.add("js");

  var welcome = document.getElementById("welcome");
  var book = document.querySelector(".book");
  var pages = Array.prototype.slice.call(document.querySelectorAll(".page"));
  var links = Array.prototype.slice.call(document.querySelectorAll(".book-nav a"));
  var commands = document.getElementById("commands");

  // The words of the navigation and of the site's own chrome, in the language the window passes (?lang=), else the
  // browser's; the pages stay in English. The page as written is English, and stays so when the labels are missing.
  function translate() {
    var labels;
    try {
      labels = JSON.parse(document.getElementById("labels").textContent);
    } catch (e) {
      return;
    }
    var asked = [new URLSearchParams(location.search).get("lang")].concat(navigator.languages || [navigator.language]);
    var language = "en";
    for (var i = 0; i < asked.length; i++) {
      var base = String(asked[i] || "")
        .toLowerCase()
        .split(/[-_]/)[0];
      if (labels[base]) {
        language = base;
        break;
      }
    }
    var texts = labels[language];
    if (language === "en" || !texts) return;
    Array.prototype.forEach.call(document.querySelectorAll("[data-text]"), function (e) {
      var text = texts[e.dataset.text];
      if (!text) return;
      e.textContent = text;
      e.lang = language;
    });
    Array.prototype.forEach.call(document.querySelectorAll("[data-title]"), function (e) {
      if (texts[e.dataset.title]) e.title = texts[e.dataset.title];
    });
    Array.prototype.forEach.call(document.querySelectorAll("[data-label]"), function (e) {
      if (texts[e.dataset.label]) e.setAttribute("aria-label", texts[e.dataset.label]);
    });
  }

  function paint() {
    var theme = root.dataset.theme === "light" ? "light" : "dark";
    var toggle = document.querySelector(".theme-toggle");
    toggle.setAttribute("aria-label", theme === "dark" ? "Switch to the light theme" : "Switch to the dark theme");
  }

  // A hash names the splash or a place in it (#lamp), a page of the book (#wish), or a place in a page: a section
  // (#wish-paused-wishes), a command (#cmd-wish-make). No hash is the splash; a hash nothing has is Introduction.
  function route() {
    var id = decodeURIComponent(location.hash.slice(1));
    var target = id ? document.getElementById(id) : welcome;
    if (!target) target = document.getElementById("introduction");
    var page = welcome.contains(target) ? null : target.closest(".page");
    welcome.hidden = !!page;
    book.hidden = !page;
    root.classList.toggle("reading", !!page);
    pages.forEach(function (p) {
      p.hidden = p !== page;
    });
    links.forEach(function (a) {
      if (page && a.getAttribute("href") === "#" + page.id) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
    // A command is reached at once: the reference is long, and nothing in it moves.
    if (target !== welcome && target !== page) {
      target.scrollIntoView(page === commands ? { behavior: "instant" } : undefined);
    } else window.scrollTo({ top: 0, behavior: "instant" });
    if (page) {
      var current = book.querySelector('.book-nav a[aria-current="page"]');
      if (current) current.scrollIntoView({ block: "nearest" });
    }
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

  // A link to the place the address already names moves nowhere by itself: route again.
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest('a[href^="#"]');
    if (a && a.getAttribute("href") === location.hash) {
      e.preventDefault();
      route();
    }
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
  translate();
  paint();
  route();
})();
