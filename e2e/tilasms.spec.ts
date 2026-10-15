// The Documents panel, against the real djinn up --browser (see global-setup.ts). The developer makes a document from
// the wish's header, writes it, and finds it again after a reload; imports a .md, and one the editor cannot keep whole
// opens read only until asked; an HTML tilasm the lead puts with the command line opens in its frame, isolated, its
// search folded behind an icon and its versions restorable; a djinn link opens the document's tab. Every run makes a
// fresh wish, so --repeat-each works on the same djinn. Screenshots of the panel go to test-results/e2e/.
import { expect, type Page, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { mergeVisual } from "./merge-visuals";
import { openWorkspaceSection, workspacePanel } from "./workspace-selectors";

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

// A fresh wish, opened in the window.
async function openWish(page: Page, title: string): Promise<string> {
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  return wishId;
}

// The Markdown of a wish's document, as the agents read it.
const markdownOf = (code: string, wishId: string): string =>
  JSON.parse(djinn("tilasm", "get", code, "--wish", wishId, "--json")).text;

// The tilasm: a page, a script of its own that says what it could do, and an image.
const index = `<!doctype html><html><head><title>The lamp's anatomy</title></head><body>
<h1>The lamp's anatomy</h1><p>A wick carries the oil to the flame.</p><img id="img" src="img/wick.svg" alt="wick">
<p id="run">scripts do not run</p><p id="net">no answer</p><p id="djinn">no answer</p>
<script src="js/app.js"></script></body></html>`;
const app = `document.getElementById("run").textContent = "scripts run";
document.addEventListener("securitypolicyviolation", (e) => {
  if (e.violatedDirective.startsWith("connect-src"))
    document.getElementById("net").textContent = "network refused by " + e.violatedDirective;
});
fetch(new URL("/plan.v1.WishService/List", location.href)).then(
  () => (document.getElementById("net").textContent = "network reached"),
  () => {},
);
try {
  document.getElementById("djinn").textContent = "Djinn reached: " + parent.document.title;
} catch {
  document.getElementById("djinn").textContent = "Djinn out of reach";
}`;

test("a document made from the wish's header is written in place, saved, and found again after a reload", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const title = `Write the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = await openWish(page, title);
  try {
    // No document yet: no panel; the header makes one, and the panel opens on it, its title ready to be typed over.
    await expect(page.locator('[data-wish-section="tilasms"]')).toHaveCount(0);
    await page.getByRole("button", { name: "New document" }).click();
    const panel = workspacePanel(page, "tilasms");
    await expect(panel).toBeVisible();
    const tab = panel.getByRole("tab");
    await expect(tab).toHaveText("Untitled");
    await expect(tab).toHaveAttribute("aria-selected", "true");
    const titleField = panel.getByRole("textbox", { name: "Title" });
    await expect(titleField).toBeFocused();
    await page.keyboard.type("Lamp notes");
    await page.keyboard.press("Enter");
    const body = panel.locator(".tilasm-prose");
    await expect(body).toBeFocused();
    await page.keyboard.type("The wick carries the oil.");
    // "/" at a block's start opens the blocks; typing filters them, Enter makes the block and takes the command out.
    await page.keyboard.press("Enter");
    await page.keyboard.type("/head");
    const slash = page.getByRole("listbox", { name: "Blocks" });
    await expect(slash).toBeVisible();
    await expect(slash.getByRole("option").first()).toHaveText("Heading 1");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Enter");
    await expect(slash).toHaveCount(0);
    await page.keyboard.type("The flame");
    await expect(body.locator("h2")).toHaveText("The flame");
    // A "/" inside a word or a link opens nothing.
    await page.keyboard.press("Enter");
    await page.keyboard.type("See https://example.com/lamp and/or this");
    await expect(slash).toHaveCount(0);
    // The bar by a selection: bold keeps the selection.
    await page.keyboard.press("Enter");
    await page.keyboard.type("Bright");
    for (let i = 0; i < "Bright".length; i++)
      await page.keyboard.press("Shift+ArrowLeft");
    const bar = page.getByRole("toolbar", { name: "Formatting" });
    await expect(bar).toBeVisible();
    await bar.getByRole("button", { name: "Bold" }).click();
    await expect(bar.getByRole("button", { name: "Bold" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await page.keyboard.press("ArrowRight");
    await expect(bar).toBeHidden();

    // What the agents read, once saved: the Markdown, the title as its first heading.
    await expect.poll(() => markdownOf("L01", wishId)).toContain("**Bright**");
    expect(markdownOf("L01", wishId)).toMatch(
      /^# Lamp notes\n\nThe wick carries the oil\.\n\n## The flame\n\n/,
    );
    await expect(panel.locator(".tilasm-save")).toHaveText("Saved");
    await expect(tab).toHaveText("Lamp notes");
    await page.screenshot({ path: path.join(shots, "documents-dark.png") });
    await page.screenshot({ path: mergeVisual("dark/documents.png") });

    // A reload finds it, the panel on its tab.
    await page.reload();
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await openWorkspaceSection(page, "tilasms");
    await expect(
      panel.getByRole("tab", { name: "Lamp notes" }),
    ).toHaveAttribute("aria-selected", "true");
    await expect(panel.getByRole("textbox", { name: "Title" })).toHaveValue(
      "Lamp notes",
    );
    await expect(panel.locator(".tilasm-prose")).toContainText(
      "The wick carries the oil.",
    );
    await expect(panel.locator(".tilasm-prose strong")).toHaveText("Bright");

    // The lead writes a new version meanwhile: the untouched document reads it in place.
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-tilasm-"));
    const leadWrites = (text: string) => {
      const file = path.join(dir, "notes.md");
      fs.writeFileSync(file, `# Lamp notes\n\n${text}\n`);
      djinn("tilasm", "put", file, "--wish", wishId, "--code", "L01");
    };
    try {
      const prose = panel.locator(".tilasm-prose");
      leadWrites("Rewritten by the lead.");
      await expect(prose).toHaveText("Rewritten by the lead.");

      // The developer writes while the lead does: their text stays, unsaved, until they pick; "Keep mine" saves it.
      const notice = panel.locator(".tilasm-notice");
      await prose.click();
      await page.keyboard.press("End");
      await page.keyboard.type(" Mine.");
      leadWrites("Theirs, first.");
      await expect(notice).toContainText("changed elsewhere");
      await expect(prose).toHaveText("Rewritten by the lead. Mine.");
      await notice.getByRole("button", { name: "Keep mine" }).click();
      await expect(notice).toHaveCount(0);
      await expect
        .poll(() => markdownOf("L01", wishId))
        .toBe("# Lamp notes\n\nRewritten by the lead. Mine.");

      // Again, and "Reload theirs" takes the other text.
      await prose.click();
      await page.keyboard.press("End");
      await page.keyboard.type(" Again.");
      leadWrites("Theirs, second.");
      await expect(notice).toContainText("changed elsewhere");
      await notice.getByRole("button", { name: "Reload theirs" }).click();
      await expect(prose).toHaveText("Theirs, second.");
      await expect(panel.locator(".tilasm-save")).toHaveText("Saved");
      expect(markdownOf("L01", wishId)).toBe(
        "# Lamp notes\n\nTheirs, second.\n",
      );
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});

