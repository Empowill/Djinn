// The Tilasms tab of a wish: the material that explains it (T29), a folder with an index.html kept in Djinn's data
// folder. A row per tilasm, by code: its title, who made it, when, what it cites. One opens in a frame of its own,
// served by Djinn at /tilasm/<id>/ with a policy of its own (internal/plan/tilasm_files.go): its scripts run, it reaches
// no network and nothing of Djinn. A search finds words in the titles and the text, within the wish; "talisman" finds
// them all. A row's history restores an earlier version; its export writes a .zip to the Downloads folder. A folder or
// a .zip dropped on the tab becomes a tilasm, or the new version of the one a .zip exported.
import {
  Download,
  FolderInput,
  History,
  RotateCcw,
  Search,
  X,
} from "lucide-react";
import { type DragEvent, useEffect, useState } from "react";

import { Change, type Task } from "../gen/ts/plan/v1/plan_pb";
import type { Tilasm } from "../gen/ts/plan/v1/tilasm_pb";
import { message } from "./data/client";
import { useClients, useStore } from "./data/djinn";
import { when } from "./data/format";
import { type DropEntry, TooLarge, entriesOf, readDrop } from "./data/tilasms";
import { t } from "./i18n";
import { memory } from "./usage";

import "./tilasms.css";

// tilasmAddress is where Djinn serves a tilasm's latest version.
export const tilasmAddress = (id: string) => `/tilasm/${id}/`;

const latest = (tilasm: Tilasm) => tilasm.versions.at(-1)?.number ?? 0;

export function TilasmsTab({
  wishId,
  opening,
  tilasms,
  tasks,
  onToast,
}: {
  wishId: string;
  // The tilasm a djinn:// link asks to show: opened in the frame, again at each new request.
  opening?: { tilasmId: string };
  // The wish's tilasms, by code.
  tilasms: readonly Tilasm[];
  tasks: readonly Task[];
  onToast: (text: string) => void;
}) {
  const clients = useClients();
  const store = useStore();
  const [search, setSearch] = useState("");
  // What the search found, by the server; null without a search.
  const [found, setFound] = useState<Tilasm[] | null>(null);
  const [openId, setOpenId] = useState(opening?.tilasmId ?? "");
  useEffect(() => {
    if (opening) setOpenId(opening.tilasmId);
  }, [opening]);
  const [history, setHistory] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!search.trim()) return setFound(null);
    let live = true;
    // A search a keystroke at a time: the last one wins.
    const timer = setTimeout(() => {
      clients.tilasms
        .list({ wish: wishId, search })
        .then((res) => live && setFound(res.tilasms))
        .catch((error) => live && onToast(message(error)));
    }, 120);
    return () => {
      live = false;
      clearTimeout(timer);
    };
    // The wish's tilasms changing searches again; a new onToast does not.
  }, [clients, wishId, search, tilasms]);
  const run = async (write: () => Promise<string>) => {
    try {
      onToast(await write());
    } catch (error) {
      onToast(
        error instanceof TooLarge ? t("tilasms.too_large") : message(error),
      );
    } finally {
      void store.changed(wishId, [Change.TILASM]);
    }
  };
  const ref = (tilasm: Tilasm) => ({
    ref: { case: "id" as const, value: tilasm.id },
  });
  return (
    <TilasmList
      tilasms={found ?? tilasms}
      count={tilasms.length}
      codes={new Map(tasks.map((task) => [task.id, task.code]))}
      search={search}
      onSearch={setSearch}
      open={tilasms.find((tilasm) => tilasm.id === openId)}
      onOpen={setOpenId}
      history={history}
      onHistory={(id) => setHistory(history === id ? "" : id)}
      onRestore={(tilasm, version) =>
        void run(async () => {
          await clients.tilasms.restore({
            tilasm: ref(tilasm),
            version,
            author: "developer",
          });
          return t("tilasms.restored", { code: tilasm.code, version });
        })
      }
      onExport={(tilasm) =>
        void run(async () => {
          const res = await clients.tilasms.export({ tilasm: ref(tilasm) });
          return t("tilasms.exported", { file: res.file });
        })
      }
      busy={busy}
      onDrop={(entries) => {
        setBusy(true);
        void run(async () => {
          const dropped = await readDrop(entries);
          if (dropped.files.length === 0) return t("tilasms.drop_empty");
          const res = await clients.tilasms.putData({
            wish: wishId,
            name: dropped.name,
            files: dropped.files,
          });
          const code = res.tilasm?.code ?? "";
          setOpenId(res.tilasm?.id ?? "");
          return res.imported
            ? t("tilasms.imported", { code })
            : t("tilasms.put", { code });
        }).finally(() => setBusy(false));
      }}
    />
  );
}

