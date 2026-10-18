// The Comments column beside a tilasm's frame, against the real djinn up --browser (see global-setup.ts): the developer
// writes drafts on the whole tilasm, edits one in place, deletes one and puts it back with Undo, deletes it for good,
// and reloads: the drafts stay, in Djinn's store, counted on the tilasm's head, its row and the Tilasms tab. No agent
// reads a draft: djinn tilasm comments still has none sent. Full size, the column stays beside the narrowed frame, and
// Escape gives up a card without leaving full size. Every run makes a fresh wish, so --repeat-each works on the same
// djinn. Screenshots of the column, beside the frame and full size, go to test-results/e2e/.
import { type Frame, type Locator, type Page, expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { drainLead, dropLead, leadFolder, setLead } from "./lead";

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
  made.push(wishId);
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
  if (process.env.TILASM_SHOTS) {
    await page.screenshot({ path: path.join(shots, "tilasm-comments.png") });
  }
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
  if (process.env.TILASM_SHOTS) {
    await page.screenshot({
      path: path.join(shots, "tilasm-comments-full.png"),
    });
  }
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
  made.push(wishId);
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
  if (process.env.TILASM_SHOTS) {
    await cards.nth(0).evaluate((el) => el.scrollIntoView({ block: "center" }));
    await page.screenshot({ path: path.join(shots, "tilasm-reply.png") });
  }

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
  if (process.env.TILASM_SHOTS) {
    await page.screenshot({ path: path.join(shots, "tilasm-send-held.png") });
  }

  expect(errors).toEqual([]);
});

// The wishes the passages' tests made, deleted once each ends: the specs share one djinn, and its active wishes are
// few (the later ones pause).
const made: string[] = [];
test.afterEach(() => {
  for (const id of made.splice(0)) djinn("wish", "delete", id);
});

// putPage puts a tilasm of one page in a new wish and opens it in the Tilasms tab; it returns the wish and the view.
async function putPage(page: Page, title: string, html: string) {
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  made.push(wishId);
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-passages-"));
  try {
    fs.writeFileSync(path.join(dir, "index.html"), html);
    djinn("tilasm", "put", dir, "--wish", wishId);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await page.getByRole("tab", { name: /^Tilasms/ }).click();
  const panel = page.getByRole("tabpanel", { name: "Tilasms" });
  await panel
    .locator(".tilasm-row")
    .filter({ hasText: "L01" })
    .locator(".tilasm-title")
    .click();
  return { wishId, view: panel.locator(".tilasm-view") };
}

// frameOf is the tilasm's frame, to read what the bridge did in it: the window itself never reads its document.
async function frameOf(page: Page): Promise<Frame> {
  await expect
    .poll(() => page.frames().some((f) => f.url().includes("/tilasm/")))
    .toBe(true);
  return page.frames().find((f) => f.url().includes("/tilasm/"))!;
}

// highlighted is what each of the bridge's highlights covers in the frame's page.
const highlighted = (frame: Frame) =>
  frame.evaluate(() =>
    Object.fromEntries(
      ["djinn-draft", "djinn-writing", "djinn-sent"].map((name) => [
        name,
        [...(CSS.highlights.get(name) ?? [])].map((r) => r.toString()),
      ]),
    ),
  );

// bring puts an element of the frame in sight, near the top of the frame, and returns where it stands once still. The
// terminal panel covers the bottom of the view: the frame and the element come near the top first.
async function bring(page: Page, target: Locator) {
  await page
    .locator("iframe.tilasm-frame")
    .evaluate((el) => el.scrollIntoView({ block: "start" }));
  // Away from the frame's edge, where a drag that selects scrolls the page.
  await target.evaluate((el) => {
    el.scrollIntoView({ block: "start" });
    window.scrollBy(0, -80);
  });
  // The view may scroll smoothly: the element stands still first.
  let box = (await target.boundingBox())!;
  await expect
    .poll(async () => {
      const was = box;
      box = (await target.boundingBox())!;
      return box.y === was.y && box.x === was.x;
    })
    .toBe(true);
  return box;
}

// drag selects an element's text with the mouse, from its first character to its last, as the developer does.
async function drag(page: Page, target: Locator) {
  const box = await bring(page, target);
  const y = box.y + box.height / 2;
  await page.mouse.move(box.x + 1, y);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2, y, { steps: 5 });
  await page.mouse.move(box.x + box.width - 1, y, { steps: 5 });
  await page.mouse.up();
}

