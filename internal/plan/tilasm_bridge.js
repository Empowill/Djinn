// The bridge between a tilasm's frame and Djinn's window (L08, step 3). Djinn appends it to each HTML page of a tilasm
// it serves, never to the tilasm's files. The frame is sandboxed, its origin opaque: the window reads nothing of its
// document, and postMessage, open to any page in a frame, is the one way left between them. The bridge tells the window
// the passage the developer selects, where it ends on the screen, and Ctrl+Alt+M; in Aim mode (step 4), the block
// under the mouse and the one clicked, and Escape. The window sends it the anchors of the developer's comments, which
// it finds in the page, perhaps changed by a new version (step 5), highlights with CSS.highlights, adding no element
// to the page, and places: where each stands, again as the page scrolls, for the dots in the window's margin. It gives
// the tilasm no new right.
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
  // not in the page: relocate looks for it changed.
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

  // The share of a quote's words a passage or an element must keep to be found changed, rather than lost.
  var KEEP = 0.6;

  // word is a word as compared: its letters and digits, in lower case. Punctuation and case never make two words
  // differ.
  function word(w) {
    return w.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, "");
  }

  // wordsOf are the words of a text, with where each starts and ends in it, the punctuation around it left out; those
  // that are only punctuation left out too.
  function wordsOf(text) {
    var out = [];
    var re = /\S+/g;
    for (var m = re.exec(text); m; m = re.exec(text)) {
      var core = /[\p{L}\p{N}](?:.*[\p{L}\p{N}])?/u.exec(m[0]);
      if (core)
        out.push({
          w: word(core[0]),
          start: m.index + core.index,
          end: m.index + core.index + core[0].length,
        });
    }
    return out;
  }

  // relocate finds an anchor's passage in a page's text, perhaps a later version of the page than the one it was
  // quoted in (L08, step 5): found, where its exact text is (locate); changed, where a run of as many words as the
  // quote shares at least KEEP of them with it, the most, then the nearest to where it was, without the words at its
  // ends the quote did not have; else lost. Pure: tests/tilasm-bridge.test.mjs gives it pairs of texts.
  function relocate(text, anchor) {
    var found = locate(text, anchor);
    if (found) return { status: "found", start: found.start, end: found.end };
    var want = wordsOf(anchor.exact || "").map(function (x) {
      return x.w;
    });
    var n = want.length;
    var words = wordsOf(text);
    if (n === 0 || words.length === 0) return { status: "lost" };
    var bag = {};
    for (var i = 0; i < n; i++) bag[want[i]] = (bag[want[i]] || 0) + 1;
    // The words of the window, counted, and how many of them the quote has: the window slides a word at a time.
    var have = {};
    var hit = 0;
    var best = -1;
    var score = 0;
    var size = Math.min(n, words.length);
    var at = anchor.start || 0;
    for (var j = 0; j < words.length; j++) {
      var w = words[j].w;
      if ((have[w] || 0) < (bag[w] || 0)) hit++;
      have[w] = (have[w] || 0) + 1;
      if (j >= size) {
        var out = words[j - size].w;
        have[out]--;
        if (have[out] < (bag[out] || 0)) hit--;
      }
      if (j < size - 1) continue;
      var first = j - size + 1;
      if (
        hit / n > score ||
        (hit / n === score && best >= 0 && Math.abs(words[first].start - at) < Math.abs(words[best].start - at))
      ) {
        score = hit / n;
        best = first;
      }
    }
    if (best < 0 || score < KEEP) return { status: "lost" };
    var lo = best;
    var hi = best + size - 1;
    while (lo < hi && !bag[words[lo].w]) lo++;
    while (hi > lo && !bag[words[hi].w]) hi--;
    // The quote's words before the first found and after the last were replaced: as many words as they were, within
    // the same block, stand in for them.
    for (var left = want.indexOf(words[lo].w); left > 0 && lo > 0 && sameBlock(text, words[lo - 1], words[lo]); left--)
      lo--;
    for (
      var right = n - 1 - want.lastIndexOf(words[hi].w);
      right > 0 && hi < words.length - 1 && sameBlock(text, words[hi], words[hi + 1]);
      right--
    )
      hi++;
    return {
      status: "changed",
      start: words[lo].start,
      end: words[hi].end,
      score: score,
    };
  }

  // sameBlock tells two words, a before b, with no new line between them: the page's text starts each block on one.
  function sameBlock(text, a, b) {
    return text.slice(a.end, b.start).indexOf("\n") < 0;
  }

  // shared is the share of a text's words another text has, each word counted as often as it shows.
  function shared(want, have) {
    var a = wordsOf(want);
    if (a.length === 0) return 0;
    var bag = {};
    wordsOf(have).forEach(function (x) {
      bag[x.w] = (bag[x.w] || 0) + 1;
    });
    var hit = 0;
    a.forEach(function (x) {
      if (bag[x.w]) {
        hit++;
        bag[x.w]--;
      }
    });
    return hit / a.length;
  }

  // sameWords tells two texts with the same words, in the same order: punctuation, case and spacing aside.
  function sameWords(a, b) {
    var x = wordsOf(a);
    var y = wordsOf(b);
    if (x.length !== y.length) return false;
    for (var i = 0; i < x.length; i++) if (x[i].w !== y[i].w) return false;
    return true;
  }

  // tagOf is the element a path ends on: figure for "section:nth-of-type(2) > figure:nth-of-type(1)".
  function tagOf(path) {
    var last = (path || "").split(">").pop().trim();
    return last.replace(/:.*$/, "");
  }

  // findElement finds an anchor's element among a page's elements, given in the page's order as { id, path, tag,
  // fingerprint }: found, the one with its id, else with the same words at its path, else elsewhere of the same tag;
  // changed, the element of its tag whose words share at least KEEP of its own, the most; else lost. Pure, as relocate.
  function findElement(elements, anchor) {
    var tag = tagOf(anchor.path);
    var i;
    if (anchor.elementId) {
      for (i = 0; i < elements.length; i++)
        if (elements[i].id === anchor.elementId) return { status: "found", index: i };
    }
    var same = [];
    for (i = 0; i < elements.length; i++) if (!tag || elements[i].tag === tag) same.push(i);
    for (i = 0; i < same.length; i++) {
      var e = elements[same[i]];
      if (e.path === anchor.path && sameWords(e.fingerprint, anchor.fingerprint))
        return { status: "found", index: same[i] };
    }
    for (i = 0; i < same.length; i++)
      if (sameWords(elements[same[i]].fingerprint, anchor.fingerprint)) return { status: "found", index: same[i] };
    var best = -1;
    var score = 0;
    for (i = 0; i < same.length; i++) {
      var s = shared(anchor.fingerprint || "", elements[same[i]].fingerprint);
      if (s > score) {
        score = s;
        best = same[i];
      }
    }
    return best >= 0 && score >= KEEP ? { status: "changed", index: best, score: score } : { status: "lost" };
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
      relocate: relocate,
      findElement: findElement,
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

  // What Aim mode takes: a block the developer can point at, a figure or a drawing above all, which no selection takes.
  var AIMS = "p, li, tr, figure, pre, blockquote, table, h1, h2, h3, h4, h5, h6, img, svg, dt, dd, details";
  // The longest fingerprint, the start of an element's text.
  var MAX_PRINT = 160;
  // What a label calls each element, before the start of its text.
  var KINDS = {
    p: "paragraph",
    li: "item",
    tr: "table row",
    figure: "figure",
    pre: "code",
    blockquote: "quote",
    table: "table",
    img: "image",
    svg: "figure",
    dt: "term",
    dd: "definition",
    details: "details",
  };

  // aimed is the block an event's target lies in: a drawing or an image within a figure is the figure.
  function aimed(target) {
    var el = target && target.nodeType === 1 ? target : target && target.parentElement;
    var block = el && el.closest(AIMS);
    if (!block) return null;
    if (block.localName === "svg" || block.localName === "img") {
      var figure = block.closest("figure");
      if (figure) return figure;
      // An SVG within an SVG: the outer drawing.
      while (block.parentElement && block.parentElement.closest("svg")) block = block.parentElement.closest("svg");
    }
    return block;
  }

  // pathOf is an element's path from the body: "section:nth-of-type(2) > p:nth-of-type(3)".
  function pathOf(el) {
    var parts = [];
    for (var e = el; e && e !== document.body && e.parentElement; e = e.parentElement) {
      var i = 1;
      for (var s = e.previousElementSibling; s; s = s.previousElementSibling) if (s.localName === e.localName) i++;
      parts.unshift(e.localName + ":nth-of-type(" + i + ")");
    }
    return parts.join(" > ");
  }

  // clip cuts a text to max characters, an ellipsis last.
  function clip(text, max) {
    return text.length > max ? text.slice(0, max - 1).replace(/\s+$/, "") + "\u2026" : text;
  }

  // printOf is the start of an element's text, its text nodes apart (an SVG's labels would run into each other), its
  // white space as one space: what finds it again in another version, and what the lead reads of it.
  function printOf(el) {
    var parts = [];
    var walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT, {
      acceptNode: function (node) {
        var p = node.parentElement;
        return p && !p.closest(SKIP) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
      },
    });
    var length = 0;
    for (var node = walker.nextNode(); node && length <= MAX_PRINT; node = walker.nextNode()) {
      parts.push(node.data);
      length += node.data.length + 1;
    }
    var text = parts.join(" ").replace(/\s+/g, " ").trim();
    if (!text) text = el.getAttribute("alt") || el.getAttribute("aria-label") || "";
    return clip(text.replace(/\s+/g, " ").trim(), MAX_PRINT);
  }

  // labelOf is what the developer saw: "figure: The path of a notice", its caption for a figure or a table.
  function labelOf(el) {
    var name = el.localName;
    var kind = KINDS[name] || (/^h[1-6]$/.test(name) ? "heading" : name);
    var caption = el.querySelector(name === "figure" ? "figcaption" : name === "table" ? "caption" : "summary");
    var text =
      (caption && (name === "figure" || name === "table" || name === "details") ? caption.textContent : "") || "";
    text = text.replace(/\s+/g, " ").trim() || printOf(el);
    return text ? kind + ": " + clip(text, 80) : kind;
  }

  // elementOf is the anchor of an element: its id, its path, the start of its text, and its label.
  function elementOf(el) {
    return {
      page: page,
      elementId: clip(el.id || "", 1000),
      path: clip(pathOf(el), 2000),
      fingerprint: printOf(el),
      label: labelOf(el),
    };
  }

  // findIn finds an element anchor in the page, among the elements of its tag.
  function findIn(anchor) {
    var tag = tagOf(anchor.path);
    var list = tag ? document.body.getElementsByTagName(tag) : [];
    var elements = [];
    var byId = anchor.elementId ? document.getElementById(anchor.elementId) : null;
    if (byId) elements.push(byId);
    for (var i = 0; i < list.length; i++) if (list[i] !== byId) elements.push(list[i]);
    var found = findElement(
      elements.map(function (el) {
        return {
          id: el.id,
          path: pathOf(el),
          tag: el.localName,
          fingerprint: printOf(el),
        };
      }),
      anchor,
    );
    return found.status === "lost" ? found : { status: found.status, el: elements[found.index] };
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
  // Ctrl+Alt+M comments the selection, and Escape leaves Aim mode: a key pressed in the frame stays in the frame's
  // document, and these two alone are told.
  window.addEventListener(
    "keydown",
    function (event) {
      if (aiming && event.key === "Escape") {
        event.preventDefault();
        event.stopImmediatePropagation();
        aim(false);
        post({ type: "key", name: "escape" });
        return;
      }
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

  // Aim mode: the block under the mouse is told, for the window to frame it, and a click takes it, nothing else of the
  // page's own; the cursor says so, through a sheet the page adopts while it lasts.
  var aiming = false;
  var hovered = null;
  var aimSheet = null;
  if ("adoptedStyleSheets" in document) {
    try {
      aimSheet = new CSSStyleSheet();
      aimSheet.replaceSync("* { cursor: crosshair !important; }");
    } catch (e) {
      aimSheet = null;
    }
  }
  function aim(on) {
    if (aiming === on) return;
    aiming = on;
    hover(null);
    if (aimSheet)
      document.adoptedStyleSheets = on
        ? document.adoptedStyleSheets.concat([aimSheet])
        : document.adoptedStyleSheets.filter(function (s) {
            return s !== aimSheet;
          });
  }
  function boxOf(el) {
    var r = el.getBoundingClientRect();
    return { left: r.left, top: r.top, right: r.right, bottom: r.bottom };
  }
  function hover(el) {
    if (el === hovered) return;
    hovered = el;
    post({ type: "element", hover: el ? boxOf(el) : null });
  }
  document.addEventListener(
    "mousemove",
    function (event) {
      if (aiming) hover(aimed(event.target));
    },
    { passive: true, capture: true },
  );
  document.documentElement.addEventListener("mouseleave", function () {
    if (aiming) hover(null);
  });
  // Pressed in Aim mode, the mouse selects nothing and moves no focus.
  window.addEventListener(
    "mousedown",
    function (event) {
      if (!aiming) return;
      event.preventDefault();
      event.stopImmediatePropagation();
    },
    true,
  );
  window.addEventListener(
    "click",
    function (event) {
      if (!aiming) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      var el = aimed(event.target);
      if (!el) return;
      var at = boxOf(el);
      aim(false);
      post({ type: "element", element: elementOf(el), at: at });
    },
    true,
  );

  // The highlights: gold for a draft, stronger for the one being written or the active one, blue once sent. A
  // stylesheet the page adopts adds no element either; neither does the flash of a passage the window reveals.
  var NAMES = {
    draft: "djinn-draft",
    writing: "djinn-writing",
    active: "djinn-active",
    sent: "djinn-sent",
    flash: "djinn-flash",
  };
  var highlights = typeof CSS !== "undefined" && CSS.highlights && typeof Highlight === "function";
  if (highlights && "adoptedStyleSheets" in document) {
    try {
      var sheet = new CSSStyleSheet();
      sheet.replaceSync(
        "::highlight(djinn-draft) { background-color: rgb(233 193 92 / 0.4); }\n" +
          "::highlight(djinn-writing) { background-color: rgb(233 193 92 / 0.75); }\n" +
          "::highlight(djinn-active) { background-color: rgb(233 193 92 / 0.75); }\n" +
          "::highlight(djinn-sent) { background-color: rgb(96 165 250 / 0.35); }\n" +
          "::highlight(djinn-flash) { background-color: rgb(233 193 92 / 0.95); }\n",
      );
      document.adoptedStyleSheets = document.adoptedStyleSheets.concat([sheet]);
    } catch (e) {
      // A page that refuses the sheet keeps the browser's own colour for a highlight: none, so no highlight shows.
    }
  }

  // The anchors the window sent, as found in the page: { id, status, range, el, now }, and the version it says the page
  // is, told back with each place.
  var placed = [];
  var version = 0;
  // An element's text, as a range: what a highlight covers of it.
  function contentsOf(el) {
    var range = document.createRange();
    range.selectNodeContents(el);
    return range;
  }
  function mark(anchors) {
    var layout = read();
    var ranges = { draft: [], writing: [], active: [], sent: [] };
    placed = [];
    for (var i = 0; i < anchors.length; i++) {
      var a = anchors[i];
      if (!a || typeof a !== "object" || a.page !== page || !ranges[a.state]) continue;
      var p = {
        id: typeof a.id === "string" ? a.id : "",
        status: "lost",
        range: null,
        el: null,
        now: "",
      };
      if (typeof a.exact === "string" && a.exact) {
        var found = relocate(layout.text, a);
        p.status = found.status;
        if (found.status !== "lost") {
          p.range = rangeOf(layout, found.start, found.end);
          if (found.status === "changed") p.now = clip(layout.text.slice(found.start, found.end), MAX_QUOTE);
        }
      } else if (typeof a.path === "string" && (a.path || a.elementId)) {
        var el = findIn({
          elementId: a.elementId || "",
          path: a.path,
          fingerprint: a.fingerprint || "",
        });
        p.status = el.status;
        if (el.el) {
          p.el = el.el;
          p.range = contentsOf(el.el);
          if (el.status === "changed") p.now = labelOf(el.el);
        }
      } else continue;
      if (p.range) ranges[a.state].push(p.range);
      if (p.id) placed.push(p);
    }
    if (highlights)
      for (var state in ranges) {
        if (ranges[state].length > 0) CSS.highlights.set(NAMES[state], new Highlight(...ranges[state]));
        else CSS.highlights.delete(NAMES[state]);
      }
    tellPlaces();
  }

  // tellPlaces tells the window where each anchor stands now in the frame's viewport, for the dots of its margin.
  function tellPlaces() {
    var out = [];
    for (var i = 0; i < placed.length; i++) {
      var p = placed[i];
      var place = { id: p.id, status: p.status };
      var r = p.el ? p.el.getBoundingClientRect() : p.range ? p.range.getBoundingClientRect() : null;
      if (r && (r.width > 0 || r.height > 0)) {
        place.top = r.top;
        place.bottom = r.bottom;
      }
      if (p.now) place.now = p.now;
      out.push(place);
    }
    post({ type: "placed", version: version, anchors: out });
  }
  // The page scrolls, or changes size, or lays out again: the places, again, once a frame.
  var placing = false;
  function replace() {
    if (aiming && hovered) post({ type: "element", hover: boxOf(hovered) });
    if (placing || placed.length === 0) return;
    placing = true;
    requestAnimationFrame(function () {
      placing = false;
      tellPlaces();
    });
  }
  window.addEventListener("scroll", replace, { passive: true, capture: true });
  window.addEventListener("resize", replace, { passive: true });
  if (typeof ResizeObserver === "function" && document.body) new ResizeObserver(replace).observe(document.body);

  // reveal brings an anchor into sight, a third down the frame, and flashes it.
  var flashing = 0;
  function reveal(id) {
    for (var i = 0; i < placed.length; i++) {
      var p = placed[i];
      if (p.id !== id || !p.range) continue;
      var r = (p.el || p.range).getBoundingClientRect();
      window.scrollBy(0, r.top - window.innerHeight / 3);
      if (highlights) {
        CSS.highlights.set(NAMES.flash, new Highlight(p.range));
        clearTimeout(flashing);
        flashing = setTimeout(function () {
          CSS.highlights.delete(NAMES.flash);
        }, 900);
      }
      return;
    }
  }

  window.addEventListener("message", function (event) {
    var data = event.data;
    if (event.source !== parent || !data || typeof data !== "object" || data.djinn !== "tilasm") return;
    if (data.type === "anchors" && Array.isArray(data.anchors)) {
      if (typeof data.version === "number") version = data.version;
      mark(data.anchors);
    } else if (data.type === "aim") aim(data.on === true);
    else if (data.type === "reveal" && typeof data.id === "string") reveal(data.id);
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
