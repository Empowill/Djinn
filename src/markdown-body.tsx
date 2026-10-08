import { useState, isValidElement, type ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useDjinn } from "./data/djinn";
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
export function MarkdownBody({ text }: { text: string }) {
  const djinn = useDjinn();
  return (
    <div className="ac-markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          pre: ({ children }) => <MarkdownCode>{children}</MarkdownCode>,
          a: ({ href, children }) =>
            !href || !/^https?:\/\//i.test(href) ? (
              <span>{children}</span>
            ) : (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer"
                onClick={(e) => {
                  // In the window, the system's browser opens the link.
                  if (!djinn) return;
                  e.preventDefault();
                  void djinn.clients.ui
                    .openExternal({ url: href })
                    .catch(() => window.open(href, "_blank", "noopener"));
                }}
              >
                {children}
              </a>
            ),
          img: ({ alt }) => <span>Image{alt ? ` : ${alt}` : ""}</span>,
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}