const passagesPage = `<!doctype html><title>Notices of the lead</title>
<style>body { font: 18px/1.6 sans-serif; margin: 2rem; padding-bottom: 120vh; }</style>
<h1>Notices of the lead</h1>
<h2>Grouping</h2>
<p>Notices that come in less than <span id="two">two seconds</span> leave together, in one line of the digest.</p>
<h2>Display</h2>
<p>A notice waits <span id="again">two seconds</span>, then shows in the <b id="tab">tab of the wish</b>.</p>
<script>(function () { document.body.dataset.own = "ran"; })();</script>`;

test("a passage selected in the frame with the mouse is commented from the Comment button or Ctrl+Alt+M, and stays highlighted", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Comment a passage ${randomUUID().slice(0, 8)}`;
  const { wishId, view } = await putPage(page, title, passagesPage);
  const frame = await frameOf(page);
  const inFrame = page.frameLocator("iframe.tilasm-frame");
  // The tilasm's own script ran, and the bridge is at the end of the page as served, not in its files.
  await expect(inFrame.locator("body")).toHaveAttribute("data-own", "ran");
  expect(
    await frame.evaluate(() =>
      [...document.scripts].map((s) => s.getAttribute("src")),
    ),
  ).toEqual([null, "/tilasm/bridge.js"]);
  expect(djinn("tilasm", "get", "L01", "--wish", wishId)).not.toContain(
    "bridge",
  );

  // The mouse selects "two seconds" in Grouping: the Comment button shows under it, over the frame.
  const two = inFrame.locator("#two");
  await drag(page, two);
  const pick = page.getByRole("button", { name: "Comment", exact: true });
  await expect(pick).toBeVisible();
  const at = (await two.boundingBox())!;
  const button = (await pick.boundingBox())!;
  expect(button.y).toBeGreaterThan(at.y + at.height - 1);
  expect(button.y).toBeLessThan(at.y + at.height + 40);
  if (process.env.TILASM_SHOTS) {
    await page.screenshot({
      path: path.join(shots, "tilasm-passage-pick.png"),
    });
  }

  // Comment: the column opens on a new card on the passage, its quote and the heading above it; the passage stays
  // highlighted while it is written, the frame's own selection gone.
  await pick.click();
  await expect(pick).toHaveCount(0);
  const column = view.getByRole("complementary", { name: "Comments" });
  const editor = column.getByRole("textbox", {
    name: "Your comment on the passage",
  });
  await expect(editor).toBeFocused();
  const cards = column.locator(".tilasm-comment");
  await expect(cards.last()).toContainText("§ Grouping");
  await expect(cards.last().locator(".tilasm-comment-quote")).toHaveText(
    "two seconds",
  );
  await expect
    .poll(() => highlighted(frame))
    .toEqual({
      "djinn-draft": [],
      "djinn-writing": ["two seconds"],
      "djinn-sent": [],
    });
  expect(await frame.evaluate(() => getSelection()?.isCollapsed)).toBe(true);
  await editor.fill("Two seconds is the watch's: say so.");
  await editor.press("Control+Enter");
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toContainText("C1");
  await expect(cards.first()).toContainText("§ Grouping");
  await expect(cards.first().locator(".tilasm-comment-quote")).toHaveText(
    "two seconds",
  );
  // The draft's passage, gold in the page: the first "two seconds", not the one in Display.
  await expect
    .poll(() => highlighted(frame))
    .toEqual({
      "djinn-draft": ["two seconds"],
      "djinn-writing": [],
      "djinn-sent": [],
    });
  const first = await frame.evaluate(() => {
    const r = [...CSS.highlights.get("djinn-draft")!][0] as Range;
    return document.getElementById("two")!.contains(r.startContainer);
  });
  expect(first).toBe(true);
  // Highlights add nothing to the page.
  expect(
    await frame.evaluate(() => document.body.querySelectorAll("*").length),
  ).toBe(10);
  if (process.env.TILASM_SHOTS) {
    await page.screenshot({
      path: path.join(shots, "tilasm-passage-draft.png"),
    });
  }

  // Ctrl+Alt+M, pressed in the frame on another selection, does what the button does; Escape gives the card up.
  await drag(page, inFrame.locator("#tab"));
  await expect(pick).toBeVisible();
  await page.keyboard.press("Control+Alt+KeyM");
  await expect(editor).toBeFocused();
  await expect(cards.last()).toContainText("§ Display");
  await expect(cards.last().locator(".tilasm-comment-quote")).toHaveText(
    "tab of the wish",
  );
  await editor.press("Escape");
  await expect(editor).toHaveCount(0);
  await expect
    .poll(() => highlighted(frame))
    .toEqual({
      "djinn-draft": ["two seconds"],
      "djinn-writing": [],
      "djinn-sent": [],
    });

  // A click elsewhere in the frame takes the button away.
  await drag(page, inFrame.locator("#again"));
  await expect(pick).toBeVisible();
  await inFrame.locator("h1").click();
  await expect(pick).toHaveCount(0);

  // Reopened, the frame's page finds the draft's passage again.
  await view.getByRole("button", { name: "Close the tilasm" }).click();
  await page
    .getByRole("tabpanel", { name: "Tilasms" })
    .locator(".tilasm-title")
    .click();
  const again = await frameOf(page);
  await expect
    .poll(() => highlighted(again))
    .toEqual({
      "djinn-draft": ["two seconds"],
      "djinn-writing": [],
      "djinn-sent": [],
    });

  // Full size, the button is in reach over the frame.
  await view.getByRole("button", { name: "Full size" }).click();
  await drag(page, inFrame.locator("#again"));
  await expect(pick).toBeVisible();
  await pick.click();
  await expect(cards.last()).toContainText("§ Display");
  await editor.fill("Which tab?");
  await editor.press("Control+Enter");
  await expect(cards).toHaveCount(2);
  await expect
    .poll(async () => (await highlighted(again))["djinn-draft"].length)
    .toBe(2);
  // Still no agent reads a draft.
  expect(djinn("tilasm", "comments", "L01", "--wish", wishId)).toContain(
    "No comment sent yet.",
  );
  expect(errors).toEqual([]);
});

// A tilasm a question links to opens over the view (tilasm-over.tsx): it is commented there as in the Tilasms tab, a
// passage selected, an element aimed at, in the same column beside the narrowed frame, and the drafts are the tab's.
// Over a view, it took no comment until W276: L19, opened from its question, let nothing be selected to comment.
test("a tilasm opened from a question's link is commented over the view, as in the Tilasms tab", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Comment over ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  made.push(wishId);
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-over-"));
  let id: string;
  try {
    fs.writeFileSync(path.join(dir, "index.html"), passagesPage);
    id = JSON.parse(djinn("tilasm", "put", dir, "--wish", wishId, "--json"))
      .tilasm.id as string;
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  const href = `djinn://tilasm/${id}`;
  const question = "Which wait for the notices?";
  djinn(
    "question",
    "ask",
    question,
    wishId,
    "--options",
    "Two seconds",
    "--options",
    "Five seconds",
    "--context",
    `The notices: ${href}`,
  );

  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  const link = page
    .locator(".question-card")
    .filter({ hasText: question })
    .locator(".question-context")
    .getByRole("link", { name: href });
  await link.evaluate((el) => el.scrollIntoView({ block: "center" }));
  await link.click();
  const over = page.locator(".main-shell > .tilasm-view.over");
  const inFrame = page.frameLocator(".tilasm-view.over iframe.tilasm-frame");
  await expect(inFrame.locator("body")).toHaveAttribute("data-own", "ran");
  const frame = await frameOf(page);

  // The mouse selects a passage: the Comment button shows over the frame, and opens the column on its card, beside
  // the frame, which narrows for it.
  await drag(page, inFrame.locator("#two"));
  const pick = page.getByRole("button", { name: "Comment", exact: true });
  await expect(pick).toBeVisible();
  await pick.click();
  const column = over.getByRole("complementary", { name: "Comments" });
  const editor = column.getByRole("textbox", {
    name: "Your comment on the passage",
  });
  await expect(editor).toBeFocused();
  const cards = column.locator(".tilasm-comment");
  await expect(cards.last()).toContainText("§ Grouping");
  await expect(cards.last().locator(".tilasm-comment-quote")).toHaveText(
    "two seconds",
  );
  const box = over.locator("iframe.tilasm-frame");
  await expect
    .poll(async () => {
      const f = await box.boundingBox();
      const c = await column.boundingBox();
      return f && c ? Math.round(c.x - (f.x + f.width)) : NaN;
    })
    .toBeGreaterThanOrEqual(0);
  await editor.fill("Two seconds is the watch's: say so.");
  await editor.press("Control+Enter");
  await expect(cards).toHaveCount(1);
  await expect(over.locator(".tilasm-comments-toggle")).toHaveAccessibleName(
    "Comments · 1 draft comment",
  );
  await expect
    .poll(() => highlighted(frame))
    .toEqual({
      "djinn-draft": ["two seconds"],
      "djinn-writing": [],
      "djinn-sent": [],
    });
  if (process.env.TILASM_SHOTS) {
    await page.screenshot({
      path: path.join(shots, "tilasm-over-comments.png"),
    });
  }

  // Aim mode too: a click on a heading takes it; Escape gives the card up and leaves the tilasm open.
  await over.getByRole("button", { name: "Aim at an element" }).click();
  const heading = await bring(page, inFrame.locator("h2").last());
  await page.mouse.click(heading.x + 5, heading.y + heading.height / 2);
  const onElement = column.getByRole("textbox", {
    name: "Your comment on the element",
  });
  await expect(onElement).toBeFocused();
  await onElement.press("Escape");
  await expect(onElement).toHaveCount(0);
  await expect(over).toBeVisible();

  // Closed, the draft is the Tilasms tab's.
  await over.getByRole("button", { name: "Close the tilasm" }).click();
  await expect(over).toHaveCount(0);
  const tab = page.getByRole("tab", { name: /^Tilasms/ });
  await expect(tab.locator(".draft-dot")).toHaveText("1");
  expect(errors).toEqual([]);
});

