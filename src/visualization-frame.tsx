import { useEffect, useRef, useState } from "react";
import type { Artifact } from "./types";
import {
  visualizationDocument,
  visualizationMessage,
} from "./visualization-document";
import { t } from "./i18n";
export function VisualizationFrame({ artifact }: { artifact: Artifact }) {
  const frame = useRef<HTMLIFrameElement>(null),
    [url, setUrl] = useState(""),
    [height, setHeight] = useState(420),
    [error, setError] = useState(""),
    [source, setSource] = useState(false);
  useEffect(() => {
    let active = true,
      blob = "";
    const token = crypto.randomUUID();
    const tokens: string[] = [token];
    setError("");
    setUrl("");
    setHeight(420);
    const receive = (event: MessageEvent) => {
      const message = visualizationMessage(
        event,
        frame.current?.contentWindow || null,
        tokens,
      );
      if (message && "height" in message) setHeight(message.height!);
      if (message && "error" in message) setError(message.error!);
    };
    window.addEventListener("message", receive);
    try {
      const document = visualizationDocument(artifact.content, token);
      if (window.djinn?.renderVisualization)
        void window.djinn
          .renderVisualization(document)
          .then((result) => {
            if (active) {
              tokens.push(result.token);
              setUrl(result.url);
            }
          })
          .catch(() => active && setError(t("visualization.prepare_failed")));
      else {
        blob = URL.createObjectURL(new Blob([document], { type: "text/html" }));
        setUrl(blob);
      }
    } catch (e) {
      setError((e as Error).message);
    }
    return () => {
      active = false;
      window.removeEventListener("message", receive);
      if (blob) URL.revokeObjectURL(blob);
    };
  }, [artifact.id, artifact.content]);
  return (
    <div className="visualization-support">
      <div className="art-stage-caption">
        <span>{t("visualization.caption")}</span>
        <button className="text-button" onClick={() => setSource(!source)}>
          {source
            ? t("visualization.show_visualization")
            : t("visualization.show_source")}
        </button>
      </div>
      {error ? (
        <p role="alert">{t("visualization.unavailable", { error })}</p>
      ) : null}
      {!url && !error && <p role="status">{t("visualization.preparing")}</p>}
      <iframe
        ref={frame}
        title={artifact.title}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        allow="camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'"
        src={url || undefined}
        style={{
          width: "100%",
          height,
          display: source ? "none" : "block",
          border: 0,
        }}
        onError={() => setError(t("visualization.load_failed"))}
      />
      {source && <pre className="visualization-source">{artifact.content}</pre>}
    </div>
  );
}
