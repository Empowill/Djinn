// A tilasm on claude.ai (W493, L56): its row in the Tilasms tab says where it is published and whether its latest
// version is, "Up to date" or "v3 published, v4 waiting", and publishes it, as a private artifact of the developer's
// Claude account. The button shows only when this machine can publish: Claude Code signed in and its Artifact tool
// offered, which the server checks once per start (TilasmService.CheckPublish). The first publish of each tilasm asks
// first: the page leaves for claude.ai, and the developer checks it holds no personal data of a client, its images
// included (Q134). A publish again on its address asks no more.
import { CloudUpload, ExternalLink, LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";

import { type Tilasm } from "../gen/ts/plan/v1/tilasm_pb";
import { useClients } from "./data/djinn";
import { t } from "./i18n";
import { Link } from "./link";

const latest = (tilasm: Tilasm) => tilasm.versions.at(-1)?.number ?? 0;

// needsConfirm tells whether publishing the tilasm asks the developer first: it has neither an address nor their
// go-ahead.
export const needsConfirm = (tilasm: Tilasm) =>
  !tilasm.artifact?.url && !tilasm.artifact?.confirmTime;

// checked is the server's answer for this window: it checks once per start of Djinn, the window asks once.
let checked: Promise<boolean> | undefined;

// useCanPublish tells whether this machine can publish tilasms; false until the server says so.
export function useCanPublish(): boolean {
  const clients = useClients();
  const [can, setCan] = useState(false);
  useEffect(() => {
    let live = true;
    checked ??= clients.tilasms
      .checkPublish({})
      .then((res) => res.available)
      .catch(() => {
        checked = undefined; // Asked again next time: the server may come back.
        return false;
      });
    void checked.then((ok) => live && setCan(ok));
    return () => {
      live = false;
    };
  }, [clients]);
  return can;
}

// ArtifactState is a tilasm's place on claude.ai, on a line of its row: its link and whether its latest version is
// published, and that it is being published. Empty: nothing shows.
export function ArtifactState({
  tilasm,
  publishing,
}: {
  tilasm: Tilasm;
  // This tilasm is being published.
  publishing: boolean;
}) {
  const url = tilasm.artifact?.url ?? "";
  const published = tilasm.artifact?.version ?? 0;
  const waiting = published < latest(tilasm);
  if (!url && !publishing) return null;
  return (
    <span className="tilasm-published">
      {url && (
        <Link href={url} className="tilasm-artifact">
          <ExternalLink size={13} aria-hidden="true" />
          <span>{t("tilasms.artifact_open")}</span>
        </Link>
      )}
      {url && (
        <span className={`tilasm-artifact-state ${waiting ? "waiting" : ""}`}>
          {waiting
            ? t("tilasms.artifact_waiting", {
                published,
                latest: latest(tilasm),
              })
            : t("tilasms.artifact_current")}
        </span>
      )}
      {publishing && (
        <span className="tilasm-publishing" role="status">
          <LoaderCircle size={13} aria-hidden="true" />
          {t("tilasms.publishing")}
        </span>
      )}
    </span>
  );
}

// PublishButton publishes a tilasm, among its row's actions: one never published, or a version behind; none while it
// is being published, nor when the machine cannot.
export function PublishButton({
  tilasm,
  canPublish,
  publishing,
  onPublish,
}: {
  tilasm: Tilasm;
  canPublish: boolean;
  publishing: boolean;
  onPublish: (tilasm: Tilasm) => void;
}) {
  const url = tilasm.artifact?.url ?? "";
  const waiting = (tilasm.artifact?.version ?? 0) < latest(tilasm);
  if (!canPublish || publishing || (url && !waiting)) return null;
  const label = url ? t("tilasms.republish") : t("tilasms.publish");
  return (
    <button
      type="button"
      className="icon-button"
      title={label}
      aria-label={label}
      onClick={() => onPublish(tilasm)}
    >
      <CloudUpload size={14} />
    </button>
  );
}

// PublishConfirm asks the developer, before the first publish of a tilasm, to check what leaves for claude.ai.
export function PublishConfirm({
  tilasm,
  onConfirm,
  onCancel,
}: {
  tilasm: Tilasm;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div
      className="tilasm-publish-confirm"
      role="group"
      aria-label={t("tilasms.publish_confirm_label", { code: tilasm.code })}
    >
      <p>{t("tilasms.publish_confirm_where")}</p>
      <p>
        <strong>{t("tilasms.publish_confirm_check")}</strong>
      </p>
      <div className="tilasm-publish-buttons">
        <button
          type="button"
          className="button accent small"
          onClick={onConfirm}
        >
          <CloudUpload size={13} aria-hidden="true" />
          {t("tilasms.publish_confirm")}
        </button>
        <button
          type="button"
          className="button secondary small"
          onClick={onCancel}
        >
          {t("tilasms.publish_cancel")}
        </button>
      </div>
    </div>
  );
}
