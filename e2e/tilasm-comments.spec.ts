// The Comments column beside a tilasm's frame, against the real djinn up --browser (see global-setup.ts): the developer
// writes drafts on the whole tilasm, edits one in place, deletes one and puts it back with Undo, deletes it for good,
// and reloads: the drafts stay, in Djinn's store, counted on the tilasm's head, its row and the Tilasms tab. No agent
// reads a draft: djinn tilasm comments still has none sent. Full size, the column stays beside the narrowed frame, and
// Escape gives up a card without leaving full size. Every run makes a fresh wish, so --repeat-each works on the same
// djinn. Screenshots of the column, beside the frame and full size, go to test-results/e2e/.
import {
  type Frame,
  type Locator,
  type Page,
  expect,
  test,
} from "@playwright/test";
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

// drag selects an element's text with the mouse, from its first character to its last, as the developer does. The
// terminal panel covers the bottom of the view: the frame and the element come near the top first.
async function drag(page: Page, target: Locator) {
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
  await page.screenshot({ path: path.join(shots, "tilasm-passage-pick.png") });

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
  await page.screenshot({ path: path.join(shots, "tilasm-passage-draft.png") });

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

  // Reloaded, the frame's page finds the draft's passage again.
  await page.reload();
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await page.getByRole("tab", { name: /^Tilasms/ }).click();
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
