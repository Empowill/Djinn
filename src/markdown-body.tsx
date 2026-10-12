import { ChevronDown } from "lucide-react";
import { memo, useMemo, useState, isValidElement, type ReactNode } from "react";
import ReactMarkdown, {
  type Components,
  defaultUrlTransform,
} from "react-markdown";
import remarkGfm from "remark-gfm";
import { useDjinn } from "./data/djinn";
import { isDjinnLink, openDjinnLink } from "./data/links";
import { foldMarkdown } from "./data/text";
import { MermaidDiagram } from "./mermaid-diagram";
import { SafeSvg } from "./safe-svg";
import "./agent-chat.css";
import { Command } from "./copy-command";
import { t } from "./i18n";
function MarkdownCode({ children }: { children?: ReactNode }) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  const code = isValidElement<{ children?: ReactNode; className?: string }>(
    children,
  )
    ? children
    : undefined;
  const text = String(code?.props.children || "").replace(/\n$/, "");
  if (code?.props.className === "language-mermaid")
    return <MermaidDiagram source={text} />;
  if (code?.props.className === "language-svg")
    return <SafeSvg source={text} />;
  return (
    <div className="ac-code">
      <div className="ac-code-heading">
        <span>
          {code?.props.className?.replace(/^language-/, "") ||
            t("markdown.code")}
        </span>
        <button
          type="button"
          aria-label={
            failed ? t("markdown.copy_failed") : t("markdown.copy_code")
          }
          className="ac-copy"
          onClick={() =>
            void navigator.clipboard
              .writeText(text)
              .then(() => {
                setCopied(true);
                setFailed(false);
              })
              .catch(() => setFailed(true))
          }
        >
          {failed
            ? t("markdown.copy_failed")
            : copied
              ? t("markdown.copied")
              : t("markdown.copy")}
        </button>
      </div>
      <pre>{children}</pre>
    </div>
  );
}
// A link: a djinn:// one opens what it names in place, a tilasm or a wish; in the window, the system's browser opens
// an http(s) one; anything else stays text.
function MarkdownLink({
  href,
  children,
}: {
  href?: string;
  children?: ReactNode;
}) {
  const djinn = useDjinn();
  if (href && isDjinnLink(href))
    return (
      <a
        href={href}
        className="djinn-link"
        onClick={(e) => {
          e.preventDefault();
          if (djinn) void openDjinnLink(djinn, href);
        }}
      >
        {children}
      </a>
    );
  if (!href || !/^https?:\/\//i.test(href)) return <span>{children}</span>;
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      onClick={(e) => {
        if (!djinn) return;
        e.preventDefault();
        void djinn.clients.ui
          .openExternal({ url: href })
          .catch(() => window.open(href, "_blank", "noopener"));
      }}
    >
      {children}
    </a>
  );
}

// Stable across renders: a components object made in the render gives react-markdown a new component type each time,
// so every code block and every Mermaid frame below it was unmounted and mounted again at each render of the card,
// at each key typed in its comment box.
const plugins = [remarkGfm];
// react-markdown empties a link of a scheme it does not know: djinn:// is Djinn's own.
const urlTransform = (url: string) =>
  isDjinnLink(url) ? url : defaultUrlTransform(url);
const components: Components = {
  pre: ({ children }) => <MarkdownCode>{children}</MarkdownCode>,
  a: ({ href, children }) => (
    <MarkdownLink href={href}>{children}</MarkdownLink>
  ),
  img: ({ alt }) => <span>Image{alt ? ` : ${alt}` : ""}</span>,
};
// The same, each inline code a command to copy: a question's context names what to run. A code block's code holds a
// line break and stays as it is, inside its block, which has its own copy button.
const copyComponents: Components = {
  ...components,
  code: ({ className, children }) =>
    className || /\n/.test(String(children)) ? (
      <code className={className}>{children}</code>
    ) : (
      <Command text={String(children)} />
    ),
};

// Markdown as the agents write it. Memoised: a card that re-renders (a key typed in its comment box) does not parse
// its text again.
export const MarkdownBody = memo(function MarkdownBody({
  text,
  copy = false,
}: {
  text: string;
  // Each inline code has a copy button.
  copy?: boolean;
}) {
  return (
    <div className="ac-markdown">
      <ReactMarkdown
        remarkPlugins={plugins}
        skipHtml
        components={copy ? copyComponents : components}
        urlTransform={urlTransform}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
});

// FoldedMarkdown is a long Markdown text in its first view (src/data/text.ts), its first section or so, and a button
// that unfolds the rest in place, and folds it again.
export const FoldedMarkdown = memo(function FoldedMarkdown({
  text,
  more,
  less,
  copy = false,
}: {
  text: string;
  // What the button says, folded and unfolded.
  more: string;
  less: string;
  copy?: boolean;
}) {
  const fold = useMemo(() => foldMarkdown(text), [text]);
  const [open, setOpen] = useState(false);
  return (
    <>
      <div className={`prose${fold.more && !open ? " folded-text" : ""}`}>
        <MarkdownBody
          text={fold.more && !open ? fold.head : text}
          copy={copy}
        />
      </div>
      {fold.more && (
        <button
          type="button"
          className="read-more"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          <ChevronDown
            size={14}
            aria-hidden="true"
            className={open ? "rotated" : ""}
          />
          {open ? less : more}
        </button>
      )}
    </>
  );
});
