// How the editor shows a live HTML block (src/tilasm-html.ts): its HTML runs in a frame that blends into the page, with
// no header, badge or border, in the document's colours and type, as tall as what it draws. The frame is the document
// tilasm-html-frame.html, loaded by its address and sandboxed with scripts only: an opaque origin that reads nothing of
// Djinn's, calls none of its services and reaches no network. A click inside it stays inside it: the editor keeps its
// selection.
//
// The block is selected as a whole by a click on its margin, and moved by the editor's block handle. Selected, Enter
// or a double-click shows its HTML in place, as plain text to edit; Escape or leaving the field shows it running again.
import type { Editor, NodeViewRenderer } from "@tiptap/core";
import type { Node as PMNode } from "@tiptap/pm/model";
import { NodeSelection } from "@tiptap/pm/state";

import { t } from "./i18n";
import { EDIT_EVENT } from "./tilasm-html";
import { onTheme, shownTheme, token } from "./theme";

// The frame's document, which the build copies next to the page's scripts.
const FRAME = new URL("./tilasm-html-frame.html", import.meta.url).href;

// The block's own policy, written again with its document: nothing loads but inline code and data: addresses.
const POLICY =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; " +
  "connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'";

// The tallest a block may say it is, in pixels: a page that grows without end stops there.
const TALLEST = 20000;

// The script each block's document starts with: it tells the page the height of what it draws whenever that changes,
// and takes the page's colours when its theme changes.
const RUNTIME = `(()=>{const root=document.documentElement;let last=-1;const send=()=>{const body=document.body;if(!body)return;const height=Math.ceil(Math.max(body.scrollHeight,body.getBoundingClientRect().height));if(height===last)return;last=height;parent.postMessage({type:"djinn:html-live-height",height},"*")};addEventListener("message",(event)=>{const m=event.data;if(event.source!==parent||!m||m.type!=="djinn:html-live-theme"||!m.vars||typeof m.vars!=="object")return;for(const [name,value] of Object.entries(m.vars))if(/^--[a-z0-9-]+$/.test(name)&&typeof value==="string")root.style.setProperty(name,value);if(m.scheme==="light"||m.scheme==="dark")root.style.colorScheme=m.scheme});const watch=()=>{const observer=new ResizeObserver(send);observer.observe(document.body);observer.observe(root);send()};if(document.body)watch();else addEventListener("DOMContentLoaded",watch);addEventListener("load",send)})();`;

// The page's colours and type, as the block's document reads them: the tokens of theme.css under their own names, and
// the text's colour, size and line height as the editor shows them.
function look(prose: Element) {
  const style = getComputedStyle(prose);
  const vars: Record<string, string> = {
    "--text": style.color,
    "--font-size": style.fontSize,
    "--line-height": style.lineHeight,
    "--font-sans": token("font-sans"),
    "--mono": token("mono"),
  };
  for (const name of [
    "bg",
    "surface",
    "surface-raised",
    "surface-soft",
    "line",
    "line-strong",
    "muted",
    "muted-dim",
    "accent",
  ])
    vars[`--${name}`] = token(name);
  // A value that would end the declaration or the style element is dropped.
  for (const [name, value] of Object.entries(vars))
    if (!value || /[;{}<>]/.test(value)) delete vars[name];
  return { scheme: shownTheme(), vars };
}

// The faces of DM Sans the page has loaded, as data: addresses: the block loads no file, so its document carries them.
let faces: Promise<string> | undefined;
function dmSans(): Promise<string> {
  faces ??= (async () => {
    const found: { url: string; weight: string; style: string }[] = [];
    for (const sheet of Array.from(document.styleSheets)) {
      let rules: CSSRuleList;
      try {
        rules = sheet.cssRules;
      } catch {
        continue; // Another origin's sheet.
      }
      for (const rule of Array.from(rules)) {
        if (!(rule instanceof CSSFontFaceRule)) continue;
        const family = rule.style.getPropertyValue("font-family");
        if (family.replace(/["']/g, "").trim() !== "DM Sans") continue;
        const src = /url\(\s*["']?([^"')]+\.woff2[^"')]*)["']?\s*\)/.exec(
          rule.style.getPropertyValue("src"),
        );
        if (!src) continue;
        found.push({
          url: new URL(src[1]!, sheet.href ?? document.baseURI).href,
          weight: rule.style.getPropertyValue("font-weight") || "400",
          style: rule.style.getPropertyValue("font-style") || "normal",
        });
      }
    }
    const read = async ({ url, weight, style }: (typeof found)[number]) => {
      const blob = await (await fetch(url)).blob();
      const data = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result));
        reader.onerror = () => reject(reader.error);
        reader.readAsDataURL(blob);
      });
      if (!/^data:[\w/+.-]+;base64,[\w+/=]+$/.test(data)) return "";
      return `@font-face{font-family:"DM Sans";font-style:${style};font-weight:${weight};font-display:block;src:url(${data}) format("woff2")}`;
    };
    const all = await Promise.all(
      found.map((face) => read(face).catch(() => "")),
    );
    return all.join("");
  })().catch(() => "");
  return faces;
}

