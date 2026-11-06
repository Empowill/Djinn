// The window's moves by the keyboard (W491, src/window-keys.ts, src/view-history.ts): Alt+Left and Alt+Right go back and
// forward in the views, a wish's tab, another wish, a tilasm opened over the page, as in Chrome; Ctrl+Up and Ctrl+Down
// go to the wish above or below in the side panel, the granted ones skipped; none of them takes a key from a text
// field. The questions' own keys are in question-column.spec.ts and question-shortcuts.spec.ts.
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
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

test("Alt+arrows go back and forward in the views, Ctrl+Up and Ctrl+Down through the wishes, none in a field", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);
  const tag = randomUUID().slice(0, 8);
  const first = `Keys to the lamp ${tag}`;
  const second = `Keys to the oil ${tag}`;
  const granted = `Keys to the brass ${tag}`;
  const make = (title: string) =>
    JSON.parse(djinn("wish", "make", title, "--json")).wish.id as string;
  const firstId = make(first);
  const secondId = make(second);
  const grantedId = make(granted);
  djinn("wish", "grant", grantedId);
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-keys-"));
  try {
    fs.writeFileSync(
      path.join(dir, "index.html"),
      "<!doctype html><html><head><title>The lamp's keys</title></head><body><h1>The lamp's keys</h1></body></html>",
    );
    const tilasm = JSON.parse(
      djinn("tilasm", "put", dir, "--wish", firstId, "--json"),
    ).tilasm.id as string;
    const href = `djinn://tilasm/${tilasm}`;
    djinn(
      "question",
      "ask",
      "Which key for the lamp?",
      firstId,
      "--options",
      "Brass",
      "--options",
      "Iron",
      "--recommendation",
      "**A**: brass shines.",
      "--context",
      `The lamp's keys: ${href}`,
    );

    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(process.env.DJINN_URL!);
    await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible({
      timeout: 15_000,
    });
    const heading = page.locator(".hero h1");
    const tab = (name: string) => page.locator(`#view-tab-${name}`);
    const shown = (name: string) =>
      expect(tab(name)).toHaveAttribute("aria-selected", "true");
    const over = page.locator(".main-shell > .tilasm-view.over");
    await page.locator(".wish-nav").filter({ hasText: first }).click();
    await expect(heading).toHaveText(first);
    // Landed by the side panel: the first question has the focus, on its option recommended.
    const question = page
      .locator(".question-row")
      .filter({ hasText: "Which key for the lamp?" });
    const brass = question.getByRole("button", { name: /Brass/ });
    await expect(brass).toBeFocused();

    // Ctrl+Down: the wish below in the side panel, landed on as by a click; Ctrl+Up back; nothing above the first.
    await page.keyboard.press("Control+ArrowDown");
    await expect(heading).toHaveText(second);
    await shown("main");
    await page.keyboard.press("Control+ArrowUp");
    await expect(heading).toHaveText(first);
    await expect(brass).toBeFocused();
    await page.keyboard.press("Control+ArrowUp");
    await expect(heading).toHaveText(first);

    // A tab, then back and forward.
    await tab("tasks").click();
    await shown("tasks");
    await page.keyboard.press("Alt+ArrowLeft");
    await shown("main");
    await page.keyboard.press("Alt+ArrowRight");
    await shown("tasks");
    // Back over the wishes: the tab, then the wish before, then the first wish again.
    await page.keyboard.press("Alt+ArrowLeft");
    await shown("main");
    await page.keyboard.press("Alt+ArrowLeft");
    await expect(heading).toHaveText(second);
    await page.keyboard.press("Alt+ArrowLeft");
    await expect(heading).toHaveText(first);
    await page.keyboard.press("Alt+ArrowRight");
    await expect(heading).toHaveText(second);
    await page.keyboard.press("Alt+ArrowRight");
    await expect(heading).toHaveText(first);
    await shown("main");
    await page.keyboard.press("Alt+ArrowRight");
    await shown("tasks");

    // A tilasm a question links to, opened over the page: back closes it, forward opens it again.
    await tab("main").click();
    const link = question
      .locator(".question-context")
      .getByRole("link", { name: href });
    await link.evaluate((el) => el.scrollIntoView({ block: "center" }));
    await link.click();
    await expect(over.locator("iframe.tilasm-frame")).toBeVisible();
    // The keys of the page, not of the tilasm's frame (its document keeps its own).
    await page.evaluate(() => (document.activeElement as HTMLElement).blur());
    await page.keyboard.press("Alt+ArrowLeft");
    await expect(over).toHaveCount(0);
    await shown("main");
    await page.keyboard.press("Alt+ArrowRight");
    await expect(over.locator("iframe.tilasm-frame")).toBeVisible();
    await page.keyboard.press("Alt+ArrowLeft");
    await expect(over).toHaveCount(0);

    // In a text field, the keys are the field's: the note keeps them, the view and the wish stay.
    const note = question.getByLabel("Note with the answer");
    await note.click();
    await page.keyboard.type("Brass");
    for (const key of [
      "Alt+ArrowLeft",
      "Alt+ArrowRight",
      "Control+ArrowDown",
      "Control+ArrowUp",
      "ArrowLeft",
      "ArrowUp",
    ])
      await page.keyboard.press(key);
    await expect(note).toBeFocused();
    await expect(note).toHaveValue("Brass");
    await expect(heading).toHaveText(first);
    await shown("main");
    await expect(over).toHaveCount(0);
    await expect(brass).toHaveAttribute("aria-pressed", "true");

    // A granted wish is no stop: from it (opened by its link), Ctrl+Up goes to a wish of the lists above.
    await note.fill("");
    await page.evaluate(() => (document.activeElement as HTMLElement).blur());
    await page.locator("button.sidebar-section-label.sub").click();
    await page.locator(".wish-nav").filter({ hasText: granted }).click();
    await expect(heading).toHaveText(granted);
    await page.keyboard.press("Control+ArrowUp");
    await expect(heading).not.toHaveText(granted);
    await expect(heading).not.toHaveText(first);
    await page.keyboard.press("Control+ArrowDown");
    await expect(heading).not.toHaveText(granted);
    expect(errors).toEqual([]);
  } finally {
    for (const id of [firstId, secondId, grantedId])
      djinn("wish", "delete", id);
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
