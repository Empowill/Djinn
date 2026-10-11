import { memo } from "react";
import "./svg-diagrams.css";

const DANGEROUS_TAGS = [
  "script",
  "foreignobject",
  "iframe",
  "object",
  "embed",
  "applet",
  "audio",
  "video",
  "form",
  "input",
  "button",
  "link",
  "meta",
];

// sanitizeSvg strips scripts, dangerous elements, event handlers, and external URLs from SVG markup.
export function sanitizeSvg(raw: string): string {
  if (!raw) return "";
  const start = raw.search(/<svg\b/i);
  const end = raw.toLowerCase().lastIndexOf("</svg>");
  if (start === -1 || end === -1 || end < start) {
    return "";
  }
  let s = raw.slice(start, end + 6);

  // 1. Remove XML comments
  s = s.replace(/<!--[\s\S]*?-->/g, "");

  // 2. Strip dangerous elements and their contents
  for (const tag of DANGEROUS_TAGS) {
    const rePair = new RegExp(`<${tag}\\b[\\s\\S]*?<\\/${tag}\\s*>`, "gi");
    s = s.replace(rePair, "");
    const reSingle = new RegExp(`<${tag}\\b[^>]*\\/?>`, "gi");
    s = s.replace(reSingle, "");
    const reClose = new RegExp(`<\\/${tag}\\s*>`, "gi");
    s = s.replace(reClose, "");
  }

  // 3. Strip event handlers (onload, onclick, onerror, etc.)
  s = s.replace(/\s+on[a-z0-9_-]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)/gi, "");

  // 4. Sanitize href and xlink:href (only allow local fragment #...)
  s = s.replace(
    /\s+(?:xlink:)?href\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/gi,
    (match, p1, p2, p3) => {
      const val = (p1 ?? p2 ?? p3 ?? "").trim();
      return val.startsWith("#") ? match : "";
    },
  );

  // 5. Strip src attributes
  s = s.replace(/\s+src\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)/gi, "");

  // 6. Clean style tags / attributes of non-local url(), expression(), @import
  s = s.replace(/<style\b[\s\S]*?<\/style\s*>/gi, (match) => {
    return match
      .replace(/@import\b[^;]*;?/gi, "")
      .replace(/url\s*\(\s*(?!['"]?#)[^)]*\)/gi, "none")
      .replace(/expression\s*\([^)]*\)/gi, "none");
  });
  s = s.replace(/\s+style\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)/gi, (match) => {
    return match
      .replace(/url\s*\(\s*(?!['"]?#)[^)]*\)/gi, "none")
      .replace(/expression\s*\([^)]*\)/gi, "none");
  });

  return s;
}

export const SafeSvg = memo(function SafeSvg({
  source,
  className,
}: {
  source: string;
  className?: string;
}) {
  const sanitized = sanitizeSvg(source);
  if (!sanitized) return null;
  return (
    <div
      className={className ? `dia-frame ${className}` : "dia-frame"}
      // eslint-disable-next-line @eslint-react/dom-no-dangerously-set-innerhtml -- Sanitized SVG.
      dangerouslySetInnerHTML={{ __html: sanitized }}
    />
  );
});
