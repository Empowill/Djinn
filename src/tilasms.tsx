// The Documents panel of a wish (T25): its tilasms, the documents the developer and the agents write together. A tab
// per document, by code, and the open one on the panel's own surface, as a page. A Markdown tilasm is edited in place
// (src/tilasm-editor.tsx): each edit saved after a pause from the revision it started from; a save another writer
// outran keeps the developer's text and asks which to keep. An HTML tilasm shows in a frame of its own, served by Djinn
// at /tilasm/<id>/ with a policy of its own (internal/plan/tilasm_files.go): its scripts run, it reaches no network
// and nothing of Djinn. The panel's header keeps two actions before its close button: a new document, and More, a small
// menu with a search (its field unfolds in the header) that finds words in the titles and the text, within the wish; an
// import; and for the open one, its versions, which restore an earlier one, its download (a Markdown document as its
// .md, an HTML page as a .zip, in the Downloads folder) and its djinn:// link copied. A .md imported, or
// dropped anywhere on the panel, becomes a document; a folder or a .zip, an HTML tilasm or the new version of the one
// a .zip exported.
import { Code as ConnectCode, ConnectError } from "@connectrpc/connect";
import { type Editor } from "@tiptap/core";
import {
  CheckCheck,
  Download,
  Ellipsis,
  FileUp,
  History,
  Link,
  Plus,
  RotateCcw,
  Search,
} from "lucide-react";
import {
  type DragEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  createContext,
  useCallback,
  use,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";

import { Change, type Task } from "../gen/ts/plan/v1/plan_pb";
import { type Tilasm, TilasmFormat } from "../gen/ts/plan/v1/tilasm_pb";
import { type Clients, message } from "./data/client";
import { useClients, useStore } from "./data/djinn";
import { when } from "./data/format";
import { type DropEntry, TooLarge, entriesOf, readDrop } from "./data/tilasms";
import { t } from "./i18n";
import { TilasmDiscussions } from "./tilasm-discussions";
import { TilasmEditor, useTilasmEditor } from "./tilasm-editor";
import {
  type TilasmLoss,
  joinTitle,
  splitTitle,
  tilasmLoss,
} from "./tilasm-markdown";
import { memory } from "./usage";

import "./tilasms.css";

// tilasmAddress is where Djinn serves a tilasm's latest version.
export const tilasmAddress = (id: string) => `/tilasm/${id}/`;

const latest = (tilasm: Tilasm) => tilasm.versions.at(-1)?.number ?? 0;

export const isMarkdown = (tilasm: Tilasm) =>
  tilasm.format === TilasmFormat.MARKDOWN;

// The pause after the last edit before it is saved.
const SAVE_DELAY = 800;

// The document a wish showed last, kept by the browser: a preference, never needed.
const openKey = (wishId: string) => `djinn.tilasms.open.${wishId}`;

function readOpen(wishId: string): string {
  try {
    return globalThis.localStorage?.getItem(openKey(wishId)) ?? "";
  } catch {
    // A refused storage (a private window, an embedded view) remembers nothing.
    return "";
  }
}

function writeOpen(wishId: string, id: string) {
  try {
    globalThis.localStorage?.setItem(openKey(wishId), id);
  } catch {
    // A refused or full storage only forgets the tab.
  }
}

// createTilasm makes an untitled document of the wish, the developer's, and gives its identifier.
export async function createTilasm(
  clients: Clients,
  wishId: string,
): Promise<string> {
  const res = await clients.tilasms.create({
    wish: wishId,
    title: t("tilasms.untitled"),
    markdown: "",
    author: "developer",
  });
  return res.tilasm?.id ?? "";
}

// What a document is asked to show: its tab, and its title ready to be typed in for a new one.
export interface TilasmOpening {
  tilasmId: string;
  focus?: boolean;
}

export function TilasmsTab({
  wishId,
  opening,
  tilasms,
  tasks,
  onToast,
  actionsSlot,
}: {
  wishId: string;
  // The document a djinn:// link or a new document asks to show: its tab, again at each new request.
  opening?: TilasmOpening;
  // The wish's tilasms, by code.
  tilasms: readonly Tilasm[];
  tasks: readonly Task[];
  onToast: (text: string) => void;
  // The panel's header, where the actions show, just before its close button; null until it is there.
  actionsSlot?: HTMLElement | null;
}) {
  const clients = useClients();
  const store = useStore();
  const [openId, setOpenId] = useState(
    () => opening?.tilasmId || readOpen(wishId),
  );
  // A request to put the caret in a document's title: a new object at each request.
  const [focus, setFocus] = useState<{ id: string }>();
  useEffect(() => {
    if (!opening) return;
    setOpenId(opening.tilasmId);
    if (opening.focus) setFocus({ id: opening.tilasmId });
  }, [opening]);
  const [search, setSearch] = useState("");
  // What the search found, by the server; null without a search.
  const [found, setFound] = useState<Tilasm[] | null>(null);
  const [busy, setBusy] = useState(false);
  // Each restore asks the open document to read its text again, whatever it held.
  const [reload, setReload] = useState(0);
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
  const open =
    tilasms.find((tilasm) => tilasm.id === openId) ?? tilasms[0] ?? undefined;
  useEffect(() => {
    if (open) writeOpen(wishId, open.id);
  }, [wishId, open?.id]); // eslint-disable-line react-hooks/exhaustive-deps, @eslint-react/exhaustive-deps -- the id alone
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
  // put sends what was dropped or imported; the new document opens.
  const put = (
    read: () => Promise<{
      name: string;
      files: { path: string; content: Uint8Array }[];
    }>,
  ) => {
    setBusy(true);
    void run(async () => {
      const dropped = await read();
      if (dropped.files.length === 0) return t("tilasms.drop_empty");
      const res = await clients.tilasms.putData({
        wish: wishId,
        name: dropped.name,
        files: dropped.files,
      });
      const code = res.tilasm?.code ?? "";
      await store.changed(wishId, [Change.TILASM]);
      setOpenId(res.tilasm?.id ?? "");
      return res.imported
        ? t("tilasms.imported", { code })
        : t("tilasms.put", { code });
    }).finally(() => setBusy(false));
  };
  return (
    <TilasmPanel
      tilasms={found ?? tilasms}
      count={tilasms.length}
      search={search}
      onSearch={setSearch}
      open={open}
      onOpen={(id) => {
        setFocus(undefined);
        setOpenId(id);
      }}
      busy={busy}
      onNew={() =>
        void createTilasm(clients, wishId)
          .then(async (id) => {
            await store.changed(wishId, [Change.TILASM]);
            setOpenId(id);
            setFocus({ id });
          })
          .catch((error) => onToast(message(error)))
      }
      onImport={(file) =>
        put(async () => ({
          name: file.name,
          files: [
            {
              path: file.name,
              content: new Uint8Array(await file.arrayBuffer()),
            },
          ],
        }))
      }
      onDrop={(entries) => put(() => readDrop(entries))}
      onRestore={(tilasm, version) =>
        void run(async () => {
          await clients.tilasms.restore({
            tilasm: ref(tilasm),
            version,
            author: "developer",
          });
          setReload((n) => n + 1);
          return t("tilasms.restored", { code: tilasm.code, version });
        })
      }
      onExport={(tilasm) =>
        void run(async () => {
          const res = await clients.tilasms.export({ tilasm: ref(tilasm) });
          return t("tilasms.exported", { file: res.file });
        })
      }
      onDownload={(tilasm) =>
        void run(async () => {
          // A Markdown document downloads as its .md, its saved text; an HTML page as its .zip.
          const res = await clients.tilasms.export({
            tilasm: ref(tilasm),
            markdown: isMarkdown(tilasm),
          });
          return t("tilasms.downloaded", { file: res.file });
        })
      }
      onCopyLink={(tilasm) =>
        void copyText(tilasmLink(tilasm.id)).then(
          () => onToast(t("tilasms.link_copied")),
          () => onToast(t("tilasms.link_not_copied")),
        )
      }
      actionsSlot={actionsSlot}
    >
      {open &&
        (isMarkdown(open) ? (
          <MarkdownTilasm
            key={open.id}
            tilasm={open}
            codes={new Map(tasks.map((task) => [task.id, task.code]))}
            focus={focus?.id === open.id ? focus : undefined}
            reload={reload}
          />
        ) : (
          <HtmlTilasm tilasm={open} />
        ))}
    </TilasmPanel>
  );
}

// tilasmLink is a tilasm's link, as Djinn gives it: it opens the document from anywhere.
export const tilasmLink = (id: string) => `djinn://tilasm/${id}`;

// copyText puts text on the clipboard, by the older command where the clipboard is refused.
async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const field = document.createElement("textarea");
    field.value = text;
    field.setAttribute("readonly", "");
    field.style.position = "fixed";
    field.style.opacity = "0";
    document.body.append(field);
    field.select();
    const done = document.execCommand("copy");
    field.remove();
    if (!done) throw new Error("copy refused");
  }
}

