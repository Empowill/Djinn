// The Tilasms tab of a wish: the material that explains it (T25), an .html file or a folder with an index.html kept in
// Djinn's data folder. A regular card list in a single left column, newest first: its title, who made it, when, what it
// cites. One opens in a frame of its own, served by Djinn at /tilasm/<id>/ with a policy of its own
// (internal/plan/tilasm_files.go): its scripts run, it reaches no network and nothing of Djinn. A search finds words in
// the titles and the text, within the wish; "talisman" finds them all. A row's history restores an earlier version; its
// export writes a .zip to the Downloads folder. An .html file, a folder or a .zip dropped on the tab or chosen in the
// drop zone becomes a tilasm, or the new version of the one a .zip exported. The open one goes full size: its frame
// covers the main shell, the side panel beside it; leaving puts the tab back as it was, the frame never reloaded. The
// same frame opens over any view, full size, as a link in a question asks (tilasm-over.tsx). Beside the frame in the
// tab, full size and over a view too, the developer's draft comments on it (tilasm-comments.tsx): counted on its head,
// on its row and on the tab. A passage selected in the frame is commented from a button over it, its drafts highlighted in the page
// (tilasm-passages.tsx); Aim mode comments an element, and numbered dots in a margin beside the frame stand at the
// height of each anchor (tilasm-margin.tsx). A new version places the anchors again: the window writes where each
// stands, found, changed or lost.
import {
  Crosshair,
  Download,
  FolderInput,
  History,
  Maximize2,
  MessageSquare,
  Minimize2,
  RotateCcw,
  Search,
  X,
} from "lucide-react";
import { type DragEvent, useEffect, useRef, useState } from "react";

import { Change, type Task } from "../gen/ts/plan/v1/plan_pb";
import {
  AnchorStatus,
  SendChoice,
  type Tilasm,
  type TilasmComment,
  type TilasmHold,
} from "../gen/ts/plan/v1/tilasm_pb";
import { type Pick, marksOf } from "./data/bridge";
import { message } from "./data/client";
import {
  cards,
  draftCounts,
  draftsOn,
  placesToWrite,
  sentOn,
} from "./data/comments";
import { later } from "./data/decisions";
import { useClients, useStore } from "./data/djinn";
import { compareCodes } from "./data/flight";
import { when } from "./data/format";
import {
  type DropEntry,
  TooLarge,
  entriesFromFiles,
  entriesOf,
  readDrop,
} from "./data/tilasms";
import { useFullSize } from "./full-size";
import { t } from "./i18n";
import {
  type CommentWrites,
  CommentsColumn,
  DraftDot,
} from "./tilasm-comments";
import { AimBox, Margin, useAim, usePlaced } from "./tilasm-margin";
import { CommentPick, usePassages } from "./tilasm-passages";
import { memory } from "./usage";

import "./tilasms.css";

// tilasmAddress is where Djinn serves a tilasm's latest version.
export const tilasmAddress = (id: string) => `/tilasm/${id}/`;

const latest = (tilasm: Tilasm) => tilasm.versions.at(-1)?.number ?? 0;

const tilasmTime = (tilasm: Tilasm) => {
  const last = tilasm.versions.at(-1)?.createTime;
  if (tilasm.updateTime && last)
    return later(tilasm.updateTime, last) <= 0 ? tilasm.updateTime : last;
  return tilasm.updateTime ?? last ?? tilasm.createTime;
};

// newestTilasm orders tilasms from the most recent to the oldest.
export const newestTilasm = (a: Tilasm, b: Tilasm): number =>
  later(tilasmTime(a), tilasmTime(b)) ||
  later(a.createTime, b.createTime) ||
  compareCodes(b.code, a.code);

