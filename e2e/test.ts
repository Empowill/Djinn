// The test of every spec: Playwright's, with a fixture that fails a test leaving the place dirty. A worker's specs
// share one djinn (global-setup.ts), in an order that changes with the workers: a wish left active or a question left
// open shows in the next spec's window, and makes it red far from the cause (W32, W246: lead-message:29). So a test
// that leaves an active wish it made or activated, or an open question it asked, fails itself, named with what it
// left; the fixture pauses the wishes and withdraws the questions, so that the specs after it run on a clean place.
// Spec files import test and expect from here, never from @playwright/test.
import { test as base } from "@playwright/test";

export { expect } from "@playwright/test";
export type { Frame, Locator, Page, Request } from "@playwright/test";

// place is what a test must leave as it found: the active wishes and the open questions of the active wishes (a paused
// wish is set aside with everything it holds), by id, each with a name.
type place = { wishes: Map<string, string>; questions: Map<string, string> };

// call makes one call to a service of the worker's djinn, over its HTTP transport with its token.
async function call<T>(service: string, method: string, body: object) {
  const url = new URL(process.env.DJINN_URL!);
  const res = await fetch(new URL(`/plan.v1.${service}/${method}`, url), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${url.searchParams.get("token")}`,
    },
    body: JSON.stringify(body),
  });
  if (!res.ok)
    throw new Error(`${service}.${method}: ${res.status} ${await res.text()}`);
  return (await res.json()) as T;
}

async function readPlace(): Promise<place> {
  const { wishes = [] } = await call<{
    wishes?: { id: string; title?: string; state?: string }[];
  }>("WishService", "List", {});
  const { questions = [] } = await call<{
    questions?: { id: string; code?: string; text?: string; wishId?: string }[];
  }>("QuestionService", "List", { open: true, pageSize: 200 });
  const active = new Map(
    wishes
      .filter((w) => w.state === "WISH_STATE_ACTIVE")
      .map((w) => [w.id, `"${w.title ?? ""}" (${w.id})`]),
  );
  return {
    wishes: active,
    questions: new Map(
      questions
        .filter((q) => !q.wishId || active.has(q.wishId))
        .map((q) => [
          q.id,
          `${q.code ?? "?"} "${q.text ?? ""}" of wish ${q.wishId ?? "?"}`,
        ]),
    ),
  };
}

export const test = base.extend<{ cleanPlace: void }>({
  cleanPlace: [
    // Playwright reads the fixtures a fixture needs from this pattern: none here.
    // eslint-disable-next-line no-empty-pattern
    async ({}, use, testInfo) => {
      // The djinn of the worker may not run (a spec starting its own): no fixture then.
      if (!process.env.DJINN_URL) {
        await use();
        return;
      }
      const before = await readPlace();
      await use();
      const after = await readPlace();
      const wishes = [...after.wishes].filter(([id]) => !before.wishes.has(id));
      const questions = [...after.questions].filter(
        ([id]) => !before.questions.has(id),
      );
      if (wishes.length === 0 && questions.length === 0) return;
      // Clean for the specs after, then say it.
      await Promise.allSettled([
        ...wishes.map(([id]) => call("WishService", "Pause", { wishId: id })),
        ...questions.map(([id]) =>
          call("QuestionService", "Withdraw", {
            question: { id },
            note: "left open by an e2e test (e2e/test.ts)",
          }),
        ),
      ]);
      const left = [
        ...wishes.map(([, name]) => `active wish ${name}`),
        ...questions.map(([, name]) => `open question ${name}`),
      ];
      const message = `the test leaves the place dirty: ${left.join("; ")}. Pause or delete the wishes it makes and answer or withdraw the questions it asks before it ends (e2e/test.ts): the next spec on this djinn would open on them.`;
      // A test already red says why first: its leftovers are noted, not a second error.
      if (testInfo.status !== "passed" && testInfo.status !== "skipped") {
        testInfo.annotations.push({
          type: "dirty place",
          description: message,
        });
        return;
      }
      throw new Error(message);
    },
    { auto: true },
  ],
});
