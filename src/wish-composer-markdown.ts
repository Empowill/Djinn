// Only the editor's visible text and supported formatting cross the API boundary. HTML, URLs and attributes
// from the DOM never become executable content in the prompt.
export const MAX_PROMPT_CHARACTERS = 1_000_000;

export function promptTooLong(prompt: string): boolean {
  // Protobuf string limits count Unicode code points, rather than UTF-16 code units.
  if (prompt.length <= MAX_PROMPT_CHARACTERS) return false;
  let count = 0;
  for (const _character of prompt) {
    if (++count > MAX_PROMPT_CHARACTERS) return true;
  }
  return false;
}

function escapeText(text: string): string {
  return text
    .replace(/\u00a0/g, " ")
    .replace(/&/g, "&amp;")
    .replace(/([\\`*_{}\[\]<>])/g, "\\$1")
    .replace(/^(\s*)(#{1,6}|[+\-]|\d+[.)])(?=\s)/gm, "$1\\$2");
}

function marked(text: string, marker: string): string {
  // Markdown delimiters must touch non-whitespace; native formatting can include a trailing space.
  return text.replace(/^(\s*)([\s\S]*?\S)(\s*)$/, `$1${marker}$2${marker}$3`);
}

function inline(node: Node): string {
  if (node.nodeType === Node.TEXT_NODE)
    return escapeText(node.textContent ?? "");
  if (!(node instanceof HTMLElement)) return "";
  if (["SCRIPT", "STYLE", "IFRAME", "OBJECT"].includes(node.tagName)) return "";
  if (node.tagName === "BR") return "\n";
  if (node.tagName === "UL" || node.tagName === "OL") return list(node);
  const text = Array.from(node.childNodes).map(inline).join("");
  if (node.matches("b,strong")) return marked(text, "**");
  if (node.matches("i,em")) return marked(text, "*");
  // WebKit can express native editing commands as spans instead of semantic elements.
  let result = text;
  if (node.style.fontStyle === "italic") result = marked(result, "*");
  if (node.style.fontWeight === "bold" || Number(node.style.fontWeight) >= 600)
    result = marked(result, "**");
  return result;
}

function list(element: HTMLElement): string {
  const start = Number(element.getAttribute("start")) || 1;
  return Array.from(element.children)
    .filter((node) => node.tagName === "LI")
    .map((item, index) => {
      const parts = Array.from(item.childNodes);
      const nested = parts.filter(
        (node) => node instanceof HTMLElement && node.matches("ul,ol"),
      );
      const body = blocks(
        parts.filter((node) => !nested.includes(node)),
      ).trim();
      const marker = element.tagName === "OL" ? `${start + index}. ` : "- ";
      const indent = " ".repeat(marker.length);
      const continuation = body.replace(/\n/g, `\n${indent}`);
      const children = nested.map((node) =>
        list(node as HTMLElement)
          .split("\n")
          .map((line) => indent + line)
          .join("\n"),
      );
      return (
        marker +
        continuation +
        (children.length ? "\n" + children.join("\n") : "")
      );
    })
    .join("\n");
}

function blocks(nodes: Node[]): string {
  let result = "";
  let afterBlock = false;
  for (const node of nodes) {
    const block =
      node instanceof HTMLElement &&
      node.matches("div,p,ul,ol,h1,h2,h3,h4,h5,h6,blockquote");
    if (block || afterBlock) {
      if (result && !result.endsWith("\n\n"))
        result += result.endsWith("\n") ? "\n" : "\n\n";
    }
    result +=
      block && node instanceof HTMLElement && !node.matches("ul,ol")
        ? blocks(Array.from(node.childNodes))
        : inline(node);
    afterBlock = block;
  }
  return result;
}

export function composerMarkdown(editor: HTMLElement): string {
  return blocks(Array.from(editor.childNodes))
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}