export function TilasmsTab({
  wishId,
  opening,
  tilasms,
  drafts = [],
  holds = [],
  sent = [],
  tasks,
  onToast,
}: {
  wishId: string;
  // The tilasm a djinn:// link asks to show: opened in the frame, again at each new request.
  opening?: { tilasmId: string };
  // The wish's tilasms, by code.
  tilasms: readonly Tilasm[];
  // The developer's draft comments on them.
  drafts?: readonly TilasmComment[];
  // Why some take no send for now, as the server computes it.
  holds?: readonly TilasmHold[];
  // The comments sent on them, with the Djinn's replies.
  sent?: readonly TilasmComment[];
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
    // eslint-disable-next-line react-hooks/exhaustive-deps, @eslint-react/exhaustive-deps -- onToast left out, see above
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
  const comments = useCommentWrites(wishId, onToast);
  return (
    <TilasmList
      tilasms={found ?? tilasms}
      drafts={drafts}
      holds={holds}
      sent={sent}
      comments={comments}
      onCommentError={(error) => onToast(message(error))}
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

// useCommentWrites is what writes the developer's comments on a wish's tilasms, in the Tilasms tab and over a view;
// onToast tells what a send did.
export function useCommentWrites(
  wishId: string,
  onToast: (text: string) => void,
): CommentWrites {
  const clients = useClients();
  const store = useStore();
  // Each write reads the drafts again before it resolves: the card shows what was stored, never what it held before.
  const reread = () => store.changed(wishId, [Change.TILASM]);
  return {
    put: async (tilasmId, text, id, anchor) => {
      await clients.tilasms.putComment({ tilasmId, id, text, anchor });
      await reread();
    },
    place: async (tilasmId, version, places) => {
      await clients.tilasms.placeComments({ tilasmId, version, places });
      await reread();
    },
    remove: async (comment) => {
      const res = await clients.tilasms.deleteComment({ id: comment.id });
      await reread();
      return res.comment ?? comment;
    },
    putBack: async (comment) => {
      await clients.tilasms.putComment({
        tilasmId: comment.tilasmId,
        deleted: comment,
      });
      await reread();
    },
    send: async (tilasmId, choice, text) => {
      const res = await clients.tilasms.sendComments({
        tilasmId,
        choice,
        text,
      });
      await reread();
      const send = res.send?.number ?? 0;
      onToast(
        res.answered
          ? t("tilasms.sent_answered", { question: res.answered })
          : choice === SendChoice.ITERATE
            ? t("tilasms.sent_iterate", { send })
            : t("tilasms.sent_launch", { send }),
      );
    },
  };
}

// TilasmList is the tab as it shows, from what it is given.
export function TilasmList({
  tilasms,
  drafts = [],
  holds = [],
  sent = [],
  comments,
  onCommentError = () => {},
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
  // The developer's draft comments on the wish's tilasms.
  drafts?: readonly TilasmComment[];
  // Why some take no send for now.
  holds?: readonly TilasmHold[];
  // The comments sent on them, with the Djinn's replies.
  sent?: readonly TilasmComment[];
  // What writes them; without it, the open tilasm shows no comments.
  comments?: CommentWrites;
  onCommentError?: (error: unknown) => void;
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
  // The tilasm shown full size: the open one, until it is left or closed.
  const [fullId, setFullId] = useState("");
  const full = open !== undefined && fullId === open.id;
  const dragging = (event: DragEvent) => {
    if (!event.dataTransfer?.types.includes("Files")) return;
    event.preventDefault();
    setOver(true);
  };
  const sorted = [...tilasms].sort(newestTilasm);
  const counts = draftCounts(drafts);
  return (
    <div
      role="tabpanel"
      aria-labelledby="view-tab-tilasms"
      className={`tilasms ${over ? "dropping" : ""} ${open ? "has-open" : ""}`}
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
        <label className={`tilasm-drop ${over ? "dropping" : ""}`}>
          <FolderInput size={14} />
          <span>{busy ? t("tilasms.putting") : t("tilasms.drop")}</span>
          <input
            type="file"
            accept=".html,.htm,.zip,text/html,application/zip"
            disabled={busy}
            aria-label={t("tilasms.drop")}
            onChange={(event) => {
              const entries = entriesFromFiles(event.target.files);
              event.target.value = "";
              if (entries.length) onDrop(entries);
            }}
          />
        </label>
      </div>
      {open && (
        <TilasmFrame
          tilasm={open}
          full={full}
          onFull={(on) => setFullId(on ? open.id : "")}
          onClose={() => {
            setFullId("");
            onOpen("");
          }}
          drafts={draftsOn(drafts, open.id)}
          sent={sentOn(sent, open.id)}
          codes={codes}
          hold={holds.find((h) => h.tilasmId === open.id)}
          comments={comments}
          onCommentError={onCommentError}
        />
      )}
      {count === 0 ? (
        <p className="muted-text">{t("tilasms.none")}</p>
      ) : sorted.length === 0 ? (
        <p className="muted-text">{t("tilasms.none_found", { search })}</p>
      ) : (
        <ul className="tilasm-list" aria-label={t("tabs.tilasms")}>
          {sorted.map((tilasm) => (
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
                  {tilasm.author} · {when(tilasmTime(tilasm))}
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
                  {(counts.get(tilasm.id) ?? 0) > 0 && (
                    <DraftDot count={counts.get(tilasm.id) ?? 0} />
                  )}
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

// TilasmFrame is a tilasm in its frame. In the Tilasms tab, under its head, in the page, it goes full size and back;
// over a view, as a link in a question opens it (tilasm-over.tsx), it is full size from the start, with no head, and
// leaving it closes it; it is commented there as in the tab, from the buttons it has full size.
export function TilasmFrame({
  tilasm,
  full,
  onFull,
  onClose,
  over = false,
  drafts = [],
  sent = [],
  codes,
  hold,
  comments,
  onCommentError = () => {},
}: {
  tilasm: Tilasm;
  // Full size: the frame covers the main shell. Over a view, it always is.
  full: boolean;
  onFull: (full: boolean) => void;
  onClose: () => void;
  // Opened over a view: full size, and its button closes it.
  over?: boolean;
  // The developer's drafts on it, in the order written.
  drafts?: readonly TilasmComment[];
  // The comments sent on it, with the Djinn's replies, in the order written.
  sent?: readonly TilasmComment[];
  // The codes of the wish's tasks, by id: those a reply names.
  codes?: ReadonlyMap<string, string>;
  // Why it takes no send for now; none: it does.
  hold?: TilasmHold;
  // What writes them: the Comments column shows beside the frame, on demand; without it, none.
  comments?: CommentWrites;
  onCommentError?: (error: unknown) => void;
}) {
  const [commenting, setCommenting] = useState(false);
  const enabled = comments !== undefined;
  const column = enabled && commenting;
  const frame = useRef<HTMLIFrameElement>(null);
  const version = latest(tilasm);
  // Its comments in the page, drafts and sent alike: highlighted, placed again in a new version, dotted in the margin.
  const all = cards(drafts, sent);
  // What the developer asked to comment, until the column takes it; what the card being written is on, highlighted.
  const [ask, setAsk] = useState<Pick>();
  const [writing, setWriting] = useState<Pick>();
  // The card open from its dot, and the detached draft waiting for a passage or an element to be on.
  const [active, setActive] = useState("");
  const [reattaching, setReattaching] = useState("");
  const [aiming, setAiming] = useState(false);
  const picked = (pick: Pick) => {
    const draft = drafts.find((c) => c.id === reattaching);
    setReattaching("");
    if (draft && comments) {
      // Reattached: on the version shown, where it is found.
      comments
        .put(tilasm.id, draft.text, draft.id, {
          ...pick,
          status: AnchorStatus.FOUND,
          statusVersion: version,
        })
        .catch(onCommentError);
      return;
    }
    setCommenting(true);
    setAsk(pick);
  };
  const passages = usePassages(
    frame,
    enabled,
    marksOf(all, writing, active),
    version,
    picked,
  );
  const hover = useAim(frame, enabled, aiming, setAiming, picked);
  const placed = usePlaced(frame, enabled, version);
  // Placed in a later version than they were written on: where each stands is written once, for the cards and the lead.
  const toWrite = enabled ? placesToWrite(all, placed, version) : [];
  const writeKey = toWrite.length > 0 ? JSON.stringify(toWrite) : "";
  const placing = useRef({ comments, onCommentError, tilasmId: tilasm.id });
  placing.current = { comments, onCommentError, tilasmId: tilasm.id };
  useEffect(() => {
    if (!writeKey) return;
    const {
      comments: writes,
      onCommentError: failed,
      tilasmId,
    } = placing.current;
    writes
      ?.place(
        tilasmId,
        version,
        JSON.parse(writeKey) as ReturnType<typeof placesToWrite>,
      )
      .catch(failed);
  }, [writeKey, version]);
  const reveal = (id: string) =>
    frame.current?.contentWindow?.postMessage(
      { djinn: "tilasm", type: "reveal", id },
      "*",
    );
  const dotted =
    enabled &&
    all.some((c) => c.anchor?.exact || c.anchor?.path || c.anchor?.elementId);
  const enter = useRef<HTMLButtonElement>(null);
  const leave = useRef<HTMLButtonElement>(null);
  useFullSize(
    full || over,
    frame,
    enter,
    leave,
    over ? onClose : () => onFull(false),
  );
  const aim = (className: string) =>
    enabled && (
      <button
        type="button"
        className={`${className ? "icon-button" : "text-button"} tilasm-aim${className ? ` ${className}` : ""}`}
        aria-pressed={aiming}
        title={t("tilasms.aim_title")}
        aria-label={className ? t("tilasms.aim") : undefined}
        aria-keyshortcuts="Escape"
        data-full-keep={className ? "" : undefined}
        onClick={() => setAiming(!aiming)}
      >
        <Crosshair size={13} aria-hidden="true" />
        {!className && t("tilasms.aim")}
      </button>
    );
  const toggle = (className: string) =>
    enabled && (
      <button
        type="button"
        className={`icon-button tilasm-comments-toggle${className ? ` ${className}` : ""}`}
        aria-pressed={commenting}
        title={`${t("tilasms.comments")} · ${t("tilasms.drafts", { count: drafts.length })}`}
        aria-label={`${t("tilasms.comments")} · ${t("tilasms.drafts", { count: drafts.length })}`}
        data-full-keep={className ? "" : undefined}
        onClick={() => setCommenting(!commenting)}
      >
        <MessageSquare size={14} aria-hidden="true" />
        <span className={drafts.length > 0 ? "draft-count" : undefined}>
          {drafts.length}
        </span>
      </button>
    );
  return (
    <section
      className={`tilasm-view${over ? " over full-over" : full ? " full" : ""}${column ? " commenting" : ""}${dotted ? " dotted" : ""}`}
      aria-label={tilasm.title}
    >
      {!over && (
        <div className="tilasm-view-head">
          <span className="tilasm-code">{tilasm.code}</span>
          <strong>{tilasm.title}</strong>
          <span className="muted-text">
            {t("tilasms.version", { version: latest(tilasm) })}
          </span>
          <button
            ref={enter}
            type="button"
            className="text-button tilasm-full-enter"
            onClick={() => onFull(true)}
          >
            <Maximize2 size={13} aria-hidden="true" />
            {t("tilasms.full")}
          </button>
          {aim("")}
          {toggle("")}
          <button
            type="button"
            className="icon-button"
            title={t("tilasms.close")}
            aria-label={t("tilasms.close")}
            onClick={onClose}
          >
            <X size={14} />
          </button>
        </div>
      )}
      {/* Scripts only: an opaque origin, never Djinn's; the server's policy refuses the network. Full size changes
          the section's class only, never the frame's place in the page: a frame moved elsewhere would load again. */}
      <iframe
        key={`${tilasm.id}-${latest(tilasm)}`}
        ref={frame}
        className="tilasm-frame"
        title={`${tilasm.code} · ${tilasm.title}`}
        src={tilasmAddress(tilasm.id)}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
      />
      {/* The room the frame leaves in the page, full size: what it covers keeps its place and its scroll. It shows
          by the same class that takes the frame out, so the page never lays out without both. Over a view, the frame
          takes no room. */}
      {!over && <div className="tilasm-frame-room" />}
      {dotted && (
        <Margin
          comments={all}
          placed={placed}
          active={active}
          onDot={(id) => {
            setCommenting(true);
            setActive(id);
          }}
        />
      )}
      {column && (
        <CommentsColumn
          key={tilasm.id}
          tilasmId={tilasm.id}
          code={tilasm.code}
          version={latest(tilasm)}
          drafts={drafts}
          sent={sent}
          codes={codes}
          hold={hold}
          writes={comments}
          onError={onCommentError}
          onClose={() => setCommenting(false)}
          ask={ask}
          onAsked={() => setAsk(undefined)}
          onWriting={setWriting}
          placed={placed}
          active={active}
          onActive={(id) => {
            setActive(id);
            reveal(id);
          }}
          reattaching={reattaching}
          onReattach={setReattaching}
        />
      )}
      {passages.selected && (
        <CommentPick
          frame={frame}
          at={passages.selected.at}
          label={
            reattaching
              ? t("tilasms.reattach_pick", {
                  code: drafts.find((c) => c.id === reattaching)?.code ?? "",
                })
              : undefined
          }
          onPick={passages.pick}
        />
      )}
      {hover && <AimBox frame={frame} at={hover} />}
      {full && aim("tilasm-full-aim")}
      {full && toggle("tilasm-full-comments")}
      {(full || over) && (
        <button
          ref={leave}
          type="button"
          className="icon-button tilasm-full-leave"
          aria-label={over ? t("tilasms.close") : t("tilasms.full_leave")}
          aria-keyshortcuts="Escape"
          onClick={over ? onClose : () => onFull(false)}
        >
          {over ? (
            <X size={14} aria-hidden="true" />
          ) : (
            <Minimize2 size={14} aria-hidden="true" />
          )}
        </button>
      )}
    </section>
  );
}
