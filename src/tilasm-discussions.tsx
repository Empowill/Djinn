// The discussions and proposals of a Markdown tilasm, in its editor (src/tilasms.tsx). Djinn holds them
// (internal/plan/tilasm_discussions.go), each naming its passage by an anchor of the document's rendered text
// (src/tilasm-anchor.ts); the window finds that passage again in the editor and marks it, never in the Markdown.
//
// A discussion opens from a selection, or a block's menu, in a small window to the right of its passage; clicked away
// or left, it folds into a dot in the margin by the passage, its draft kept, and a message that arrives meanwhile marks
// the dot. A pending proposal shows in the text as a diff: the passage it replaces struck in red, its text in green
// after it; a small card accepts or rejects it, and only accepting changes the document. What Djinn no longer finds
// once, unchanged, in the latest text (a passage edited, deleted, or found twice) is never shown on another passage:
// it waits apart, under an indicator by the tabs, readable, answerable, and a proposal there can only be rejected.
import { Code as ConnectCode, ConnectError } from "@connectrpc/connect";
import { type Editor } from "@tiptap/core";
import { DOMSerializer, Node as PMNode } from "@tiptap/pm/model";
import { Plugin, PluginKey, type Transaction } from "@tiptap/pm/state";
import { Decoration, DecorationSet } from "@tiptap/pm/view";
import { Check, CornerDownLeft, MessageSquare, Unlink, X } from "lucide-react";
import {
  type ReactNode,
  type RefObject,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useReducer,
  useState,
} from "react";
import { createPortal } from "react-dom";

import {
  type Tilasm,
  type TilasmProposal,
  TilasmAnchorState,
  TilasmProposalStatus,
  type TilasmThread,
} from "../gen/ts/plan/v1/tilasm_pb";
import { message } from "./data/client";
import { useClients } from "./data/djinn";
import { when } from "./data/format";
import { t } from "./i18n";
import { MarkdownBody } from "./markdown-body";
import {
  type Anchor,
  type Rendered,
  anchorOf,
  rangeOf,
  renderDoc,
  resolve,
} from "./tilasm-anchor";

// A passage marked in the editor: a discussion's, a proposal's, or the one a new comment is being written on. lost:
// its text was deleted in the editor since it was found.
export interface Placed {
  id: string;
  kind: "thread" | "proposal" | "draft";
  from: number;
  to: number;
  lost?: boolean;
}

// How a proposal's new text shows after its passage: in the line, or as blocks of its own.
interface Look {
  key: string;
  block: boolean;
  render: () => HTMLElement;
}

interface Marks {
  placed: Placed[];
  active: string;
  looks: ReadonlyMap<string, Look>;
}

const marksKey = new PluginKey<Marks>("tilasm-discussions");

// The id of the passage a new comment is written on.
const NEW = "new";

function mapPlaced(p: Placed, tr: Transaction): Placed {
  if (p.lost) return p;
  if (p.from === p.to) {
    const at = tr.mapping.map(p.from, -1);
    return { ...p, from: at, to: at };
  }
  const from = tr.mapping.map(p.from, 1);
  const to = tr.mapping.map(p.to, -1);
  if (to <= from) return { ...p, from, to: from, lost: true };
  return { ...p, from, to };
}

function srOnly(text: string): HTMLElement {
  const span = document.createElement("span");
  span.className = "tilasm-sr";
  span.textContent = text;
  return span;
}

// marksPlugin marks the passages in the editor: decorations only, never the document. Each edit moves them with the
// text; Djinn's answer places them again.
function marksPlugin(): Plugin<Marks> {
  return new Plugin<Marks>({
    key: marksKey,
    state: {
      init: () => ({ placed: [], active: "", looks: new Map() }),
      apply(tr, value) {
        const meta = tr.getMeta(marksKey) as Partial<Marks> | undefined;
        let next = value;
        if (tr.docChanged)
          next = {
            ...next,
            placed: next.placed.map((p) => mapPlaced(p, tr)),
          };
        if (meta) next = { ...next, ...meta };
        return next;
      },
    },
    props: {
      decorations(state) {
        const marks = marksKey.getState(state);
        if (!marks || marks.placed.length === 0) return null;
        const decorations: Decoration[] = [];
        for (const p of marks.placed) {
          if (p.lost) continue;
          const active = p.id === marks.active ? " is-active" : "";
          if (p.kind !== "proposal") {
            if (p.from < p.to)
              decorations.push(
                Decoration.inline(p.from, p.to, {
                  class: `tilasm-mark${active}`,
                  "data-thread": p.id,
                }),
              );
            continue;
          }
          if (p.from < p.to) {
            decorations.push(
              Decoration.widget(
                p.from,
                () => srOnly(t("tilasms.proposal.removed")),
                { side: -1, key: `old-${p.id}` },
              ),
              Decoration.inline(p.from, p.to, {
                class: `tilasm-old${active}`,
                "data-proposal": p.id,
                title: t("tilasms.proposal.hint"),
              }),
            );
          }
          const look = marks.looks.get(p.id);
          if (!look) continue;
          let at = p.to;
          if (look.block) {
            const $to = state.doc.resolve(p.to);
            at = $to.depth >= 1 ? $to.after(1) : p.to;
          }
          decorations.push(
            Decoration.widget(at, look.render, {
              side: 1,
              key: `new-${p.id}-${look.key}`,
              ignoreSelection: true,
            }),
          );
        }
        return DecorationSet.create(state.doc, decorations);
      },
    },
  });
}