// StripSlotContext is the place in the panel's actions where the open document shows its own indicators, and where it
// offers what has no indicator of its own: its resolved discussions, opened from the More menu.
const StripSlotContext = createContext<{
  slot: HTMLElement | null;
  onResolved: (open: (() => void) | null) => void;
}>({ slot: null, onResolved: () => {} });

// TilasmPanel is the panel as it shows, from what it is given: the strip of tabs and its actions, then the open
// document. The whole panel takes a drop.
export function TilasmPanel({
  tilasms,
  count,
  search,
  onSearch,
  open,
  onOpen,
  busy = false,
  onNew,
  onImport,
  onDrop,
  onRestore,
  onExport,
  onDownload,
  onCopyLink,
  actionsSlot,
  children,
}: {
  // The tabs: the wish's tilasms, or those the search found.
  tilasms: readonly Tilasm[];
  // How many the wish has.
  count: number;
  search: string;
  onSearch: (text: string) => void;
  // The document shown.
  open?: Tilasm;
  onOpen: (id: string) => void;
  // An import or a drop is being put.
  busy?: boolean;
  onNew: () => void;
  onImport: (file: File) => void;
  onDrop: (entries: DropEntry[]) => void;
  onRestore: (tilasm: Tilasm, version: number) => void;
  onExport: (tilasm: Tilasm) => void;
  onDownload?: (tilasm: Tilasm) => void;
  onCopyLink?: (tilasm: Tilasm) => void;
  // Where the actions show: the panel's header; inline, in the strip of tabs, when not given; nowhere while null.
  actionsSlot?: HTMLElement | null;
  children?: ReactNode;
}) {
  const [over, setOver] = useState(false);
  const [searching, setSearching] = useState(search !== "");
  const [versions, setVersions] = useState(false);
  const [more, setMore] = useState(false);
  // Opens the open document's resolved discussions, when it has some and nothing detached shows them.
  const [resolved, setResolved] = useState<(() => void) | null>(null);
  const onResolved = useCallback(
    (open: (() => void) | null) => setResolved(() => open),
    [],
  );
  // Where the open document puts what stands by the tabs: its discussions apart from the text.
  const [slot, setSlot] = useState<HTMLSpanElement | null>(null);
  const moreButton = useRef<HTMLButtonElement>(null);
  const stripSlot = useMemo(() => ({ slot, onResolved }), [slot, onResolved]);
  const file = useRef<HTMLInputElement>(null);
  const tabs = useRef<HTMLDivElement>(null);
  // A drop of files only: a block the editor's handle drags is the editor's own.
  const dragging = (event: DragEvent) => {
    if (!event.dataTransfer?.types.includes("Files")) return;
    event.preventDefault();
    event.stopPropagation();
    setOver(true);
  };
  const keys = (event: ReactKeyboardEvent) => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    const at = tilasms.findIndex((tilasm) => tilasm.id === open?.id);
    const next =
      tilasms[
        (at + (event.key === "ArrowRight" ? 1 : -1) + tilasms.length) %
          tilasms.length
      ];
    if (!next) return;
    event.preventDefault();
    onOpen(next.id);
    tabs.current
      ?.querySelector<HTMLElement>(`[data-tilasm="${next.id}"]`)
      ?.focus();
  };
  const items = tilasmMoreItems({
    open,
    busy,
    onSearch: () => setSearching(true),
    onImport: () => file.current?.click(),
    onVersions: () => setVersions(true),
    onDownload,
    onCopyLink,
    onResolved: resolved,
  });
  const actions = (
    <span className="tilasm-actions">
      <span className="tilasm-strip-slot" ref={setSlot} />
      {searching && (
        <input
          className="tilasm-search"
          type="search"
          value={search}
          placeholder={t("tilasms.search")}
          aria-label={t("tilasms.search")}
          autoFocus
          onChange={(event) => onSearch(event.target.value)}
          onBlur={() => {
            if (!search) setSearching(false);
          }}
          onKeyDown={(event) => {
            if (event.key !== "Escape") return;
            onSearch("");
            setSearching(false);
            moreButton.current?.focus();
          }}
        />
      )}
      <button
        type="button"
        className="workspace-panel-action"
        title={t("tilasms.new")}
        aria-label={t("tilasms.new")}
        onClick={onNew}
      >
        <Plus size={14} aria-hidden="true" />
      </button>
      <span className="tilasm-versions-anchor">
        <button
          ref={moreButton}
          type="button"
          className="workspace-panel-action"
          aria-haspopup="menu"
          aria-expanded={more}
          aria-busy={busy}
          title={t("tilasms.more")}
          aria-label={t("tilasms.more")}
          onClick={() => {
            setVersions(false);
            setMore(!more);
          }}
        >
          <Ellipsis size={14} aria-hidden="true" />
        </button>
        {more && (
          <TilasmMore
            items={items}
            onClose={(refocus) => {
              setMore(false);
              if (refocus) moreButton.current?.focus();
            }}
          />
        )}
        {open && versions && (
          <TilasmVersions
            tilasm={open}
            onRestore={(version) => {
              setVersions(false);
              onRestore(open, version);
            }}
            onExport={() => {
              setVersions(false);
              onExport(open);
            }}
            onClose={() => setVersions(false)}
          />
        )}
      </span>
      <input
        ref={file}
        className="tilasm-file"
        type="file"
        accept=".md,.markdown,text/markdown"
        tabIndex={-1}
        aria-hidden="true"
        onChange={(event) => {
          const picked = event.target.files?.[0];
          event.target.value = "";
          if (picked) onImport(picked);
        }}
      />
    </span>
  );
  return (
    <div
      role="tabpanel"
      aria-labelledby="view-tab-tilasms"
      className={`tilasms${over ? " dropping" : ""}`}
      onDragEnterCapture={dragging}
      onDragOverCapture={dragging}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null))
          setOver(false);
      }}
      onDropCapture={(event) => {
        if (!event.dataTransfer?.types.includes("Files")) return;
        event.preventDefault();
        event.stopPropagation();
        setOver(false);
        const entries = entriesOf(event.dataTransfer);
        if (entries.length) onDrop(entries);
      }}
    >
      <div className="tilasm-strip">
        <div
          ref={tabs}
          className="tilasm-tabs"
          role="tablist"
          aria-label={t("tabs.tilasms")}
          onKeyDown={keys}
        >
          {tilasms.map((tilasm) => (
            <button
              key={tilasm.id}
              type="button"
              role="tab"
              id={`tilasm-${tilasm.id}`}
              data-tilasm={tilasm.id}
              aria-selected={tilasm.id === open?.id}
              tabIndex={tilasm.id === open?.id ? 0 : -1}
              className="tilasm-tab"
              title={`${tilasm.code} · ${tilasm.title}`}
              onClick={() => onOpen(tilasm.id)}
            >
              {tilasm.title || t("tilasms.untitled")}
            </button>
          ))}
          {count > 0 && tilasms.length === 0 && (
            <span className="tilasm-strip-note">
              {t("tilasms.none_found", { search })}
            </span>
          )}
        </div>
        {actionsSlot === undefined && (
          <div className="tilasm-strip-actions">{actions}</div>
        )}
      </div>
      {actionsSlot && createPortal(actions, actionsSlot)}
      {count === 0 ? (
        <p className="tilasm-strip-note">{t("tilasms.none")}</p>
      ) : (
        <StripSlotContext value={stripSlot}>{children}</StripSlotContext>
      )}
    </div>
  );
}

