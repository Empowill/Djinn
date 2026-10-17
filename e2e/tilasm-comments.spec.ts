// The Comments column beside a tilasm's frame, against the real djinn up --browser (see global-setup.ts): the developer
// writes drafts on the whole tilasm, edits one in place, deletes one and puts it back with Undo, deletes it for good,
// and reloads: the drafts stay, in Djinn's store, counted on the tilasm's head, its row and the Tilasms tab. No agent
// reads a draft: djinn tilasm comments still has none sent. Full size, the column stays beside the narrowed frame, and
// Escape gives up a card without leaving full size. Every run makes a fresh wish, so --repeat-each works on the same
// djinn. Screenshots of the column, beside the frame and full size, go to test-results/e2e/.
import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const shots = path.join(__dirname, "../test-results/e2e");
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

test("drafts on the whole tilasm are written, edited, deleted with an Undo, and stay after a reload", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Comment the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-comments-"));
  try {
    fs.writeFileSync(
      path.join(dir, "index.html"),
      "<!doctype html><title>Notices of the lead</title><h1>Notices of the lead</h1><p>Notices group by wish.</p>",
    );
    djinn("tilasm", "put", dir, "--wish", wishId);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }

  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  const tab = page.getByRole("tab", { name: /^Tilasms/ });
  await tab.click();
  const panel = page.getByRole("tabpanel", { name: "Tilasms" });
  const row = panel.locator(".tilasm-row").filter({ hasText: "L01" });
  await row.getByRole("button", { name: "Notices of the lead" }).click();
  const view = panel.locator(".tilasm-view");
  const toggle = view.locator(".tilasm-view-head .tilasm-comments-toggle");
  await expect(toggle).toHaveAccessibleName("Comments · 0 draft comments");
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
  await toggle.click();
  const column = view.getByRole("complementary", { name: "Comments" });
  await expect(column).toContainText("No comment yet");
  const cards = column.locator(".tilasm-comment");

  // A first draft, saved by Ctrl+Enter.
  await column.getByRole("button", { name: "On the whole tilasm" }).click();
  const editor = column.getByRole("textbox", {
    name: "Your comment on the whole tilasm",
  });
  await expect(editor).toBeFocused();
  await editor.fill("A paused wish: do its notices wait?");
  await editor.press("Control+Enter");
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toContainText("C1");
  await expect(cards.first()).toContainText("The whole tilasm");
  await expect(cards.first()).toContainText("Draft");
  await expect(cards.first()).toContainText(
    "A paused wish: do its notices wait?",
  );
  await expect(toggle).toHaveAccessibleName("Comments · 1 draft comment");

  // A second one, by its button: it comes below the first.
  await column.getByRole("button", { name: "On the whole tilasm" }).click();
  await editor.fill("Say two seconds.");
  await column.getByRole("button", { name: "Save" }).click();
  await expect(cards).toHaveCount(2);
  await expect(cards.nth(1)).toContainText("C2");
  await expect(cards.nth(1)).toContainText("Say two seconds.");
  await expect(toggle).toHaveAccessibleName("Comments · 2 draft comments");
  await expect(row.locator(".draft-dot")).toHaveText("2");
  await expect(tab.locator(".draft-dot")).toHaveText("2");

  // Edited in place: Escape gives the edit up, Ctrl+Enter keeps it.
  await column.getByRole("button", { name: "Edit C1" }).click();
  const edit = column.getByRole("textbox", { name: "Edit C1" });
  await expect(edit).toBeFocused();
  await expect(edit).toHaveValue("A paused wish: do its notices wait?");
  await edit.fill("Thrown away.");
  await edit.press("Escape");
  await expect(edit).toHaveCount(0);
  await expect(cards.first()).toContainText(
    "A paused wish: do its notices wait?",
  );
  await column.getByRole("button", { name: "Edit C1" }).click();
  await edit.fill("A paused wish: its notices wait for it.");
  await edit.press("Control+Enter");
  await expect(edit).toHaveCount(0);
  await expect(cards.first()).toContainText(
    "A paused wish: its notices wait for it.",
  );
  await expect(cards.first()).toContainText("C1");

  // Deleted at once, with an Undo that puts it back in its place.
  await column.getByRole("button", { name: "Delete C2" }).click();
  await expect(cards).toHaveCount(1);
  const undo = column.getByRole("status");
  await expect(undo).toContainText("C2 deleted");
  await expect(toggle).toHaveAccessibleName("Comments · 1 draft comment");
  await undo.getByRole("button", { name: "Undo" }).click();
  await expect(cards).toHaveCount(2);
  await expect(cards.nth(1)).toContainText("C2");
  await expect(cards.nth(1)).toContainText("Say two seconds.");
  await expect(undo).toHaveCount(0);

  // Deleted again, the Undo goes after five seconds; the draft is gone for good.
  await column.getByRole("button", { name: "Delete C2" }).click();
  await expect(cards).toHaveCount(1);
  await expect(undo).toContainText("C2 deleted");
  await expect(undo).toHaveCount(0, { timeout: 8_000 });

  // No agent reads a draft.
  expect(djinn("tilasm", "comments", "L01", "--wish", wishId)).toContain(
    "No comment sent yet.",
  );

  // Reloaded: the draft is still there, counted on the head, the row and the tab.
  await page.reload();
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await tab.click();
  await expect(tab.locator(".draft-dot")).toHaveText("1");
  await expect(row.locator(".draft-dot")).toHaveText("1");
  await row.getByRole("button", { name: "Notices of the lead" }).click();
  await expect(toggle).toHaveAccessibleName("Comments · 1 draft comment");
  await toggle.click();
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toContainText("C1");
  await expect(cards.first()).toContainText(
    "A paused wish: its notices wait for it.",
  );
  await column.getByRole("button", { name: "On the whole tilasm" }).click();
  await editor.fill("A second thought, being written.");
  await page.screenshot({ path: path.join(shots, "tilasm-comments.png") });
  await editor.press("Escape");

  // Full size, the column stays beside the frame, which narrows for it; Escape gives up a card, not full size.
  await view.getByRole("button", { name: "Full size" }).click();
  const leave = page.getByRole("button", { name: "Leave full size" });
  await expect(leave).toBeFocused();
  const frame = view.locator("iframe.tilasm-frame");
  await expect
    .poll(async () => {
      const f = await frame.boundingBox();
      const c = await column.boundingBox();
      return f && c ? Math.round(c.x - (f.x + f.width)) : NaN;
    })
    .toBe(0);
  await page.screenshot({ path: path.join(shots, "tilasm-comments-full.png") });
  await column.getByRole("button", { name: "On the whole tilasm" }).click();
  await expect(editor).toBeFocused();
  await editor.press("Escape");
  await expect(editor).toHaveCount(0);
  await expect(leave).toBeVisible();
  await expect(view).toHaveClass(/\bfull\b/);
  await leave.click();
  await expect(view).not.toHaveClass(/\bfull\b/);
  await expect(column).toBeVisible();

  expect(errors).toEqual([]);
});