// lookOf renders a proposal's replacement, Markdown, as the editor would show it.
function lookOf(editor: Editor, proposal: TilasmProposal): Look | null {
  const replacement = proposal.replacement;
  if (!replacement.trim()) return null;
  let doc: PMNode | null = null;
  try {
    const json = editor.markdown?.parse(replacement);
    if (json) doc = PMNode.fromJSON(editor.schema, json);
  } catch {
    // Markdown the editor cannot read shows as its text.
  }
  const block =
    !!doc &&
    !(doc.childCount === 1 && doc.firstChild?.type.name === "paragraph");
  const render = () => {
    const box = document.createElement(block ? "div" : "span");
    box.className = block ? "tilasm-new is-block" : "tilasm-new";
    box.contentEditable = "false";
    box.dataset.proposal = proposal.id;
    box.tabIndex = 0;
    box.setAttribute("role", "button");
    box.setAttribute(
      "aria-label",
      t("tilasms.proposal.open", { author: authorName(proposal.author) }),
    );
    box.appendChild(srOnly(t("tilasms.proposal.added")));
    const serializer = DOMSerializer.fromSchema(editor.schema);
    if (doc) {
      const content =
        !block && doc.firstChild ? doc.firstChild.content : doc.content;
      box.appendChild(serializer.serializeFragment(content));
    } else box.appendChild(document.createTextNode(replacement));
    return box;
  };
  return { key: `${replacement.length}-${hash(replacement)}`, block, render };
}

function hash(text: string): string {
  let h = 0;
  for (let i = 0; i < text.length; i++)
    h = (Math.imul(h, 31) + text.charCodeAt(i)) | 0;
  return (h >>> 0).toString(36);
}

// authorName is who wrote a message or a proposal, as the developer reads it.
export function authorName(author: string): string {
  if (author === "developer") return t("tilasms.author.you");
  if (author === "lead") return t("tilasms.author.lead");
  if (author === "document") return t("tilasms.author.document");
  return author;
}

// Why a passage is apart, as the developer reads it.
type Apart = "changed" | "missing" | "ambiguous";

function apartOf(state: TilasmAnchorState): Apart {
  if (state === TilasmAnchorState.MISSING) return "missing";
  if (state === TilasmAnchorState.AMBIGUOUS) return "ambiguous";
  return "changed";
}

const apartText: Record<Apart, () => string> = {
  changed: () => t("tilasms.apart.changed"),
  missing: () => t("tilasms.apart.missing"),
  ambiguous: () => t("tilasms.apart.ambiguous"),
};

// refusal is what a refused request says, the reason Djinn gives first.
function refusal(error: unknown): string {
  if (error instanceof ConnectError) {
    if (error.code === ConnectCode.Aborted) return t("tilasms.proposal.moved");
    if (error.code === ConnectCode.FailedPrecondition) {
      const reason = /^(changed|missing|ambiguous):/.exec(error.rawMessage);
      if (reason) return apartText[reason[1] as Apart]();
    }
  }
  return message(error);
}

const pending = (proposal: TilasmProposal) =>
  proposal.status === TilasmProposalStatus.PENDING;

// A draft of a message, kept as it is typed; requestId names its sending, again when it is sent again unchanged.
interface Draft {
  text: string;
  requestId: string;
  sentText: string;
}

const draftsKey = (tilasmId: string) => `djinn.tilasm.drafts.${tilasmId}`;

function readDrafts(tilasmId: string): Map<string, Draft> {
  try {
    const raw = globalThis.sessionStorage?.getItem(draftsKey(tilasmId));
    if (raw)
      return new Map(Object.entries(JSON.parse(raw) as Record<string, Draft>));
  } catch {
    // A refused or unreadable storage keeps the drafts in memory only.
  }
  return new Map();
}

function writeDrafts(tilasmId: string, drafts: Map<string, Draft>) {
  try {
    const kept = Object.fromEntries(
      [...drafts].filter(([, draft]) => draft.text.trim()),
    );
    globalThis.sessionStorage?.setItem(
      draftsKey(tilasmId),
      JSON.stringify(kept),
    );
  } catch {
    // A refused or full storage keeps the drafts in memory only.
  }
}

interface Data {
  revision: bigint;
  threads: TilasmThread[];
  proposals: TilasmProposal[];
}

// What floats over the document: a discussion, a new comment, or a proposal's card.
type Opened =
  | { kind: "thread"; id: string }
  | { kind: "new" }
  | { kind: "proposal"; id: string };

export interface DiscussionsProps {
  editor: Editor;
  // A new object at each change of the wish's tilasms: the discussions are read again.
  tilasm: Tilasm;
  // The editor's box, where the windows and the dots stand.
  box: RefObject<HTMLDivElement | null>;
  // The title, when the Markdown starts with it as a heading: the rendered text starts with it.
  titleText: () => string;
  // Saves what is not saved yet, and says whether Djinn holds the text the editor shows.
  settle: () => Promise<boolean>;
  // Reads the document again once a proposal is accepted, the caret kept: positions from after on move with the text.
  reload: (after: number) => Promise<void>;
  // Where the indicator of what is apart goes, in the strip of tabs.
  strip: HTMLElement | null;
  // Set to what opens a comment on a range of the editor.
  commentRef: RefObject<((from: number, to: number) => void) | null>;
}