export interface TilasmMoreItem {
  key: string;
  icon: ReactNode;
  label: string;
  disabled?: boolean;
  run: () => void;
}

// tilasmMoreItems is what the More menu holds: the search and the import always; the versions, the download and the
// link for the open document alone; the resolved discussions when the open document has some and nothing detached.
export function tilasmMoreItems({
  open,
  busy,
  onSearch,
  onImport,
  onVersions,
  onDownload,
  onCopyLink,
  onResolved,
}: {
  open?: Tilasm;
  busy: boolean;
  onSearch: () => void;
  onImport: () => void;
  onVersions: () => void;
  onDownload?: (tilasm: Tilasm) => void;
  onCopyLink?: (tilasm: Tilasm) => void;
  onResolved?: (() => void) | null;
}): TilasmMoreItem[] {
  const items: TilasmMoreItem[] = [
    {
      key: "search",
      icon: <Search size={14} aria-hidden="true" />,
      label: t("tilasms.search"),
      run: onSearch,
    },
    {
      key: "import",
      icon: <FileUp size={14} aria-hidden="true" />,
      label: busy ? t("tilasms.putting") : t("tilasms.import"),
      disabled: busy,
      run: onImport,
    },
  ];
  if (open) {
    items.push({
      key: "versions",
      icon: <History size={14} aria-hidden="true" />,
      label: t("tilasms.history"),
      run: onVersions,
    });
    if (onDownload)
      items.push({
        key: "download",
        icon: <Download size={14} aria-hidden="true" />,
        label: isMarkdown(open)
          ? t("tilasms.download_md")
          : t("tilasms.download_zip"),
        run: () => onDownload(open),
      });
    if (onCopyLink)
      items.push({
        key: "copy",
        icon: <Link size={14} aria-hidden="true" />,
        label: t("tilasms.copy_link"),
        run: () => onCopyLink(open),
      });
  }
  if (onResolved)
    items.push({
      key: "resolved",
      icon: <CheckCheck size={14} aria-hidden="true" />,
      label: t("tilasms.apart.resolved_label"),
      run: onResolved,
    });
  return items;
}

