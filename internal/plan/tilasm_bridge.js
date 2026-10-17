// The bridge between a tilasm's frame and Djinn's window (L08, step 3). Djinn appends it to each HTML page of a tilasm
// it serves, never to the tilasm's files. The frame is sandboxed, its origin opaque: the window reads nothing of its
// document, and postMessage, open to any page in a frame, is the one way left between them. The bridge tells the window
// the passage the developer selects, where it ends on the screen, and Ctrl+Alt+M; the window sends it the anchors of
// the developer's comments, which it finds in the page and highlights with CSS.highlights, adding no element to the
// page. It gives the tilasm no new right.
//
// A function called at once, with no global name: the tilasm's own scripts share the page, and WebKit refuses a
// top-level `const top` (W1). Without a document, under node --test, it hands its pure functions to
// globalThis.tilasmBridge and does nothing else (tests/tilasm-bridge.test.mjs).
(function () {
  "use strict";

  // How many characters of the page's text an anchor keeps on each side of its quote.
  var CONTEXT = 32;
  // The longest quote: a longer selection keeps its first characters.
  var MAX_QUOTE = 2000;
  // The longest label, the heading above a passage.
  var MAX_LABEL = 200;

  var space = /\s/;

  // pageText lays out the text of a page from its text nodes, in order: pieces of { data, block }, where block names
  // the block that holds the piece. A run of white space reads as one space, as the page shows it, and a new block
  // starts on a new line, so that the last word of a paragraph never runs into the first of the next. Beside the text,
  // for each of its characters, the piece it comes from and its offset there; a block's new line counts as the first
  // character of the block's text.
  function pageText(pieces) {
    var chars = [];
    var piece = [];
    var offset = [];
    var block;
    // A new block started: a new line before its first character.
    var due = false;
    for (var i = 0; i < pieces.length; i++) {
      var data = pieces[i].data;
      if (pieces[i].block !== block) {
        block = pieces[i].block;
        due = chars.length > 0;
      }
      for (var j = 0; j < data.length; j++) {
        var c = data[j];
        if (space.test(c)) {
          if (chars.length === 0 || due || chars[chars.length - 1] === " ") continue;
          c = " ";
        } else if (due) {
          if (chars[chars.length - 1] === " ") {
            chars.pop();
            piece.pop();
            offset.pop();
          }
          chars.push("\n");
          piece.push(i);
          offset.push(j);
          due = false;
        }
        chars.push(c);
        piece.push(i);
        offset.push(j);
      }
    }
    if (chars[chars.length - 1] === " ") {
      chars.pop();
      piece.pop();
      offset.pop();
    }
    return { text: chars.join(""), piece: piece, offset: offset };
  }

  // textOffset is the place in a page's text of a point in its pieces, offset characters into piece i: the first
  // character at or after it, or the text's end.
  function textOffset(layout, i, offset) {
    var lo = 0;
    var hi = layout.piece.length;
    while (lo < hi) {
      var mid = (lo + hi) >> 1;
      var p = layout.piece[mid];
      if (p < i || (p === i && layout.offset[mid] < offset)) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }

  // quote is the anchor of the text between start and end: its white space at both ends left out, its first MAX_QUOTE
  // characters, and the CONTEXT characters on each side. Nothing but white space quotes nothing: null.
  function quote(text, start, end) {
    while (start < end && space.test(text[start])) start++;
    while (end > start && space.test(text[end - 1])) end--;
    if (end - start > MAX_QUOTE) {
      end = start + MAX_QUOTE;
      while (end > start && space.test(text[end - 1])) end--;
    }
    if (start >= end) return null;
    return {
      exact: text.slice(start, end),
      prefix: text.slice(Math.max(0, start - CONTEXT), start),
      suffix: text.slice(end, end + CONTEXT),
      start: start,
      end: end,
    };
  }

  // locate finds a quote again in a page's text: where its exact text is, and, when it is there more than once, where
  // the characters around it match its prefix and suffix best, then the nearest to where it was. Null when its text is
  // not in the page: finding a passage that changed comes later (L08, step 5).
  function locate(text, anchor) {
    var exact = anchor.exact;
    if (!exact) return null;
    var best = -1;
    var score = -Infinity;
    for (var at = text.indexOf(exact); at >= 0; at = text.indexOf(exact, at + 1)) {
      var s =
        common(text.slice(Math.max(0, at - CONTEXT), at), anchor.prefix || "", true) +
        common(text.slice(at + exact.length, at + exact.length + CONTEXT), anchor.suffix || "", false) -
        Math.abs(at - (anchor.start || 0)) / 1e9;
      if (s > score) {
        score = s;
        best = at;
      }
    }
    return best < 0 ? null : { start: best, end: best + exact.length };
  }

  // common counts the characters two texts share at their start, or at their end.
  function common(a, b, fromEnd) {
    var n = 0;
    while (n < a.length && n < b.length && (fromEnd ? a[a.length - 1 - n] === b[b.length - 1 - n] : a[n] === b[n])) n++;
    return n;
  }

  // pageName is the page a path serves, within its tilasm: /tilasm/<id>/@<key>/notes/a.html is notes/a.html, and
  // /tilasm/<id>/ its index.html.
  function pageName(pathname) {
    var parts = pathname.split("/").slice(3);
    if (parts.length > 0 && parts[0].charAt(0) === "@") parts.shift();
    var name = parts.map(decode).join("/");
    return name === "" || name.charAt(name.length - 1) === "/" ? name + "index.html" : name;
  }

  function decode(part) {
    try {
      return decodeURIComponent(part);
    } catch (e) {
      return part;
    }
  }

  if (typeof document === "undefined") {
    globalThis.tilasmBridge = {
      pageText: pageText,
      textOffset: textOffset,
      quote: quote,
      locate: locate,
      pageName: pageName,
    };
    return;
  }
  // A page opened alone has no window to talk to.
  if (window.parent === window) return;
  var parent = window.parent;
  var page = pageName(location.pathname);

  // What holds no text the page shows as text, and the blocks that start a new line in the page's text.
  var SKIP = "script, style, noscript, template, textarea, title";
  var BLOCKS = {};
  (
    "address article aside blockquote body caption dd details dialog div dl dt fieldset figcaption figure footer form " +
    "h1 h2 h3 h4 h5 h6 header hgroup hr li main nav ol p pre section summary table tbody td tfoot th thead tr ul " +
    "text svg"
  )
    .split(" ")
    .forEach(function (name) {
      BLOCKS[name] = true;
    });

  // read is the text of the page as it stands, with its text nodes.
  function read() {
    var root = document.body || document.documentElement;
    var nodes = [];
    var pieces = [];
    var blocks = new Map();
    var walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
      acceptNode: function (node) {
        var el = node.parentElement;
        return el && !el.closest(SKIP) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
      },
    });
    for (var node = walker.nextNode(); node; node = walker.nextNode()) {
      var el = node.parentElement;
      if (!blocks.has(el)) {
        var at = el;
        while (at && !BLOCKS[at.localName]) at = at.parentElement;
        blocks.set(el, at);
      }
      nodes.push(node);
      pieces.push({ data: node.data, block: blocks.get(el) });
    }
    var layout = pageText(pieces);
    layout.nodes = nodes;
    return layout;
  }

  // rangeOf is the DOM range of the page's text between start and end.
  function rangeOf(layout, start, end) {
    if (start >= end || end > layout.text.length) return null;
    var range = document.createRange();
    range.setStart(layout.nodes[layout.piece[start]], layout.offset[start]);
    range.setEnd(layout.nodes[layout.piece[end - 1]], layout.offset[end - 1] + 1);
    return range;
  }

  // headingAbove is the last heading before a node, or around it: "\u00a7 Grouping". The bridge is ASCII only: a page
  // that names no charset would read it as windows-1252.
  function headingAbove(node) {
    var label = "";
    var headings = document.querySelectorAll("h1, h2, h3, h4, h5, h6");
    for (var i = 0; i < headings.length; i++) {
      var h = headings[i];
      if (h.contains(node) || h.compareDocumentPosition(node) & Node.DOCUMENT_POSITION_FOLLOWING)
        label = h.textContent.replace(/\s+/g, " ").trim();
      else break;
    }
    if (label.length > MAX_LABEL) label = label.slice(0, MAX_LABEL - 1) + "\u2026";
    return label ? "\u00a7 " + label : "";
  }

  // selected is the passage the developer selected, with where its last line ends in the frame; null for none.
  function selected() {
    var selection = window.getSelection();
    if (!selection || selection.isCollapsed || selection.rangeCount === 0) return null;
    var range = selection.getRangeAt(0);
    var layout = read();
    var first = -1;
    var last = -1;
    for (var i = 0; i < layout.nodes.length; i++) {
      if (!range.intersectsNode(layout.nodes[i])) continue;
      if (first < 0) first = i;
      last = i;
    }
    if (first < 0) return null;
    var start = textOffset(layout, first, layout.nodes[first] === range.startContainer ? range.startOffset : 0);
    var end = textOffset(
      layout,
      last,
      layout.nodes[last] === range.endContainer ? range.endOffset : layout.nodes[last].data.length,
    );
    var passage = quote(layout.text, start, end);
    if (!passage) return null;
    passage.page = page;
    passage.label = headingAbove(layout.nodes[layout.piece[passage.start]]);
    return { passage: passage, range: range };
  }

  // placeOf is where a range's last line ends, in the frame's viewport, to put the Comment button under it.
  function placeOf(range) {
    var rects = range.getClientRects();
    var r = rects.length > 0 ? rects[rects.length - 1] : range.getBoundingClientRect();
    return { left: r.left, top: r.top, right: r.right, bottom: r.bottom };
  }

  function post(message) {
    message.djinn = "tilasm";
    parent.postMessage(message, "*");
  }

  // The selection, told a moment after it stops changing, as a mouse drags or a key extends it; at once once gone.
  var current = null;
  var timer = 0;
  function tell() {
    clearTimeout(timer);
    timer = 0;
    var now = selected();
    if (!now && !current) return;
    current = now;
    post(now ? { type: "selection", passage: now.passage, at: placeOf(now.range) } : { type: "selection", passage: null });
  }
  document.addEventListener("selectionchange", function () {
    var selection = window.getSelection();
    if (!selection || selection.isCollapsed) {
      tell();
      return;
    }
    clearTimeout(timer);
    timer = setTimeout(tell, 120);
  });
  // The frame scrolls, or changes size: the button follows the passage.
  var moving = false;
  function follow() {
    if (!current || moving) return;
    moving = true;
    requestAnimationFrame(function () {
      moving = false;
      if (current) post({ type: "selection", passage: current.passage, at: placeOf(current.range) });
    });
  }
  window.addEventListener("scroll", follow, { passive: true, capture: true });
  window.addEventListener("resize", follow, { passive: true });
  // Ctrl+Alt+M comments the selection: a key pressed in the frame stays in the frame's document.
  window.addEventListener(
    "keydown",
    function (event) {
      if (
        !event.ctrlKey ||
        !event.altKey ||
        event.shiftKey ||
        event.metaKey ||
        (event.code !== "KeyM" && (event.key || "").toLowerCase() !== "m")
      )
        return;
      event.preventDefault();
      if (timer) tell();
      post({ type: "key", name: "comment" });
    },
    true,
  );

  // The highlights: gold for a draft, stronger for the one being written, blue once sent. A stylesheet the page adopts
  // adds no element either.
  var NAMES = { draft: "djinn-draft", writing: "djinn-writing", sent: "djinn-sent" };
  var highlights = typeof CSS !== "undefined" && CSS.highlights && typeof Highlight === "function";
  if (highlights && "adoptedStyleSheets" in document) {
    try {
      var sheet = new CSSStyleSheet();
      sheet.replaceSync(
        "::highlight(djinn-draft) { background-color: rgb(233 193 92 / 0.4); }\n" +
          "::highlight(djinn-writing) { background-color: rgb(233 193 92 / 0.75); }\n" +
          "::highlight(djinn-sent) { background-color: rgb(96 165 250 / 0.35); }\n",
      );
      document.adoptedStyleSheets = document.adoptedStyleSheets.concat([sheet]);
    } catch (e) {
      // A page that refuses the sheet keeps the browser's own colour for a highlight: none, so no highlight shows.
    }
  }
  function mark(anchors) {
    if (!highlights) return;
    var layout = read();
    var ranges = { draft: [], writing: [], sent: [] };
    for (var i = 0; i < anchors.length; i++) {
      var a = anchors[i];
      if (!a || typeof a !== "object" || a.page !== page || !ranges[a.state] || typeof a.exact !== "string") continue;
      var found = locate(layout.text, a);
      var range = found && rangeOf(layout, found.start, found.end);
      if (range) ranges[a.state].push(range);
    }
    for (var state in NAMES) {
      if (ranges[state].length > 0) CSS.highlights.set(NAMES[state], new Highlight(...ranges[state]));
      else CSS.highlights.delete(NAMES[state]);
    }
  }

  window.addEventListener("message", function (event) {
    var data = event.data;
    if (event.source !== parent || !data || typeof data !== "object" || data.djinn !== "tilasm") return;
    if (data.type === "anchors" && Array.isArray(data.anchors)) mark(data.anchors);
    else if (data.type === "unselect") {
      // The window commented the selection: it goes, and the window learns when, past what was on its way to it.
      clearTimeout(timer);
      timer = 0;
      current = null;
      window.getSelection().removeAllRanges();
      post({ type: "selection", passage: null });
    }
  });
  post({ type: "ready", page: page });
})();