// TilasmDiscussions marks the passages of a document's discussions and proposals, and shows their windows.
export function TilasmDiscussions({
  editor,
  tilasm,
  box,
  titleText,
  settle,
  reload,
  strip,
  commentRef,
}: DiscussionsProps) {
  const clients = useClients();
  const id = tilasm.id;
  const ref = useMemo(
    () => ({ ref: { case: "id" as const, value: id } }),
    [id],
  );
  const [data, setData] = useState<Data | null>(null);
  const [opened, setOpened] = useState<Opened | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState("");
  // A change of the editor or its layout: the marks and windows are placed again.
  const [, redrawn] = useReducer((n: number) => n + 1, 0);
  // The drafts of the document's messages, by discussion: the component lives as long as its document is open.
  const [draftMap] = useState(() => readDrafts(id));
  const drafts = useRef(draftMap);
  // How many messages of each discussion the developer has seen.
  const seen = useRef(new Map<string, number>());
  const [unread, setUnread] = useState<ReadonlySet<string>>(() => new Set());
  const live = useRef({ data, opened });
  useLayoutEffect(() => {
    live.current = { data, opened };
  });

  // Read the discussions, again at each change of the tilasm; the last answer wins.
  const asked = useRef(0);
  const refetch = useCallback(async () => {
    const n = ++asked.current;
    try {
      const res = await clients.tilasms.discussions({ tilasm: ref });
      if (n !== asked.current) return;
      setData({
        revision: res.revision,
        threads: res.threads,
        proposals: res.proposals,
      });
    } catch {
      // The next change of the tilasm reads them again.
    }
  }, [clients, ref]);
  useEffect(() => {
    void refetch();
  }, [refetch, tilasm]);

  // Messages that arrived in a discussion not shown mark its dot.
  const first = useRef(true);
  useEffect(() => {
    if (!data) return;
    const now = live.current.opened;
    const fresh = new Set<string>();
    for (const thread of data.threads) {
      const count = thread.messages.length;
      if (now?.kind === "thread" && now.id === thread.id) {
        seen.current.set(thread.id, count);
        continue;
      }
      if (!seen.current.has(thread.id))
        seen.current.set(thread.id, first.current ? count : 0);
      if (count > (seen.current.get(thread.id) ?? 0)) fresh.add(thread.id);
    }
    first.current = false;
    setUnread(fresh);
  }, [data]);

  // The marks plugin lives with this component.
  useEffect(() => {
    editor.registerPlugin(marksPlugin());
    return () => {
      if (!editor.isDestroyed) editor.unregisterPlugin(marksKey);
    };
  }, [editor]);

  const looks = useMemo(() => {
    const out = new Map<string, Look>();
    for (const proposal of data?.proposals ?? []) {
      if (!pending(proposal)) continue;
      const look = lookOf(editor, proposal);
      if (look) out.set(proposal.id, look);
    }
    return out;
  }, [editor, data]);

  // place finds each passage Djinn says is anchored in the editor's text, as the editor holds it now.
  const place = useCallback(() => {
    const now = live.current.data;
    if (!now || editor.isDestroyed) return;
    const r = renderDoc(editor.state.doc, titleText());
    const size = editor.state.doc.content.size;
    const placed: Placed[] = [];
    const find = (
      itemId: string,
      kind: Placed["kind"],
      anchor: Anchor | undefined,
      state: TilasmAnchorState,
    ) => {
      if (!anchor || state !== TilasmAnchorState.ANCHORED) return;
      const range = locate(r, anchor, size);
      if (range) placed.push({ id: itemId, kind, ...range });
    };
    for (const thread of now.threads)
      if (!thread.resolved)
        find(thread.id, "thread", thread.anchor, thread.anchorState);
    for (const proposal of now.proposals)
      if (pending(proposal))
        find(proposal.id, "proposal", proposal.anchor, proposal.anchorState);
    const before = marksKey.getState(editor.state);
    const draft = before?.placed.find((p) => p.kind === "draft");
    if (draft) placed.push(draft);
    editor.view.dispatch(
      editor.state.tr
        .setMeta(marksKey, { placed, looks })
        .setMeta("addToHistory", false),
    );
  }, [editor, titleText, looks]);
  useEffect(place, [place, data]);

  // A text Djinn sets (a load, another writer, an accepted proposal) replaces the document: the passages are found
  // again in it. Any change, or a frame that resizes, places the windows again.
  useEffect(() => {
    let frame = 0;
    const redraw = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => redrawn());
    };
    const transaction = ({ transaction: tr }: { transaction: Transaction }) => {
      if (tr.docChanged && tr.getMeta("addToHistory") === false)
        queueMicrotask(place);
      redraw();
    };
    editor.on("transaction", transaction);
    const resize = new ResizeObserver(redraw);
    resize.observe(editor.view.dom);
    window.addEventListener("resize", redraw);
    return () => {
      cancelAnimationFrame(frame);
      editor.off("transaction", transaction);
      resize.disconnect();
      window.removeEventListener("resize", redraw);
    };
  }, [editor, place]);

  // The active passage stands out.
  const activeId = opened?.kind === "new" ? NEW : opened ? opened.id : "";
  useEffect(() => {
    if (editor.isDestroyed) return;
    const marks = marksKey.getState(editor.state);
    if (!marks || marks.active === activeId) return;
    editor.view.dispatch(
      editor.state.tr
        .setMeta(marksKey, { active: activeId })
        .setMeta("addToHistory", false),
    );
  }, [editor, activeId]);

  const setDraftItem = useCallback(
    (range: { from: number; to: number } | null) => {
      const marks = marksKey.getState(editor.state);
      if (!marks) return;
      const placed = marks.placed.filter((p) => p.kind !== "draft");
      if (range) placed.push({ id: NEW, kind: "draft", ...range });
      editor.view.dispatch(
        editor.state.tr
          .setMeta(marksKey, { placed })
          .setMeta("addToHistory", false),
      );
    },
    [editor],
  );

  // A comment asked on a range: its window opens on it, a draft started before kept.
  useLayoutEffect(() => {
    commentRef.current = (from, to) => {
      setDraftItem({ from, to });
      setOpened({ kind: "new" });
    };
    return () => {
      commentRef.current = null;
    };
  }, [commentRef, setDraftItem]);

  // A proposal's text in the document opens its card, by a click or by Enter on it.
  useEffect(() => {
    const dom = editor.view.dom;
    const target = (event: Event) =>
      (event.target as HTMLElement | null)?.closest?.<HTMLElement>(
        "[data-proposal]",
      );
    const click = (event: MouseEvent) => {
      const found = target(event);
      if (found?.dataset.proposal)
        setOpened({ kind: "proposal", id: found.dataset.proposal });
    };
    const key = (event: KeyboardEvent) => {
      if (event.key !== "Enter" && event.key !== " ") return;
      const found = target(event);
      if (!found?.dataset.proposal || !found.classList.contains("tilasm-new"))
        return;
      event.preventDefault();
      event.stopPropagation();
      setOpened({ kind: "proposal", id: found.dataset.proposal });
    };
    dom.addEventListener("click", click);
    dom.addEventListener("keydown", key, true);
    return () => {
      dom.removeEventListener("click", click);
      dom.removeEventListener("keydown", key, true);
    };
  }, [editor]);

  const draftOf = (key: string): Draft =>
    drafts.current.get(key) ?? { text: "", requestId: "", sentText: "" };
  const saveDraft = (key: string, text: string) => {
    drafts.current.set(key, { ...draftOf(key), text });
    writeDrafts(id, drafts.current);
  };
  const dropDraft = (key: string) => {
    drafts.current.delete(key);
    writeDrafts(id, drafts.current);
  };
  const fail = (key: string, text: string) =>
    setErrors((before) => ({ ...before, [key]: text }));

  // close folds the window open: a new comment with nothing written goes, its passage unmarked.
  const close = useCallback(
    (focusEditor: boolean) => {
      const now = live.current.opened;
      if (now?.kind === "new" && !drafts.current.get(NEW)?.text.trim())
        setDraftItem(null);
      setOpened(null);
      if (focusEditor) editor.commands.focus();
    },
    [editor, setDraftItem],
  );

  const sendNew = async (text: string) => {
    const draft = marksKey
      .getState(editor.state)
      ?.placed.find((p) => p.kind === "draft");
    if (!draft || draft.lost) return fail(NEW, t("tilasms.thread.gone"));
    setBusy(NEW);
    fail(NEW, "");
    try {
      if (!(await settle())) return fail(NEW, t("tilasms.thread.save_first"));
      const r = renderDoc(editor.state.doc, titleText());
      const anchor = anchorOf(r, draft.from, draft.to);
      if (!anchor) return fail(NEW, t("tilasms.thread.gone"));
      const kept = draftOf(NEW);
      const requestId =
        kept.sentText === text && kept.requestId
          ? kept.requestId
          : crypto.randomUUID();
      drafts.current.set(NEW, { text, requestId, sentText: text });
      const ask = (a: Anchor) =>
        clients.tilasms.comment({
          tilasm: ref,
          quote: a.quote,
          prefix: a.prefix,
          suffix: a.suffix,
          text,
          author: "developer",
          requestId,
        });
      let res;
      try {
        res = await ask(anchor);
      } catch (error) {
        // Djinn reads the text around the passage otherwise: the quote alone names it when it is there once.
        if (
          error instanceof ConnectError &&
          error.code === ConnectCode.FailedPrecondition &&
          /^(missing|changed):/.test(error.rawMessage) &&
          anchor.quote
        )
          res = await ask({ quote: anchor.quote, prefix: "", suffix: "" });
        else throw error;
      }
      const thread = res.thread;
      dropDraft(NEW);
      setDraftItem(null);
      if (thread) {
        seen.current.set(thread.id, thread.messages.length);
        setData((before) =>
          before && !before.threads.some((x) => x.id === thread.id)
            ? { ...before, threads: [...before.threads, thread] }
            : before,
        );
        setOpened({ kind: "thread", id: thread.id });
      }
      void refetch();
    } catch (error) {
      fail(NEW, refusal(error));
    } finally {
      setBusy("");
    }
  };

  const sendReply = async (thread: TilasmThread, text: string) => {
    setBusy(thread.id);
    fail(thread.id, "");
    const kept = draftOf(thread.id);
    const requestId =
      kept.sentText === text && kept.requestId
        ? kept.requestId
        : crypto.randomUUID();
    drafts.current.set(thread.id, { text, requestId, sentText: text });
    try {
      const res = await clients.tilasms.reply({
        thread: thread.id,
        text,
        author: "developer",
        requestId,
      });
      dropDraft(thread.id);
      const next = res.thread;
      if (next) {
        seen.current.set(next.id, next.messages.length);
        setData((before) =>
          before
            ? {
                ...before,
                threads: before.threads.map((x) =>
                  x.id === next.id ? next : x,
                ),
              }
            : before,
        );
      }
      void refetch();
      return true;
    } catch (error) {
      fail(thread.id, refusal(error));
      return false;
    } finally {
      setBusy("");
    }
  };

  const setResolved = async (thread: TilasmThread, resolved: boolean) => {
    setBusy(thread.id);
    fail(thread.id, "");
    try {
      await clients.tilasms.resolve({ thread: thread.id, resolved });
      if (resolved) close(false);
      await refetch();
    } catch (error) {
      fail(thread.id, refusal(error));
    } finally {
      setBusy("");
    }
  };

  const accept = async (proposal: TilasmProposal) => {
    setBusy(proposal.id);
    fail(proposal.id, "");
    try {
      // Djinn decides on the text it holds: what the developer typed is saved first.
      if (!(await settle()))
        return fail(proposal.id, t("tilasms.proposal.save_first"));
      const at = marksKey
        .getState(editor.state)
        ?.placed.find((p) => p.id === proposal.id);
      await clients.tilasms.accept({
        proposal: proposal.id,
        author: "developer",
      });
      if (live.current.opened?.kind === "proposal") setOpened(null);
      await reload(at ? at.to : editor.state.doc.content.size);
      await refetch();
    } catch (error) {
      fail(proposal.id, refusal(error));
    } finally {
      setBusy("");
    }
  };

  const reject = async (proposal: TilasmProposal) => {
    setBusy(proposal.id);
    fail(proposal.id, "");
    try {
      await clients.tilasms.reject({
        proposal: proposal.id,
        author: "developer",
      });
      if (
        live.current.opened?.kind === "proposal" &&
        live.current.opened.id === proposal.id
      )
        setOpened(null);
      await refetch();
    } catch (error) {
      fail(proposal.id, refusal(error));
    } finally {
      setBusy("");
    }
  };

  // What the editor marks now, and what is apart.
  const marks = editor.isDestroyed
    ? undefined
    : marksKey.getState(editor.state);
  const shown = new Map<string, Placed>();
  for (const p of marks?.placed ?? []) if (!p.lost) shown.set(p.id, p);
  const threads = data?.threads ?? [];
  const proposals = data?.proposals ?? [];
  const apartThreads = threads.filter((x) => !x.resolved && !shown.has(x.id));
  const apartProposals = proposals.filter(
    (x) => pending(x) && !shown.has(x.id),
  );
  const resolvedThreads = threads.filter((x) => x.resolved);
  const reasonOf = (state: TilasmAnchorState) =>
    state === TilasmAnchorState.ANCHORED
      ? apartText.changed()
      : apartText[apartOf(state)]();

  const view = layoutOf(editor, box);

  const threadBody = (thread: TilasmThread, onClose?: () => void) => (
    <ThreadBody
      key={thread.id}
      thread={thread}
      proposals={proposals.filter((x) => x.threadId === thread.id)}
      draft={draftOf(thread.id).text}
      onDraft={(text) => saveDraft(thread.id, text)}
      busy={busy}
      errors={errors}
      onSend={(text) => sendReply(thread, text)}
      onResolve={(resolved) => void setResolved(thread, resolved)}
      onAccept={(proposal) => void accept(proposal)}
      onReject={(proposal) => void reject(proposal)}
      canAccept={(proposal) => shown.has(proposal.id)}
      onClose={onClose}
    />
  );

  let floating: ReactNode = null;
  if (opened && view) {
    const range = opened.kind === "new" ? shown.get(NEW) : shown.get(opened.id);
    const at = range ? view.windowAt(range) : null;
    const thread =
      opened.kind === "thread"
        ? threads.find((x) => x.id === opened.id)
        : undefined;
    const proposal =
      opened.kind === "proposal"
        ? proposals.find((x) => x.id === opened.id)
        : undefined;
    if (at && (opened.kind === "new" || thread || proposal))
      floating = (
        <FloatingWindow
          key={opened.kind === "new" ? NEW : opened.id}
          at={at}
          label={
            opened.kind === "proposal"
              ? t("tilasms.proposal.title")
              : t("tilasms.thread.title")
          }
          onClose={close}
        >
          {opened.kind === "new" ? (
            <NewComment
              draft={draftOf(NEW).text}
              onDraft={(text) => saveDraft(NEW, text)}
              busy={busy === NEW}
              error={errors[NEW] ?? ""}
              onSend={(text) => void sendNew(text)}
              onCancel={() => {
                dropDraft(NEW);
                setDraftItem(null);
                setOpened(null);
                editor.commands.focus();
              }}
            />
          ) : thread ? (
            threadBody(thread)
          ) : proposal ? (
            <ProposalCard
              proposal={proposal}
              busy={busy === proposal.id}
              error={errors[proposal.id] ?? ""}
              canAccept
              onAccept={() => void accept(proposal)}
              onReject={() => void reject(proposal)}
            />
          ) : null}
        </FloatingWindow>
      );
  }

  // A dot by each discussion's passage, and by a new comment's folded draft.
  const dots: { id: string; count: number; draft: boolean; top: number }[] = [];
  if (view) {
    for (const p of shown.values()) {
      if (p.kind === "proposal") continue;
      if (opened && (opened.kind === "new" ? NEW : opened.id) === p.id)
        continue;
      const top = view.dotAt(p);
      if (top === null) continue;
      const thread = threads.find((x) => x.id === p.id);
      dots.push({
        id: p.id,
        count: thread?.messages.length ?? 0,
        draft: p.kind === "draft",
        top,
      });
    }
    dots.sort((a, b) => a.top - b.top);
    for (let i = 1; i < dots.length; i++)
      dots[i]!.top = Math.max(dots[i]!.top, dots[i - 1]!.top + 26);
  }

  const apartCount = apartThreads.length + apartProposals.length;
  return (
    <>
      {view &&
        dots.map((dot) => (
          <button
            key={dot.id}
            type="button"
            className={`tilasm-dot${unread.has(dot.id) ? " is-unread" : ""}${dot.draft ? " is-draft" : ""}`}
            style={{ top: dot.top, left: view.dotLeft }}
            data-dot={dot.id}
            title={
              dot.draft
                ? t("tilasms.thread.draft")
                : t("tilasms.thread.open", { count: dot.count })
            }
            aria-label={
              dot.draft
                ? t("tilasms.thread.draft")
                : unread.has(dot.id)
                  ? t("tilasms.thread.open_unread", { count: dot.count })
                  : t("tilasms.thread.open", { count: dot.count })
            }
            onClick={() => {
              close(false);
              if (dot.draft) setOpened({ kind: "new" });
              else {
                const thread = threads.find((x) => x.id === dot.id);
                if (thread) seen.current.set(thread.id, thread.messages.length);
                setUnread((before) => {
                  const next = new Set(before);
                  next.delete(dot.id);
                  return next;
                });
                setOpened({ kind: "thread", id: dot.id });
              }
            }}
          >
            <MessageSquare size={12} aria-hidden="true" />
            {!dot.draft && <span>{dot.count}</span>}
          </button>
        ))}
      {floating}
      {strip &&
        (apartCount > 0 || resolvedThreads.length > 0) &&
        createPortal(
          <Apart
            count={apartCount}
            threads={apartThreads}
            proposals={apartProposals}
            resolved={resolvedThreads}
            reasonOf={reasonOf}
            threadBody={threadBody}
            busy={busy}
            errors={errors}
            onReject={(proposal) => void reject(proposal)}
            onReopen={(thread) => void setResolved(thread, false)}
          />,
          strip,
        )}
    </>
  );
}

