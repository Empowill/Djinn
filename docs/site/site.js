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

  // Concept icons from wishicons/bank.json and src/status.tsx (two-tone line + gilding, offline SVG shapes, no id).
  var ICONS = {
    "book-open":
      '<g class="wish-icon-line"><path d="M12 6c-2-1.5-5-2-9-1.5V19c4-.5 7 0 9 1.5 2-1.5 5-2 9-1.5V4.5C17 4 14 4.5 12 6ZM12 6v14.5"/></g><g class="wish-icon-gild"><path d="M15.5 5.2h2.4v6.3l-1.2-1.2-1.2 1.2Z" fill="currentColor" stroke="none"/></g>',
    compass:
      '<g class="wish-icon-line"><circle cx="12" cy="12" r="9.5"/><g stroke-width="1"><path d="M13.4 13.4 8.5 15.5 10.6 10.6M12 2.5V4.5M21.5 12h-2M12 21.5v-2M2.5 12h2"/></g></g><g class="wish-icon-gild"><path d="M15.5 8.5 13.4 13.4 10.6 10.6Z" fill="currentColor" stroke="none"/></g>',
    star:
      '<g class="wish-icon-line"><path d="M12 2.5L13.82 7.61L18.72 5.28L16.39 10.18L21.5 12L16.39 13.82L18.72 18.72L13.82 16.39L12 21.5L10.18 16.39L5.28 18.72L7.61 13.82L2.5 12L7.61 10.18L5.28 5.28L10.18 7.61Z"/></g><g class="wish-icon-gild"><circle cx="12" cy="12" r="1.8" fill="currentColor" stroke="none"/></g>',
    target:
      '<g class="wish-icon-line"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="5.5"/></g><g class="wish-icon-gild"><circle cx="12" cy="12" r="2.2" fill="currentColor" stroke="none"/></g>',
    hammer:
      '<g class="wish-icon-line"><g transform="rotate(45 12 12)"><path d="M6.5 3.5H17.5L19.5 5.5V7L17.5 9H6.5L4.5 7V5.5Z"/><path d="M12 9V21.5"/></g></g><g class="wish-icon-gild"><path d="M3 8.2l2.4.3-.3.8Z" fill="currentColor" stroke="none"/><path d="M4.6 4.4l1.9 1.6-.6.6Z" fill="currentColor" stroke="none"/><path d="M8.4 3l.6 2.3-.8.2Z" fill="currentColor" stroke="none"/></g>',
    link:
      '<g class="wish-icon-line"><g transform="rotate(-45 12 12)"><rect x="2.5" y="9" width="10" height="6" rx="3"/><rect x="11.5" y="9" width="10" height="6" rx="3"/></g></g><g class="wish-icon-gild"><g transform="rotate(-45 12 12)"><path d="M12 10.6L13.4 12L12 13.4L10.6 12Z" fill="currentColor" stroke="none"/></g></g>',
    glasses:
      '<g class="wish-icon-line"><circle cx="6.5" cy="15" r="3.5"/><circle cx="17.5" cy="15" r="3.5"/><path d="M10.17 16V16A2 2 0 1 1 13.83 16V16"/><path d="M3 15 5 6M21 15 19 6"/></g><g class="wish-icon-gild"><path d="M4.6 14.2 6.4 12.4 7 13 5.2 14.8Z" fill="currentColor" stroke="none"/><path d="M15.6 14.2l1.8-1.8.6.6-1.8 1.8Z" fill="currentColor" stroke="none"/></g>',
    palette:
      '<g class="wish-icon-line"><path d="M12 21A9 9 0 1 1 21 12a3 3 0 0 1-3 3h-2a2 2 0 0 0-1.6 3.2A1.7 1.7 0 0 1 12 21Z"/><g stroke-width="1"><path d="M5.2 9.5A7.4 7.4 0 0 1 9 5.2"/></g></g><g class="wish-icon-gild"><circle cx="7.5" cy="11" r="1.5" fill="currentColor" stroke="none"/><circle cx="10.5" cy="7" r="1.5" fill="currentColor" stroke="none"/><circle cx="15.5" cy="8" r="1.5" fill="currentColor" stroke="none"/></g>',
    scale:
      '<g class="wish-icon-line"><path d="M12 4v16M8 20.5h8M4 7h16"/><path d="M4 7l-2.5 6.5a2.5 2.5 0 0 0 5 0ZM20 7l-2.5 6.5a2.5 2.5 0 0 0 5 0Z"/></g><g class="wish-icon-gild"><circle cx="12" cy="4" r="1.4" fill="currentColor" stroke="none"/></g>',
    smoke:
      '<g class="wish-icon-line"><path d="M8 21c0-3 3-3 3-6s-3-3-3-6 3-3 3-6"/><path d="M14 21c0-2.5 2.5-2.5 2.5-5S14 13.5 14 11"/></g><g class="wish-icon-gild"><circle cx="16" cy="5" r="1.4" fill="currentColor" stroke="none"/><circle cx="18.5" cy="8" r="1" fill="currentColor" stroke="none"/></g>',
    boxes:
      '<g class="wish-icon-line"><path d="M12 2.4L15.98 4.7V9.3L12 11.6L8.02 9.3V4.7ZM8.02 4.7L12 7L15.98 4.7M12 7V11.6"/><path d="M7.6 11L11.58 13.3V17.9L7.6 20.2L3.62 17.9V13.3ZM3.62 13.3L7.6 15.6L11.58 13.3M7.6 15.6V20.2"/><path d="M16.4 11L20.38 13.3V17.9L16.4 20.2L12.42 17.9V13.3ZM12.42 13.3L16.4 15.6L20.38 13.3M16.4 15.6V20.2"/></g><g class="wish-icon-gild"><path d="M12 3.6L13.1 4.7L12 5.8L10.9 4.7Z" fill="currentColor" stroke="none"/><path d="M7.6 12.2L8.7 13.3L7.6 14.4L6.5 13.3Z" fill="currentColor" stroke="none"/><path d="M16.4 12.2L17.5 13.3L16.4 14.4L15.3 13.3Z" fill="currentColor" stroke="none"/></g>',
    sprout:
      '<g class="wish-icon-line"><path d="M12 21.5V12"/><path d="M8.5 6A5.67 5.67 0 0 1 11.5 11A3 3 0 0 1 5.5 11A5.67 5.67 0 0 1 8.5 6Z" transform="rotate(-45 8.5 11)"/><path d="M15.5 4.5A5.67 5.67 0 0 1 18.5 9.5A3 3 0 0 1 12.5 9.5A5.67 5.67 0 0 1 15.5 4.5Z" transform="rotate(45 15.5 9.5)"/></g><g class="wish-icon-gild"><path d="M15.5 5.5A5 5 0 0 1 17.5 9.5A2 2 0 0 1 13.5 9.5A5 5 0 0 1 15.5 5.5Z" fill="currentColor" stroke="none" transform="rotate(45 15.5 9.5)"/></g>',
    rocket:
      '<g class="wish-icon-line"><g transform="rotate(45 12 12)"><path d="M12 2A11 11 0 0 1 16 10.5V16.5H8V10.5A11 11 0 0 1 12 2Z"/><path d="M8 12.5 5 16v3h3M16 12.5l3 3.5v3h-3"/><circle cx="12" cy="9" r="1.6"/></g></g><g class="wish-icon-gild"><g transform="rotate(45 12 12)"><circle cx="12" cy="19.4" r="1.3" fill="currentColor" stroke="none"/><circle cx="12" cy="22.1" r="0.9" fill="currentColor" stroke="none"/></g></g>',
    map:
      '<g class="wish-icon-line"><path d="M3 6 9 3l6 3 6-3v15l-6 3-6-3-6 3Z"/><g stroke-width="1"><path d="M9 3v15M15 6v15"/></g></g><g class="wish-icon-gild"><circle cx="6" cy="11" r="0.9" fill="currentColor" stroke="none"/><circle cx="12" cy="13" r="0.9" fill="currentColor" stroke="none"/><path d="M18 7.5L19.5 9L18 10.5L16.5 9Z" fill="currentColor" stroke="none"/></g>',
    arch:
      '<g class="wish-icon-line"><path d="M5.13 21V13A7.5 7.5 0 1 1 18.87 13V21"/><path d="M3 21h18"/><g stroke-width="1"><path d="M7.64 21V13A4.8 4.8 0 1 1 16.36 13V21"/></g></g><g class="wish-icon-gild"><path d="M12 1.2L13.4 2.6L12 4L10.6 2.6Z" fill="currentColor" stroke="none"/></g>',
    plug:
      '<g class="wish-icon-line"><path d="M9 2.5V7M15 2.5V7"/><path d="M6 7h12v4l-4 4h-4l-4-4Z"/><g stroke-width="1"><path d="M12 15v1.5a2 2 0 1 0 2 2h1.5a2 2 0 0 1 2 2V22"/></g></g><g class="wish-icon-gild"><path d="M12 9L13.5 10.5L12 12L10.5 10.5Z" fill="currentColor" stroke="none"/></g>',
    lamp:
      '<g class="wish-icon-line"><path d="M4 12h10l7-3.5-4 5.5A6.5 4.5 0 0 1 4 12Z"/><path d="M10 7A3.88 3.88 0 0 1 12.2 10.5A2.2 2.2 0 0 1 7.8 10.5A3.88 3.88 0 0 1 10 7Z"/><path d="M4.5 13.5a2.3 2.3 0 0 1-.8-4.3"/><path d="M10.5 16.5v3.5M7.5 20.5h6"/><g stroke-width="1"><path d="M6.5 14.2h7"/></g></g><g class="wish-icon-gild"><path d="M21 3.6A4.35 4.35 0 0 1 22.2 6.6A1.2 1.2 0 0 1 19.8 6.6A4.35 4.35 0 0 1 21 3.6Z" fill="currentColor" stroke="none"/><circle cx="18.5" cy="2" r="0.8" fill="currentColor" stroke="none"/></g>',
    "messages-square":
      '<g class="wish-icon-line"><path d="M6.5 3.5h6.5l2.5 2.5v5l-2.5 2.5H8L4 17V6Z"/><g stroke-width="1"><path d="M18.5 8.5h1L21 10v11l-3.5-3h-6l-1.5-1.5V16"/></g></g><g class="wish-icon-gild"><circle cx="7.5" cy="8.5" r="0.9" fill="currentColor" stroke="none"/><circle cx="9.8" cy="8.5" r="0.9" fill="currentColor" stroke="none"/><circle cx="12.1" cy="8.5" r="0.9" fill="currentColor" stroke="none"/></g>',
    lantern:
      '<g class="wish-icon-line"><path d="M9 4.5h6l2 3H7ZM12 4.5V2"/><path d="M7.5 7.5v10L9 19h6l1.5-1.5v-10M10 21.5h4M12 19v2.5"/><g stroke-width="1"><path d="M9.5 7.5v10M14.5 7.5v10"/></g></g><g class="wish-icon-gild"><path d="M12 10A7.25 7.25 0 0 1 14 15A2 2 0 0 1 10 15A7.25 7.25 0 0 1 12 10Z" fill="currentColor" stroke="none"/></g>',
    seal:
      '<g class="wish-icon-line"><g transform="rotate(0 12 12)"><path d="M5.28 5.28H7.92M10.52 5.28H18.72M12 2.5L13.86 4.36M15.7 6.2L21.5 12"/></g><g transform="rotate(90 12 12)"><path d="M5.28 5.28H7.92M10.52 5.28H18.72M12 2.5L13.86 4.36M15.7 6.2L21.5 12"/></g><g transform="rotate(180 12 12)"><path d="M5.28 5.28H7.92M10.52 5.28H18.72M12 2.5L13.86 4.36M15.7 6.2L21.5 12"/></g><g transform="rotate(270 12 12)"><path d="M5.28 5.28H7.92M10.52 5.28H18.72M12 2.5L13.86 4.36M15.7 6.2L21.5 12"/></g></g><g class="wish-icon-gild"><path d="M12 7.6A10.46 10.46 0 0 1 14.6 14.5A2.6 2.6 0 0 1 9.4 14.5A10.46 10.46 0 0 1 12 7.6Z" fill="currentColor" stroke="none"/></g>',
    notebook:
      '<g class="wish-icon-line"><path d="M7.5 3H16.5L19 5.5V18.5L16.5 21H7.5L5 18.5V5.5Z"/><path d="M8.5 3v18"/><g stroke-width="1"><path d="M11.5 8h4.5M11.5 11h3.5"/></g></g><g class="wish-icon-gild"><circle cx="5" cy="7" r="0.9" fill="currentColor" stroke="none"/><circle cx="5" cy="12" r="0.9" fill="currentColor" stroke="none"/><circle cx="5" cy="17" r="0.9" fill="currentColor" stroke="none"/></g>',
    scroll:
      '<g class="wish-icon-line"><path d="M7 4h11a2 2 0 0 1 2 2v1.5h-4M7 4a2 2 0 0 0-2 2v11h11v1a2 2 0 0 0 2 2H7a2 2 0 0 1-2-2V17M16 7.5V18a2 2 0 0 0 2 2"/><g stroke-width="1"><path d="M8 8h5M8 10.5h4"/></g></g><g class="wish-icon-gild"><circle cx="12.5" cy="13.5" r="1.6" fill="currentColor" stroke="none"/></g>',
    "app-window":
      '<g class="wish-icon-line"><path d="M5 4H19L21.5 6.5V17.5L19 20H5L2.5 17.5V6.5Z"/><path d="M2.5 8.5h19"/><g stroke-width="1"><path d="M6 12.5h8M6 15.5h5"/></g></g><g class="wish-icon-gild"><circle cx="5.2" cy="6.25" r="0.8" fill="currentColor" stroke="none"/><circle cx="7.6" cy="6.25" r="0.8" fill="currentColor" stroke="none"/><circle cx="10" cy="6.25" r="0.8" fill="currentColor" stroke="none"/></g>',
    envelope:
      '<g class="wish-icon-line"><path d="M5 5H19L21.5 7.5V16.5L19 19H5L2.5 16.5V7.5Z"/><path d="M2.5 7.5l9.5 6.5 9.5-6.5"/></g><g class="wish-icon-gild"><circle cx="12" cy="14" r="1.7" fill="currentColor" stroke="none"/></g>',
    scan:
      '<g class="wish-icon-line"><path d="M3 8V5l2-2h3M16 3h3l2 2v3M21 16v3l-2 2h-3M8 21H5l-2-2v-3"/><g stroke-width="1"><path d="M3 12h4.5M16.5 12H21"/></g></g><g class="wish-icon-gild"><path d="M12 8.8L15.2 12L12 15.2L8.8 12Z" fill="currentColor" stroke="none"/></g>',
    shield:
      '<g class="wish-icon-line"><path d="M12 2.5 20 5.5V11A10 10 0 0 1 12 21.5 10 10 0 0 1 4 11V5.5Z"/><g stroke-width="1"><path d="M12 5 17.5 7v4A7.5 7.5 0 0 1 12 18.5 7.5 7.5 0 0 1 6.5 11V7Z"/></g></g><g class="wish-icon-gild"><path d="M12 8.3L12.9 10.6L15.2 11.5L12.9 12.4L12 14.7L11.1 12.4L8.8 11.5L11.1 10.6Z" fill="currentColor" stroke="none"/></g>',
    gauge:
      '<g class="wish-icon-line"><path d="M3.5 17a9 9 0 1 1 17 0"/><path d="M12 15l4.5-5"/><g stroke-width="1"><path d="M6 12.5h1.5M12 6.5V8M16.5 12.5H18"/></g></g><g class="wish-icon-gild"><circle cx="12" cy="15" r="1.7" fill="currentColor" stroke="none"/></g>',
    coins:
      '<g class="wish-icon-line"><circle cx="9" cy="9" r="6"/><path d="M14.5 6.6A6 6 0 1 1 9.3 15"/></g><g class="wish-icon-gild"><path d="M9 6.8L11.2 9L9 11.2L6.8 9Z" fill="currentColor" stroke="none"/></g>',
    globe:
      '<g class="wish-icon-line"><circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18"/><g stroke-width="1"><path d="M4.5 7.5h15M4.5 16.5h15"/></g></g><g class="wish-icon-gild"><path d="M12 9.8L12.62 11.38L14.2 12L12.62 12.62L12 14.2L11.38 12.62L9.8 12L11.38 11.38Z" fill="currentColor" stroke="none"/></g>',
    "refresh-cw":
      '<g class="wish-icon-line"><path d="M20 11a8 8 0 0 0-14.3-4.3M4 13a8 8 0 0 0 14.3 4.3"/></g><g class="wish-icon-gild"><path d="M4 3.2 4.6 8.6 9.6 7Z" fill="currentColor" stroke="none"/><path d="M20 20.8l-.6-5.4-5 1.6Z" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1" fill="currentColor" stroke="none"/></g>',
    terminal:
      '<g class="wish-icon-line"><path d="M4.5 7l5 5-5 5"/><g stroke-width="1"><path d="M4.5 3H19.5L22.5 6V18L19.5 21H4.5L1.5 18V6Z"/></g></g><g class="wish-icon-gild"><path d="M12 16.5h7.5v1.6H12Z" fill="currentColor" stroke="none"/></g>',
    quill:
      '<g class="wish-icon-line"><path d="M21 3C14 5 9 10 7 17l-3 4.5"/><path d="M9.5 12.5l4 1M12 9l4 .5"/></g><g class="wish-icon-gild"><circle cx="4" cy="21.5" r="1.3" fill="currentColor" stroke="none"/><path d="M21 3c-4 4-7 8-9.5 12.5L10 14c2-5 6-9 11-11Z" fill="currentColor" stroke="none"/></g>',
    archive:
      '<g class="wish-icon-line"><path d="M3 10h18v9l-2 2H5l-2-2Z"/><path d="M3 10V8a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4v2"/><g stroke-width="1"><path d="M3 13.5h7M14 13.5h7"/></g></g><g class="wish-icon-gild"><path d="M12 12L14 14L12 16L10 14Z" fill="currentColor" stroke="none"/><circle cx="12" cy="17.5" r="0.8" fill="currentColor" stroke="none"/></g>',
    eye:
      '<g class="wish-icon-line"><path d="M2.2 12c2.2-4.5 5.6-6.8 9.8-6.8s7.6 2.3 9.8 6.8c-2.2 4.5-5.6 6.8-9.8 6.8S4.4 16.5 2.2 12Z"/><circle cx="12" cy="12" r="3.2"/></g><g class="wish-icon-gild"><circle cx="12" cy="12" r="1.35" fill="currentColor" stroke="none"/></g>',
    flame:
      '<g class="wish-icon-line"><path d="M12 1.8C18.5 7.8 18 15.8 12 22.2 6 15.8 5.5 7.8 12 1.8Z"/></g><g class="wish-icon-gild"><path d="M12 8.8c2.8 3.4 2.4 7.4 0 10.6-2.4-3.2-2.8-7.2 0-10.6Z" fill="currentColor" stroke="none"/></g>',
    wrench:
      '<g class="wish-icon-line"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76Z"/></g><g class="wish-icon-gild"><circle cx="6.5" cy="17.5" r="1.15" fill="currentColor" stroke="none"/></g>',
  };

  // Every page in Nav: [icon, tone, optional worker/role badge label].
  var CONCEPTS = {
    welcome: ["lamp", "lead"],
    introduction: ["book-open", "lead"],
    "getting-started": ["compass", "lead"],
    wish: ["star", "lead"],
    azima: ["target", "work"],
    "task-and-worker": ["hammer", "work", "Work"],
    dependencies: ["link", "work"],
    watcher: ["eye", "watch", "Watcher"],
    investigation: ["glasses", "inv", "Investigation"],
    mockup: ["palette", "mock", "Mockup"],
    decision: ["scale", "lead"],
    block: ["smoke", "inv"],
    "project-and-settings": ["boxes", "work"],
    "integration-branch": ["sprout", "work"],
    "merges-and-corrections": ["wrench", "fix", "Merge correction"],
    push: ["rocket", "work"],
    "push-strategy": ["map", "work"],
    gates: ["arch", "work"],
    "pull-requests": ["plug", "work"],
    "the-djinn": ["lamp", "lead", "Lead"],
    "question-and-move": ["messages-square", "waiting"],
    enlighten: ["lantern", "inv", "Question investigator"],
    tilasm: ["seal", "mock"],
    "lead-notes": ["notebook", "lead"],
    "skills-and-templates": ["scroll", "work"],
    ifrit: ["flame", "ifrit", "Ifrit"],
    "the-window": ["app-window", "run"],
    inbox: ["envelope", "waiting"],
    review: ["scan", "fix", "Merge review"],
    rights: ["shield", "run"],
    "machine-and-load": ["gauge", "run"],
    "usage-and-cost": ["coins", "run"],
    sharing: ["globe", "mock"],
    "restarts-and-updates": ["refresh-cw", "run"],
    commands: ["terminal", "run"],
  };

  // Concept cards in #words on the splash page.
  var WORD_CARDS = {
    "word-wishes": ["star", "lead"],
    "word-lead": ["lamp", "lead"],
    "word-azimas": ["target", "work"],
    "word-azima-drafts": ["quill", "work"],
    "word-overlaps": ["link", "work"],
    "word-workers": ["hammer", "work"],
    "word-investigations": ["glasses", "inv"],
    "word-questions": ["messages-square", "waiting"],
    "word-push-strategy": ["map", "work"],
    "word-gates": ["arch", "work"],
    "word-machine": ["gauge", "run"],
    "word-tilasms": ["seal", "mock"],
    "word-blocks": ["smoke", "inv"],
    "word-journal": ["archive", "run"],
    "word-routing": ["envelope", "waiting"],
    "word-personal-space": ["app-window", "run"],
    "word-eye": ["eye", "watch"],
    "word-skills": ["scroll", "work"],
    "word-summoning-circle": ["compass", "lead"],
    "word-usage": ["coins", "run"],
  };

  // Status, worker-kind and button labels in tables and prose, mapped to their window tone.
  var STATUS_TONES = {
    Done: "done",
    Granted: "done",
    Merged: "done",
    "PR merged": "done",
    Replied: "done",
    "up to date": "done",
    "Mark done": "done",
    Validate: "done",
    Verified: "done",
    Build: "work",
    Launch: "work",
    "Builds:": "work",
    "Merged into main": "done",
    Running: "run",
    "In progress": "run",
    Active: "run",
    Merging: "run",
    Resuming: "run",
    "Resumed as": "run",
    Open: "run",
    "PR open": "run",
    "Sent · send 1": "run",
    "Read by the Djinn": "run",
    Send: "run",
    "Resume the worker": "run",
    "Start anyway": "run",
    "See the diff": "run",
    "See uncommitted files": "run",
    Standard: "work",
    "On demand": "lead",
    "Waits for your answer": "waiting",
    "To validate": "waiting",
    "Merge being fixed by": "fix",
    "Merge being fixed by W6": "fix",
    "To review": "fix",
    "PR proposed": "waiting",
    Blocking: "waiting",
    "Needs you": "waiting",
    "Turn comments into work": "fix",
    "To the lead": "lead",
    Failed: "failed",
    "Merge failed": "failed",
    "Stop the worker": "failed",
    Watching: "watch",
    Investigating: "inv",
    Investigate: "inv",
    "Investigates:": "inv",
    "Enlighten me": "inv",
    Iterate: "mock",
    "Iterates:": "mock",
    "Iterate · round 1/3": "mock",
    Ifrit: "ifrit",
    Lit: "ifrit",
    Planned: "planned",
    Paused: "planned",
    Draft: "planned",
    Stopped: "planned",
    Cancelled: "planned",
    "Waiting for the limit": "planned",
    Interrupted: "waiting",
    "Can wait": "planned",
    "Pause the worker": "planned",
    Out: "planned",
  };

  function iconSvg(name, size) {
    var body = ICONS[name];
    if (!body) return "";
    return (
      '<svg class="grimoire-icon" viewBox="0 0 24 24" width="' +
      (size || 16) +
      '" height="' +
      (size || 16) +
      '" fill="none" stroke="currentColor" stroke-width="1.35" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
      body +
      "</svg>"
    );
  }

  function decorateChrome() {
    links.forEach(function (a) {
      if (a.querySelector(".nav-icon")) return;
      var slug = (a.getAttribute("href") || "").replace(/^#/, "");
      var meta = CONCEPTS[slug];
      if (!meta) return;
      a.dataset.tone = meta[1];
      var span = document.createElement("span");
      span.className = "nav-icon tone-" + meta[1];
      span.setAttribute("aria-hidden", "true");
      span.innerHTML = iconSvg(meta[0], 15);
      a.insertBefore(span, a.firstChild);
    });

    if (welcome) {
      Array.prototype.forEach.call(welcome.querySelectorAll(".card.word"), function (card) {
        var meta = WORD_CARDS[card.id];
        if (!meta) {
          var a = card.querySelector("h3 a[href^='#']");
          var slug = a ? a.getAttribute("href").replace(/^#/, "").split("-")[0] : "";
          meta = CONCEPTS[slug];
        }
        if (!meta) return;
        card.dataset.tone = meta[1];
        var h3 = card.querySelector("h3");
        if (h3 && !h3.querySelector(".word-icon")) {
          var span = document.createElement("span");
          span.className = "word-icon tone-" + meta[1];
          span.setAttribute("aria-hidden", "true");
          span.innerHTML = iconSvg(meta[0], 18);
          h3.insertBefore(span, h3.firstChild);
        }
      });
    }
  }

  function decoratePage(page) {
    var meta = CONCEPTS[page.id];
    if (meta) page.dataset.tone = meta[1];
    var prose = page.querySelector(".prose");
    if (!prose) return;

    var h1 = prose.querySelector("h1");
    if (h1 && meta && !h1.querySelector(".page-h1-icon")) {
      var iconWrap = document.createElement("span");
      iconWrap.className = "page-h1-icon tone-" + meta[1];
      iconWrap.setAttribute("aria-hidden", "true");
      iconWrap.innerHTML = iconSvg(meta[0], 24);
      h1.insertBefore(iconWrap, h1.firstChild);

      if (meta[2] && !prose.querySelector(".page-kicker")) {
        var kicker = document.createElement("div");
        kicker.className = "page-kicker";
        kicker.setAttribute("aria-hidden", "true");
        kicker.innerHTML =
          '<span class="status-badge tone-' +
          meta[1] +
          '"><span class="status-dot"></span><span>' +
          meta[2] +
          "</span></span>";
        prose.insertBefore(kicker, h1);
      }
    }

    // Callout blockquotes: Example, Sketch, or Note.
    Array.prototype.forEach.call(prose.querySelectorAll("blockquote"), function (bq) {
      var strong = bq.querySelector("p:first-child > strong:first-child");
      var label = strong ? strong.textContent.trim() : "";
      var iconName = "lantern";
      if (/^Example\.?$/i.test(label)) {
        bq.classList.add("callout-example");
        iconName = meta ? meta[0] : "compass";
      } else if (/sketch/i.test(label)) {
        bq.classList.add("callout-sketch");
        iconName = "quill";
      } else {
        bq.classList.add("callout-note");
      }
      if (strong && !strong.querySelector(".callout-icon")) {
        var span = document.createElement("span");
        span.className = "callout-icon";
        span.setAttribute("aria-hidden", "true");
        span.innerHTML = iconSvg(iconName, 15);
        strong.insertBefore(span, strong.firstChild);
      }
    });

    // Tables: concept links and status/button first-column cells.
    Array.prototype.forEach.call(prose.querySelectorAll("tbody tr"), function (tr) {
      var td = tr.querySelector("td:first-child");
      if (!td) return;
      var link = td.querySelector("a[href^='#']");
      if (link && !link.querySelector(".nav-icon")) {
        var slug = link.getAttribute("href").replace(/^#/, "");
        var cmeta = CONCEPTS[slug];
        if (cmeta) {
          link.classList.add("concept-link", "tone-" + cmeta[1]);
          var span = document.createElement("span");
          span.className = "nav-icon tone-" + cmeta[1];
          span.setAttribute("aria-hidden", "true");
          span.innerHTML = iconSvg(cmeta[0], 14);
          link.insertBefore(span, link.firstChild);
          return;
        }
      }
      var strong = td.querySelector("strong");
      var target = strong || (td.children.length === 0 ? td : null);
      if (!target) return;
      var text = target.textContent.trim();
      var tone = STATUS_TONES[text];
      if (!tone) return;
      target.classList.add("status-cell", "tone-" + tone);
      if (!target.querySelector(".status-dot")) {
        var dot = document.createElement("span");
        dot.className = "status-dot";
        dot.setAttribute("aria-hidden", "true");
        target.insertBefore(dot, target.firstChild);
      }
    });

    // Inline status & button chips in prose paragraphs and lists.
    Array.prototype.forEach.call(prose.querySelectorAll("p > strong, li > strong"), function (strong) {
      if (strong.classList.contains("status-cell")) return;
      var text = strong.textContent.trim();
      var tone = STATUS_TONES[text];
      if (!tone) return;
      strong.classList.add("prose-chip", "tone-" + tone);
    });

    // Read next / Where to go next lists and related links.
    Array.prototype.forEach.call(prose.querySelectorAll("h2"), function (h2) {
      if (!/-(read|where-to-go)-next$/.test(h2.id)) return;
      var next = h2.nextElementSibling;
      if (!next) return;
      if (next.tagName === "UL") {
        next.classList.add("read-next-list");
        Array.prototype.forEach.call(next.querySelectorAll("li"), function (li) {
          var a = li.querySelector("a[href^='#']");
          if (!a) return;
          var slug = a.getAttribute("href").replace(/^#/, "");
          var cmeta = CONCEPTS[slug];
          if (!cmeta) return;
          li.classList.add("tone-" + cmeta[1]);
          if (!a.querySelector(".nav-icon")) {
            var span = document.createElement("span");
            span.className = "nav-icon tone-" + cmeta[1];
            span.setAttribute("aria-hidden", "true");
            span.innerHTML = iconSvg(cmeta[0], 16);
            a.insertBefore(span, a.firstChild);
          }
        });
      } else if (next.tagName === "P") {
        next.classList.add("read-next-related");
        Array.prototype.forEach.call(next.querySelectorAll("a[href^='#']"), function (a) {
          var slug = a.getAttribute("href").replace(/^#/, "");
          var cmeta = CONCEPTS[slug];
          if (!cmeta || a.querySelector(".nav-icon")) return;
          a.classList.add("concept-pill", "tone-" + cmeta[1]);
          var span = document.createElement("span");
          span.className = "nav-icon tone-" + cmeta[1];
          span.setAttribute("aria-hidden", "true");
          span.innerHTML = iconSvg(cmeta[0], 14);
          a.insertBefore(span, a.firstChild);
        });
      }
    });

    // Previous / Next turn cards at the foot of the page.
    Array.prototype.forEach.call(page.querySelectorAll(".page-turn a.turn[href^='#']"), function (turn) {
      var slug = turn.getAttribute("href").replace(/^#/, "");
      var cmeta = CONCEPTS[slug];
      if (!cmeta) return;
      turn.classList.add("tone-" + cmeta[1]);
      var strong = turn.querySelector("strong");
      if (strong && !strong.querySelector(".nav-icon")) {
        var span = document.createElement("span");
        span.className = "nav-icon tone-" + cmeta[1];
        span.setAttribute("aria-hidden", "true");
        span.innerHTML = iconSvg(cmeta[0], 15);
        strong.insertBefore(span, strong.firstChild);
      }
    });
  }

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
      var icon = e.querySelector && e.querySelector(".nav-icon");
      e.textContent = text;
      if (icon) e.insertBefore(icon, e.firstChild);
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
    decoratePage(page);
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
  decorateChrome();
  translate(document);
  paint();
  route();
  if (document.readyState === "complete") idle(preload);
  else
    window.addEventListener("load", function () {
      idle(preload);
    });
})();
