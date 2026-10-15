// The anchors of a Markdown tilasm's discussions and proposals, on the window's side (src/tilasm-discussions.tsx). An
// anchor names a passage by a quote of the document's rendered text, with some 32 characters of context on each side:
// never an offset. Djinn reads anchors the same way (internal/plan/tilasm_anchor.go), and the two must agree to the
// character: the rendered text is every textblock's text in document order (paragraphs, headings, list items, table
// cells, code), each a blank apart; images, raw HTML, rules and live HTML blocks give none; every run of white space is
// one space, and the ends are trimmed. Here it is read from the editor's document, each character mapped back to its
// position there, so that a passage found in the text is a range of the editor.
import type { Node as PMNode } from "@tiptap/pm/model";

// Where an anchor stands in a text, as Djinn names it.
export type AnchorState = "anchored" | "changed" | "missing" | "ambiguous";

export interface Anchor {
  quote: string;
  prefix: string;
  suffix: string;
}

// Rendered is a document's rendered text, each character mapped to the editor's positions: from and to are the range
// of the character it stands for, -1 for the title's (the page shows the title in a field of its own). A space that
// stands for a run of white space, or for a block's end, is empty, where that run starts.
export interface Rendered {
  text: string;
  from: number[];
  to: number[];
}

const isSpace = (ch: string) => /\s/u.test(ch);

class Builder {
  text = "";
  from: number[] = [];
  to: number[] = [];
  private space = false;
  private at = 0;

  blank(at: number) {
    if (this.space) return;
    this.space = true;
    this.at = at;
  }

  // chars writes s, its first character at the editor's position start (-1: not in the editor).
  chars(s: string, start: number) {
    let pos = start;
    for (const ch of s) {
      if (isSpace(ch)) {
        this.blank(pos);
      } else {
        if (this.space) {
          if (this.text.length > 0) {
            this.text += " ";
            this.from.push(this.at);
            this.to.push(this.at);
          }
          this.space = false;
        }
        this.text += ch;
        for (let i = 0; i < ch.length; i++) {
          this.from.push(start < 0 ? -1 : pos + i);
          this.to.push(start < 0 ? -1 : pos + i + 1);
        }
      }
      if (start >= 0) pos += ch.length;
    }
  }
}

// renderDoc is the rendered text of the editor's document, its title first when the Markdown starts with it as a
// heading (title is then that title, else empty).
export function renderDoc(doc: PMNode, title = ""): Rendered {
  const b = new Builder();
  if (title.trim()) {
    b.chars(title, -1);
    b.blank(0);
  }
  doc.descendants((node, pos) => {
    if (node.isTextblock) {
      node.forEach((child, offset) => {
        const at = pos + 1 + offset;
        if (child.isText) b.chars(child.text ?? "", at);
        else if (child.type.name === "hardBreak") b.blank(at);
        // Any other inline leaf (an image) shows no text.
      });
      b.blank(pos + node.nodeSize - 1);
      return false;
    }
    // A leaf block (a rule, a live HTML block) holds no text a reader sees.
    return !node.isLeaf && !node.isAtom;
  });
  return { text: b.text, from: b.from, to: b.to };
}

// squeeze is s as anchors compare it: every run of white space one space.
export function squeeze(s: string): string {
  let out = "";
  let space = false;
  for (const ch of s) {
    if (isSpace(ch)) {
      space = true;
      continue;
    }
    if (space && out.length > 0) out += " ";
    space = false;
    out += ch;
  }
  if (space && out.length > 0) out += " ";
  return out;
}

// lastChars and firstChars are the last or first n characters (code points) of s.
export function lastChars(s: string, n: number): string {
  let i = s.length;
  for (let k = 0; k < n && i > 0; k++) {
    const low = s.charCodeAt(i - 1);
    i -= low >= 0xdc00 && low <= 0xdfff && i >= 2 ? 2 : 1;
  }
  return s.slice(i);
}

export function firstChars(s: string, n: number): string {
  let i = 0;
  for (let k = 0; k < n && i < s.length; k++) {
    const high = s.charCodeAt(i);
    i += high >= 0xd800 && high <= 0xdbff && i + 1 < s.length ? 2 : 1;
  }
  return s.slice(0, i);
}

// Place is where an anchor stands in a rendered text: at to end, equal for a place to insert at; count is how many
// places matched, for an ambiguous anchor.
export interface Place {
  state: AnchorState;
  at: number;
  end: number;
  count: number;
}

const nowhere = (state: AnchorState, count = 0): Place => ({
  state,
  at: 0,
  end: 0,
  count,
});
const found = (state: AnchorState, at: number, end: number): Place => ({
  state,
  at,
  end,
  count: 1,
});