// locate finds an anchor in the editor's text; null when it is not there once, as it was.
function locate(r: Rendered, anchor: Anchor, size: number) {
  const place = resolve(r.text, anchor, true);
  if (place.state !== "anchored") return null;
  return rangeOf(r, place, size);
}

// layoutOf measures where the windows and the dots stand, in the editor's box, at each render.
function layoutOf(editor: Editor, box: RefObject<HTMLDivElement | null>) {
  const frame = box.current;
  if (!frame || editor.isDestroyed) return null;
  const view = editor.view;
  const outer = frame.getBoundingClientRect();
  const prose = view.dom.getBoundingClientRect();
  const scroller = scrollBox(frame);
  const right = scroller ? scroller.getBoundingClientRect().right : innerWidth;
  const room = right - prose.right;
  const coords = (pos: number) => {
    try {
      return view.coordsAtPos(Math.min(pos, view.state.doc.content.size));
    } catch {
      return null;
    }
  };
  return {
    // The dots stand in the margin at the right of the text, or at its edge when there is none.
    dotLeft: prose.right - outer.left + (room >= 48 ? 10 : -34),
    dotAt(p: { from: number }) {
      const at = coords(p.from);
      return at ? at.top - outer.top - 3 : null;
    },
    // A window stands to the right of its passage when the page has room, else under it.
    windowAt(p: { from: number; to: number }) {
      const start = coords(p.from);
      const end = coords(p.to);
      if (!start || !end) return null;
      const width = Math.min(300, outer.width);
      if (room >= width + 28)
        return {
          top: start.top - outer.top - 6,
          left: prose.right - outer.left + 18,
          width,
        };
      return {
        top: end.bottom - outer.top + 8,
        left: Math.max(
          0,
          Math.min(start.left - outer.left, outer.width - width),
        ),
        width,
      };
    },
  };
}

