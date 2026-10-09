import { useEffect, useId, useRef, useState } from "react";
import {
  visualizationMessage,
  VISUALIZATION_POLICY,
} from "./visualization-document";
import "./mermaid-diagram.css";
import { t } from "./i18n";
import { token, useShownTheme } from "./theme";

// Load the locally bundled engine only when a Markdown Mermaid block is visible.
// It executes in a scripts-only iframe, never in the page itself.
let engine: Promise<string> | undefined;
const loadEngine = () =>
  (engine ||= import("./vendor/mermaid-11.16.1.min.js?raw").then(
    (m) => m.default,
  ));
const scriptLiteral = (value: string) =>
  JSON.stringify(value).replaceAll("<", "\\u003c");

// The diagram's colours: the theme on the page, from the tokens of theme.css.
function diagramLook(theme: "dark" | "light") {
  const [ground, text, box, border, line] = [
    "n-19",
    "n-e7",
    "n-28",
    "n-65",
    "n-a9",
  ].map(token);
  return {
    css: `:root{color-scheme:${theme}}body{margin:12px;background:${ground};color:${text};font:12px/1.6 system-ui}`,
    config: {
      theme: theme === "light" ? "neutral" : "dark",
      themeVariables: {
        fontSize: "12px",
        background: ground,
        primaryColor: box,
        primaryTextColor: text,
        primaryBorderColor: border,
        lineColor: line,
        textColor: text,
      },
    },
  };
}

function DiagramFrame({ source }: { source: string }) {
  const theme = useShownTheme();
  const frame = useRef<HTMLIFrameElement>(null);
  const [url, setUrl] = useState("");
  const [height, setHeight] = useState(240);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true,
      blob = "";
    const token = crypto.randomUUID();
    setError("");
    setUrl("");
    const receive = (event: MessageEvent) => {
      const result = visualizationMessage(
        event,
        frame.current?.contentWindow || null,
        [token],
      );
      if (result && "height" in result) setHeight(result.height!);
      if (result && "error" in result) setError(result.error!);
    };
    window.addEventListener("message", receive);
    if (source.length > 30000) setError(t("mermaid.too_long"));
    else
      void loadEngine()
        .then((runtime) => {
          if (!active) return;
          const look = diagramLook(theme);
          const html = `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${VISUALIZATION_POLICY}"><style>${look.css}svg{max-width:100%;height:auto;display:block;margin:auto}#render{overflow:auto}</style></head><body><div id="render"></div><script>${runtime.replaceAll("</script", "<\\/script")}</script><script>(async()=>{const token=${scriptLiteral(token)},send=(type,data)=>parent.postMessage({type,token,...data},'*');try{mermaid.initialize({startOnLoad:false,securityLevel:'strict',fontFamily:'system-ui',...${JSON.stringify(look.config).replaceAll("<", "\\u003c")},flowchart:{htmlLabels:false},maxTextSize:30000,suppressErrorRendering:true});const {svg}=await mermaid.render('diagram',${scriptLiteral(source)});document.getElementById('render').innerHTML=svg;const report=()=>send('djinn:visualization-height',{height:Math.min(4000,Math.max(120,Math.ceil(document.documentElement.scrollHeight)))});new ResizeObserver(report).observe(document.body);report()}catch(e){send('djinn:visualization-error',{message:${scriptLiteral(t("mermaid.render_failed"))}})}})();</script></body></html>`;
          blob = URL.createObjectURL(new Blob([html], { type: "text/html" }));
          setUrl(blob);
        })
        .catch(() => active && setError(t("mermaid.engine_failed")));
    return () => {
      active = false;
      window.removeEventListener("message", receive);
      if (blob) URL.revokeObjectURL(blob);
    };
  }, [source, theme]);
  return (
    <>
      {error ? (
        <p role="alert">{error}</p>
      ) : !url ? (
        <p role="status">{t("mermaid.preparing")}</p>
      ) : null}
      {url && !error ? (
        <iframe
          ref={frame}
          title={t("mermaid.diagram")}
          src={url}
          sandbox="allow-scripts"
          referrerPolicy="no-referrer"
          allow="camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'"
          style={{ width: "100%", height, border: 0 }}
        />
      ) : null}
    </>
  );
}

export function MermaidDiagram({ source }: { source: string }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const id = useId();
  const [showSource, setShowSource] = useState(false);
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    if (expanded) dialog.current?.showModal();
    else dialog.current?.close();
  }, [expanded]);
  return (
    <section className="mermaid-support" aria-label={t("mermaid.artifact")}>
      <div className="mermaid-toolbar">
        <span>Mermaid</span>
        <button
          type="button"
          aria-expanded={showSource}
          aria-controls={id}
          onClick={() => setShowSource(!showSource)}
        >
          {showSource
            ? t("mermaid.show_diagram")
            : t("visualization.show_source")}
        </button>
        <button type="button" onClick={() => setExpanded(true)}>
          {t("mermaid.expand")}
        </button>
      </div>
      <div hidden={showSource}>
        <DiagramFrame source={source} />
      </div>
      <pre id={id} hidden={!showSource}>
        {source}
      </pre>
      <dialog
        ref={dialog}
        className="mermaid-dialog"
        onCancel={() => setExpanded(false)}
        onClose={() => setExpanded(false)}
        aria-label={t("mermaid.expanded")}
      >
        <div className="mermaid-toolbar">
          <strong>{t("mermaid.diagram")}</strong>
          <button type="button" onClick={() => setExpanded(false)}>
            {t("common.close")}
          </button>
        </div>
        {expanded ? <DiagramFrame source={source} /> : null}
      </dialog>
    </section>
  );
}
