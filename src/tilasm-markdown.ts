// The Markdown of a tilasm, as the window's editor reads and writes it (src/tilasms.tsx). Markdown is the reference
// both the developer and the agents share: the editor (Tiptap, its Markdown extension) parses it in and writes it back
// out. What the editor cannot keep (raw HTML, images, footnotes, or text a round trip changes) is found before any
// edit, so a document is never saved through a lossy conversion without the developer asking for it.
import type { AnyExtension } from "@tiptap/core";
import { TableKit } from "@tiptap/extension-table";
import { TaskItem, TaskList } from "@tiptap/extension-list";
import { Placeholder } from "@tiptap/extensions";
import { Markdown, type MarkdownManager } from "@tiptap/markdown";
import StarterKit from "@tiptap/starter-kit";

// tilasmExtensions are the blocks and marks the editor offers, each one written back to Markdown: paragraphs,
// headings, lists (task lists too), quotes, code, rules, hard breaks, links, bold, italic, strike, inline code and GFM
// tables. No underline: Markdown has none.
// placeholder is what an empty block says, emptyNodeClass the class it takes then (the editor's stylesheet styles it).
export function tilasmExtensions({
  placeholder = "",
  emptyNodeClass = "",
}: { placeholder?: string; emptyNodeClass?: string } = {}): AnyExtension[] {
  return [
    StarterKit.configure({
      underline: false,
      link: {
        openOnClick: false,
        autolink: true,
        linkOnPaste: true,
      },
      // The line that shows where a dragged block lands.
      dropcursor: { color: "var(--accent)", width: 2 },
    }),
    TaskList,
    TaskItem.configure({ nested: true }),
    TableKit.configure({ table: { resizable: false } }),
    Placeholder.configure({ placeholder, emptyNodeClass }),
    Markdown.configure({ indentation: { style: "space", size: 2 } }),
  ];
}

// The kinds of content the editor would not keep, named to the developer.
export type TilasmLoss = "html" | "image" | "footnote" | "content";

interface Token {
  type?: string;
  raw?: string;
  text?: string;
  href?: string;
  tag?: string;
  tokens?: Token[];
  items?: Token[];
  header?: Token[];
  rows?: Token[][];
}

// children are the tokens a token holds, whatever its kind: inline tokens, list items, table cells.
function children(token: Token): Token[] | undefined {
  const out: Token[] = [];
  if (Array.isArray(token.tokens)) out.push(...token.tokens);
  if (Array.isArray(token.items)) out.push(...token.items);
  if (Array.isArray(token.header)) out.push(...token.header);
  if (Array.isArray(token.rows)) for (const row of token.rows) out.push(...row);
  return out.length > 0 ? out : undefined;
}

function walk(tokens: readonly Token[], visit: (token: Token) => void) {
  for (const token of tokens) {
    visit(token);
    const inner = children(token);
    if (inner) walk(inner, visit);
  }
}

const entities: Record<string, string> = {
  amp: "&",
  lt: "<",
  gt: ">",
  quot: '"',
  apos: "'",
  nbsp: " ",
};

function decode(text: string): string {
  return text.replace(
    /&(#x[0-9a-f]+|#[0-9]+|[a-z]+);/gi,
    (all, name: string) => {
      if (name[0] === "#")
        return String.fromCodePoint(
          name[1] === "x" || name[1] === "X"
            ? parseInt(name.slice(2), 16)
            : parseInt(name.slice(1), 10),
        );
      return entities[name.toLowerCase()] ?? all;
    },
  );
}

// signature is what a text says, without its Markdown: its letters and digits, then the addresses of its links. Two
// texts that differ only by how they are written (a bullet's mark, an escape, a table's padding) sign alike.
function signature(manager: MarkdownManager, markdown: string): string {
  const words: string[] = [];
  const hrefs: string[] = [];
  walk(manager.instance.lexer(markdown) as Token[], (token) => {
    if (token.type === "def") return; // Resolved into its links.
    if (token.href) hrefs.push(token.href);
    if (!children(token) && typeof (token.text ?? token.raw) === "string")
      words.push(decode(token.text ?? token.raw ?? ""));
  });
  const letters = words.join(" ").replace(/[^\p{L}\p{N}]+/gu, "");
  return `${letters}\n${hrefs.join("\n")}`;
}

// tilasmLoss names what the editor would lose of markdown: constructs it has no block for, and any text a round trip
// (parsed, then written back) changes. Empty: the editor keeps it all.
export function tilasmLoss(
  manager: MarkdownManager,
  markdown: string,
): TilasmLoss[] {
  if (!markdown.trim()) return [];
  const lost = new Set<TilasmLoss>();
  walk(manager.instance.lexer(markdown) as Token[], (token) => {
    if (token.type === "html") lost.add("html");
    if (token.type === "image") lost.add("image");
    if (token.type === "def" && token.tag?.startsWith("^"))
      lost.add("footnote");
  });
  if (/\[\^[^\]\s]+\]/.test(markdown) && /^\s*\[\^[^\]\s]+\]:/m.test(markdown))
    lost.add("footnote");
  // Any other change a round trip makes, named only when nothing more precise was.
  if (lost.size === 0) {
    const written = manager.serialize(manager.parse(markdown));
    if (signature(manager, markdown) !== signature(manager, written))
      lost.add("content");
  }
  return [...lost];
}

// splitTitle takes the tilasm's title out of its Markdown: a first # heading that says the title shows as the title
// line, not again in the body. headed tells to write it back on save; a document with no text yet gets one too, so
// that its index.md names itself for the agents.
export function splitTitle(
  markdown: string,
  title: string,
): { headed: boolean; body: string } {
  if (!markdown.trim()) return { headed: true, body: "" };
  const first = /^[ \t]*#[ \t]+(.*?)[ \t]*#*[ \t]*(?:\r?\n|$)/.exec(markdown);
  if (first && first[1]!.trim() === title.trim())
    return {
      headed: true,
      body: markdown.slice(first[0].length).replace(/^(?:[ \t]*\r?\n)+/, ""),
    };
  return { headed: false, body: markdown };
}

// joinTitle is the Markdown to save: the title as its first heading when it had one, then the body.
export function joinTitle(
  headed: boolean,
  title: string,
  body: string,
): string {
  const line = title.replace(/\s+/g, " ").trim();
  if (!headed || !line) return body;
  return body.trim() ? `# ${line}\n\n${body}` : `# ${line}\n`;
}
