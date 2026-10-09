import { memo, useState, isValidElement, type ReactNode } from "react";
import ReactMarkdown, {
  type Components,
  defaultUrlTransform,
} from "react-markdown";
import remarkGfm from "remark-gfm";
import { useDjinn } from "./data/djinn";
import { isDjinnLink, openDjinnLink } from "./data/links";
import { MermaidDiagram } from "./mermaid-diagram";
import "./agent-chat.css";
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

// Markdown as the agents write it. Memoised: a card that re-renders (a key typed in its comment box) does not parse
// its text again.
export const MarkdownBody = memo(function MarkdownBody({
  text,
}: {
  text: string;
}) {
  return (
    <div className="ac-markdown">
      <ReactMarkdown
        remarkPlugins={plugins}
        skipHtml
        components={components}
        urlTransform={urlTransform}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
});