test("a .md imported becomes a document; one the editor cannot keep whole opens read only until asked", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const wishId = await openWish(
    page,
    `Import the lamp ${randomUUID().slice(0, 8)}`,
  );
  try {
    await page.getByRole("button", { name: "New document" }).click();
    const panel = workspacePanel(page, "tilasms");
    await expect(panel.getByRole("tab", { name: "Untitled" })).toBeVisible();
    const input = panel.locator('input[type="file"]');
    await input.setInputFiles({
      name: "notes.md",
      mimeType: "text/markdown",
      buffer: Buffer.from(
        "# Imported notes\n\n- one\n- two\n\n| a | b |\n|---|---|\n| 1 | 2 |\n",
      ),
    });
    const imported = panel.getByRole("tab", { name: "Imported notes" });
    await expect(imported).toHaveAttribute("aria-selected", "true");
    const body = panel.locator(".tilasm-prose");
    await expect(body.locator("li")).toHaveCount(2);
    await expect(body.locator("td")).toHaveCount(2);
    await expect(body).toHaveAttribute("contenteditable", "true");
    await expect(panel.locator(".tilasm-notice")).toHaveCount(0);

    // The handle by the table, dragged above the list, moves the table there, a line showing where; undo takes it
    // back.
    const first = () =>
      body.evaluate((prose) => prose.firstElementChild?.tagName ?? "");
    expect(await first()).toBe("UL");
    const table = body.locator("table");
    await table.hover();
    const handle = panel.getByRole("button", {
      name: "Drag to move the block",
    });
    await expect(handle).toBeVisible();
    const from = (await handle.boundingBox())!;
    const list = (await body.locator("ul").boundingBox())!;
    await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
    await page.mouse.down();
    await page.mouse.move(list.x + 20, list.y + 4, { steps: 5 });
    await expect(panel.locator(".tilasm-drop-line")).toBeVisible();
    await page.mouse.up();
    await expect(panel.locator(".tilasm-drop-line")).toHaveCount(0);
    await expect.poll(first).not.toBe("UL");
    await body.locator("li").first().click();
    await page.keyboard.press("ControlOrMeta+z");
    await expect.poll(first).toBe("UL");

    // Raw HTML and an image: named, read only, nothing saved until "Edit anyway".
    await input.setInputFiles({
      name: "page.md",
      mimeType: "text/markdown",
      buffer: Buffer.from(
        '# Rich notes\n\nText <span class="x">inline</span>.\n\n![wick](wick.png)\n',
      ),
    });
    await expect(
      panel.getByRole("tab", { name: "Rich notes" }),
    ).toHaveAttribute("aria-selected", "true");
    const notice = panel.locator(".tilasm-notice");
    await expect(notice).toContainText("raw HTML");
    await expect(notice).toContainText("images");
    await expect(body).toHaveAttribute("contenteditable", "false");
    await expect(panel.locator(".tilasm-save")).toHaveText("Read only");
    await notice.getByRole("button", { name: "Edit anyway" }).click();
    await expect(body).toHaveAttribute("contenteditable", "true");
    await expect(notice).toHaveCount(0);
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});

