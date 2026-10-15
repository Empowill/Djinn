// A live HTML block of a tilasm: HTML that runs between the paragraphs (a chart, a comparison, a small simulator). In
// Markdown it is a fenced code block whose info string is `html live`, so it stays plain, readable Markdown wherever it
// goes and an agent writes one as easily as any code. A fence that says only `html` stays an example: shown as code,
// never run. The block keeps its source byte for byte: it is an atom whose one attribute is the fence's body.
//
// How it shows (a sandboxed frame) is the editor's: src/tilasm-html-view.tsx.
import { mergeAttributes, Node, type NodeViewRenderer } from "@tiptap/core";
import { NodeSelection } from "@tiptap/pm/state";

export const HTML_LIVE = "htmlLive";

// The event Enter sends a selected block's view, to show its HTML (src/tilasm-html-view.tsx).
export const EDIT_EVENT = "tilasm-html-edit";

// The first line of a live block's fence: three or more backticks or tildes, then `html live`.
const opening = /^ {0,3}(?:`{3,}|~{3,})[ \t]*html[ \t]+live[ \t]*$/i;

// isLiveFence tells a fenced code block's raw Markdown that opens a live block.
export function isLiveFence(raw: string): boolean {
  return opening.test(raw.split("\n", 1)[0]!.replace(/\r$/, ""));
}

// liveFence writes a live block back: a fence longer than any run of backticks in the source, so that no line of it
// closes the block early.
export function liveFence(source: string): string {
  const longest = Math.max(
    0,
    ...(source.match(/`+/g) ?? []).map((run) => run.length),
  );
  const fence = "`".repeat(Math.max(3, longest + 1));
  return source
    ? `${fence}html live\n${source}\n${fence}`
    : `${fence}html live\n${fence}`;
}

export interface HtmlLiveOptions {
  // The node view the editor shows it with; none outside a page (the Markdown manager of the tests).
  view: NodeViewRenderer | null;
}

export const HtmlLive = Node.create<HtmlLiveOptions>({
  name: HTML_LIVE,
  group: "block",
  atom: true,
  selectable: true,
  draggable: false,
  // Above the code block's handler, which reads every fence otherwise; below the paragraph, the schema's default block.
  priority: 110,

  addOptions() {
    return { view: null };
  },

  addAttributes() {
    return {
      source: {
        default: "",
        // The source is the element's text, never an attribute: copied out, the block reads as an HTML code example.
        parseHTML: (element) => element.textContent ?? "",
        renderHTML: () => ({}),
      },
    };
  },

  parseHTML() {
    return [{ tag: "pre[data-html-live]", priority: 100 }];
  },

  renderHTML({ node, HTMLAttributes }) {
    return [
      "pre",
      mergeAttributes(HTMLAttributes, { "data-html-live": "" }),
      ["code", { class: "language-html" }, String(node.attrs.source ?? "")],
    ];
  },

  renderText({ node }) {
    return String(node.attrs.source ?? "");
  },

  markdownTokenName: "code",

  parseMarkdown: (token, helpers) => {
    if (typeof token.raw !== "string" || !isLiveFence(token.raw)) return [];
    return helpers.createNode(HTML_LIVE, {
      source: typeof token.text === "string" ? token.text : "",
    });
  },

  renderMarkdown: (node) => liveFence(String(node.attrs?.source ?? "")),

  addNodeView() {
    return this.options.view;
  },

  addKeyboardShortcuts() {
    return {
      // Enter on a selected block shows its HTML to edit.
      Enter: ({ editor }) => {
        const { selection } = editor.state;
        if (!editor.isEditable || !(selection instanceof NodeSelection))
          return false;
        if (selection.node.type.name !== HTML_LIVE) return false;
        const dom = editor.view.nodeDOM(selection.from);
        if (!(dom instanceof HTMLElement)) return false;
        dom.dispatchEvent(new CustomEvent(EDIT_EVENT));
        return true;
      },
    };
  },
});
