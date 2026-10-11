// The read mark, the click that says "I saw it" on an open question without a word: it goes to the lamp
// (MarkService.Put), where the lead reads it in the brief and with djinn mark list. A block takes no mark: it is for
// agents (src/agent-blocks.tsx). And the writes the screens share: an answer, a mark, a request to investigate, each
// read again once djinn has it.
import { Check, Eye } from "lucide-react";
import { useCallback, useMemo, useState } from "react";

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

export type OnMark<T = unknown> = (
  kind: MarkKind,
  remove: boolean,
  item?: T,
) => Promise<void>;

// MarkButtons are an open question's read mark, a toggle. A decision has none: it was taken (the decision log).
export function MarkButtons<T extends { marks?: Mark[] }>({
  item,
  onMark,
}: {
  item: T;
  onMark: (kind: MarkKind, remove: boolean, item?: T) => Promise<void>;
}) {
  const [sending, setSending] = useState(false);
  const toggle = (kind: MarkKind) => {
    setSending(true);
    void onMark(kind, !!markOf(item, kind), item)
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

const QUESTIONS_CHANGES = [Change.QUESTION, Change.WISH];

// useWrites gives the writes of the screens: djinn's refusal shows as a toast and rejects, and what changed in the
// wish is read again either way.
export function useWrites(onToast: (text: string) => void) {
  const clients = useClients();
  const store = useStore();
  // done is the toast once run succeeds, or makes it then, from what run found.
  const act = useCallback(
    async (
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
    },
    [store, onToast],
  );

  const quiet = useCallback(
    (promise: Promise<unknown>) => void promise.catch(() => {}),
    [],
  );

  const answer = useCallback(
    (wishId: string, id: string, choice: Choice, note: string) =>
      act(
        wishId,
        () =>
          clients.questions.answer({
            question: { ref: { case: "id", value: id } },
            choice,
            note,
            wishId,
          }),
        QUESTIONS_CHANGES,
      ),
    [act, clients.questions],
  );

  const enlighten = useCallback(
    (wishId: string, id: string, note: string) =>
      act(
        wishId,
        () =>
          clients.questions.enlighten({
            question: { ref: { case: "id", value: id } },
            note,
            wishId,
          }),
        QUESTIONS_CHANGES,
        t("question.enlighten_toast"),
      ),
    [act, clients.questions],
  );

  // mark marks a question, by its id.
  const mark = useCallback(
    (wishId: string, id: string, kind: MarkKind, remove: boolean) =>
      act(
        wishId,
        () =>
          clients.marks.put({
            target: { ref: { case: "id", value: id } },
            kind,
            remove,
            wishId,
          }),
        QUESTIONS_CHANGES,
      ),
    [act, clients.marks],
  );

  const move = useCallback(
    (wishId: string, id: string, targetWish: string, follow: boolean) =>
      act(
        wishId,
        async () => {
          const res = await clients.questions.move({
            question: { ref: { case: "id", value: id } },
            wish: targetWish,
            follow,
            wishId,
          });
          if (res.question?.wishId) {
            void store.changed(res.question.wishId, [
              Change.QUESTION,
              Change.TASK,
              Change.WISH,
            ]);
          }
          return res;
        },
        [Change.QUESTION, Change.BLOCK, Change.TASK, Change.WISH],
        t("question.moved_toast"),
      ),
    [act, clients.questions, store],
  );

  const withdraw = useCallback(
    (wishId: string, id: string, note: string) =>
      act(
        wishId,
        () =>
          clients.questions.withdraw({
            question: { ref: { case: "id", value: id } },
            note,
            wishId,
          }),
        QUESTIONS_CHANGES,
        t("question.withdrawn_toast"),
      ),
    [act, clients.questions],
  );

  return useMemo(
    () => ({
      clients,
      act,
      quiet,
      answer,
      enlighten,
      mark,
      move,
      withdraw,
    }),
    [clients, act, quiet, answer, enlighten, mark, move, withdraw],
  );
}
