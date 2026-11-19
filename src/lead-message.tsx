// Writing to the lead, the Djinn in the window: the pill at the bottom centre of a wish, or of the flying carpet,
// unfolds into a box whose text goes to the one lead's terminal (LeadService.Tell), about the wish if there is one, as
// if the developer typed it there. Enter sends, Shift+Enter breaks the line, Escape folds the pill and returns focus
// to it, Cmd/Ctrl+I opens it. A click or the focus going anywhere else folds it too, so the shortcut always opens it
// focused. The draft stays with the wish, the flying carpet's with it. When the lead answers or tells a decision it
// took alone (LeadService.Say: blocks of kind reply or decided), the eye comes to the socket and speaks in a bubble
// held to the pill (Decision Q78 A, tilasm L13 v6), one sentence that fades 2 minutes after it shows (T70) unless the
// pointer rests on it. A file pasted into the box (Ctrl+V: a screenshot, a log) becomes a chip above the text, sent with
// it: the server keeps it in the wish and the lead's message names its path (design A of L34).
import {
  Check,
  Clock,
  FileText,
  MessageSquare,
  Send,
  Terminal,
  X,
} from "lucide-react";
import {
  type ClipboardEvent,
  type KeyboardEvent,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import type { Block } from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useOptionalData, useStore } from "./data/djinn";
import {
  advanceLeadSay,
  bubblePlace,
  leadSayLeftMs,
  leadSayQueue,
  leadSayShown,
  type LeadSayShown,
  readLastSent,
  readSeenIds,
  writeLastSent,
  writeSeenIds,
} from "./data/eye";
import type { WishDetail } from "./data/store";
import { EyePerch, useEyeLead, useEyeSpeaking } from "./djinn-eye";
import { t } from "./i18n";
import {
  type InstructionDraft,
  type PastedFile,
  useInstructionDraft,
} from "./instruction-draft";
import { TextField } from "./text-field";
import { platformShortcut, TooltipButton } from "./tooltip";
import { WorkspaceAction } from "./workspace-action";
import "./instruction.css";

const emptyDetails: Record<string, WishDetail> = {};
const emptyBlocks: readonly Block[] = [];

// The most files one message hands the lead, and the largest, as the server takes them (LeadServiceTellRequest).
export const maxFiles = 10;
export const maxFileBytes = 20 * 1024 * 1024;

// pastedFiles are the files a paste carries: its files, else its items of kind file (a screenshot may come as an item
// only).
export function pastedFiles(data: DataTransfer | null): File[] {
  if (!data) return [];
  const files = Array.from(data.files ?? []);
  if (files.length > 0) return files;
  for (const item of Array.from(data.items ?? [])) {
    if (item.kind !== "file") continue;
    const file = item.getAsFile();
    if (file) files.push(file);
  }
  return files;
}

// clipboardFiles reads the files the clipboard holds, other than text: WebKitGTK on GTK 3 gives the paste event of a
// screenshot no type at all (measured on 2.52, W164), and gives the image to the Clipboard API only. A file is named
// after its type: image.png.
export async function clipboardFiles(): Promise<File[]> {
  const files: File[] = [];
  try {
    for (const item of await navigator.clipboard.read()) {
      const type = item.types.find((t) => !t.startsWith("text/"));
      if (!type) continue;
      const blob = await item.getType(type);
      const [kind, sub = "bin"] = type.split("/");
      const ext = sub.split(/[+;.-]/)[0] || "bin";
      files.push(
        new File([blob], `${kind === "image" ? "image" : "file"}.${ext}`, {
          type,
        }),
      );
    }
  } catch {
    // No access to the clipboard, or nothing in it: the paste brings nothing.
  }
  return files;
}

// addFiles adds the files pasted to those kept, within the limits; refused says what was left out, and why.
export function addFiles(
  kept: readonly PastedFile[],
  added: readonly File[],
  id: () => string,
): { files: PastedFile[]; refused: string } {
  const files = [...kept];
  const tooBig: string[] = [];
  const tooMany: string[] = [];
  for (const file of added) {
    if (file.size > maxFileBytes) tooBig.push(fileName(file));
    else if (files.length >= maxFiles) tooMany.push(fileName(file));
    else files.push({ id: id(), file });
  }
  const refused: string[] = [];
  if (tooBig.length > 0)
    refused.push(
      t("lead_message.file_too_big", {
        max: maxFileBytes / 1024 / 1024,
        names: tooBig.join(", "),
      }),
    );
  if (tooMany.length > 0)
    refused.push(
      t("lead_message.files_too_many", {
        max: maxFiles,
        names: tooMany.join(", "),
      }),
    );
  return { files, refused: refused.join(" ") };
}