// How many characters of context, on each side, tell a place even when the quote is elsewhere too; how many more one
// occurrence must share with its anchor than any other when none has it whole; the most an anchor keeps.
const SPECIFIC_CONTEXT = 16;
const MIN_CONTEXT_LEAD = 16;
const MAX_CONTEXT = 1000;
// How much context, at least, an anchor keeps on each side.
export const ANCHOR_CONTEXT = 32;

function indexAll(s: string, sub: string): number[] {
  const out: number[] = [];
  if (!sub) return out;
  for (let i = s.indexOf(sub); i >= 0; i = s.indexOf(sub, i + 1)) out.push(i);
  return out;
}

// before tells whether the text before at ends with p, blanks aside; after, whether the text from at starts with s.
function before(text: string, at: number, p: string, strict: boolean) {
  let i = at;
  while (i > 0 && text[i - 1] === " ") i--;
  if (!p) return !strict || i === 0;
  return i >= p.length && text.startsWith(p, i - p.length);
}

function after(text: string, at: number, s: string, strict: boolean) {
  let i = at;
  while (i < text.length && text[i] === " ") i++;
  if (!s) return !strict || i === text.length;
  return text.startsWith(s, i);
}

// commonBefore is how many characters the text before at (blanks aside) shares with the end of p; commonAfter, the
// text from at with the start of s.
function commonBefore(text: string, at: number, p: string) {
  let i = at;
  while (i > 0 && text[i - 1] === " ") i--;
  let n = 0;
  while (n < i && n < p.length && text[i - 1 - n] === p[p.length - 1 - n]) n++;
  return n;
}

function commonAfter(text: string, at: number, s: string) {
  let i = at;
  while (i < text.length && text[i] === " ") i++;
  let n = 0;
  while (i + n < text.length && n < s.length && text[i + n] === s[n]) n++;
  return n;
}

interface Span {
  at: number;
  end: number;
}

// locate finds where a passage stands by its context alone, as Djinn does: between its prefix and its suffix, close
// together, fewer and fewer of their characters looked for when the whole is found nowhere.
function locate(
  text: string,
  p: string,
  s: string,
  quoted: number,
  strict: boolean,
): { spans: Span[]; level: number } {
  if ((!p && !s) || (!strict && (!p || !s))) return { spans: [], level: 0 };
  const gap = 2 * quoted + 200;
  for (const n of [MAX_CONTEXT, SPECIFIC_CONTEXT, 8]) {
    const pk = lastChars(p, n);
    const sk = firstChars(s, n);
    const starts = pk ? indexAll(text, pk).map((at) => at + pk.length) : [0];
    const spans: Span[] = [];
    for (let at of starts) {
      let end = text.length;
      if (sk) {
        const i = text.indexOf(sk, at);
        if (i < 0) continue;
        end = i;
      }
      if (end - at > gap) continue;
      while (at < end && text[at] === " ") at++;
      while (end > at && text[end - 1] === " ") end--;
      spans.push({ at, end });
    }
    if (spans.length > 0) return { spans, level: n };
  }
  return { spans: [], level: 0 };
}

// caret finds a place to insert at: after its prefix and before its suffix; afterBlank puts it at the start of what
// follows the white space between them.
function caret(
  text: string,
  p: string,
  s: string,
  afterBlank: boolean,
  strict: boolean,
): Place {
  if (!p && !s) {
    return text.trim() === "" ? found("anchored", 0, 0) : nowhere("ambiguous");
  }
  const cuts: { before: number; after: number }[] = [];
  if (p) {
    for (const at of indexAll(text, p)) {
      const end = at + p.length;
      let next = end;
      while (next < text.length && text[next] === " ") next++;
      if (after(text, end, s, strict)) cuts.push({ before: end, after: next });
    }
  } else {
    for (const at of indexAll(text, s)) {
      let prev = at;
      while (prev > 0 && text[prev - 1] === " ") prev--;
      if (before(text, at, "", strict)) cuts.push({ before: prev, after: at });
    }
  }
  if (cuts.length === 0) {
    if (p && s) {
      const ps = indexAll(text, p);
      const ss = indexAll(text, s);
      if (ps.length === 1 || ss.length === 1) {
        const at = ps.length === 1 ? ps[0]! + p.length : ss[0]!;
        return found("changed", at, at);
      }
    }
    return nowhere("missing");
  }
  if (cuts.length === 1) {
    const at = afterBlank || !p ? cuts[0]!.after : cuts[0]!.before;
    return found("anchored", at, at);
  }
  return nowhere("ambiguous", cuts.length);
}

