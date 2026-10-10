// The read mark, the click that says "I saw it" on an open question without a word: it goes to the lamp
// (MarkService.Put), where the lead reads it in the brief and with djinn mark list. A block takes no mark: it is for
// agents (src/agent-blocks.tsx). And the writes the screens share: an answer, a mark, a request to investigate, each
// read again once djinn has it.
import { Check, Eye } from "lucide-react";
import { useState } from "react";

import {
  Change,
  type Choice,
  type Mark,
  MarkKind,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useStore } from "./data/djinn";
import { markOf } from "./data/format";
import { t } from "./i18n";

export type OnMark = (kind: MarkKind, remove: boolean) => Promise<void>;

// MarkButtons are an open question's read mark, a toggle. A decision has none: it was taken (the decision log).
export function MarkButtons({
  item,
  onMark,
}: {
  item: { marks?: Mark[] };
  onMark: OnMark;
}) {
  const [sending, setSending] = useState(false);
  const toggle = (kind: MarkKind) => {
    setSending(true);
    void onMark(kind, !!markOf(item, kind))
      .catch(() => {})
      .finally(() => setSending(false));
  };
  const read = !!markOf(item, MarkKind.READ);
  return (
    <span className="mark-buttons">
      <button
        className={`mark-toggle ${read ? "on" : ""}`}
        aria-pressed={read}
        disabled={sending}
        title={read ? t("mark.read_undo") : t("mark.read_detail")}
        onClick={() => toggle(MarkKind.READ)}
      >
        {read ? <Check size={13} /> : <Eye size={13} />}
        {read ? t("mark.read_done") : t("mark.read")}
      </button>
    </span>
  );
}

// useWrites gives the writes of the screens: djinn's refusal shows as a toast and rejects, and what changed in the
// wish is read again either way.
export function useWrites(onToast: (text: string) => void) {
  const clients = useClients();
  const store = useStore();
  // done is the toast once run succeeds, or makes it then, from what run found.
  const act = async (
    wishId: string,
    run: () => Promise<unknown>,
    changes: Change[],
    done?: string | (() => string),
  ) => {
    try {
      await run();
      const text = typeof done === "function" ? done() : done;
      if (text) onToast(text);
    } catch (error) {
      onToast(message(error));
      throw error;
    } finally {
      void store.changed(wishId, changes);
    }
  };
  const questions = [Change.QUESTION, Change.WISH];
  return {
    clients,
    act,
    quiet: (promise: Promise<unknown>) => void promise.catch(() => {}),
    answer: (wishId: string, id: string, choice: Choice, note: string) =>
      act(
        wishId,
        () =>
          clients.questions.answer({
            question: { ref: { case: "id", value: id } },
            choice,
            note,
            wishId,
          }),
        questions,
      ),
    enlighten: (wishId: string, id: string, note: string) =>
      act(
        wishId,
        () =>
          clients.questions.enlighten({
            question: { ref: { case: "id", value: id } },
            note,
            wishId,
          }),
        questions,
        t("question.enlighten_toast"),
      ),
    // mark marks a question, by its id.
    mark: (wishId: string, id: string, kind: MarkKind, remove: boolean) =>
      act(
        wishId,
        () =>
          clients.marks.put({
            target: { ref: { case: "id", value: id } },
            kind,
            remove,
            wishId,
          }),
        questions,
      ),
  };
}
