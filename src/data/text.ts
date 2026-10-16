// How the window reads a long text an agent wrote: a first view of its Markdown that keeps what follows in sight
// (a question's options, the next report), and the commands it names, to copy as they are.

// The first view of a Markdown text: its first section, at most FOLD_LINES lines of text and about FOLD_CHARS
// characters, cut between lines, never inside a code block. A single line too long is cut at the end of a sentence.
export const FOLD_LINES = 8;
export const FOLD_CHARS = 700;

// What is left out is worth a click only beyond this: two short lines more show at once.
const FOLD_SLACK_LINES = 2;
const FOLD_SLACK_CHARS = 200;

export interface Fold {
  // The first view, in Markdown.
  head: string;
  // Something is left out of it.
  more: boolean;
}

const HEADING = /^#{1,6}\s/;
const FENCE = /^(`{3,}|~{3,})/;

// foldMarkdown cuts text to its first view; a text short enough stays whole, more false.
export function foldMarkdown(
  text: string,
  lines = FOLD_LINES,
  chars = FOLD_CHARS,
): Fold {
  const all = text.replace(/\s+$/, "").split("\n");
  let taken = 0;
  let size = 0;
  // Some text other than a heading was taken: the next heading starts another section.
  let body = false;
  // The fence of the code block the line is in, and the line it opened at.
  let fence = "";
  let opened = 0;
  let cut = all.length;
  let head: string | undefined;
  for (let i = 0; i < all.length; i++) {
    const line = all[i].trim();
    if (fence) {
      if (line.startsWith(fence) && !line.slice(fence.length).trim())
        fence = "";
      else if ((taken >= lines * 2 || size >= chars * 2) && opened > 0) {
        // A long code block below some text: the view stops before it.
        cut = opened;
        break;
      }
      taken++;
      size += line.length;
      continue;
    }
    if (!line) continue;
    if (body && (HEADING.test(line) || taken >= lines || size >= chars)) {
      cut = i;
      break;
    }
    const marker = FENCE.exec(line)?.[1];
    if (marker) {
      fence = marker;
      opened = i;
    }
    if (!marker && size + line.length > chars) {
      // A line longer than what is left: the view ends with the sentence that crosses the budget.
      const end = sentenceEnd(line, Math.max(chars - size, chars / 2));
      if (end > 0 && end < line.length) {
        head = [...all.slice(0, i), all[i].slice(0, all[i].indexOf(line) + end)]
          .join("\n")
          .trimEnd();
        cut = i;
        break;
      }
    }
    taken++;
    size += line.length;
    if (!HEADING.test(line)) body = true;
  }
  if (head === undefined) {
    if (cut >= all.length) return { head: text, more: false };
    const rest = all.slice(cut).filter((l) => l.trim());
    const restSize = rest.reduce((n, l) => n + l.trim().length, 0);
    if (
      !HEADING.test(rest[0]?.trim() ?? "") &&
      rest.length <= FOLD_SLACK_LINES &&
      restSize <= FOLD_SLACK_CHARS
    )
      return { head: text, more: false };
    head = all.slice(0, cut).join("\n").trimEnd();
  }
  return { head, more: true };
}

// sentenceEnd is where a sentence of line ends at most near characters in, past its first third, with its Markdown
// marks paired so that the cut renders as written; 0 when there is none.
function sentenceEnd(line: string, near: number): number {
  const ends = [...line.matchAll(/[.!?;:](?=\s)/g)]
    .map((m) => m.index + 1)
    .filter((i) => i <= near && i >= near / 3)
    .reverse();
  return (
    ends.find((i) => {
      const cut = line.slice(0, i);
      return (
        (cut.match(/`/g)?.length ?? 0) % 2 === 0 &&
        (cut.match(/\*\*/g)?.length ?? 0) % 2 === 0
      );
    }) ?? 0
  );
}

// The heading Djinn puts above the versions history it appends to a question's context at each revision
// (internal/plan.HistoryHeader), in French as Djinn writes it.
export const HISTORY_HEADER =
  "## Historique des versions pr\u00e9c\u00e9dentes";

export interface Versions {
  // The question's context, without its history.
  context: string;
  // The earlier versions, below their heading, in Markdown; "" when the question was never revised.
  history: string;
}

// splitHistory parts a question's context from the versions history Djinn appended to it, where the Go side cuts it
// (internal/plan.CleanContext): at the heading, on a line of its own.
export function splitHistory(context: string): Versions {
  const at = context.startsWith(HISTORY_HEADER)
    ? 0
    : context.indexOf("\n" + HISTORY_HEADER);
  if (at < 0) return { context, history: "" };
  const end = context.indexOf(HISTORY_HEADER, at) + HISTORY_HEADER.length;
  return {
    context: context.slice(0, at).trimEnd(),
    history: context.slice(end).trim(),
  };
}

