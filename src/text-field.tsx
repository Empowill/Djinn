// The window's free-text fields (W526): one textarea, TextField, and one line, TextLine, that every field the
// developer writes free text in uses (the pill, a question's note, a send bar, a comment). Their text is the browser's
// own: a key renders nothing around it, forces no layout, and its owner hears it without a render (W472, L50, L55: a
// controlled field rendered its whole card on each key, 3 to 9 ms of script, 3 to 5 times more under load).
//
// The owner holds the text out of its state, in useTyped, or in a store of its own (the pill's draft), and renders
// only when what it shows around the text changes: a send button the first letter enables, a plan a note changes.
// The text it renders with is written into the field when it differs from it: cleared once sent, another wish's.
// A textarea fits its height to its text, up to its CSS max-height, at the next frame after keys, once for all of
// them. A field with max shows its count from 80 % of it, warming at 95 %, the count kept out of the reader's place
// (scroll-anchor.ts).
import {
  type InputHTMLAttributes,
  type Ref,
  type RefObject,
  type TextareaHTMLAttributes,
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import { NOTE_MAX } from "./data/format";
import { onTextSize } from "./text-size";

// grow fits a textarea to its text, up to its CSS max-height, then lets it scroll inside: one forced layout, the
// text's height read once.
function grow(textarea: HTMLTextAreaElement) {
  textarea.style.height = "auto";
  textarea.style.overflowY = "hidden";
  const style =
    typeof window !== "undefined" &&
    typeof window.getComputedStyle === "function"
      ? window.getComputedStyle(textarea)
      : undefined;
  const maxHeight = Number.parseFloat(style?.maxHeight ?? "");
  // A border-box height holds the borders: without them the text was clipped by their width, and the field, never
  // fitting its text, was fitted again at each frame of typing (W526, a question's note).
  const borders =
    style?.boxSizing === "border-box"
      ? (Number.parseFloat(style.borderTopWidth) || 0) +
        (Number.parseFloat(style.borderBottomWidth) || 0)
      : 0;
  const full = textarea.scrollHeight + borders;
  const height = Number.isFinite(maxHeight) ? Math.min(full, maxHeight) : full;
  textarea.style.height = `${height}px`;
  textarea.style.overflowY = full > height ? "auto" : "hidden";
}

// useGrowSoon gives a function that fits the textarea at the next frame, once for all the keys that came before it:
// a key forces no layout of the page, under load the keys that arrive together cost one (W472). A text that grew and
// still fits, or that scrolls already, keeps its height: the frame's own layout is then the only one.
function useGrowSoon(box: RefObject<HTMLTextAreaElement | null>) {
  const frame = useRef(0);
  const length = useRef(0);
  useEffect(
    () => () => {
      if (frame.current) window.cancelAnimationFrame(frame.current);
      frame.current = 0;
    },
    [],
  );
  return useCallback(() => {
    if (frame.current) return;
    frame.current = window.requestAnimationFrame(() => {
      frame.current = 0;
      const textarea = box.current;
      if (!textarea) return;
      const longer = textarea.value.length > length.current;
      length.current = textarea.value.length;
      if (
        longer &&
        (textarea.style.overflowY === "auto" ||
          textarea.scrollHeight <= textarea.clientHeight)
      )
        return;
      grow(textarea);
    });
  }, [box]);
}

// useGrowTextarea fits a textarea now, when key changes, and when the window or its text size changes; nothing while
// it is not active (hidden).
export function useGrowTextarea(
  box: RefObject<HTMLTextAreaElement | null>,
  key: unknown,
  active = true,
) {
  useLayoutEffect(() => {
    if (!active) return;
    const textarea = box.current;
    if (!textarea) return;
    const resize = () => grow(textarea);
    resize();
    const stopTextSize = onTextSize(resize);
    window.addEventListener("resize", resize);
    return () => {
      stopTextSize();
      window.removeEventListener("resize", resize);
    };
  }, [box, key, active]);
}

// Typed is a field's text out of its owner's state: text is the text at the last render, read() the text now (a
// handler made at a render reads it, keys typed since included); type keeps a key's text, set changes it from code.
export type Typed<S> = {
  text: string;
  read: () => string;
  shown: S;
  type: (text: string) => void;
  set: (text: string) => void;
  // How many times set changed the text: a render it brings.
  revision: number;
};

// useTyped keeps a field's text out of its owner's state: a key renders the owner only when shape(text) changes,
// a primitive (that it holds text at all, its plan). shape must be the same function at each render.
export function useTyped<S>(
  shape: (text: string) => S,
  initial = "",
): Typed<S> {
  const text = useRef(initial);
  const [shownState, setShownState] = useState(() => shape(initial));
  // The shape last set: React may render the owner once for a state set to the value it holds already, so a key
  // that keeps the shape sets nothing.
  const last = useRef(shownState);
  const setShown = useCallback((next: S) => {
    if (Object.is(next, last.current)) return;
    last.current = next;
    setShownState(next);
  }, []);
  // set renders the owner even when the shape stays, so the field gets the text written in it.
  const [revision, setRevision] = useState(0);
  const read = useCallback(() => text.current, []);
  const type = useCallback(
    (next: string) => {
      text.current = next;
      setShown(shape(next));
    },
    [shape, setShown],
  );
  const set = useCallback(
    (next: string) => {
      text.current = next;
      setShown(shape(next));
      setRevision((n) => n + 1);
    },
    [shape, setShown],
  );
  return { text: text.current, read, shown: shownState, type, set, revision };
}

// hasText is the shape of a text whose owner only shows whether it holds something.
export const hasText = (text: string) => text.trim() !== "";

// countOf is the count a field shows for length, -1 when it shows none: from 80 % of max.
const countOf = (length: number, max?: number) =>
  max !== undefined && length >= max * 0.8 ? length : -1;

// NoteCounter shows a character count from 80 % of the limit (6400 / 8000), warming at 95 % (7600 / 8000). Its text
// changes at each key: the reader's place does not hear it (data-anchor-skip, scroll-anchor.ts).
export function NoteCounter({
  length,
  max = NOTE_MAX,
}: {
  length: number;
  max?: number;
}) {
  if (length < max * 0.8) return null;
  return (
    <span
      className={`note-count${length >= max * 0.95 ? " near" : ""}`}
      data-anchor-skip=""
    >
      {length} / {max}
    </span>
  );
}

type Own<E> = {
  // The text the owner holds: the field's at its first render, written into it when a render brings another.
  text: string;
  // Each key's text, as typed.
  onText: (text: string) => void;
  // The field's element, for focus and selection.
  fieldRef?: Ref<E | null>;
};

// useField writes the owner's text into the field when it differs, and keeps the count shown: the count renders this
// field alone, and only while it shows.
function useField<E extends HTMLTextAreaElement | HTMLInputElement>(
  field: RefObject<E | null>,
  text: string,
  onText: (text: string) => void,
  fieldRef: Ref<E | null> | undefined,
  max: number | undefined,
  written?: () => void,
) {
  useImperativeHandle(fieldRef, () => field.current as E, [field]);
  const [countState, setCountState] = useState(() => countOf(text.length, max));
  // Set only when it changes, as useTyped's shape: a key under 80 % renders not even the field.
  const shownCount = useRef(countState);
  const setCount = (next: number) => {
    if (next === shownCount.current) return;
    shownCount.current = next;
    setCountState(next);
  };
  // The owner's text is written only when it changes: a render of the field alone (its count), or of the owner for
  // another reason, brings a text the keys since have left behind.
  const applied = useRef(text);
  useLayoutEffect(() => {
    if (applied.current === text) return;
    applied.current = text;
    const el = field.current;
    if (!el || el.value === text) return;
    el.value = text;
    setCount(countOf(text.length, max));
    written?.();
  });
  const change = (value: string) => {
    setCount(countOf(value.length, max));
    onText(value);
  };
  return { count: countState, change };
}

export type TextFieldProps = Own<HTMLTextAreaElement> &
  Omit<
    TextareaHTMLAttributes<HTMLTextAreaElement>,
    "value" | "defaultValue" | "onChange"
  > & {
    // Fits its height to its text, up to its CSS max-height; off, it keeps its rows.
    grow?: boolean;
    // Fitted again when it changes (another wish).
    fitKey?: unknown;
    // Off while the field is not shown (a folded pill): fitted when it is again.
    fit?: boolean;
  };

// TextField is the free-text textarea: see the top of the file.
export function TextField({
  text,
  onText,
  fieldRef,
  grow: grows = true,
  fitKey,
  fit = true,
  maxLength,
  ...rest
}: TextFieldProps) {
  const field = useRef<HTMLTextAreaElement>(null);
  const growSoon = useGrowSoon(field);
  const { count, change } = useField(
    field,
    text,
    onText,
    fieldRef,
    maxLength,
    grows ? growSoon : undefined,
  );
  useGrowTextarea(field, fitKey, grows && fit);
  return (
    <>
      <textarea
        {...rest}
        ref={field}
        maxLength={maxLength}
        defaultValue={text}
        onChange={(event) => {
          change(event.target.value);
          if (grows) growSoon();
        }}
      />
      {count >= 0 && <NoteCounter length={count} max={maxLength} />}
    </>
  );
}

export type TextLineProps = Own<HTMLInputElement> &
  Omit<
    InputHTMLAttributes<HTMLInputElement>,
    "value" | "defaultValue" | "onChange" | "type"
  >;

// TextLine is the free-text line: see the top of the file.
export function TextLine({ text, onText, fieldRef, ...rest }: TextLineProps) {
  const field = useRef<HTMLInputElement>(null);
  const { change } = useField(field, text, onText, fieldRef, undefined);
  return (
    <input
      {...rest}
      ref={field}
      type="text"
      defaultValue={text}
      onChange={(event) => change(event.target.value)}
    />
  );
}

// noShape is the shape of a text whose owner shows nothing of it: a key never renders the owner.
export const noShape = () => 0;
