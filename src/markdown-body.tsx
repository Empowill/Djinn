import { useState, isValidElement, type ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { MermaidDiagram } from "./mermaid-diagram";
import "./agent-chat.css";
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
          {code?.props.className?.replace(/^language-/, "") || "Code"}
        </span>
        <button
          type="button"
          aria-label={failed ? "Copie impossible" : "Copier le code"}
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
          {failed ? "Copie impossible" : copied ? "Copié" : "Copier"}
        </button>
      </div>
      <pre>{children}</pre>
    </div>
  );
}
export function MarkdownBody({ text }: { text: string }) {
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
                  if (window.djinn) {
                    e.preventDefault();
                    void window.djinn.openExternal(href);
                  }
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