function scrollBox(element: HTMLElement): HTMLElement | null {
  for (let at = element.parentElement; at; at = at.parentElement) {
    const { overflowY } = getComputedStyle(at);
    if (overflowY === "auto" || overflowY === "scroll") return at;
  }
  return null;
}

// FloatingWindow is a small window over the document. A press outside it, or the focus leaving it, folds it; a move
// of the focus between its own fields and buttons does not. Escape folds it and gives the text the caret back.
function FloatingWindow({
  at,
  label,
  onClose,
  children,
}: {
  at: { top: number; left: number; width: number };
  label: string;
  onClose: (focusEditor: boolean) => void;
  children: ReactNode;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const closing = useRef(onClose);
  useLayoutEffect(() => {
    closing.current = onClose;
  });
  useEffect(() => {
    const outside = (event: PointerEvent) => {
      const target = event.target as Element | null;
      if (!target || panel.current?.contains(target)) return;
      // The bar and the menus that open a window are not outside it: they open the next one.
      if (target.closest?.(".tilasm-dot")) return;
      closing.current(false);
    };
    document.addEventListener("pointerdown", outside, true);
    return () => document.removeEventListener("pointerdown", outside, true);
  }, []);
  return (
    <div
      ref={panel}
      className="tilasm-float"
      role="dialog"
      aria-label={label}
      style={{ top: at.top, left: at.left, width: at.width }}
      onKeyDown={(event) => {
        if (event.key !== "Escape") return;
        event.preventDefault();
        event.stopPropagation();
        closing.current(true);
      }}
      onBlur={(event) => {
        const next = event.relatedTarget as Node | null;
        if (next && !event.currentTarget.contains(next)) closing.current(false);
      }}
    >
      {children}
    </div>
  );
}

// Composer is the field of a message, with its send button: Enter with Cmd or Ctrl sends.
function Composer({
  draft,
  onDraft,
  busy,
  label,
  placeholder,
  onSend,
  autoFocus,
  children,
}: {
  draft: string;
  onDraft: (text: string) => void;
  busy: boolean;
  label: string;
  placeholder: string;
  // Says, when it can, whether the message went: the field empties then.
  onSend: (text: string) => Promise<boolean> | void;
  autoFocus?: boolean;
  children?: ReactNode;
}) {
  const [text, setText] = useState(draft);
  const send = async () => {
    if (!text.trim() || busy) return;
    if ((await onSend(text.trim())) === true) setText("");
  };
  return (
    <form
      className="tilasm-composer"
      onSubmit={(event) => {
        event.preventDefault();
        void send();
      }}
    >
      <textarea
        value={text}
        rows={2}
        aria-label={label}
        placeholder={placeholder}
        autoFocus={autoFocus}
        onChange={(event) => {
          setText(event.target.value);
          onDraft(event.target.value);
        }}
        onKeyDown={(event) => {
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            void send();
          }
        }}
      />
      <div className="tilasm-float-actions">
        {children}
        <button
          type="submit"
          className="icon-button tilasm-send"
          disabled={!text.trim() || busy}
          aria-busy={busy}
          title={t("tilasms.thread.send")}
          aria-label={t("tilasms.thread.send")}
        >
          <CornerDownLeft size={14} aria-hidden="true" />
        </button>
      </div>
    </form>
  );
}