// A tilasm may declare its own policy in a meta tag: the browser applies both, and the bridge, a script from the
// server's origin, loads only where both let it. Where it does not, the page still reads, and the whole tilasm is
// still commented.
test("a tilasm's own policy in a meta tag decides whether the bridge loads; the page reads either way", async ({
  page,
}) => {
  const page_ = (
    policy: string,
  ) => `<!doctype html><meta http-equiv="Content-Security-Policy" content="${policy}">
<title>Own policy</title><h1>Own policy</h1><p>Select <span id="me">this passage</span>.</p>
<script>document.body.dataset.own = "ran";</script>`;
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const pick = page.getByRole("button", { name: "Comment", exact: true });

  // Inline scripts only: the bridge is refused, the tilasm's own script runs, no Comment button shows.
  const { view } = await putPage(
    page,
    `Own policy inline ${randomUUID().slice(0, 8)}`,
    page_(
      "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'",
    ),
  );
  const inFrame = page.frameLocator("iframe.tilasm-frame");
  await expect(inFrame.locator("body")).toHaveAttribute("data-own", "ran");
  await drag(page, inFrame.locator("#me"));
  await page.waitForTimeout(400);
  await expect(pick).toHaveCount(0);
  await view.locator(".tilasm-comments-toggle").click();
  await expect(
    view.getByRole("button", { name: "On the whole tilasm" }),
  ).toBeEnabled();

  // Its own policy names 'self': Chromium reads it as the address's origin, the server's, and loads the bridge.
  await putPage(
    page,
    `Own policy self ${randomUUID().slice(0, 8)}`,
    page_("default-src 'self'; script-src 'self' 'unsafe-inline'"),
  );
  await expect(inFrame.locator("body")).toHaveAttribute("data-own", "ran");
  await drag(page, inFrame.locator("#me"));
  await expect(pick).toBeVisible();
  expect(errors).toEqual([]);
});