// fileName is how a chip names a file; a screenshot may come with none.
export function fileName(file: File): string {
  return file.name || "image";
}

// sizeText is a size in bytes as a person reads it, as the lead's message says it: 512 B, 240 KB, 3.4 MB.
export function sizeText(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

let fileIds = 0;
const nextFileId = () => `f${++fileIds}`;

// thumbnail draws an image file small, for its chip: a data URL (the page's policy takes no blob: image) and its size
// in pixels; nothing for a file that is no image the engine reads.
async function thumbnail(
  file: File,
): Promise<{ thumb: string; dims: string } | undefined> {
  if (!file.type.startsWith("image/")) return;
  try {
    const bitmap = await createImageBitmap(file);
    const side = 64;
    const scale = Math.min(1, side / Math.max(bitmap.width, bitmap.height));
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    canvas
      .getContext("2d")
      ?.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const dims = `${bitmap.width}\u00d7${bitmap.height}`;
    bitmap.close();
    return { thumb: canvas.toDataURL("image/png"), dims };
  } catch {
    return;
  }
}

// keepFiles adds the files pasted to the draft, says what it left out, then draws the images' thumbnails.
function keepFiles(
  draft: {
    get: () => InstructionDraft;
    set: (patch: Partial<InstructionDraft>) => void;
  },
  added: readonly File[],
) {
  const before = draft.get().files.length;
  const { files, refused } = addFiles(draft.get().files, added, nextFileId);
  draft.set({ files, error: refused, notice: "" });
  for (const pasted of files.slice(before)) {
    void thumbnail(pasted.file).then((drawn) => {
      if (!drawn) return;
      draft.set({
        files: draft
          .get()
          .files.map((f) => (f.id === pasted.id ? { ...f, ...drawn } : f)),
      });
    });
  }
}

export function LeadMessage({
  wishId = "",
  stopped,
  onResume,
}: {
  // The wish the text is about; none on the flying carpet.
  wishId?: string;
  // No lead, or it ended or could not start: the pill offers to start it, as Lead does, before its text can reach it.
  stopped: boolean;
  onResume: () => Promise<unknown>;
}) {
  const clients = useClients();
  const store = useStore();
  const { draft, state } = useInstructionDraft(store, wishId);
  const [open, setOpen] = useState(false);
  const [resuming, setResuming] = useState(false);
  // A resume may focus the lead's terminal on its way; the box takes the focus back after it, so it stays open.
  const resumingNow = useRef(false);
  // The last text was refused, the lead not running in its terminal: the pill offers to start it too.
  const [refused, setRefused] = useState(false);
  const offline = stopped || refused;
  useEyeLead(offline);
  const details = useOptionalData((s) => s.details, emptyDetails);
  const blocks = useMemo(() => {
    if (wishId) return details[wishId]?.blocks ?? emptyBlocks;
    const all: Block[] = [];
    for (const d of Object.values(details)) {
      for (const b of d.blocks) all.push(b);
    }
    return all;
  }, [details, wishId]);
  const [seen, setSeen] = useState<Set<string>>(() => readSeenIds());
  const [lastSent, setLastSent] = useState<string>(() => readLastSent(wishId));
  useEffect(() => {
    setLastSent(readLastSent(wishId));
  }, [wishId]);
  const queue = useMemo(() => leadSayQueue(blocks, seen), [blocks, seen]);
  const current = queue[0];
  useEyeSpeaking(queue.length > 0);
  const [bubbleOpen, setBubbleOpen] = useState(false);
  // The front message fades, seen, 2 minutes after it shows; the pointer on the bubble holds it while it is read.
  const [shown, setShown] = useState<LeadSayShown | undefined>();
  const [hovered, setHovered] = useState(false);
  const frontId = current?.id;
  useEffect(() => {
    setShown((prev) => leadSayShown(frontId, prev, Date.now()));
  }, [frontId]);
  useEffect(() => {
    if (!shown || shown.id !== frontId || hovered) return;
    const timer = setTimeout(
      () => {
        const next = new Set(seen);
        next.add(shown.id);
        setSeen(next);
        writeSeenIds(next);
      },
      leadSayLeftMs(shown, Date.now()),
    );
    return () => clearTimeout(timer);
  }, [shown, frontId, hovered, seen]);
  const hoverTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const anchor = useRef<HTMLDivElement>(null);
  const [roomLeft, setRoomLeft] = useState(1024);
  const box = useRef<HTMLTextAreaElement>(null);
  const root = useRef<HTMLDivElement>(null);
  const restoreFocus = useRef(false);
  const id = useId();
  const mounted = useRef(true);
  const shortcut = platformShortcut("I");
  const pasteShortcut = platformShortcut("V");
  const placeholder = wishId
    ? t("lead_message.placeholder")
    : t("plan.write_placeholder");
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      if (hoverTimer.current) clearTimeout(hoverTimer.current);
    };
  }, []);
  useLayoutEffect(() => {
    if (!current) {
      setBubbleOpen(false);
      setHovered(false);
      return;
    }
    const measure = () => {
      const rect = anchor.current?.getBoundingClientRect();
      if (rect && rect.width > 0) setRoomLeft(rect.left - 24);
    };
    measure();
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, [current, open]);
  useEffect(() => {
    if (open) {
      box.current?.focus({ preventScroll: true });
      return;
    }
    if (!restoreFocus.current) return;
    restoreFocus.current = false;
    const frame = window.requestAnimationFrame(() =>
      document.getElementById(`${id}-toggle`)?.focus({ preventScroll: true }),
    );
    return () => window.cancelAnimationFrame(frame);
  }, [open, wishId, id]);
  // The box is not controlled (TextField): a key goes to the draft (draft.type), which renders nothing (W472, L50:
  // under load each render and layout of a key cost 3 to 5 times their time). It is fitted when it opens, on another
  // wish, and at the frame after keys; a text the draft changes itself (cleared once sent) is written in it.
  useEffect(() => {
    const keys = (event: globalThis.KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.repeat ||
        event.isComposing ||
        event.altKey ||
        event.shiftKey ||
        !(event.metaKey || event.ctrlKey) ||
        event.key.toLowerCase() !== "i"
      )
        return;
      if (document.querySelector(".modal-backdrop")) return;
      if (event.target instanceof HTMLElement) {
        if (
          event.target.isContentEditable ||
          event.target.matches(
            "input, textarea, select, [contenteditable='true']",
          ) ||
          event.target.closest(".xterm")
        )
          return;
      }
      event.preventDefault();
      setOpen(true);
      box.current?.focus({ preventScroll: true });
    };
    window.addEventListener("keydown", keys);
    return () => window.removeEventListener("keydown", keys);
  }, []);
  // Focus out folds the box, the focus left where it went: a press or a focus outside the pill, not the window losing
  // the focus to another application.
  useEffect(() => {
    if (!open) return;
    const away = (event: Event) => {
      if (resumingNow.current) return;
      const target = event.target;
      if (target instanceof Node && root.current?.contains(target)) return;
      setOpen(false);
    };
    document.addEventListener("pointerdown", away, true);
    document.addEventListener("focusin", away, true);
    return () => {
      document.removeEventListener("pointerdown", away, true);
      document.removeEventListener("focusin", away, true);
    };
  }, [open]);
  useEffect(() => {
    if (!state.notice) return;
    const notice = state.notice;
    const timer = setTimeout(() => {
      if (draft.get().notice === notice) draft.set({ notice: "" });
    }, 4000);
    return () => clearTimeout(timer);
  }, [draft, state.notice]);

  const close = () => {
    restoreFocus.current = true;
    setOpen(false);
  };
  const send = async () => {
    // Synchronous lock, so Enter followed at once by a click sends the text once.
    const text = draft.begin();
    if (text === undefined) return;
    const pasted = draft.get().files;
    try {
      const files = await Promise.all(
        pasted.map(async ({ file }) => ({
          name: file.name,
          type: file.type,
          data: new Uint8Array(await file.arrayBuffer()),
        })),
      );
      const { waiting } = await clients.lead.tell({ text, wishId, files });
      const said = text || pasted.map((p) => fileName(p.file)).join(", ");
      writeLastSent(wishId, said);
      setLastSent(said.replace(/\s+/g, " ").trim());
      draft.set({ text: "", files: [], notice: waiting ? "waiting" : "sent" });
      setRefused(false);
    } catch (error) {
      // Said in the pill, by the text it concerns: no toast besides.
      draft.set({ error: message(error) });
      setRefused(true);
    } finally {
      draft.set({ busy: false });
      if (mounted.current) box.current?.focus();
    }
  };
  const resume = async () => {
    if (resuming) return;
    setResuming(true);
    resumingNow.current = true;
    try {
      await onResume();
      draft.set({ error: "" });
      setRefused(false);
    } catch {
      // Lead's own toast says why it could not start, as from the header's Lead.
    } finally {
      resumingNow.current = false;
      if (mounted.current) {
        setResuming(false);
        box.current?.focus();
      }
    }
  };
  // A paste that carries files keeps them as chips; a paste of text alone goes in the box as usual.
  const paste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    if (state.busy) return;
    const files = pastedFiles(event.clipboardData);
    if (files.length > 0) {
      event.preventDefault();
      keepFiles(draft, files);
      return;
    }
    // A paste that carries no type at all is a screenshot in WebKitGTK: read it from the clipboard, while the paste
    // still allows it.
    if (
      (event.clipboardData?.types.length ?? 0) > 0 ||
      !navigator.clipboard?.read
    )
      return;
    void clipboardFiles().then((read) => {
      if (read.length > 0 && mounted.current) keepFiles(draft, read);
    });
  };
  const removeFile = (id: string) => {
    draft.set({ files: draft.get().files.filter((f) => f.id !== id) });
    box.current?.focus({ preventScroll: true });
  };
  const keys = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      close();
    } else if (
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      void send();
    }
  };
  const place = bubblePlace(open, roomLeft);
  const isDecided = current?.kind.toLowerCase() === "decided";
  return (
    <div className="lead-message" ref={root} data-eye-place="">
      <div className="lead-message-anchor" ref={anchor}>
        {current && (
          <div
            className={`lead-bubble ${place === "side" ? "is-side" : "is-above"}${bubbleOpen ? " is-open" : ""}`}
            role="status"
            aria-live="polite"
            onMouseDown={(event) => event.preventDefault()}
            onMouseEnter={() => {
              setHovered(true);
              if (hoverTimer.current) clearTimeout(hoverTimer.current);
              hoverTimer.current = setTimeout(() => setBubbleOpen(true), 150);
            }}
            onMouseLeave={() => {
              setHovered(false);
              if (hoverTimer.current) clearTimeout(hoverTimer.current);
              hoverTimer.current = setTimeout(() => setBubbleOpen(false), 300);
            }}
            onClick={() => {
              if (hoverTimer.current) clearTimeout(hoverTimer.current);
              const next = advanceLeadSay(queue, seen);
              setSeen(next.seen);
              writeSeenIds(next.seen);
              if (!next.next) setBubbleOpen(false);
            }}
          >
            <div className="lead-bubble-head">
              <span className="lead-bubble-kind">
                {isDecided
                  ? t("lead_message.decided")
                  : t("lead_message.reply")}
              </span>
              {queue.length > 1 && (
                <span className="lead-bubble-count">1 / {queue.length}</span>
              )}
            </div>
            {!isDecided && lastSent && (
              <div className="lead-bubble-quote">
                {t("lead_message.in_reply_to", { text: lastSent })}
              </div>
            )}
            <div className="lead-bubble-text">{current.content?.trim()}</div>
          </div>
        )}
        <WorkspaceAction
          tone="secondary"
          expanded={open}
          className="workspace-action-lead"
          perch={
            // The socket on the pill's left, a porthole of the seal's emerald: its icon at rest, the eye when it listens,
            // behind two halves that part for it (djinn-eye.css). The whole pill is the eye's place: folded or open, the
            // focus in it brings the eye to the socket.
            <span className="lead-socket">
              <EyePerch name="pill" opens />
              <span className="lead-socket-upper" />
              <span className="lead-socket-lower" />
              <MessageSquare size={14} aria-hidden="true" />
              <span className="lead-socket-ring" />
            </span>
          }
        >
          {open ? (
            <div
              id={`${id}-panel`}
              className="lead-message-panel workspace-action-editor-shell"
              role="dialog"
              aria-label={t("lead_message.title")}
              onKeyDown={(event) => {
                if (event.key !== "Escape") return;
                event.preventDefault();
                event.stopPropagation();
                close();
              }}
            >
              <form
                className={`workspace-action-editor ${offline ? "" : "is-running"}${state.files.length > 0 ? " has-files" : ""}`}
                onSubmit={(event) => {
                  event.preventDefault();
                  void send();
                }}
              >
                {offline && (
                  <TooltipButton
                    className="workspace-action-leading"
                    disabled={resuming}
                    onClick={() => void resume()}
                    label={t("lead_message.resume")}
                  >
                    <Terminal size={13} aria-hidden="true" />
                  </TooltipButton>
                )}
                {state.files.length > 0 && (
                  <ul
                    className="lead-files"
                    aria-label={t("lead_message.files")}
                  >
                    {state.files.map((pasted) => (
                      <li key={pasted.id} className="lead-file">
                        {pasted.thumb ? (
                          <img
                            className="lead-file-thumb"
                            src={pasted.thumb}
                            alt=""
                          />
                        ) : (
                          <span className="lead-file-thumb">
                            <FileText size={14} aria-hidden="true" />
                          </span>
                        )}
                        <span className="lead-file-text">
                          <span
                            className="lead-file-name"
                            title={fileName(pasted.file)}
                          >
                            {fileName(pasted.file)}
                          </span>
                          <span className="lead-file-size">
                            {pasted.dims ? `${pasted.dims} \u00b7 ` : ""}
                            {sizeText(pasted.file.size)}
                          </span>
                        </span>
                        <TooltipButton
                          className="lead-file-remove"
                          disabled={state.busy}
                          onClick={() => removeFile(pasted.id)}
                          label={t("lead_message.file_remove", {
                            name: fileName(pasted.file),
                          })}
                        >
                          <X size={12} aria-hidden="true" />
                        </TooltipButton>
                      </li>
                    ))}
                  </ul>
                )}
                <label
                  htmlFor={`${id}-input`}
                  className="workspace-action-sr-only"
                >
                  {placeholder}
                </label>
                <TextField
                  key={wishId}
                  id={`${id}-input`}
                  fieldRef={box}
                  rows={1}
                  text={state.text}
                  onText={(text) => draft.type(text)}
                  fitKey={wishId}
                  fit={open}
                  onKeyDown={keys}
                  onPaste={paste}
                  placeholder={placeholder}
                  aria-invalid={!!state.error}
                  aria-describedby={
                    state.error
                      ? `${id}-error`
                      : state.notice
                        ? `${id}-notice`
                        : undefined
                  }
                  readOnly={state.busy}
                />
                {state.error && (
                  <p
                    id={`${id}-error`}
                    className="instruction-error workspace-action-feedback"
                    role="alert"
                  >
                    {state.error}
                  </p>
                )}
                {state.notice && (
                  <span
                    id={`${id}-notice`}
                    className={`lead-message-sent workspace-action-feedback ${state.notice}`}
                    role="status"
                  >
                    {state.notice === "waiting" ? (
                      <Clock size={12} aria-hidden="true" />
                    ) : (
                      <Check size={12} aria-hidden="true" />
                    )}
                    {state.notice === "waiting"
                      ? t("lead_message.waiting")
                      : t("lead_message.sent")}
                  </span>
                )}
                <TooltipButton
                  type="submit"
                  className="workspace-action-send"
                  disabled={
                    state.busy ||
                    (!state.text.trim() && state.files.length === 0)
                  }
                  label={t("lead_message.send")}
                  tooltip={`${t("lead_message.hint")} \u00b7 ${t("lead_message.paste_hint", { paste: pasteShortcut.label })}`}
                >
                  <Send size={14} aria-hidden="true" />
                </TooltipButton>
                <TooltipButton
                  className="workspace-action-close"
                  onClick={close}
                  label={t("lead_message.close")}
                >
                  <X size={13} aria-hidden="true" />
                </TooltipButton>
              </form>
            </div>
          ) : (
            <TooltipButton
              id={`${id}-toggle`}
              className="workspace-action-control"
              onClick={() => setOpen(true)}
              label={t("lead_message.open")}
              tooltip=""
              shortcut={shortcut.label}
              aria-keyshortcuts={shortcut.aria}
              aria-expanded={false}
              aria-controls={`${id}-panel`}
            >
              <span>{t("lead_message.open")}</span>
            </TooltipButton>
          )}
        </WorkspaceAction>
      </div>
    </div>
  );
}