function NewComment({
  draft,
  onDraft,
  busy,
  error,
  onSend,
  onCancel,
}: {
  draft: string;
  onDraft: (text: string) => void;
  busy: boolean;
  error: string;
  onSend: (text: string) => void;
  onCancel: () => void;
}) {
  return (
    <>
      <Composer
        draft={draft}
        onDraft={onDraft}
        busy={busy}
        label={t("tilasms.thread.comment")}
        placeholder={t("tilasms.thread.comment_placeholder")}
        onSend={onSend}
        autoFocus
      >
        <button type="button" className="text-button" onClick={onCancel}>
          {t("tilasms.thread.cancel")}
        </button>
      </Composer>
      {error && (
        <p className="tilasm-float-error" role="alert">
          {error}
        </p>
      )}
    </>
  );
}

// ThreadBody is a discussion: its passage, its messages, its proposals, and the field to answer.
function ThreadBody({
  thread,
  proposals,
  draft,
  onDraft,
  busy,
  errors,
  onSend,
  onResolve,
  onAccept,
  onReject,
  canAccept,
  onClose,
}: {
  thread: TilasmThread;
  proposals: TilasmProposal[];
  draft: string;
  onDraft: (text: string) => void;
  busy: string;
  errors: Record<string, string>;
  onSend: (text: string) => Promise<boolean>;
  onResolve: (resolved: boolean) => void;
  onAccept: (proposal: TilasmProposal) => void;
  onReject: (proposal: TilasmProposal) => void;
  canAccept: (proposal: TilasmProposal) => boolean;
  onClose?: () => void;
}) {
  const quote = thread.current || thread.anchor?.quote || "";
  return (
    <div className="tilasm-thread">
      {quote && <p className="tilasm-thread-quote">{clip(quote, 160)}</p>}
      <ol className="tilasm-thread-messages">
        {thread.messages.map((m) => (
          <li key={m.id}>
            <p className="tilasm-thread-meta">
              <strong>{authorName(m.author)}</strong>
              <span>{when(m.createTime)}</span>
            </p>
            <MarkdownBody text={m.text} />
          </li>
        ))}
      </ol>
      {proposals.map((proposal) => (
        <ProposalCard
          key={proposal.id}
          proposal={proposal}
          busy={busy === proposal.id}
          error={errors[proposal.id] ?? ""}
          canAccept={canAccept(proposal)}
          onAccept={() => onAccept(proposal)}
          onReject={() => onReject(proposal)}
          nested
        />
      ))}
      {!thread.resolved && (
        <Composer
          draft={draft}
          onDraft={onDraft}
          busy={busy === thread.id}
          label={t("tilasms.thread.reply")}
          placeholder={t("tilasms.thread.reply_placeholder")}
          onSend={onSend}
          autoFocus={!onClose}
        >
          <button
            type="button"
            className="text-button"
            onClick={() => onResolve(true)}
          >
            <Check size={13} aria-hidden="true" />
            {t("tilasms.thread.resolve")}
          </button>
        </Composer>
      )}
      {errors[thread.id] && (
        <p className="tilasm-float-error" role="alert">
          {errors[thread.id]}
        </p>
      )}
    </div>
  );
}