// TilasmList is the tab as it shows, from what it is given.
export function TilasmList({
  tilasms,
  count,
  codes,
  search,
  onSearch,
  open,
  onOpen,
  history,
  onHistory,
  onRestore,
  onExport,
  busy = false,
  onDrop,
}: {
  // What shows: the wish's tilasms, or those the search found.
  tilasms: readonly Tilasm[];
  // How many the wish has.
  count: number;
  // The codes of the wish's tasks and azimas, by id: what a tilasm cites.
  codes: ReadonlyMap<string, string>;
  search: string;
  onSearch: (text: string) => void;
  // The tilasm shown in the frame.
  open?: Tilasm;
  // Shows a tilasm in the frame; "" closes it.
  onOpen: (id: string) => void;
  // The tilasm whose versions show.
  history: string;
  onHistory: (id: string) => void;
  onRestore: (tilasm: Tilasm, version: number) => void;
  onExport: (tilasm: Tilasm) => void;
  // A drop is being put.
  busy?: boolean;
  onDrop: (entries: DropEntry[]) => void;
}) {
  const [over, setOver] = useState(false);
  const dragging = (event: DragEvent) => {
    if (!event.dataTransfer?.types.includes("Files")) return;
    event.preventDefault();
    setOver(true);
  };
  return (
    <div
      role="tabpanel"
      aria-labelledby="view-tab-tilasms"
      className={`tilasms ${over ? "dropping" : ""}`}
      onDragEnter={dragging}
      onDragOver={dragging}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null))
          setOver(false);
      }}
      onDrop={(event) => {
        event.preventDefault();
        setOver(false);
        const entries = entriesOf(event.dataTransfer);
        if (entries.length) onDrop(entries);
      }}
    >
      <div className="tilasm-bar">
        <label className="tilasm-search">
          <Search size={13} />
          <input
            type="search"
            value={search}
            placeholder={t("tilasms.search")}
            aria-label={t("tilasms.search")}
            onChange={(event) => onSearch(event.target.value)}
          />
        </label>
        <span className="tilasm-drop">
          <FolderInput size={13} />
          {busy ? t("tilasms.putting") : t("tilasms.drop")}
        </span>
      </div>
      {open && (
        <section className="tilasm-view" aria-label={open.title}>
          <div className="tilasm-view-head">
            <span className="tilasm-code">{open.code}</span>
            <strong>{open.title}</strong>
            <span className="muted-text">
              {t("tilasms.version", { version: latest(open) })}
            </span>
            <button
              type="button"
              className="icon-button"
              title={t("tilasms.close")}
              aria-label={t("tilasms.close")}
              onClick={() => onOpen("")}
            >
              <X size={14} />
            </button>
          </div>
          {/* Scripts only: an opaque origin, never Djinn's; the server's policy refuses the network. */}
          <iframe
            key={`${open.id}-${latest(open)}`}
            className="tilasm-frame"
            title={`${open.code} · ${open.title}`}
            src={tilasmAddress(open.id)}
            sandbox="allow-scripts"
            referrerPolicy="no-referrer"
          />
        </section>
      )}
      {count === 0 ? (
        <p className="muted-text">{t("tilasms.none")}</p>
      ) : tilasms.length === 0 ? (
        <p className="muted-text">{t("tilasms.none_found", { search })}</p>
      ) : (
        <ul className="tilasm-list" aria-label={t("tabs.tilasms")}>
          {tilasms.map((tilasm) => (
            <li
              key={tilasm.id}
              id={`tilasm-${tilasm.id}`}
              className={`tilasm-row ${open?.id === tilasm.id ? "open" : ""}`}
            >
              <div className="tilasm-line">
                <span className="tilasm-code">{tilasm.code}</span>
                <button
                  type="button"
                  className="text-button tilasm-title"
                  title={t("tilasms.open")}
                  onClick={() => onOpen(tilasm.id)}
                >
                  {tilasm.title}
                </button>
                <span className="tilasm-meta">
                  {tilasm.author} · {when(tilasm.updateTime)}
                </span>
                {tilasm.cites.length > 0 && (
                  <span className="tilasm-cites">
                    {t("tilasms.cites")}{" "}
                    {tilasm.cites.map((id) => (
                      <span key={id} className="tilasm-code">
                        {codes.get(id) ?? id.slice(0, 8)}
                      </span>
                    ))}
                  </span>
                )}
                <span className="tilasm-actions">
                  <button
                    type="button"
                    className="icon-button"
                    aria-expanded={history === tilasm.id}
                    title={t("tilasms.history")}
                    aria-label={t("tilasms.history")}
                    onClick={() => onHistory(tilasm.id)}
                  >
                    <History size={14} />
                  </button>
                  <button
                    type="button"
                    className="icon-button"
                    title={t("tilasms.export")}
                    aria-label={t("tilasms.export")}
                    onClick={() => onExport(tilasm)}
                  >
                    <Download size={14} />
                  </button>
                </span>
              </div>
              {history === tilasm.id && (
                <ol className="tilasm-history" reversed>
                  {[...tilasm.versions].reverse().map((v) => (
                    <li key={v.number}>
                      <span className="tilasm-code">
                        {t("tilasms.version", { version: v.number })}
                      </span>
                      <span className="tilasm-meta">
                        {v.author} · {when(v.createTime)} ·{" "}
                        {t("tilasms.files", { count: v.files })} ·{" "}
                        {memory(v.size)}
                        {v.restoredFrom > 0 &&
                          ` · ${t("tilasms.restored_from", { version: v.restoredFrom })}`}
                      </span>
                      {v.number !== latest(tilasm) && (
                        <button
                          type="button"
                          className="text-button"
                          onClick={() => onRestore(tilasm, v.number)}
                        >
                          <RotateCcw size={13} />
                          {t("tilasms.restore")}
                        </button>
                      )}
                    </li>
                  ))}
                </ol>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