// The foot of the column (decision Q29): a last word on the whole tilasm, then Iterate or Launch, each confirmed in a
// line that Escape gives up and Enter sends. Iterate sends the drafts and the last word in one send, which holds the
// buttons until the Djinn reads it; the sent cards keep their place, and the reply djinn tilasm reply writes shows under
// its card, with the task it names. Launch without a comment answers the question that links the tilasm with its
// recommended option; the tilasm, launched, greys its comments and its buttons, with a line that says why.
test("the comments leave in one send, Iterate or Launch, and a launched tilasm greys", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Send the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-send-"));
  let launched = "";
  try {
    for (const name of ["Notices of the lead", "The flow"]) {
      fs.writeFileSync(
        path.join(dir, "index.html"),
        `<!doctype html><title>${name}</title><h1>${name}</h1><p>Notices group by wish.</p>`,
      );
      launched = JSON.parse(
        djinn("tilasm", "put", dir, "--wish", wishId, "--json"),
      ).tilasm.id as string;
    }
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  const question = JSON.parse(
    djinn(
      "question",
      "ask",
      "Which flow?",
      wishId,
      "--options",
      "Drafts, one send",
      "--options",
      "A review",
      "--recommendation",
      "**A**: one send, one event.",
      "--context",
      `The design: djinn://tilasm/${launched}`,
      "--json",
    ),
  ).question.code as string;

  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await page.getByRole("tab", { name: /^Tilasms/ }).click();
  const panel = page.getByRole("tabpanel", { name: "Tilasms" });
  const view = panel.locator(".tilasm-view");
  const column = view.getByRole("complementary", { name: "Comments" });
  const launch = column.getByRole("button", { name: "Launch", exact: true });
  const iterate = column.getByRole("button", { name: "Iterate", exact: true });
  const word = column.getByRole("textbox", {
    name: "A comment on the whole tilasm, sent with the others",
  });
  const confirm = column.getByRole("group", { name: "Confirm the send" });
  const go = confirm.getByRole("button", { name: "Send to the Djinn" });

  // L01: a draft and a last word leave in one send, to iterate.
  await panel
    .locator(".tilasm-row")
    .filter({ hasText: "L01" })
    .getByRole("button", { name: "Notices of the lead" })
    .click();
  await view.locator(".tilasm-view-head .tilasm-comments-toggle").click();
  await expect(launch).toBeEnabled();
  await expect(iterate).toBeDisabled();
  await column.getByRole("button", { name: "On the whole tilasm" }).click();
  const editor = column.getByRole("textbox", {
    name: "Your comment on the whole tilasm",
  });
  await editor.fill("A paused wish: do its notices wait?");
  await editor.press("Control+Enter");
  await expect(column.locator(".tilasm-comment")).toHaveCount(1);
  await expect(iterate).toBeEnabled();
  await expect(iterate).toHaveAttribute("title", /next version/);
  await expect(launch).toHaveAttribute("title", /recommended option/);
  await word.fill("And say what a granted wish does.");
  await iterate.click();
  await expect(go).toBeFocused();
  await expect(confirm).toContainText(
    "The 2 comments on L01 · v1 leave for the Djinn in one send",
  );
  await page.keyboard.press("Escape");
  await expect(confirm).toHaveCount(0);
  await expect(view).not.toHaveClass(/\bfull\b/);
  await iterate.click();
  await expect(go).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(
    page.getByText("Send 1 left for the Djinn: it makes the next version."),
  ).toBeVisible();
  // The sent comments keep their cards, which no longer change.
  const cards = column.locator(".tilasm-comment");
  await expect(cards).toHaveCount(2);
  await expect(cards.nth(0)).toContainText("C1");
  await expect(cards.nth(0)).toContainText("Sent · send 1");
  await expect(cards.nth(1)).toContainText("And say what a granted wish does.");
  await expect(column.getByRole("button", { name: "Edit C1" })).toHaveCount(0);
  await expect(word).toHaveValue("");
  // Until the Djinn reads it, the send holds the buttons, not the comments.
  await expect(column).toContainText("Send 1 waits for the Djinn to read it.");
  await expect(launch).toBeDisabled();
  await expect(iterate).toBeDisabled();
  await expect(word).toBeEnabled();
  await expect(
    column.getByRole("button", { name: "On the whole tilasm" }),
  ).toBeEnabled();
  const read = djinn("tilasm", "comments", "L01", "--wish", wishId);
  expect(read).toContain(
    "2 comments · iterate: put its next version with them",
  );
  expect(read).toContain("And say what a granted wish does.");

  // The Djinn's reply, written by the command, shows under its card with the task it made of the comment.
  const azima = JSON.parse(
    djinn(
      "task",
      "spawn",
      wishId,
      "--kind",
      "azima",
      "--title",
      "Paused wishes",
      "--json",
    ),
  ).task.code as string;
  const replied = JSON.parse(
    djinn(
      "tilasm",
      "reply",
      "L01",
      "C1",
      "--wish",
      wishId,
      "--text",
      "A paused wish keeps its notices: the next version says so.",
      "--task",
      azima,
      "--json",
    ),
  ) as { unanswered?: number };
  expect(replied.unanswered).toBe(1);
  const reply = cards.nth(0).getByRole("note", { name: "The reply to C1" });
  await expect(reply).toContainText("Reply from the Djinn");
  await expect(reply).toContainText(
    "A paused wish keeps its notices: the next version says so.",
  );
  await expect(reply).toContainText(`Tasks: ${azima}`);
  await expect(cards.nth(0)).toContainText("Replied");
  await expect(cards.nth(1)).toContainText("Sent · send 1");
  await expect(cards.nth(1).getByRole("note")).toHaveCount(0);
  expect(djinn("tilasm", "comments", "L01", "--wish", wishId)).toContain(
    "1 without a reply.",
  );
  await cards.nth(0).evaluate((el) => el.scrollIntoView({ block: "center" }));
  await page.screenshot({ path: path.join(shots, "tilasm-reply.png") });

  // L02: Launch without a comment is a yes to the question that links it.
  await view.getByRole("button", { name: "Close the tilasm" }).click();
  await panel
    .locator(".tilasm-row")
    .filter({ hasText: "L02" })
    .getByRole("button", { name: "The flow" })
    .click();
  await view.locator(".tilasm-view-head .tilasm-comments-toggle").click();
  await expect(iterate).toBeDisabled();
  await launch.click();
  await expect(confirm).toContainText(
    `No comment: the question that links L02 gets its recommended option`,
  );
  await go.click();
  await expect(
    page.getByText(
      `${question} answered with its recommended option: the work launches.`,
    ),
  ).toBeVisible();
  // Launched: the comments and the buttons grey, and a line says why.
  await expect(column).toContainText("Launched already, by send 1.");
  await expect(column).toHaveClass(/\bheld\b/);
  await expect(launch).toBeDisabled();
  await expect(iterate).toBeDisabled();
  await expect(word).toBeDisabled();
  await expect(
    column.getByRole("button", { name: "On the whole tilasm" }),
  ).toBeDisabled();
  const answered = (
    JSON.parse(djinn("question", "list", "--wish-id", wishId, "--json"))
      .questions as { code: string; answer?: { choice: string } }[]
  ).find((q) => q.code === question);
  expect(answered?.answer?.choice).toBe("CHOICE_A");
  await page.screenshot({ path: path.join(shots, "tilasm-send-held.png") });

  expect(errors).toEqual([]);
});