export interface TextPart {
  text: string;
  // A command, to copy as it is.
  code?: boolean;
  // A link, to open: the text is its URL.
  href?: boolean;
}

// The commands of djinn's own command line: "djinn wish allow …" in a sentence is one to copy.
const DJINN_COMMANDS =
  "backup|block|command|gate|inbox|lead|load|machine|mark|open|plan|project|question|skill|talisman|task|tilasm|up|update|version|wish";
const DJINN_LINE = new RegExp(
  `(^|[^\\w./-])(djinn[ \\t]+(?:${DJINN_COMMANDS})\\b(?:[ \\t]+[^\\s,;()\`]+)*)`,
  "g",
);

// A link written in plain text: a web address, or one of Djinn's own (src/data/links.ts).
const LINK = /\b(?:https?|djinn):\/\/[^\s<>"'`]+/gi;

// linkEnd trims what a sentence puts after a link: its final punctuation, and a closing bracket it did not open.
function linkEnd(url: string): string {
  for (;;) {
    const last = url.at(-1) ?? "";
    const pair = { ")": "(", "]": "[", "}": "{" }[last];
    const count = (c: string) => url.split(c).length - 1;
    if (/[.,;:!?'"*_]/.test(last)) url = url.slice(0, -1);
    else if (pair && count(pair) < count(last)) url = url.slice(0, -1);
    else return url;
  }
}

// linksOf lists the links a text names, in order, as written in plain text or Markdown.
export function linksOf(text: string): string[] {
  return [...text.matchAll(LINK)]
    .map((m) => linkEnd(m[0]))
    .filter((url) => !/^[a-z]+:\/\/$/i.test(url));
}

// linkParts splits plain text into its words and its links.
function linkParts(text: string): TextPart[] {
  const parts: TextPart[] = [];
  let at = 0;
  for (const m of text.matchAll(LINK)) {
    const url = linkEnd(m[0]);
    if (/^[a-z]+:\/\/$/i.test(url)) continue;
    if (m.index > at) parts.push({ text: text.slice(at, m.index) });
    parts.push({ text: url, href: true });
    at = m.index + url.length;
  }
  if (at < text.length) parts.push({ text: text.slice(at) });
  return parts;
}

// djinnAutolinks makes the bare djinn:// links of a Markdown text links, as GFM does for web addresses: <djinn://…>.
// Code, in a block or between backticks, and a link already written as one stay as they are.
export function djinnAutolinks(markdown: string): string {
  if (!/djinn:\/\//i.test(markdown)) return markdown;
  let fence = "";
  return markdown
    .split("\n")
    .map((line) => {
      const marker = FENCE.exec(line.trim())?.[1];
      if (fence) {
        if (line.trim().startsWith(fence) && marker === line.trim()) fence = "";
        return line;
      }
      if (marker) {
        fence = marker;
        return line;
      }
      return line
        .split(/(`+[^`]*`+)/)
        .map((piece, i) =>
          i % 2
            ? piece
            : piece.replace(
                /(^|[^([<\w/])(djinn:\/\/[^\s<>"'`]+)/gi,
                (_, before: string, url: string) => {
                  const link = linkEnd(url);
                  return `${before}<${link}>${url.slice(link.length)}`;
                },
              ),
        )
        .join("");
    })
    .join("\n");
}

// commandParts splits a line of text, an option of a question, into its words, its commands and its links: what is
// between backticks, and a djinn command named in plain text up to a comma, a semicolon or the end of the line, are
// commands; a web address or a djinn:// link outside them is a link.
export function commandParts(text: string): TextPart[] {
  const parts: TextPart[] = [];
  const push = (part: TextPart) => {
    if (!part.text) return;
    if (part.code) parts.push(part);
    else parts.push(...linkParts(part.text));
  };
  text.split(/(`[^`\n]+`)/).forEach((piece, i) => {
    if (i % 2) {
      push({ text: piece.slice(1, -1), code: true });
      return;
    }
    let at = 0;
    for (const m of piece.matchAll(DJINN_LINE)) {
      const command = m[2].replace(/[.:!?]+$/, "");
      const start = m.index + m[1].length;
      push({ text: piece.slice(at, start) });
      push({ text: command, code: true });
      at = start + command.length;
    }
    push({ text: piece.slice(at) });
  });
  return parts;
}