function clip(text: string, n: number) {
  return text.length > n ? `${text.slice(0, n - 1)}…` : text;
}

// ProposalCard is a proposal: who, why, what it changes when it is not shown in the text, and the decision.
function ProposalCard({
  proposal,
  busy,
  error,
  canAccept,
  onAccept,
  onReject,
  nested = false,
}: {
  proposal: TilasmProposal;
  busy: boolean;
  error: string;
  canAccept: boolean;
  onAccept: () => void;
  onReject: () => void;
  nested?: boolean;
}) {
  const decided = !pending(proposal);
  return (
    <div className={`tilasm-proposal${nested ? " is-nested" : ""}`}>
      <p className="tilasm-thread-meta">
        <strong>{authorName(proposal.author)}</strong>
        <span>
          {decided
            ? proposal.status === TilasmProposalStatus.ACCEPTED
              ? t("tilasms.proposal.accepted")
              : t("tilasms.proposal.rejected")
            : t("tilasms.proposal.pending")}
        </span>
      </p>
      {proposal.note && <p className="tilasm-proposal-note">{proposal.note}</p>}
      {(nested || !canAccept) && !decided && (
        <p className="tilasm-proposal-diff">
          {(proposal.current || proposal.anchor?.quote) && (
            <del>
              <span className="tilasm-sr">{t("tilasms.proposal.removed")}</span>
              {clip(proposal.current || proposal.anchor?.quote || "", 200)}
            </del>
          )}
          {proposal.replacement && (
            <ins>
              <span className="tilasm-sr">{t("tilasms.proposal.added")}</span>
              {clip(proposal.replacement, 300)}
            </ins>
          )}
        </p>
      )}
      {!decided && (
        <div className="tilasm-float-actions">
          <button
            type="button"
            className="text-button"
            disabled={busy}
            onClick={onReject}
          >
            <X size={13} aria-hidden="true" />
            {t("tilasms.proposal.reject")}
          </button>
          {canAccept && (
            <button
              type="button"
              className="text-button tilasm-accept"
              disabled={busy}
              aria-busy={busy}
              onClick={onAccept}
            >
              <Check size={13} aria-hidden="true" />
              {t("tilasms.proposal.accept")}
            </button>
          )}
        </div>
      )}
      {error && (
        <p className="tilasm-float-error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

// Apart is the indicator, by the tabs, of what no longer stands on a passage, and the small window that lists it:
// each with why, a discussion opened in place, a proposal only rejected; the resolved discussions follow.
function Apart({
  count,
  threads,
  proposals,
  resolved,
  reasonOf,
  threadBody,
  busy,
  errors,
  onReject,
  onReopen,
}: {
  count: number;
  threads: TilasmThread[];
  proposals: TilasmProposal[];
  resolved: TilasmThread[];
  reasonOf: (state: TilasmAnchorState) => string;
  threadBody: (thread: TilasmThread, onClose?: () => void) => ReactNode;
  busy: string;
  errors: Record<string, string>;
  onReject: (proposal: TilasmProposal) => void;
  onReopen: (thread: TilasmThread) => void;
}) {
  const [open, setOpen] = useState(false);
  const [shown, setShown] = useState("");
  const anchor = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (!anchor.current?.contains(event.target as Node)) setOpen(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", escape);
    };
  }, [open]);
  const label =
    count > 0
      ? t("tilasms.apart.label", { count })
      : t("tilasms.apart.resolved_label");
  return (
    <span className="tilasm-versions-anchor" ref={anchor}>
      <button
        type="button"
        className={`icon-button tilasm-apart-button${count > 0 ? " has-apart" : ""}`}
        aria-expanded={open}
        title={label}
        aria-label={label}
        onClick={() => setOpen(!open)}
      >
        <Unlink size={14} aria-hidden="true" />
        {count > 0 && <span>{count}</span>}
      </button>
      {open && (
        <div
          className="tilasm-popover tilasm-apart"
          role="dialog"
          aria-label={label}
        >
          {count > 0 && (
            <p className="tilasm-apart-head">{t("tilasms.apart.title")}</p>
          )}
          <ul className="tilasm-apart-list">
            {threads.map((thread) => (
              <li key={thread.id}>
                <button
                  type="button"
                  className="tilasm-apart-item"
                  aria-expanded={shown === thread.id}
                  onClick={() => setShown(shown === thread.id ? "" : thread.id)}
                >
                  <MessageSquare size={13} aria-hidden="true" />
                  <span>
                    <strong>{clip(thread.anchor?.quote ?? "", 80)}</strong>
                    <small>{reasonOf(thread.anchorState)}</small>
                  </span>
                </button>
                {shown === thread.id && threadBody(thread, () => setShown(""))}
              </li>
            ))}
            {proposals.map((proposal) => (
              <li key={proposal.id}>
                <p className="tilasm-apart-reason">
                  {reasonOf(proposal.anchorState)}
                </p>
                <ProposalCard
                  proposal={proposal}
                  busy={busy === proposal.id}
                  error={errors[proposal.id] ?? ""}
                  canAccept={false}
                  onAccept={() => {}}
                  onReject={() => onReject(proposal)}
                  nested
                />
              </li>
            ))}
          </ul>
          {resolved.length > 0 && (
            <>
              <p className="tilasm-apart-head">{t("tilasms.apart.resolved")}</p>
              <ul className="tilasm-apart-list">
                {resolved.map((thread) => (
                  <li key={thread.id}>
                    <button
                      type="button"
                      className="tilasm-apart-item"
                      aria-expanded={shown === thread.id}
                      onClick={() =>
                        setShown(shown === thread.id ? "" : thread.id)
                      }
                    >
                      <Check size={13} aria-hidden="true" />
                      <span>
                        <strong>{clip(thread.anchor?.quote ?? "", 80)}</strong>
                        <small>
                          {thread.messages.at(-1)
                            ? authorName(thread.messages.at(-1)!.author)
                            : ""}
                        </small>
                      </span>
                    </button>
                    {shown === thread.id && (
                      <>
                        {threadBody(thread, () => setShown(""))}
                        <button
                          type="button"
                          className="text-button"
                          disabled={busy === thread.id}
                          onClick={() => onReopen(thread)}
                        >
                          {t("tilasms.thread.reopen")}
                        </button>
                      </>
                    )}
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>
      )}
    </span>
  );
}