// blockDocument is the whole document a block runs: its policy, the page's look, the runtime, then its HTML as written.
function blockDocument(
  source: string,
  fonts: string,
  { scheme, vars }: ReturnType<typeof look>,
): string {
  const declared = Object.entries(vars)
    .map(([name, value]) => `${name}:${value};`)
    .join("");
  const style =
    `${fonts}:root{color-scheme:${scheme};${declared}}` +
    "html,body{margin:0;padding:0;background:transparent;overflow:hidden}" +
    "body{display:flow-root;color:var(--text);font-family:var(--font-sans);font-size:var(--font-size);" +
    "line-height:var(--line-height);overflow-wrap:break-word}";
  return (
    `<!doctype html><html><head><meta charset="utf-8">` +
    `<meta http-equiv="Content-Security-Policy" content="${POLICY}">` +
    `<meta name="referrer" content="no-referrer">` +
    `<style>${style}</style><script>${RUNTIME}</script></head>` +
    `<body>${source}</body></html>`
  );
}

// The height each source last drew at: a block drawn again (another document, the same one reopened) keeps its place.
const heights = new Map<string, number>();

// The editors whose next new empty block opens its HTML at once: the one the "/" menu inserts.
const opening = new WeakSet<Editor>();

export function openNextBlock(editor: Editor) {
  opening.add(editor);
  requestAnimationFrame(() => opening.delete(editor));
}

class LiveBlock {
  dom: HTMLElement;
  private stage: HTMLElement;
  private frame: HTMLIFrameElement | null = null;
  private field: HTMLTextAreaElement | null = null;
  private visible = false;
  private source: string;
  private stopTheme: () => void;
  private seen: IntersectionObserver | null = null;

  constructor(
    private node: PMNode,
    private editor: Editor,
    private getPos: () => number | undefined,
  ) {
    this.source = String(node.attrs.source ?? "");
    this.dom = document.createElement("div");
    this.dom.className = "tilasm-html";
    this.dom.contentEditable = "false";
    this.stage = document.createElement("div");
    this.stage.className = "tilasm-html-stage";
    this.dom.append(this.stage);
    this.dom.addEventListener("dblclick", this.onDoubleClick);
    this.dom.addEventListener(EDIT_EVENT, this.open);
    window.addEventListener("message", this.onMessage);
    this.stopTheme = onTheme(this.onTheme);
    // A block off screen draws when it comes near.
    if (typeof IntersectionObserver === "function") {
      this.seen = new IntersectionObserver(
        (entries) => {
          if (!entries.some((entry) => entry.isIntersecting)) return;
          this.visible = true;
          this.seen?.disconnect();
          this.seen = null;
          this.render();
        },
        { rootMargin: "600px 0px" },
      );
      this.seen.observe(this.dom);
    } else this.visible = true;
    this.render();
    if (opening.has(editor) && !this.source) {
      opening.delete(editor);
      requestAnimationFrame(this.open);
    }
  }

  // render shows the block running, or says it is empty; a block not yet near the screen keeps its place.
  private render() {
    this.frame?.remove();
    this.frame = null;
    this.stage.replaceChildren();
    if (!this.source.trim()) {
      const empty = document.createElement("p");
      empty.className = "tilasm-html-empty";
      empty.textContent = t("tilasms.html.empty");
      this.stage.append(empty);
      this.stage.style.minHeight = "";
      return;
    }
    const known = heights.get(this.source);
    this.stage.style.minHeight = `${known ?? 120}px`;
    if (!this.visible) return;
    const frame = document.createElement("iframe");
    frame.className = "tilasm-html-frame is-drawing";
    frame.setAttribute("sandbox", "allow-scripts");
    frame.setAttribute("referrerpolicy", "no-referrer");
    frame.title = t("tilasms.html.frame");
    frame.style.colorScheme = shownTheme();
    if (known) frame.style.height = `${known}px`;
    frame.src = FRAME;
    this.frame = frame;
    this.stage.append(frame);
  }