// TilasmMore is the panel's More menu, under its button: the arrows move between its items, Escape, Tab or a click
// outside close it.
export function TilasmMore({
  items,
  onClose,
}: {
  items: TilasmMoreItem[];
  // refocus: the menu closed by the keyboard, its button takes the focus back.
  onClose: (refocus: boolean) => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  close.current = onClose;
  const entries = () => [
    ...(box.current?.querySelectorAll<HTMLButtonElement>(
      '[role="menuitem"]:not(:disabled)',
    ) ?? []),
  ];
  useEffect(() => {
    entries()[0]?.focus();
    const outside = (event: PointerEvent) => {
      const anchor = box.current?.parentElement;
      if (anchor && !anchor.contains(event.target as Node))
        close.current(false);
    };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, []);
  const keys = (event: ReactKeyboardEvent) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onClose(true);
      return;
    }
    if (event.key === "Tab") return onClose(false);
    const all = entries();
    const at = all.indexOf(document.activeElement as HTMLButtonElement);
    const next =
      event.key === "ArrowDown"
        ? (at + 1) % all.length
        : event.key === "ArrowUp"
          ? (at - 1 + all.length) % all.length
          : event.key === "Home"
            ? 0
            : event.key === "End"
              ? all.length - 1
              : -1;
    if (next < 0) return;
    event.preventDefault();
    all[next]?.focus();
  };
  return (
    <div
      ref={box}
      className="tilasm-popover tilasm-more"
      role="menu"
      aria-label={t("tilasms.more")}
      onKeyDown={keys}
    >
      {items.map((item) => (
        <button
          key={item.key}
          type="button"
          role="menuitem"
          className="tilasm-menu-item"
          disabled={item.disabled}
          onClick={() => {
            onClose(false);
            item.run();
          }}
        >
          {item.icon}
          {item.label}
        </button>
      ))}
    </div>
  );
}