test("an HTML tilasm put by the command line opens in its frame, isolated; its search is folded and its versions restore", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-tilasm-"));
  const title = `Explain the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
    .id as string;
  try {
    fs.mkdirSync(path.join(dir, "js"));
    fs.mkdirSync(path.join(dir, "img"));
    fs.writeFileSync(path.join(dir, "index.html"), index);
    fs.writeFileSync(path.join(dir, "js/app.js"), app);
    fs.writeFileSync(
      path.join(dir, "img/wick.svg"),
      '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><rect width="20" height="20"/></svg>',
    );
    const put = JSON.parse(
      djinn("tilasm", "put", dir, "--wish", wishId, "--json"),
    ).tilasm;
    expect(put.code).toBe("L01");

    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const section = page.locator('[data-wish-section="tilasms"]');
    await expect(section.locator("strong")).toHaveText("Documents");
    await expect(section.locator(".count")).toHaveText("1");
    await openWorkspaceSection(page, "tilasms");
    const panel = page.getByRole("tabpanel", { name: "Documents" });
    await expect(
      panel.getByRole("tab", { name: "The lamp's anatomy" }),
    ).toHaveAttribute("aria-selected", "true");

    // The frame runs the tilasm's own script and shows its image; the network and Djinn are out of reach.
    const frame = page.frameLocator("iframe.tilasm-frame");
    await expect(frame.locator("#run")).toHaveText("scripts run");
    await expect(frame.locator("#net")).toHaveText(
      "network refused by connect-src",
    );
    await expect(frame.locator("#djinn")).toHaveText("Djinn out of reach");
    await expect
      .poll(() =>
        frame.locator("#img").evaluate((img: HTMLImageElement) => img.width),
      )
      .toBe(20);
    await page.screenshot({ path: path.join(shots, "tilasms-dark.png") });
    await page.screenshot({ path: mergeVisual("dark/tilasms.png") });

    // The search, folded behind its icon: on the titles and the text within the wish; the kind's name finds them all.
    await expect(panel.getByRole("searchbox")).toHaveCount(0);
    await panel
      .getByRole("button", { name: "Search the documents of this wish" })
      .click();
    const search = panel.getByRole("searchbox");
    for (const words of ["wick flame", "ANATOMY", "talisman"]) {
      await search.fill(words);
      await expect(panel.getByRole("tab")).toHaveCount(1);
    }
    await search.fill("paraffin");
    await expect(panel.getByRole("tab")).toHaveCount(0);
    await expect(panel).toContainText("No document holds “paraffin”");
    await search.press("Escape");
    await expect(panel.getByRole("searchbox")).toHaveCount(0);
    await expect(panel.getByRole("tab")).toHaveCount(1);

    // A new version put by the command line replaces what the frame shows; the versions restore the first one.
    fs.writeFileSync(
      path.join(dir, "index.html"),
      index.replace("A wick carries the oil to the flame.", "Second version."),
    );
    djinn("tilasm", "put", dir, "--wish", wishId, "--code", "L01");
    await expect(frame.locator("body")).toContainText("Second version.");
    await panel.getByRole("button", { name: "Versions" }).click();
    const versions = page.getByRole("dialog", { name: "Versions" });
    await expect(versions.locator("li")).toHaveCount(2);
    await versions
      .locator("li")
      .nth(1)
      .getByRole("button", { name: "Restore this version" })
      .click();
    await expect(frame.locator("body")).toContainText(
      "A wick carries the oil to the flame.",
    );
    await panel.getByRole("button", { name: "Versions" }).click();
    await expect(versions.locator("li")).toHaveCount(3);
    await expect(versions.locator("li").first()).toContainText("developer");
    await expect(versions.locator("li").first()).toContainText("restores v1");
    expect(errors).toEqual([]);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
    djinn("wish", "pause", wishId);
  }
});

test("a djinn link to a document opens its wish's Documents panel on its tab", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const wishId = await openWish(
    page,
    `Link the lamp ${randomUUID().slice(0, 8)}`,
  );
  try {
    for (const name of ["First", "Second"])
      djinn(
        "tilasm",
        "create",
        name,
        "--wish",
        wishId,
        "--markdown",
        `Text of ${name}.`,
      );
    await expect(page.locator('[data-wish-section="tilasms"]')).toBeVisible();
    djinn("tilasm", "open", "L02", "--wish", wishId);
    const panel = workspacePanel(page, "tilasms");
    await expect(panel).toBeVisible();
    await expect(panel.getByRole("tab", { name: "Second" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(panel.locator(".tilasm-prose")).toHaveText("Text of Second.");
    djinn("tilasm", "open", "L01", "--wish", wishId);
    await expect(panel.getByRole("tab", { name: "First" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(panel.locator(".tilasm-prose")).toHaveText("Text of First.");
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});
