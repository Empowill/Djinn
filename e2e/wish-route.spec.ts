// Every request finds its wish, against the real djinn up --browser (see global-setup.ts): the lead hands a request over
// with djinn wish route --ask, the developer rubs the lamp on the card, and Djinn makes the new wish, the request in
// its blocks, opens it in the window, and tells the one lead where the request went, in its terminal: no second lead
// starts. The lead is a fake claude on djinn's PATH that repeats what it reads.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import {
  dropLead,
  leadFolder,
  leadWatch,
  runningTerminals,
  setLead,
} from "./lead";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
// Not the session of the other specs: one session runs in one terminal.
const session = "8a9b0c1d-2e3f-4a5b-8c6d-7e8f9a0b1c2d";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

test.skip(process.platform === "win32", "the fake claude is a shell script");

test.afterEach(dropLead);

test("a request routed to a new wish makes it, the window opens it, and the one lead is told, at a rub of the lamp", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "brass-")),
  );
  djinn("project", "add", folder, "--name", "brass");
  const here = JSON.parse(djinn("wish", "make", "Route from here", "--json"))
    .wish.id as string;
  // The lead runs, on its session.
  const leadDir = leadFolder();
  setLead(session, leadDir);
  djinn("lead", "resume");

  const request = "wax the brass canoe paddles";
  const asked = JSON.parse(
    djinn("wish", "route", request, "--wish-id", here, "--ask", "--json"),
  );
  const title = "Wax the brass canoe paddles";
  // A new wish first; then the earlier specs' wishes, far as they are; keeping it here last.
  const options: string[] = asked.question.options;
  expect(options[0]).toBe(`New wish “${title}”, in brass`);
  expect(options.at(-1)).toBe("Keep it in “Route from here”");
  for (const o of options.slice(1, -1)) expect(o).toMatch(/^File it in “/);

  await page.goto(process.env.DJINN_URL!);
  await page
    .locator(".wish-nav")
    .filter({ hasText: "Route from here" })
    .click();
  const card = page
    .locator(".question-card")
    .filter({ hasText: "Where does this request go?" });
  await expect(card).toContainText(request);
  await card.getByRole("button", { name: "Rub the lamp" }).click();

  // The window goes to the new wish, active, the request in its blocks.
  await expect(page.locator(".hero h1")).toHaveText(title);
  const made = JSON.parse(djinn("wish", "list", "--json")).wishes.find(
    (w: { title: string }) => w.title === title,
  );
  expect(made.state).toBe("WISH_STATE_ACTIVE");
  const blocks = JSON.parse(
    djinn("block", "list", made.id, "--kind", "request", "--json"),
  ).blocks;
  expect(blocks.map((b: { content: string }) => b.content)).toEqual([request]);

  // The one lead is told where the request went, by its queue (decision Q191): its watch prints the answer.
  expect(leadWatch(session, "--timeout", "15s").digest).toContain(
    `${asked.question.code} answer: answered: the request went to the new wish "${title}"`,
  );
  // No second lead: the lead's terminal is the only one that runs claude, on its own session.
  const claudes = (await runningTerminals()).filter((t) =>
    t.command.join(" ").includes("claude"),
  );
  expect(claudes.map((t) => t.name)).toEqual(["lead"]);
  expect(claudes[0].command.join(" ")).toContain(`claude --resume ${session}`);

  djinn("wish", "pause", made.id);
  djinn("wish", "pause", here);
});