// TilasmVersions is the small window of a document's versions, the latest first, each earlier one restorable, and
// its export. A click outside or Escape closes it.
export function TilasmVersions({
  tilasm,
  onRestore,
  onExport,
  onClose,
}: {
  tilasm: Tilasm;
  onRestore: (version: number) => void;
  onExport: () => void;
  onClose: () => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const outside = (event: PointerEvent) => {
      const anchor = box.current?.parentElement;
      if (anchor && !anchor.contains(event.target as Node)) onClose();
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", escape);
    };
  }, [onClose]);
  const html = !isMarkdown(tilasm);
  return (
    <div
      ref={box}
      className="tilasm-popover"
      role="dialog"
      aria-label={t("tilasms.history")}
    >
      <ol className="tilasm-history" reversed>
        {[...tilasm.versions].reverse().map((v) => (
          <li key={v.number}>
            <span className="tilasm-history-line">
              <strong>{t("tilasms.version", { version: v.number })}</strong>
              <span className="tilasm-history-meta">
                {v.author} · {when(v.updateTime ?? v.createTime)}
                {html &&
                  ` · ${t("tilasms.files", { count: v.files })} · ${memory(v.size)}`}
                {v.restoredFrom > 0 &&
                  ` · ${t("tilasms.restored_from", { version: v.restoredFrom })}`}
              </span>
            </span>
            {v.number !== latest(tilasm) && (
              <button
                type="button"
                className="icon-button"
                title={t("tilasms.restore")}
                aria-label={t("tilasms.restore")}
                onClick={() => onRestore(v.number)}
              >
                <RotateCcw size={13} aria-hidden="true" />
              </button>
            )}
          </li>
        ))}
      </ol>
      <button type="button" className="text-button" onClick={onExport}>
        <Download size={13} aria-hidden="true" />
        {t("tilasms.export")}
      </button>
    </div>
  );
}

