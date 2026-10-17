// A move the lead asks once a task opened a merge request: its title a call to action, its context with the links where
// the developer acts and the command to run.
// - the card and the attention bar show the move's main link as a button, which opens it in the browser;
// - a link in the context or an option opens where it points, and never picks the option around it;
// - the command block of the context copies as it is.
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import path from "node:path";
import {
  UiServiceOpenExternalRequestSchema,
  UiServiceOpenExternalResponseSchema,
} from "../gen/ts/ui/v1/ui_pb";
import { portable } from "./portable";

portable();

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

const MR = "https://gitlab.com/empowill/app/-/merge_requests/123";
const CI = "https://gitlab.com/empowill/app/-/pipelines/9";
const READY = "glab mr update 123 --ready";
const QUESTION = "Review MR !123 and take it out of draft";

test("a move's links open where the developer acts, its main link a button, and its command copies", async ({
  page,
  context,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  await context.grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: new URL(process.env.DJINN_URL!).origin,
  });
  // The window's browser is not opened by a test: the links it would open are kept here.
  const opened: string[] = [];
  await page.route("**/ui.v1.UiService/OpenExternal", (route) => {
    const req = fromBinary(
      UiServiceOpenExternalRequestSchema,
      route.request().postDataBuffer() ?? Buffer.alloc(0),
    );
    opened.push(req.url);
    return route.fulfill({
      contentType: "application/proto",
      body: Buffer.from(
        toBinary(
          UiServiceOpenExternalResponseSchema,
          create(UiServiceOpenExternalResponseSchema, { url: req.url }),
        ),
      ),
    });
  });
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();

  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const title = `Brass ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;

  try {
    djinn(
      "question",
      "ask",
      QUESTION,
      wishId,
      "--options",
      `Done: it is ready, ${MR}`,
      "--options",
      "Not yet: I have comments",
      "--context",
      `W12 opened ${MR} as a draft; its CI passed: ${CI}.\n\nOnce read, take it out of draft:\n\n` +
        "```sh\n" +
        READY +
        "\n```",
      "--recommendation",
      "A: read the diff first",
      "--before",
      "before the release",
      "--move",
    );

    const item = page.locator(".wish-nav").filter({ hasText: title });
    await expect(item).toBeVisible();
    await item.click();

    const card = page.locator(".question-card").filter({ hasText: QUESTION });
    await expect(card).toBeVisible();

    // The move's main link: a button on the card, and beside its line in the attention bar.
    const button = card.locator(".question-open-top .move-link");
    await expect(button).toHaveText("Open MR !123");
    await expect(button).toHaveAttribute("href", MR);
    const line = page
      .locator(".attention-bar li")
      .filter({ hasText: QUESTION });
    await expect(line.locator(".attention-item")).toContainText("Your move");
    await expect(line.locator(".move-link")).toHaveText("Open MR !123");

    // A click opens it in the browser, through Djinn: no page opens in the window.
    await button.click();
    await expect.poll(() => opened).toEqual([MR]);
    await line.locator(".move-link").click();
    await expect.poll(() => opened).toEqual([MR, MR]);
    expect(context.pages()).toHaveLength(1);

    // The context's CI link opens too.
    await card
      .locator(".question-context")
      .getByRole("link", { name: CI })
      .click();
    await expect.poll(() => opened.at(-1)).toBe(CI);

    // An option's link opens without picking the option.
    const optionA = card.getByRole("button", { name: /Done: it is ready/ });
    const optionB = card.getByRole("button", { name: /Not yet/ });
    await optionB.click();
    await expect(optionB).toHaveAttribute("aria-pressed", "true");
    await optionA.getByRole("link", { name: MR }).click();
    await expect.poll(() => opened.at(-1)).toBe(MR);
    await expect(optionB).toHaveAttribute("aria-pressed", "true");

    // The command block copies exactly.
    await card
      .locator(".question-context .ac-code")
      .getByRole("button", { name: "Copy the code" })
      .click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      READY,
    );

    // Nothing answered the question.
    const [asked] = JSON.parse(
      djinn("question", "list", "--wish-id", wishId, "--json"),
    ).questions;
    expect(asked.answer).toBeUndefined();
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "delete", wishId);
  }
});
