// The writes the screens share: an answer, a request to investigate, a task reviewed (src/review-tab.tsx), a move, a
// withdrawal or a question put off for later, each read again once djinn has it.
import { useCallback, useMemo } from "react";

import {
  Change,
  type Choice,
  Closer,
  MarkKind,
} from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useClients, useStore } from "./data/djinn";
import { t } from "./i18n";

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
        async () => {
          const res = await clients.questions.answer({
            question: { ref: { case: "id", value: id } },
            choice,
            note,
            wishId,
            by: Closer.DEVELOPER,
          });
          // The card turns answered now, whatever the reads that follow take.
          if (res.question) store.putQuestion(res.question);
        },
        QUESTIONS_CHANGES,
      ),
    [act, clients.questions, store],
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

  // review checks a task reviewed, by its id, or takes the mark off.
  const review = useCallback(
    (wishId: string, id: string, remove: boolean) =>
      act(
        wishId,
        () =>
          clients.marks.put({
            target: { ref: { case: "id", value: id } },
            kind: MarkKind.REVIEWED,
            remove,
            wishId,
          }),
        [Change.TASK],
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
            by: Closer.DEVELOPER,
          }),
        QUESTIONS_CHANGES,
        t("question.withdrawn_toast"),
      ),
    [act, clients.questions],
  );

  // later puts an open question off for later, to the bottom of its list, or gives it its place back with clear.
  const later = useCallback(
    (wishId: string, id: string, clear: boolean) =>
      act(
        wishId,
        async () => {
          const res = await clients.questions.later({
            question: { ref: { case: "id", value: id } },
            wishId,
            clear,
          });
          // It takes its new place now, whatever the reads that follow take.
          if (res.question) store.putQuestion(res.question);
        },
        QUESTIONS_CHANGES,
      ),
    [act, clients.questions, store],
  );

  return useMemo(
    () => ({
      clients,
      act,
      quiet,
      answer,
      enlighten,
      review,
      move,
      withdraw,
      later,
    }),
    [clients, act, quiet, answer, enlighten, review, move, withdraw, later],
  );
}