// HtmlTilasm shows an HTML tilasm as its page, read only. Scripts only: an opaque origin, never Djinn's; the server's
// policy refuses the network.
export function HtmlTilasm({ tilasm }: { tilasm: Tilasm }) {
  return (
    <iframe
      key={`${tilasm.id}-${latest(tilasm)}`}
      className="tilasm-frame"
      title={`${tilasm.code} · ${tilasm.title}`}
      src={tilasmAddress(tilasm.id)}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      // A frame in a panel not shown waits until it shows.
      loading="lazy"
    />
  );
}

type SaveState = "saved" | "saving" | "unsaved" | "failed";

// Another writer's text, met by a save or a live update while the developer's own was not saved.
interface Theirs {
  markdown: string;
  revision: bigint;
  title: string;
}

const lossNames: Record<TilasmLoss, () => string> = {
  html: () => t("tilasms.loss.html"),
  image: () => t("tilasms.loss.image"),
  footnote: () => t("tilasms.loss.footnote"),
  content: () => t("tilasms.loss.content"),
};

// read gets a document's Markdown and revision.
async function read(clients: Clients, id: string) {
  const res = await clients.tilasms.get({
    tilasm: { ref: { case: "id", value: id } },
  });
  return {
    markdown: res.text,
    revision: res.tilasm?.revision ?? 0n,
    title: res.tilasm?.title ?? "",
  };
}

