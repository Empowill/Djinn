// The Grimoire: its navigation on the left from the first view, and beside it the splash page or the page the address
// names, Introduction when it names none of them; each page filled from its script when first opened, the others'
// scripts loaded in idle time; the navigation's words in the reader's language; the theme; the splash's chapters
// revealed as they come into view; and the search of the command line, which only hides what does not match: the
// reference itself is plain HTML.
(function () {
  "use strict";
  var root = document.documentElement;
  root.classList.add("js");

  var welcome = document.getElementById("welcome");
  var pages = Array.prototype.slice.call(document.querySelectorAll(".page"));
  var nav = document.querySelector(".book-nav");
  var links = Array.prototype.slice.call(nav.querySelectorAll("a"));
  var commands = document.getElementById("commands");
  var introduction = document.getElementById("introduction");

  // The words of the navigation and of the site's own chrome, in the language the window passes (?lang=), else the
  // browser's; the pages stay in English. The page as written is English, and stays so when the labels are missing.
  var language = "en";
  var texts = null;
  (function () {
    var labels;
    try {
      labels = JSON.parse(document.getElementById("labels").textContent);
    } catch (e) {
      return;
    }
    var asked = [new URLSearchParams(location.search).get("lang")].concat(navigator.languages || [navigator.language]);
    for (var i = 0; i < asked.length; i++) {
      var base = String(asked[i] || "")
        .toLowerCase()
        .split(/[-_]/)[0];
      if (labels[base]) {
        language = base;
        break;
      }
    }
    texts = labels[language] || null;
  })();

  // translate puts the reader's words in what root holds: the page at start, a page when it is filled.
  function translate(root) {
    if (language === "en" || !texts) return;
    if (root === document) {
      var waking = document.getElementById("djinn-splash-text");
      if (waking && texts["app.waking"]) {
        waking.textContent = texts["app.waking"];
        waking.lang = language;
      }
    }
    Array.prototype.forEach.call(root.querySelectorAll("[data-text]"), function (e) {
      var text = texts[e.dataset.text];
      if (!text) return;
      e.textContent = text;
      e.lang = language;
    });
    Array.prototype.forEach.call(root.querySelectorAll("[data-title]"), function (e) {
      if (texts[e.dataset.title]) e.title = texts[e.dataset.title];
    });
    Array.prototype.forEach.call(root.querySelectorAll("[data-label]"), function (e) {
      if (texts[e.dataset.label]) e.setAttribute("aria-label", texts[e.dataset.label]);
    });
  }

  // hideSplash removes the waking splash and its style sheet once the initial page's content is ready.
  function hideSplash() {
    var splash = document.getElementById("djinn-splash");
    if (splash) splash.remove();
    var style = document.getElementById("djinn-splash-style");
    if (style) style.remove();
  }

  function paint() {
    var theme = root.dataset.theme === "light" ? "light" : "dark";
    var toggle = document.querySelector(".theme-toggle");
    toggle.setAttribute("aria-label", theme === "dark" ? "Switch to the light theme" : "Switch to the dark theme");
  }

  // The pages' HTML their scripts handed over, by slug, until each is put in its shell; the scripts asked for; the page
  // shown, which its script fills as it arrives.
  var arrived = {};
  var asked = {};
  var shown = null;

  // A page's script, pages/<slug>.js, calls this with the page's HTML.
  window.grimoire = {
    page: function (slug, html) {
      arrived[slug] = html;
      if (shown && shown.id === slug) route();
    },
  };

  // load adds a page's script, once; done runs when it has loaded or failed. A failed one is asked again next time.
  function load(slug, done) {
    if (asked[slug]) return;
    asked[slug] = true;
    var script = document.createElement("script");
    script.src = "pages/" + slug + ".js";
    script.onload = done || null;
    script.onerror = function () {
      delete asked[slug];
      script.remove();
      if (shown && shown.id === slug) hideSplash();
      if (done) done();
    };
    document.head.appendChild(script);
  }

  // slot is where a page takes its HTML, while it is still empty: the page itself, or the Command line's reference.
  function slot(page) {
    return page.hasAttribute("data-slot") ? page : page.querySelector("[data-slot]");
  }

  // fill puts a page's HTML in its shell, or asks for its script: true once the page is there.
  function fill(page) {
    var into = slot(page);
    if (!into) return true;
    if (arrived[page.id] === undefined) {
      load(page.id);
      return false;
    }
    into.innerHTML = arrived[page.id];
    delete arrived[page.id];
    into.removeAttribute("data-slot");
    translate(into);
    if (page === commands) filter();
    return true;
  }

  // owner is the page an id belongs to when the page is not filled yet: a section's id starts with its page's slug
  // (wish-paused-wishes), the longest slug that fits (push-strategy-… is Push strategy's, not Push's); a command's, a
  // group's and the global flags' are the Command line's. internal/docsite checks that every id reads so.
  function owner(id) {
    if (/^(cmd|group)-/.test(id) || id === "global-flags") return commands;
    var best = null;
    pages.forEach(function (p) {
      if (id.indexOf(p.id + "-") === 0 && (!best || p.id.length > best.id.length)) best = p;
    });
    return best;
  }

  // A hash names the splash or a place in it (#lamp), a page of the book (#wish), or a place in a page: a section
  // (#wish-paused-wishes), a command (#cmd-wish-make). No hash is the splash; a hash nothing has is Introduction. A
  // page not filled yet shows empty until its script arrives, then the place is reached.
  function route() {
    var id = decodeURIComponent(location.hash.slice(1));
    var target = id ? document.getElementById(id) : welcome;
    var page = target ? (welcome.contains(target) ? null : target.closest(".page")) : owner(id);
    if (!target && !page) target = page = introduction;
    shown = page;
    var filled = !page || fill(page);
    if (page && (!target || !page.contains(target))) target = filled ? document.getElementById(id) || page : page;
    welcome.hidden = !!page;
    root.classList.toggle("reading", !!page);
    pages.forEach(function (p) {
      p.hidden = p !== page;
    });
    var href = "#" + (page ? page.id : "welcome");
    links.forEach(function (a) {
      if (a.getAttribute("href") === href) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
    // A command is reached at once: the reference is long, and nothing in it moves.
    if (target !== welcome && target !== page) {
      target.scrollIntoView(page === commands ? { behavior: "instant" } : undefined);
    } else window.scrollTo({ top: 0, behavior: "instant" });
    // The navigation shows its current entry where it scrolls by itself, beside the page; where it does not, under the
    // splash on a narrow screen, the page stays where it is.
    var current = nav.querySelector('a[aria-current="page"]');
    if (current && nav.scrollHeight > nav.clientHeight) current.scrollIntoView({ block: "nearest" });
    if (filled) hideSplash();
  }

  // Once the first view has loaded, the other pages' scripts load in idle time, one after the other: a page opened
  // later shows at once. Their HTML waits as text; only the page opened is put in the document.
  // The smoke's animation may leave no idle time at all: a second at most between two scripts.
  function idle(f) {
    if (window.requestIdleCallback) window.requestIdleCallback(f, { timeout: 1000 });
    else setTimeout(f, 50);
  }
  var tried = {};
  function preload() {
    var next = pages.filter(function (p) {
      return slot(p) && !asked[p.id] && !tried[p.id] && arrived[p.id] === undefined;
    })[0];
    if (!next) return;
    tried[next.id] = true;
    load(next.id, function () {
      idle(preload);
    });
  }

  // The search keeps the commands whose text holds every word typed, and the groups that keep one.
  var search = document.querySelector(".cli-search input");
  function filter() {
    if (slot(commands)) return;
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
  translate(document);
  paint();
  route();
  if (document.readyState === "complete") idle(preload);
  else
    window.addEventListener("load", function () {
      idle(preload);
    });
})();