  private onMessage = (event: MessageEvent) => {
    const frame = this.frame;
    if (!frame || event.source !== frame.contentWindow) return;
    if (event.origin !== "null") return;
    const data: unknown = event.data;
    if (!data || typeof data !== "object" || Array.isArray(data)) return;
    const message = data as Record<string, unknown>;
    if (message.type === "djinn:html-live-ready") {
      const source = this.source;
      void dmSans().then((fonts) => {
        if (this.frame !== frame || this.source !== source) return;
        frame.contentWindow?.postMessage(
          {
            type: "djinn:html-live-render",
            document: blockDocument(source, fonts, look(this.dom)),
          },
          "*",
        );
      });
      return;
    }
    if (
      message.type === "djinn:html-live-height" &&
      typeof message.height === "number" &&
      Number.isFinite(message.height) &&
      message.height >= 0
    ) {
      const height = Math.min(TALLEST, Math.ceil(message.height));
      heights.set(this.source, height);
      frame.style.height = `${height}px`;
      frame.classList.remove("is-drawing");
      this.stage.style.minHeight = "";
    }
  };

  private onTheme = () => {
    const frame = this.frame;
    if (!frame) return;
    const shown = look(this.dom);
    frame.style.colorScheme = shown.scheme;
    frame.contentWindow?.postMessage(
      { type: "djinn:html-live-theme", ...shown },
      "*",
    );
  };

  private onDoubleClick = (event: MouseEvent) => {
    if (this.field?.contains(event.target as globalThis.Node)) return;
    event.preventDefault();
    this.open();
  };

  // open shows the block's HTML in place of the frame, to edit, when the editor is editable.
  private open = () => {
    if (this.field || !this.editor.isEditable) return;
    const field = document.createElement("textarea");
    field.className = "tilasm-html-source";
    field.value = this.source;
    field.spellcheck = false;
    field.setAttribute("aria-label", t("tilasms.html.source"));
    const fit = () => {
      field.style.height = "auto";
      field.style.height = `${field.scrollHeight + 2}px`;
    };
    field.addEventListener("input", fit);
    field.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      this.close(true);
    });
    field.addEventListener("blur", () => this.close(false));
    this.field = field;
    this.stage.hidden = true;
    this.dom.classList.add("is-editing");
    this.dom.append(field);
    fit();
    field.focus();
  };

  // close keeps what was written and shows the block running again; reselect gives the selection back to the block.
  private close(reselect: boolean) {
    const field = this.field;
    if (!field) return;
    this.field = null;
    const written = field.value;
    field.remove();
    this.stage.hidden = false;
    this.dom.classList.remove("is-editing");
    const pos = this.getPos();
    if (typeof pos !== "number") return;
    const { view } = this.editor;
    let tr = view.state.tr;
    if (written !== this.source)
      tr = tr.setNodeMarkup(pos, undefined, {
        ...this.node.attrs,
        source: written,
      });
    if (reselect) tr = tr.setSelection(NodeSelection.create(tr.doc, pos));
    if (tr.docChanged || reselect) view.dispatch(tr);
    if (reselect) view.focus();
  }

  update(node: PMNode): boolean {
    if (node.type !== this.node.type) return false;
    this.node = node;
    const source = String(node.attrs.source ?? "");
    if (source === this.source) return true;
    this.source = source;
    if (this.field) this.field.value = source;
    else this.render();
    return true;
  }

  // The field's own keys, clicks and text stay its own: the editor does not read them as its document's.
  stopEvent(event: Event): boolean {
    return !!this.field && this.field.contains(event.target as globalThis.Node);
  }

  ignoreMutation(): boolean {
    return true;
  }

  // Selected as a whole, it shows an outline (tilasms.css).
  selectNode() {
    this.dom.classList.add("is-selected");
  }

  deselectNode() {
    this.dom.classList.remove("is-selected");
  }

  destroy() {
    this.dom.removeEventListener("dblclick", this.onDoubleClick);
    this.dom.removeEventListener(EDIT_EVENT, this.open);
    window.removeEventListener("message", this.onMessage);
    this.stopTheme();
    this.seen?.disconnect();
  }
}

export const htmlLiveView: NodeViewRenderer = ({ node, editor, getPos }) =>
  new LiveBlock(node, editor, getPos);
