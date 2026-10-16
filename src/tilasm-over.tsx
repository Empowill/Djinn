// A tilasm a link in a question names opens over the view the question shows in (the flying carpet, a wish's first
// page), full size, in the Tilasms tab's own frame (TilasmFrame, tilasms.tsx), without navigating: the view underneath
// stays mounted, made inert, its scroll and its open question as they were. Closing it, by its button or Escape, gives
// the focus back to the link, or to its question if the link is gone, without scrolling: nothing underneath moves.
import {
  type ReactNode,
  createContext,
  use,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import type { Tilasm } from "../gen/ts/plan/v1/tilasm_pb";
import { useDjinn } from "./data/djinn";
import { parseDjinnLink } from "./data/links";
import { TilasmFrame } from "./tilasms";

// Opens the tilasm a djinn:// link names over the view, from the element clicked; undefined outside a TilasmOver.
type OpenOver = (href: string, from: HTMLElement) => void;

export const TilasmOverContext = createContext<OpenOver | undefined>(undefined);
export const useTilasmOver = () => use(TilasmOverContext);

// Where the focus goes back once the tilasm closes: the link, else the same link in its question drawn again, else
// the question.
export interface Back {
  link: HTMLElement;
  href: string;
  // The question card's id (question-<id>); empty out of a question.
  question: string;
}

// backOf is where the focus goes back to, for a link clicked.
export function backOf(link: HTMLElement, href: string): Back {
  return { link, href, question: link.closest(".question-card")?.id ?? "" };
}

// giveBack gives the focus back to the link, else to its question, without scrolling; it returns what took it.
export function giveBack(back: Back, doc: Document = document) {
  let to: HTMLElement | null = back.link.isConnected ? back.link : null;
  const question = back.question ? doc.getElementById(back.question) : null;
  if (!to && question)
    to =
      [...question.querySelectorAll<HTMLElement>("a.djinn-link")].find(
        (a) => a.getAttribute("href") === back.href,
      ) ?? question;
  to?.focus({ preventScroll: true });
  return to;
}

// TilasmOver holds the tilasm opened over the view, for the links within; view names the view shown: another one
// closes the tilasm, and gives the focus back to nothing.
export function TilasmOver({
  view,
  children,
}: {
  view: string;
  children: ReactNode;
}) {
  const djinn = useDjinn();
  const [over, setOver] = useState<{
    tilasm: Tilasm;
    view: string;
    back: Back;
  }>();
  // The tilasm was opened over another view, now gone: it closes with it.
  if (over && over.view !== view) setOver(undefined);
  // Where the focus goes back once the frame is gone and its cover lifted (its effects end before this one runs).
  const returning = useRef<Back>(undefined);
  useEffect(() => {
    if (over) return;
    const back = returning.current;
    returning.current = undefined;
    if (back) giveBack(back);
  }, [over]);
  const open = useCallback<OpenOver>(
    (href, from) => {
      if (!djinn) return;
      const id = parseDjinnLink(href)?.id ?? "";
      const back = backOf(from, href);
      djinn.clients.tilasms
        .get({ tilasm: { ref: { case: "id", value: id } } })
        .then((res) => {
          if (!res.tilasm) throw new Error("no tilasm");
          setOver({ tilasm: res.tilasm, view, back });
        })
        .catch(() => djinn.focus.show({ unknownLink: href }));
    },
    [djinn, view],
  );
  const close = useCallback(() => {
    returning.current = over?.back;
    setOver(undefined);
  }, [over]);
  return (
    <TilasmOverContext value={open}>
      {children}
      {over && (
        <TilasmFrame
          tilasm={over.tilasm}
          full
          over
          onFull={close}
          onClose={close}
        />
      )}
    </TilasmOverContext>
  );
}
