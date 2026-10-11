// An answer reaches the one lead, against the real djinn up --browser (see global-setup.ts): answered while the lead
// does not run, it waits, and djinn types it in the lead's terminal, then Enter, once the lead is resumed on its
// session; answered in another wish while the lead runs, it goes to the same lead at once. The lead is a fake claude on
// djinn's PATH that says each line it reads.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import path from "node:path";
import { dropLead, leadFolder, leadRows as rows, setLead } from "./lead";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
// Not the session of wish-resume.spec.ts: one session runs in one terminal.
const session = "6f1c9d2e-3a4b-4c5d-8e7f-a0b1c2d3e4f5";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

async function show(terminal: string) {
  const url = new URL(process.env.DJINN_URL!);
  const res = await fetch(new URL("/ui.v1.UiService/Show", url), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${url.searchParams.get("token")}`,
    },
    body: JSON.stringify({ terminal }),
  });
  expect(res.ok).toBe(true);
}

// askAndAnswer asks a question with two options in a wish, and answers it from a plain shell, as the developer.
function askAndAnswer(wishId: string, first: string, second: string) {
  const asked = JSON.parse(
    djinn(
      "question",
      "ask",
      `Which lamp first, ${first} or ${second}?`,
      wishId,
      "--options",
      first,
      "--options",
      second,
      "--json",
    ),
  );
  const code = asked.question.code as string;
  djinn(
    "question",
    "answer",
    code,
    "b",
    "--wish-id",
    wishId,
    "--note",
    "lighter",
  );
  return code;
}

test.skip(process.platform === "win32", "the fake claude is a shell script");

test.afterEach(dropLead);

test("an answer waits for the lead, then is typed in its terminal once resumed; another wish's goes to the same lead", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const folder = leadFolder();
  const wishId = JSON.parse(djinn("wish", "make", "Tell the lead", "--json"))
    .wish.id as string;
  const other = JSON.parse(
    djinn("wish", "make", "Tell the same lead", "--json"),
  ).wish.id as string;
  setLead(session, folder);
  const first = askAndAnswer(wishId, "Brass", "Glass");

  djinn("lead", "resume");

  await show("lead");
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = "/";
  await page.goto(url.toString());
  await expect(
    rows(page, `fake-claude --resume ${session} in ${folder}`),
  ).toHaveCount(1);
  // Typed, so echoed, then read after Enter: the fake lead says it again.
  await expect(rows(page, `Djinn: ${first} answered B — "Glass"`)).toHaveCount(
    2,
    { timeout: 15_000 },
  );

  // The lead runs: an answer in another wish reaches it at once, in the same terminal.
  const second = askAndAnswer(other, "Copper", "Tin");
  await expect(rows(page, `Djinn: ${second} answered B — "Tin"`)).toHaveCount(
    2,
    { timeout: 15_000 },
  );
  await expect(
    rows(page, `fake-claude --resume ${session} in ${folder}`),
  ).toHaveCount(1);

  djinn("wish", "delete", wishId);
  djinn("wish", "delete", other);
});
