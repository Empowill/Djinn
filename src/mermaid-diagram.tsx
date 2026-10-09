import { useEffect, useId, useRef, useState } from "react";
import { visualizationMessage } from "./visualization-document";
import { MERMAID_FRAME } from "./mermaid-frame";
import "./mermaid-diagram.css";
import { t } from "./i18n";
import { token, useShownTheme } from "./theme";

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
    scheme: theme,
    ground,
    text,
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

// The diagram draws in the frame the server serves (mermaid-frame.ts), which loads the bundled engine only when a
// Markdown Mermaid block is on the page. The page posts it the source and the theme; it answers the height it drew.
function DiagramFrame({ source }: { source: string }) {
  const theme = useShownTheme();
  const frame = useRef<HTMLIFrameElement>(null);
  const [loaded, setLoaded] = useState(false);
  const [drawn, setDrawn] = useState(false);
  const [height, setHeight] = useState(240);
  const [error, setError] = useState("");
  const tooLong = source.length > 30000;
  useEffect(() => {
    const target = frame.current?.contentWindow;
    setError("");
    if (!loaded || !target || tooLong) return;
    const token = crypto.randomUUID();
    const receive = (event: MessageEvent) => {
      const result = visualizationMessage(event, target, [token]);
      if (result && "height" in result) {
        setHeight(result.height!);
        setDrawn(true);
      }
      if (result && "error" in result)
        setError(
          result.error === "engine"
            ? t("mermaid.engine_failed")
            : t("mermaid.render_failed"),
        );
    };
    window.addEventListener("message", receive);
    target.postMessage(
      { type: "djinn:mermaid-render", token, source, ...diagramLook(theme) },
      "*",
    );
    return () => window.removeEventListener("message", receive);
  }, [source, theme, loaded, tooLong]);
  const shown = error || (tooLong && t("mermaid.too_long"));
  return (
    <>
      {shown ? (
        <p role="alert">{shown}</p>
      ) : !drawn ? (
        <p role="status">{t("mermaid.preparing")}</p>
      ) : null}
      <iframe
        ref={frame}
        title={t("mermaid.diagram")}
        src={MERMAID_FRAME}
        hidden={Boolean(shown)}
        onLoad={() => setLoaded(true)}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        allow="camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'"
        style={{ width: "100%", height, border: 0 }}
      />
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