const figurePage = `<!doctype html><title>Notices of the lead</title>
<style>body { font: 16px/1.6 sans-serif; margin: 2rem; padding-bottom: 150vh; } figure { margin: 1rem 0; }</style>
<h1>Notices of the lead</h1>
<p>Notices that come in less than <span id="two">two seconds</span> leave together.</p>
<figure id="path"><svg viewBox="0 0 420 90" width="420" height="90"><rect x="6" y="24" width="110" height="40" rx="8"
fill="none" stroke="black"/><text x="20" y="48">task done</text><text x="200" y="48">lead queue</text></svg>
<figcaption>Figure 1 - The path of a notice</figcaption></figure>
<table><tr><th>Notice</th><th>Wait</th></tr><tr id="failure"><td>Failure</td><td>none</td></tr></table>
<p><a id="link" href="#elsewhere">A link the page follows</a></p>`;

test("Aim mode comments an element, Escape leaves it in the window or the frame, and a dot opens its card", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const { view } = await putPage(
    page,
    `Aim at a figure ${randomUUID().slice(0, 8)}`,
    figurePage,
  );
  const frame = await frameOf(page);
  const inFrame = page.frameLocator("iframe.tilasm-frame");
  await expect(inFrame.locator("h1")).toBeVisible();
  const aim = view.locator(".tilasm-view-head").getByRole("button", {
    name: "Aim at an element",
  });
  const box = page.locator(".tilasm-aim-box");

  // Escape in the window leaves Aim mode; full size, it leaves Aim mode alone.
  await aim.click();
  await expect(aim).toHaveAttribute("aria-pressed", "true");
  await page.keyboard.press("Escape");
  await expect(aim).toHaveAttribute("aria-pressed", "false");
  await view.getByRole("button", { name: "Full size" }).click();
  await expect(view).toHaveClass(/\bfull\b/);
  const fullAim = view.locator(".tilasm-full-aim");
  await fullAim.click();
  await expect(fullAim).toHaveAttribute("aria-pressed", "true");
  await page.keyboard.press("Escape");
  await expect(fullAim).toHaveAttribute("aria-pressed", "false");
  await expect(view).toHaveClass(/\bfull\b/);
  await page.keyboard.press("Escape");
  await expect(view).not.toHaveClass(/\bfull\b/);

  // In Aim mode, the block under the mouse is framed over the frame: the figure, for its drawing.
  await aim.click();
  const figure = inFrame.locator("#path");
  const at = await bring(page, figure);
  await page.mouse.move(at.x + 60, at.y + 40);
  await expect(box).toBeVisible();
  const framed = (await box.boundingBox())!;
  expect(Math.abs(framed.y - at.y)).toBeLessThan(6);
  expect(Math.abs(framed.height - at.height)).toBeLessThan(8);
  // A table row is a block of its own.
  const row = (await inFrame.locator("#failure").boundingBox())!;
  await page.mouse.move(row.x + 10, row.y + row.height / 2);
  await expect
    .poll(async () => Math.round((await box.boundingBox())?.height ?? 0))
    .toBeLessThan(Math.round(row.height) + 8);
  // Escape pressed in the frame leaves Aim mode too.
  await page.locator("iframe.tilasm-frame").focus();
  await page.keyboard.press("Escape");
  await expect(aim).toHaveAttribute("aria-pressed", "false");
  await expect(box).toHaveCount(0);

  // A click in Aim mode takes the element and nothing of the page's own: the link is not followed.
  await aim.click();
  const link = await bring(page, inFrame.locator("#link"));
  await page.mouse.click(link.x + 5, link.y + link.height / 2);
  const column = view.getByRole("complementary", { name: "Comments" });
  const editor = column.getByRole("textbox", {
    name: "Your comment on the element",
  });
  await expect(editor).toBeFocused();
  expect(await frame.evaluate(() => location.hash)).toBe("");
  await editor.press("Escape");

  // The figure, clicked on its drawing: a card on it, labelled by its caption.
  await aim.click();
  const figureAt = await bring(page, figure);
  await page.mouse.click(figureAt.x + 60, figureAt.y + 40);
  await expect(aim).toHaveAttribute("aria-pressed", "false");
  await expect(editor).toBeFocused();
  const cards = column.locator(".tilasm-comment");
  await expect(cards.last()).toContainText(
    "figure: Figure 1 - The path of a notice",
  );
  await editor.fill("The arrow to the window is missing.");
  await editor.press("Control+Enter");
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toContainText("C1");
  await expect(cards.first()).toContainText(
    "figure: Figure 1 - The path of a notice",
  );
  // Its text highlighted, gold, adding nothing to the page.
  await expect
    .poll(async () => (await highlighted(frame))["djinn-draft"].join(" "))
    .toContain("Figure 1 - The path of a notice");

  // A passage too: two dots in the margin, at the heights of their anchors.
  await drag(page, inFrame.locator("#two"));
  await page.getByRole("button", { name: "Comment", exact: true }).click();
  await column
    .getByRole("textbox", { name: "Your comment on the passage" })
    .fill("Say where two seconds comes from.");
  await page.keyboard.press("Control+Enter");
  await expect(cards).toHaveCount(2);
  const margin = view.getByRole("group", { name: "Comments in the page" });
  const dot = (code: string) =>
    margin.getByRole("button", { name: `Open ${code}` });
  await expect(dot("C1")).toBeVisible();
  await expect(dot("C2")).toBeVisible();
  await bring(page, figure);
  await expect
    .poll(async () => {
      const d = (await dot("C1").boundingBox())!;
      const f = (await figure.boundingBox())!;
      return Math.abs(d.y - f.y) < 4;
    })
    .toBe(true);
  if (process.env.TILASM_SHOTS) {
    await page.screenshot({ path: path.join(shots, "tilasm-margin-dots.png") });
  }

  // The page scrolls: a dot whose anchor leaves the frame goes, and comes back with it.
  await frame.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await expect(dot("C1")).toHaveCount(0);
  await expect(dot("C2")).toHaveCount(0);

  // A click on a card's anchor shows it in the page: its dot is back.
  await cards
    .first()
    .getByRole("button", { name: "figure: Figure 1 - The path of a notice" })
    .click();
  await expect(dot("C1")).toBeVisible();
  await expect(cards.first()).toHaveClass(/\bactive\b/);

  // The column closed, a click on a dot opens it on its card.
  await column.getByRole("button", { name: "Close the comments" }).click();
  await expect(column).toHaveCount(0);
  await bring(page, inFrame.locator("#two"));
  await dot("C2").click();
  await expect(column).toBeVisible();
  await expect(cards.nth(1)).toHaveClass(/\bactive\b/);
  await expect(cards.first()).not.toHaveClass(/\bactive\b/);
  await expect(dot("C2")).toHaveClass(/\bactive\b/);
  expect(errors).toEqual([]);
});

