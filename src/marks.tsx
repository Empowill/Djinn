// Read marks and approvals, the clicks that say "I saw it" and "go as it is" without a word: they go to the lamp
// (MarkService.Put), where the lead reads them in the brief and with djinn mark list. And the writes the screens share:
// an answer, a mark, a request to investigate, each read again once djinn has it.
import { Check, Eye, ThumbsUp } from "lucide-react";
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

// MarkButtons are an item's read mark and, for a block, its approval: each a toggle. A decision has none: it was taken
// (the decision log).
export function MarkButtons({
  item,
  approve,
  onMark,
}: {
  item: { marks?: Mark[] };
  approve: boolean;
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
  const approved = !!markOf(item, MarkKind.APPROVED);
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
      {approve && (
        <button
          className={`mark-toggle approve ${approved ? "on" : ""}`}
          aria-pressed={approved}
          disabled={sending}
          title={approved ? t("mark.approve_undo") : t("mark.approve_detail")}
          onClick={() => toggle(MarkKind.APPROVED)}
        >
          <ThumbsUp size={13} />
          {approved ? t("mark.approved") : t("mark.approve")}
        </button>
      )}
    </span>
  );
}

// useWrites gives the writes of the screens: djinn's refusal shows as a toast and rejects, and what changed in the
// wish is read again either way.
export function useWrites(onToast: (text: string) => void) {
  const clients = useClients();
  const store = useStore();
  const act = async (
    wishId: string,
    run: () => Promise<unknown>,
    changes: Change[],
    done?: string,
  ) => {
    try {
      await run();
      if (done) onToast(done);
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
    // mark marks a question or a block, by its id.
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
        [...questions, Change.BLOCK],
      ),
  };
}
