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