// Two versions of a tilasm (the mockup's pairs, L08): v2 moves the digest line, rewords the failure line, drops the
// line on read notices, and draws an arrow into the figure.
const versionPage = (
  v: 1 | 2,
) => `<!doctype html><title>Notices of the lead</title>
<style>body { font: 14px/1.6 sans-serif; margin: 1rem; padding-bottom: 120vh; } span { white-space: nowrap; }</style>
<h1>Notices of the lead</h1>
${v === 2 ? "<h2>Paused wish</h2><p>The notices of a paused wish wait for it to resume.</p>" : ""}
<h2>Grouping</h2>
<p>They leave <span id="digest">in one line of the digest</span>.</p>
<p>A failure: <span id="burst">${v === 1 ? "it leaves alone and at once" : "it leaves alone and without delay"}</span>.</p>
<h2>Display</h2>
<p>${v === 1 ? 'A notice read goes <span id="ten">after ten seconds</span>.' : 'A notice read stays in the <span id="journal">wish journal</span>.'}</p>
<figure id="${v === 1 ? "path" : "path-v2"}"><svg viewBox="0 0 420 90" width="420" height="90"><text x="20" y="48">task done</text>
<text x="200" y="48">lead queue</text>${v === 2 ? '<text x="320" y="48">window</text>' : ""}</svg>
<figcaption>Figure 1 - The path of a notice${v === 2 ? ", up to the window" : ""}</figcaption></figure>`;