// resolve finds an anchor in a rendered text, as Djinn does. strict reads an empty prefix as the text's start and an
// empty suffix as its end, as Djinn stores anchors; otherwise they say nothing.
export function resolve(text: string, a: Anchor, strict: boolean): Place {
  const q = squeeze(a.quote).trim();
  const rawPrefix = squeeze(a.prefix);
  const p = rawPrefix.trim();
  const s = squeeze(a.suffix).trim();
  if (!q) return caret(text, p, s, rawPrefix.endsWith(" "), strict);
  const occ = indexAll(text, q);
  const fits = occ.filter(
    (at) =>
      before(text, at, p, strict) && after(text, at + q.length, s, strict),
  );
  if (fits.length === 1)
    return found("anchored", fits[0]!, fits[0]! + q.length);
  if (fits.length > 1) return nowhere("ambiguous", fits.length);
  const { spans, level } = locate(text, p, s, q.length, strict);
  if (
    spans.length === 1 &&
    (level >= SPECIFIC_CONTEXT ||
      occ.length === 0 ||
      text.slice(spans[0]!.at, spans[0]!.end).includes(q))
  ) {
    const sp = spans[0]!;
    const inside = indexAll(text.slice(sp.at, sp.end), q);
    if (inside.length === 1) {
      const at = sp.at + inside[0]!;
      return found("anchored", at, at + q.length);
    }
    if (inside.length > 1) return nowhere("ambiguous", inside.length);
    if (sp.at === sp.end) return nowhere("missing");
    return found("changed", sp.at, sp.end);
  }
  if (occ.length === 0) {
    return spans.length > 1
      ? nowhere("ambiguous", spans.length)
      : nowhere("missing");
  }
  if (occ.length === 1) {
    if (p.includes(q) || s.includes(q)) return nowhere("ambiguous", 1);
    return found("anchored", occ[0]!, occ[0]! + q.length);
  }
  let best = -1;
  let second = -1;
  let at = 0;
  for (const o of occ) {
    const score = commonBefore(text, o, p) + commonAfter(text, o + q.length, s);
    if (score > best) {
      second = best;
      best = score;
      at = o;
    } else if (score > second) second = score;
  }
  if (best - second >= MIN_CONTEXT_LEAD)
    return found("anchored", at, at + q.length);
  return nowhere("ambiguous", occ.length);
}

// anchorAt is the anchor of the text from at to end: ANCHOR_CONTEXT characters of context on each side, more while
// an identical passage elsewhere has the same, as Djinn takes them.
export function anchorAt(text: string, at: number, end: number): Anchor {
  for (let n = ANCHOR_CONTEXT; ; n *= 2) {
    n = Math.min(n, MAX_CONTEXT);
    const a = {
      quote: text.slice(at, end),
      prefix: lastChars(text.slice(0, at), n),
      suffix: firstChars(text.slice(end), n),
    };
    const got = resolve(text, a, false);
    if ((got.state === "anchored" && got.at === at) || n === MAX_CONTEXT)
      return a;
  }
}

// anchorOf is the anchor of the editor's range from to to: the text it holds, or, when it holds none, the place
// where its text would start. null when the document holds no text there to name it by.
export function anchorOf(r: Rendered, from: number, to: number): Anchor | null {
  let at = -1;
  let end = -1;
  for (let i = 0; i < r.text.length; i++) {
    if (r.text[i] === " " || r.from[i]! < 0) continue;
    if (r.from[i]! >= from && r.to[i]! <= to) {
      if (at < 0) at = i;
      end = i + 1;
    }
  }
  if (at >= 0) return anchorAt(r.text, at, end);
  // No text in the range: the place before the first character after it.
  let next = r.text.length;
  for (let i = 0; i < r.text.length; i++) {
    if (r.text[i] !== " " && r.from[i]! >= from) {
      next = i;
      break;
    }
  }
  if (!r.text.trim()) return { quote: "", prefix: "", suffix: "" };
  return anchorAt(r.text, next, next);
}

// rangeOf is the editor's range of a place found in the rendered text; null when it falls in the title, out of the
// editor.
export function rangeOf(
  r: Rendered,
  place: { at: number; end: number },
  docEnd: number,
): { from: number; to: number } | null {
  const { at, end } = place;
  if (at === end) {
    let pos: number;
    if (at > 0 && r.text[at - 1] !== " ") pos = r.to[at - 1]!;
    else if (at < r.text.length) pos = r.from[at]!;
    else if (at > 0) pos = r.to[at - 1]!;
    else pos = Math.min(1, docEnd);
    return pos < 0 ? null : { from: pos, to: pos };
  }
  const from = r.from[at]!;
  const to = r.to[end - 1]!;
  if (from < 0 || to < 0) return null;
  return { from, to };
}