// MarkdownTilasm is a Markdown document on the panel: its title line, a quiet line of who and when and whether it is
// saved, then its text in the editor.
function MarkdownTilasm({
  tilasm,
  codes,
  focus,
  reload,
}: {
  tilasm: Tilasm;
  // The codes of the wish's tasks and azimas, by id: what a tilasm cites.
  codes: ReadonlyMap<string, string>;
  // A request to put the caret in the title.
  focus?: { id: string };
  // Changes when the document must read its text again, unsaved edits dropped: a restore.
  reload: number;
}) {
  const clients = useClients();
  const id = tilasm.id;
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [failure, setFailure] = useState("");
  const [title, setTitle] = useState(tilasm.title);
  const [save, setSave] = useState<SaveState>("saved");
  const [loss, setLoss] = useState<TilasmLoss[]>([]);
  // The developer chose to edit a document the editor cannot keep whole.
  const [anyway, setAnyway] = useState(false);
  const [theirs, setTheirs] = useState<Theirs | null>(null);
  // A save finished: a revision listed meanwhile is read then.
  const [settled, setSettled] = useState(0);
  const titleField = useRef<HTMLInputElement>(null);

  // What the saves work from, out of React's renders.
  const base = useRef(0n); // the revision the editor's text started from
  const saved = useRef(""); // the Markdown at that revision, as last read or saved
  const headed = useRef(false); // the Markdown starts with the title as a heading
  const titleNow = useRef(tilasm.title);
  const dirty = useRef(false);
  const edits = useRef(0);
  const saving = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const editable = phase === "ready" && (loss.length === 0 || anyway);
  const state = useRef({ editable, theirs });
  useLayoutEffect(() => {
    state.current = { editable, theirs };
  });

  const edited = useRef(() => {});
  const editor = useTilasmEditor(() => edited.current());
  const editorRef = useRef<Editor | null>(null);
  useLayoutEffect(() => {
    editorRef.current = editor;
  }, [editor]);

  // markdown is the document as it would be saved now: the title line, then the editor's text.
  const markdown = useCallback(() => {
    const current = editorRef.current;
    if (!current) return saved.current;
    return joinTitle(headed.current, titleNow.current, current.getMarkdown());
  }, []);

  // apply puts a text read from Djinn in the editor, out of its undo history; keep leaves the caret where it was, and
  // a caret from after on moves with the text the change added or took there.
  const apply = useCallback(
    (
      text: string,
      revision: bigint,
      named: string,
      keep: boolean,
      after = Infinity,
    ) => {
      const current = editorRef.current;
      if (!current) return;
      clearTimeout(timer.current);
      const split = splitTitle(text, named);
      headed.current = split.headed;
      const lost = current.markdown
        ? tilasmLoss(current.markdown, split.body)
        : [];
      const { from, to } = current.state.selection;
      const before = current.state.doc.content.size;
      current
        .chain()
        .command(({ tr }) => {
          tr.setMeta("addToHistory", false);
          return true;
        })
        .setContent(split.body, { contentType: "markdown", emitUpdate: false })
        .run();
      if (keep && (current.isFocused || after !== Infinity)) {
        const size = current.state.doc.content.size;
        const moved = (pos: number) =>
          Math.max(0, Math.min(pos >= after ? pos + size - before : pos, size));
        current.commands.setTextSelection({ from: moved(from), to: moved(to) });
      }
      base.current = revision;
      saved.current = text;
      dirty.current = false;
      titleNow.current = named;
      setTitle(named);
      setLoss(lost);
      setTheirs(null);
      setSave("saved");
    },
    [],
  );

  // flush saves the document now, from the revision it started from. Another writer first: their text is kept aside
  // and the developer picks.
  const flush = useCallback(async () => {
    clearTimeout(timer.current);
    if (!dirty.current || !state.current.editable || state.current.theirs)
      return;
    if (saving.current) return;
    const text = markdown();
    const at = edits.current;
    saving.current = true;
    setSave("saving");
    try {
      const res = await clients.tilasms.save({
        id,
        baseRevision: base.current,
        markdown: text,
        title: titleNow.current.replace(/\s+/g, " ").trim(),
        author: "developer",
      });
      base.current = res.tilasm?.revision ?? base.current;
      saved.current = text;
      if (edits.current === at) {
        dirty.current = false;
        setSave("saved");
      } else {
        timer.current = setTimeout(() => void flush(), SAVE_DELAY);
      }
    } catch (error) {
      if (error instanceof ConnectError && error.code === ConnectCode.Aborted) {
        try {
          const now = await read(clients, id);
          if (now.markdown === text) {
            base.current = now.revision;
            saved.current = text;
            if (edits.current === at) dirty.current = false;
            setSave(dirty.current ? "unsaved" : "saved");
          } else {
            setTheirs(now);
            setSave("unsaved");
          }
        } catch (again) {
          setFailure(message(again));
          setSave("failed");
        }
      } else {
        setFailure(message(error));
        setSave("failed");
      }
    } finally {
      saving.current = false;
      setSettled((n) => n + 1);
    }
  }, [clients, id, markdown]);

  const changed = useCallback(() => {
    dirty.current = true;
    edits.current += 1;
    setSave("unsaved");
    clearTimeout(timer.current);
    timer.current = setTimeout(() => void flush(), SAVE_DELAY);
  }, [flush]);
  useLayoutEffect(() => {
    edited.current = changed;
  }, [changed]);

  // Read the document once the editor is ready, and again at each restore.
  useEffect(() => {
    if (!editor) return;
    let live = true;
    read(clients, id)
      .then((now) => {
        if (!live) return;
        apply(now.markdown, now.revision, now.title, false);
        setPhase("ready");
      })
      .catch((error) => {
        if (!live) return;
        setFailure(message(error));
        setPhase("error");
      });
    return () => {
      live = false;
    };
  }, [editor, clients, id, apply, reload]);

  useEffect(() => {
    editor?.setEditable(editable, false);
  }, [editor, editable]);

  // A new document: the caret in its title, the title selected to be typed over.
  useEffect(() => {
    if (phase !== "ready" || !focus) return;
    titleField.current?.focus();
    titleField.current?.select();
  }, [phase, focus]);

  // Another writer saved a later revision: an untouched document reads it in place, the caret kept; a touched one
  // keeps the developer's text and asks.
  const listed = tilasm.revision;
  useEffect(() => {
    if (phase !== "ready" || saving.current || listed <= base.current) return;
    let live = true;
    read(clients, id)
      .then((now) => {
        if (!live || saving.current || now.revision <= base.current) return;
        if (now.markdown === saved.current) {
          base.current = now.revision;
          return;
        }
        if (dirty.current) {
          clearTimeout(timer.current);
          setTheirs(now);
          setSave("unsaved");
        } else apply(now.markdown, now.revision, now.title, true);
      })
      .catch(() => {
        // The next change of the list tries again.
      });
    return () => {
      live = false;
    };
  }, [phase, listed, settled, clients, id, apply]);

  // settle saves what is not saved yet, waiting for a save under way, and says whether Djinn now holds the text the
  // editor shows: a discussion or a decision works on Djinn's text.
  const settle = useCallback(async () => {
    for (let tries = 0; tries < 100; tries++) {
      if (state.current.theirs) return false;
      if (saving.current) await new Promise((done) => setTimeout(done, 50));
      else if (dirty.current) {
        if (!state.current.editable) return false;
        await flush();
        if (dirty.current && !saving.current) return false;
      } else return true;
    }
    return false;
  }, [flush]);

  // reread reads the document again once Djinn wrote it (an accepted proposal), the caret kept.
  const reread = useCallback(
    async (after: number) => {
      const now = await read(clients, id);
      apply(now.markdown, now.revision, now.title, true, after);
    },
    [clients, id, apply],
  );

  // The title the rendered text starts with: the Markdown's first heading, when it says the title.
  const titleText = useCallback(
    () => (headed.current ? titleNow.current : ""),
    [],
  );
  const { slot: strip, onResolved } = use(StripSlotContext);
  const comment = useRef<
    ((from: number, to: number, ask?: boolean) => void) | null
  >(null);
  const onComment = useCallback(
    (from: number, to: number) => comment.current?.(from, to),
    [],
  );
  const onAsk = useCallback(
    (from: number, to: number) => comment.current?.(from, to, true),
    [],
  );

  // What is not saved yet is saved when the document closes, its tab changed or its wish left, and when the page
  // goes.
  useEffect(() => {
    const leave = () => void flush();
    window.addEventListener("pagehide", leave);
    return () => {
      window.removeEventListener("pagehide", leave);
      void flush();
    };
  }, [flush]);

  const status =
    phase === "loading"
      ? t("tilasms.loading")
      : !editable
        ? t("tilasms.read_only")
        : save === "saving"
          ? t("tilasms.saving")
          : save === "unsaved"
            ? t("tilasms.unsaved")
            : save === "failed"
              ? t("tilasms.save_failed")
              : t("tilasms.saved");
  return (
    <article className="tilasm-document" aria-label={title || tilasm.title}>
      <input
        ref={titleField}
        className="tilasm-title"
        value={title}
        placeholder={t("tilasms.untitled")}
        aria-label={t("tilasms.title")}
        readOnly={!editable}
        onChange={(event) => {
          titleNow.current = event.target.value;
          setTitle(event.target.value);
          changed();
        }}
        onKeyDown={(event) => {
          if (event.key !== "Enter" && event.key !== "ArrowDown") return;
          event.preventDefault();
          editor?.commands.focus("start");
        }}
      />
      <p className="tilasm-meta">
        <span>{tilasm.code}</span>
        <span>
          {tilasm.author} · {when(tilasm.updateTime)}
        </span>
        {tilasm.cites.length > 0 && (
          <span>
            {t("tilasms.cites")}{" "}
            {tilasm.cites
              .map((cite) => codes.get(cite) ?? cite.slice(0, 8))
              .join(" ")}
          </span>
        )}
        <span className="tilasm-save" aria-live="polite" data-state={save}>
          {status}
        </span>
      </p>
      {phase === "error" || save === "failed" ? (
        <p className="tilasm-notice" role="alert">
          {failure}
        </p>
      ) : null}
      {phase === "ready" && loss.length > 0 && !anyway && (
        <p className="tilasm-notice">
          <span>
            {t("tilasms.lossy", {
              what: loss.map((kind) => lossNames[kind]()).join(", "),
            })}
          </span>
          <button
            type="button"
            className="text-button"
            onClick={() => setAnyway(true)}
          >
            {t("tilasms.edit_anyway")}
          </button>
        </p>
      )}
      {theirs && (
        <p className="tilasm-notice" role="alert">
          <span>{t("tilasms.conflict")}</span>
          <button
            type="button"
            className="text-button"
            onClick={() =>
              apply(theirs.markdown, theirs.revision, theirs.title, false)
            }
          >
            {t("tilasms.reload_theirs")}
          </button>
          <button
            type="button"
            className="text-button"
            onClick={() => {
              base.current = theirs.revision;
              saved.current = theirs.markdown;
              state.current = { ...state.current, theirs: null };
              setTheirs(null);
              dirty.current = true;
              void flush();
            }}
          >
            {t("tilasms.keep_mine")}
          </button>
        </p>
      )}
      <TilasmEditor
        editor={editor}
        editable={editable}
        onComment={onComment}
        onAsk={onAsk}
        overlay={(current, box) =>
          phase === "ready" && (
            <TilasmDiscussions
              editor={current}
              tilasm={tilasm}
              box={box}
              titleText={titleText}
              settle={settle}
              reload={reread}
              strip={strip}
              onResolved={onResolved}
              commentRef={comment}
            />
          )
        }
      />
    </article>
  );
}