test("a new version put with --code finds each comment found, changed or lost, and a detached one is reattached", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Anchors across versions ${randomUUID().slice(0, 8)}`;
  const { wishId, view } = await putPage(page, title, versionPage(1));
  const inFrame = page.frameLocator("iframe.tilasm-frame");
  await expect(inFrame.locator("h1")).toBeVisible();
  const column = view.getByRole("complementary", { name: "Comments" });
  const cards = column.locator(".tilasm-comment");
  const pick = page.getByRole("button", { name: "Comment", exact: true });
  const comment = async (id: string, text: string) => {
    await drag(page, inFrame.locator(`#${id}`));
    await pick.click();
    const editor = column.getByRole("textbox", {
      name: "Your comment on the passage",
    });
    await editor.fill(text);
    await editor.press("Control+Enter");
    await expect(editor).toHaveCount(0);
  };
  await comment("digest", "Say the watch's two seconds.");
  await comment("burst", "Even mid-turn?");
  await comment("ten", "Ten seconds is short.");
  await view
    .locator(".tilasm-view-head")
    .getByRole("button", { name: "Aim at an element" })
    .click();
  const figure = await bring(page, inFrame.locator("figure"));
  await page.mouse.click(figure.x + 60, figure.y + 40);
  const element = column.getByRole("textbox", {
    name: "Your comment on the element",
  });
  await element.fill("The arrow to the window is missing.");
  await element.press("Control+Enter");
  await expect(cards).toHaveCount(4);
  // On the version they were written on, no card says where it stands.
  await expect(column.locator(".tilasm-comment-status")).toHaveCount(0);

  // The next version, put by a command as a task does: the frame loads it, and finds each anchor again.
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-versions-"));
  try {
    fs.writeFileSync(path.join(dir, "index.html"), versionPage(2));
    djinn("tilasm", "put", dir, "--wish", wishId, "--code", "L01");
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  await expect(view.locator(".tilasm-view-head")).toContainText("v2");
  const card = (code: string) =>
    cards.filter({ has: page.locator(".tilasm-code", { hasText: code }) });
  const detached = column.getByRole("region", { name: "Detached" });
  // C1, moved below the new section: found, nothing to say.
  await expect(card("C3")).toContainText("Gone from v2: no longer in the page");
  await expect(card("C1").locator(".tilasm-comment-status")).toHaveCount(0);
  // C2, reworded: the new passage above, the one it was on below it.
  await expect(card("C2")).toContainText("Changed in v2");
  await expect(card("C2").locator(".tilasm-comment-quote")).toHaveText([
    "it leaves alone and without delay",
    "it leaves alone and at once",
  ]);
  // C3, its line dropped: under Detached, its quote struck, last.
  await expect(detached.locator(".tilasm-comment")).toHaveCount(1);
  await expect(detached).toContainText("C3");
  await expect(detached.locator(".tilasm-comment-quote.lost")).toHaveText(
    "after ten seconds",
  );
  await expect(cards.last()).toContainText("C3");
  // C4, the figure with its arrow: changed, with what it is now.
  await expect(card("C4")).toContainText(
    "Changed in v2: figure: Figure 1 - The path of a notice, up to the window",
  );
  // The highlights follow: the moved passage, the reworded one, the figure; none for the lost one.
  const frame = await frameOf(page);
  await expect
    .poll(async () => (await highlighted(frame))["djinn-draft"].sort())
    .toEqual([
      "in one line of the digest",
      "it leaves alone and without delay",
      expect.stringContaining("up to the window"),
    ]);
  // No dot for the lost one.
  const margin = view.getByRole("group", { name: "Comments in the page" });
  await bring(page, inFrame.locator("#digest"));
  await expect(margin.getByRole("button", { name: "Open C1" })).toBeVisible();
  await expect(margin.getByRole("button", { name: "Open C3" })).toHaveCount(0);
  if (process.env.TILASM_SHOTS) {
    await page.screenshot({ path: path.join(shots, "tilasm-reanchored.png") });
  }

  // Reattach C3: on a passage selected in the new version, it leaves Detached.
  await card("C3").getByRole("button", { name: "Reattach C3" }).click();
  await expect(card("C3")).toContainText(
    "Select a passage or aim at an element in the page.",
  );
  await drag(page, inFrame.locator("#journal"));
  const reattach = page
    .getByRole("button", { name: "Reattach C3" })
    .and(page.locator(".tilasm-pick"));
  await reattach.click();
  await expect(detached).toHaveCount(0);
  await expect(card("C3").locator(".tilasm-comment-quote")).toHaveText(
    "wish journal",
  );
  await expect(card("C3").locator(".tilasm-comment-status")).toHaveCount(0);
  await expect(card("C3")).toContainText("Ten seconds is short.");
  await expect
    .poll(async () => (await highlighted(await frameOf(page)))["djinn-draft"])
    .toContain("wish journal");
  expect(errors).toEqual([]);
});

// What becomes of the comments sent (L08, step 8), with demonstration tasks: a wish imported as Djinn leaves it, W1's
// work merging, W2 done. The cards say Sent until the lead's watch reads the send, then Read by the Djinn; each reply
// names its tasks in their state; the next version, put by W2, says it is for the comments W2 was made of; and a toast
// counts what it found again of them. No worker runs, no model is called.
test("a sent comment is read by the Djinn, replied with its tasks' state, and the version a task put for it says so", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `What becomes of comments ${randomUUID().slice(0, 8)}`;
  const wishId = randomUUID();
  made.push(wishId);
  const at = "2026-10-17T08:00:00Z";
  const task = (code: string, name: string, extra: object) => ({
    id: randomUUID(),
    wish_id: wishId,
    code,
    title: name,
    status: "TASK_STATUS_DONE",
    create_time: at,
    end_time: at,
    ...extra,
  });
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-becomes-"));
  try {
    fs.writeFileSync(
      path.join(dir, "wish.json"),
      JSON.stringify({
        version: 1,
        create_time: at,
        wish: { id: wishId, title, create_time: at },
        tasks: [
          task("W1", "Say the watch's two seconds", {
            integration: {
              state: "INTEGRATION_STATE_PENDING",
              branch: "feat/notices",
              reason: "waits for its batch",
            },
          }),
          task("W2", "The next version of L01", {}),
        ],
      }),
    );
    djinn("wish", "import", path.join(dir, "wish.json"));
    fs.writeFileSync(path.join(dir, "index.html"), versionPage(1));
    djinn("tilasm", "put", dir, "--wish", wishId);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await page.getByRole("tab", { name: /^Tilasms/ }).click();
  const panel = page.getByRole("tabpanel", { name: "Tilasms" });
  await panel.locator(".tilasm-title").click();
  const view = panel.locator(".tilasm-view");
  const head = view.locator(".tilasm-view-head");
  const inFrame = page.frameLocator("iframe.tilasm-frame");
  await expect(inFrame.locator("h1")).toBeVisible();
  const column = view.getByRole("complementary", { name: "Comments" });
  const cards = column.locator(".tilasm-comment");
  const card = (code: string) =>
    cards.filter({ has: page.locator(".tilasm-code", { hasText: code }) });
  const pick = page.getByRole("button", { name: "Comment", exact: true });
  for (const [id, text] of [
    ["digest", "Say the watch's two seconds."],
    ["burst", "Even mid-turn?"],
    ["ten", "Ten seconds is short."],
  ]) {
    await drag(page, inFrame.locator(`#${id}`));
    await pick.click();
    const editor = column.getByRole("textbox", {
      name: "Your comment on the passage",
    });
    await editor.fill(text);
    await editor.press("Control+Enter");
    await expect(editor).toHaveCount(0);
  }
  await column
    .getByRole("textbox", {
      name: "A comment on the whole tilasm, sent with the others",
    })
    .fill("And a paused wish?");
  await column.getByRole("button", { name: "Iterate", exact: true }).click();
  await expect(
    column
      .getByRole("group", { name: "Confirm the send" })
      .getByRole("button", { name: "Send to the Djinn" }),
  ).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(cards).toHaveCount(4);
  await expect(card("C1")).toContainText("Sent · send 1");
  await expect(card("C4")).toContainText("Sent · send 1");

  // The lead's watch reads the send: every card of it says so, with when.
  const session = randomUUID();
  const folder = leadFolder();
  try {
    setLead(session, folder);
    drainLead(session);
  } finally {
    await dropLead();
  }
  for (const code of ["C1", "C2", "C3", "C4"])
    await expect(card(code)).toContainText(/Read by the Djinn · \S/);
  await expect(column).not.toContainText("waits for the Djinn");

  // The Djinn replies, naming the tasks it made: each shows in its state, and follows it.
  const reply = (code: string, text: string, ...tasks: string[]) =>
    djinn(
      "tilasm",
      "reply",
      "L01",
      code,
      "--wish",
      wishId,
      "--text",
      text,
      ...tasks.flatMap((t) => ["--task", t]),
    );
  reply("C1", "W2 says it in the next version.", "W2");
  reply("C2", "W1 makes a failure leave at once, mid-turn too.", "W1");
  reply("C4", "W2 adds a section on paused wishes.", "W2");
  const tasksOf = (code: string) =>
    card(code).locator(".tilasm-comment-reply-tasks");
  await expect(card("C1")).toContainText("Replied");
  await expect(tasksOf("C1")).toHaveText("Tasks: W2 Done");
  await expect(tasksOf("C2")).toHaveText("Tasks: W1 Merging");
  await expect(tasksOf("C2").locator(".status-badge")).toHaveClass(
    /tone-running/,
  );
  await expect(tasksOf("C4").locator(".status-badge")).toHaveClass(/tone-done/);
  await expect(card("C3")).toContainText("Read by the Djinn");
  await expect(head).toContainText("v1");
  await expect(head).not.toContainText("for C1");

  // W2 puts the next version: its head says for which comments, and a toast counts what it found again of them.
  const next = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-becomes-"));
  try {
    fs.writeFileSync(path.join(next, "index.html"), versionPage(2));
    djinn(
      "tilasm",
      "put",
      next,
      "--wish",
      wishId,
      "--code",
      "L01",
      "--author",
      "W2",
    );
  } finally {
    fs.rmSync(next, { recursive: true, force: true });
  }
  await expect(head).toContainText("v2 by W2, for C1, C4");
  await expect(head.getByText("v2 by W2, for C1, C4")).toHaveAttribute(
    "title",
    "W2 put this version: the Djinn's replies to C1, C4 name it",
  );
  await expect(
    page.getByRole("status").filter({
      hasText: "L01 v2, your comments: 1 found again, 1 changed, 1 detached.",
    }),
  ).toBeVisible();
  await expect(card("C2")).toContainText("Changed in v2");
  await expect(column.getByRole("region", { name: "Detached" })).toContainText(
    "C3",
  );
  if (process.env.TILASM_SHOTS) {
    await page.screenshot({ path: path.join(shots, "tilasm-becomes.png") });
  }

  // After a reload, nothing toasts again: each comment stands in v2 already.
  await page.reload();
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await page.getByRole("tab", { name: /^Tilasms/ }).click();
  await panel.locator(".tilasm-title").click();
  await view.locator(".tilasm-view-head .tilasm-comments-toggle").click();
  await expect(head).toContainText("v2 by W2, for C1, C4");
  await expect(card("C1")).toContainText("Replied");
  await expect(card("C2")).toContainText("Changed in v2");
  await expect(card("C3")).toContainText(/Read by the Djinn · \S/);
  await expect(column.getByRole("region", { name: "Detached" })).toContainText(
    "C3",
  );
  await expect(inFrame.locator("h1")).toBeVisible();
  await page.waitForTimeout(500);
  await expect(page.getByText(/your comments:/)).toHaveCount(0);
  expect(errors).toEqual([]);
});
