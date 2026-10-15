// The Documents panel, against the real djinn up --browser (see global-setup.ts). The developer makes a document from
// the wish's header, writes it, and finds it again after a reload; imports a .md, and one the editor cannot keep whole
// opens read only until asked; an HTML tilasm the lead puts with the command line opens in its frame, isolated, its
// search folded behind an icon and its versions restorable; a djinn link opens the document's tab. Every run makes a
// fresh wish, so --repeat-each works on the same djinn. Screenshots of the panel go to test-results/e2e/.
import { expect, type Locator, type Page, test } from "@playwright/test";
import { type ChildProcess, execFileSync, spawn } from "node:child_process";
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
// moreAction picks an action from the panel's More menu, opened from its header.
async function moreAction(panel: Locator, name: string) {
  await panel
    .locator(".workspace-panel-header")
    .getByRole("button", { name: "More actions" })
    .click();
  await panel
    .getByRole("menu", { name: "More actions" })
    .getByRole("menuitem", { name })
    .click();
}

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

    // The panel's header holds two actions, a new document and More, just before its close button; the tab row,
    // the tabs alone. More is a small menu: the arrows move in it, Escape closes it and gives the focus back.
    const header = panel.locator(".workspace-panel-header");
    await expect(
      panel.locator(".tilasm-strip .workspace-panel-action"),
    ).toHaveCount(0);
    const actions = await header
      .locator(".workspace-panel-actions button")
      .evaluateAll((buttons) =>
        buttons.map((b) => b.getAttribute("aria-label") ?? ""),
      );
    expect(actions).toEqual([
      "New document",
      "More actions",
      expect.stringMatching(/^Close/),
    ]);
    const moreButton = header.getByRole("button", { name: "More actions" });
    await moreButton.click();
    const menu = panel.getByRole("menu", { name: "More actions" });
    await expect(menu.getByRole("menuitem")).toHaveText([
      "Search the documents of this wish",
      "Import a Markdown file",
      "Versions",
      "Download as .md",
      "Copy link",
    ]);
    await expect(menu.getByRole("menuitem").first()).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(
      menu.getByRole("menuitem", { name: "Import a Markdown file" }),
    ).toBeFocused();
    await page.keyboard.press("ArrowUp");
    await page.keyboard.press("ArrowUp");
    await expect(
      menu.getByRole("menuitem", { name: "Copy link" }),
    ).toBeFocused();
    await expect(
      menu.getByRole("menuitem", { name: "Copy link" }),
    ).not.toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
    await page.screenshot({ path: path.join(shots, "documents-menu.png") });
    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
    await expect(moreButton).toBeFocused();
    await expect(panel).toBeVisible();
    // Copy link puts the document's djinn link on the clipboard, and says so.
    await page
      .context()
      .grantPermissions(["clipboard-read", "clipboard-write"]);
    await moreAction(panel, "Copy link");
    await expect(page.locator(".toast")).toContainText("Link copied");
    const tilasmId = JSON.parse(
      djinn("tilasm", "get", "L01", "--wish", wishId, "--json"),
    ).tilasm.id as string;
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      `djinn://tilasm/${tilasmId}`,
    );
    // Download writes its .md, the saved text, named after its title.
    await moreAction(panel, "Download as .md");
    const toast = page.locator(".toast", { hasText: "Downloaded to " });
    await expect(toast).toBeVisible();
    const downloaded = (await toast.innerText())
      .replace(/^.*Downloaded to /s, "")
      .trim();
    try {
      expect(path.basename(downloaded)).toMatch(/^lamp-notes.*\.md$/);
      expect(fs.readFileSync(downloaded, "utf8")).toBe(
        markdownOf("L01", wishId),
      );
    } finally {
      fs.rmSync(downloaded, { force: true });
    }

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
    await expect(
      page
        .getByRole("tabpanel", { name: "Documents" })
        .getByRole("tab", { name: "The lamp's anatomy" }),
    ).toHaveAttribute("aria-selected", "true");
    // The panel, its header with the actions included; an HTML page downloads as its .zip.
    const panel = workspacePanel(page, "tilasms");
    await panel.getByRole("button", { name: "More actions" }).click();
    await expect(
      panel.getByRole("menuitem", { name: "Download as a .zip" }),
    ).toBeVisible();
    await page.keyboard.press("Escape");

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
    await moreAction(panel, "Search the documents of this wish");
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
    await moreAction(panel, "Versions");
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
    await moreAction(panel, "Versions");
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

// The discussions of a document, as the agents read them.
const discussionsOf = (code: string, wishId: string) =>
  JSON.parse(djinn("doc", "discussions", code, "--wish", wishId, "--json")) as {
    threads?: {
      id: string;
      kind?: string;
      agent_pending?: boolean;
      messages?: unknown[];
      anchor?: { quote?: string; prefix?: string; suffix?: string };
    }[];
    proposals?: { id: string; status?: string; thread_id?: string }[];
  };

test("a passage is discussed in a small window that folds into a dot; a proposal shows as a diff, accepted or rejected; a block's menu deletes, undone", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const wishId = await openWish(
    page,
    `Discuss the lamp ${randomUUID().slice(0, 8)}`,
  );
  try {
    djinn(
      "tilasm",
      "create",
      "Lamp",
      "--wish",
      wishId,
      "--markdown",
      "The wick carries the oil.\n\nThe flame needs air.\n\nThe glass keeps the wind out.",
    );
    await expect(page.locator('[data-wish-section="tilasms"]')).toBeVisible();
    djinn("tilasm", "open", "L01", "--wish", wishId);
    const panel = workspacePanel(page, "tilasms");
    const body = panel.locator(".tilasm-prose");
    await expect(body).toContainText("The flame needs air.");

    // A selection's bar comments it: a small window by the passage, the passage marked, never the Markdown.
    await body.locator("p", { hasText: "The flame needs air." }).click();
    await page.keyboard.press("End");
    await page.keyboard.press("ArrowLeft");
    for (let i = 0; i < "needs air".length; i++)
      await page.keyboard.press("Shift+ArrowLeft");
    await expect
      .poll(() => page.evaluate(() => String(getSelection())))
      .toBe("needs air");
    await page
      .getByRole("toolbar", { name: "Formatting" })
      .getByRole("button", { name: "Comment" })
      .click();
    const thread = page.getByRole("dialog", { name: "Comment" });
    await expect(thread).toBeVisible();
    // As in a chat, no send button: Enter sends.
    await expect(thread.getByRole("button", { name: "Send" })).toHaveCount(0);
    const comment = thread.getByRole("textbox", { name: "Your comment" });
    await comment.fill("Why air?");
    await comment.press("Enter");
    await expect(thread.getByRole("textbox", { name: "Reply" })).toBeVisible();
    await expect(body.locator(".tilasm-mark")).toHaveText("needs air");
    const opened = discussionsOf("L01", wishId).threads ?? [];
    expect(opened).toHaveLength(1);
    expect(opened[0]!.anchor?.quote).toBe("needs air");
    expect(markdownOf("L01", wishId)).not.toContain("Why air?");
    const threadId = opened[0]!.id;

    // An agent answers while it is open; the developer answers back.
    djinn(
      "doc",
      "reply",
      threadId,
      "--text",
      "Fire burns oxygen.",
      "--author",
      "W12",
    );
    await expect(thread).toContainText("Fire burns oxygen.");
    await expect(thread).toContainText("W12");
    // The developer's own messages stand on the right, the others' on the left, named when the author changes.
    await expect(
      thread
        .locator(".tilasm-bubble.is-theirs")
        .filter({ hasText: "Fire burns oxygen." }),
    ).toContainText("W12");
    const answer = thread.getByRole("textbox", { name: "Reply" });
    await answer.pressSequentially("Thanks.");
    await page.keyboard.press("Shift+Enter");
    await answer.pressSequentially("Really.");
    await expect(answer).toHaveValue("Thanks.\nReally.");
    await answer.press("Enter");
    await expect(answer).toHaveValue("");
    await expect(
      thread.locator(".tilasm-bubble.is-mine").filter({ hasText: "Thanks." }),
    ).toContainText("Really.");
    // No agent answers here: the thread says so quietly, and asks the document agent again without a message.
    const noReply = thread.locator(".tilasm-no-reply");
    await expect(noReply).toHaveText("No reply yet · Ask again");
    const woken = page.waitForResponse(/TilasmService\/Wake/);
    await noReply.getByRole("button", { name: "Ask again" }).click();
    expect((await woken).ok()).toBe(true);
    await expect(thread.getByRole("alert")).toHaveCount(0);
    expect(
      (discussionsOf("L01", wishId).threads ?? [])[0]!.messages,
    ).toHaveLength(3);
    await page.screenshot({ path: path.join(shots, "documents-thread.png") });

    // Tabbing from the field to a button keeps it; a click elsewhere folds it into a dot, its draft kept.
    const reply = thread.getByRole("textbox", { name: "Reply" });
    await reply.fill("A draft");
    await page.keyboard.press("Tab");
    await expect(thread).toBeVisible();
    await body.locator("p").first().click();
    await expect(thread).toHaveCount(0);
    const dot = panel.locator(".tilasm-dot");
    await expect(dot).toHaveCount(1);
    // A message that arrives meanwhile marks the dot, without opening the window.
    djinn(
      "doc",
      "reply",
      threadId,
      "--text",
      "One more thing.",
      "--author",
      "lead",
    );
    await expect(dot).toHaveClass(/is-unread/);
    await expect(thread).toHaveCount(0);
    await dot.click();
    await expect(thread).toContainText("One more thing.");
    await expect(thread.getByRole("textbox", { name: "Reply" })).toHaveValue(
      "A draft",
    );
    await page.keyboard.press("Escape");
    await expect(thread).toHaveCount(0);

    // A proposal shows as a diff in the text; accepted, the Markdown holds it.
    djinn(
      "doc",
      "propose",
      "L01",
      "--wish",
      wishId,
      "--quote",
      "The glass keeps the wind out.",
      "--replacement",
      "The glass keeps the draught out.",
      "--note",
      "A plainer word",
      "--author",
      "W12",
    );
    const old = body.locator(".tilasm-old");
    await expect(old).toHaveText("The glass keeps the wind out.");
    await expect(body.locator(".tilasm-new")).toContainText(
      "The glass keeps the draught out.",
    );
    expect(markdownOf("L01", wishId)).toContain(
      "The glass keeps the wind out.",
    );
    // A bar by the change decides it in one click, shown without a click.
    const bar = panel.getByRole("toolbar", { name: "Change proposed by W12" });
    await expect(bar.getByRole("button", { name: "Accept" })).toBeVisible();
    await expect(bar.getByRole("button", { name: "Reject" })).toBeVisible();
    const barBox = (await bar.boundingBox())!;
    const changeBox = (await body.locator(".tilasm-new").boundingBox())!;
    expect(
      barBox.x >= changeBox.x + changeBox.width ||
        barBox.y >= changeBox.y + changeBox.height ||
        barBox.y + barBox.height <= changeBox.y,
    ).toBe(true);
    await old.click();
    const card = page.getByRole("dialog", { name: "Proposed change" });
    await expect(card).toContainText("A plainer word");
    await page.screenshot({ path: path.join(shots, "documents-proposal.png") });
    await card.getByRole("button", { name: "Accept" }).click();
    await expect
      .poll(() => markdownOf("L01", wishId))
      .toContain("The glass keeps the draught out.");
    await expect(body).toContainText("The glass keeps the draught out.");
    await expect(body.locator(".tilasm-old")).toHaveCount(0);
    await expect(body.locator(".tilasm-new")).toHaveCount(0);

    // Rejected, the text stays as it was.
    djinn(
      "doc",
      "propose",
      "L01",
      "--wish",
      wishId,
      "--quote",
      "The wick carries the oil.",
      "--delete",
      "--author",
      "lead",
    );
    await expect(old).toHaveText("The wick carries the oil.");
    await panel
      .getByRole("toolbar", { name: "Change proposed by Lead" })
      .getByRole("button", { name: "Reject" })
      .click();
    await expect(old).toHaveCount(0);
    await expect
      .poll(
        () =>
          (discussionsOf("L01", wishId).proposals ?? []).filter((p) =>
            /REJECTED/.test(p.status ?? ""),
          ).length,
      )
      .toBe(1);
    expect(markdownOf("L01", wishId)).toContain("The wick carries the oil.");

    // Cmd+Enter (Ctrl+Enter), the caret in a proposal's passage, accepts it.
    djinn(
      "doc",
      "propose",
      "L01",
      "--wish",
      wishId,
      "--quote",
      "The glass keeps the draught out.",
      "--replacement",
      "The glass keeps the draught away.",
      "--author",
      "lead",
    );
    await expect(old).toHaveText("The glass keeps the draught out.");
    await body.locator("p", { hasText: "The flame needs air." }).click();
    await page.keyboard.press("End");
    await page.keyboard.press("ArrowDown");
    await expect
      .poll(() => page.evaluate(() => getSelection()?.anchorNode?.textContent))
      .toBe("The glass keeps the draught out.");
    await page.keyboard.press("ControlOrMeta+Enter");
    await expect
      .poll(() => markdownOf("L01", wishId))
      .toContain("The glass keeps the draught away.");
    await expect(old).toHaveCount(0);

    // A click on a block's handle opens its menu; Delete takes the block, undo brings it back.
    await body.locator("p").first().hover();
    await panel.getByRole("button", { name: "Drag to move the block" }).click();
    const menu = page.getByRole("menu", { name: "Block actions" });
    await expect(menu).toBeVisible();
    await expect(menu.getByRole("menuitem").first()).toBeFocused();
    await menu.getByRole("menuitem", { name: "Delete" }).click();
    await expect(menu).toHaveCount(0);
    await expect(body).not.toContainText("The wick carries the oil.");
    await page.keyboard.press("ControlOrMeta+z");
    await expect(body).toContainText("The wick carries the oil.");
    await expect
      .poll(() => markdownOf("L01", wishId))
      .toContain("The wick carries the oil.");
    // From the keyboard: Enter on the handle opens it, the arrows move in it, Escape gives the handle its focus back.
    await body.locator("p").first().hover();
    const handle = panel.getByRole("button", {
      name: "Drag to move the block",
    });
    await handle.focus();
    await page.keyboard.press("Enter");
    await expect(menu.getByRole("menuitem").first()).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(
      menu.getByRole("menuitem", { name: "Duplicate" }),
    ).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
    await expect(handle).toBeFocused();

    // Nothing detached from the text: no indicator in the header. The lead's new version drops the discussed passage:
    // a quiet count shows by the actions, and lists it.
    const header = panel.locator(".workspace-panel-header");
    const detached = header.locator(".tilasm-apart-button");
    await expect(detached).toHaveCount(0);
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-tilasm-"));
    try {
      const file = path.join(dir, "lamp.md");
      fs.writeFileSync(
        file,
        "# Lamp\n\nThe wick carries the oil.\n\nThe glass keeps the draught away.\n",
      );
      djinn("tilasm", "put", file, "--wish", wishId, "--code", "L01");
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
    // The undo left the editor's text unsaved: the developer takes the lead's version.
    const reloadTheirs = panel
      .locator(".tilasm-notice")
      .getByRole("button", { name: "Reload theirs" });
    await expect
      .poll(
        async () =>
          (await reloadTheirs.isVisible()) ||
          !(await body.textContent())?.includes("The flame needs air."),
      )
      .toBe(true);
    if (await reloadTheirs.isVisible()) await reloadTheirs.click();
    await expect(body).not.toContainText("The flame needs air.");
    await expect(detached).toHaveText("1");
    await expect(detached).toHaveAccessibleName(
      "1 comment or proposal detached from the text",
    );
    const headerBox = (await header.boundingBox())!;
    const stripBox = (await panel.locator(".tilasm-strip").boundingBox())!;
    await page.screenshot({
      path: path.join(shots, "documents-header-detached.png"),
      clip: {
        x: headerBox.x - 8,
        y: headerBox.y - 8,
        width: headerBox.width + 16,
        height: stripBox.y + stripBox.height - headerBox.y + 16,
      },
    });
    await detached.click();
    await expect(
      page.getByRole("dialog", {
        name: "1 comment or proposal detached from the text",
      }),
    ).toContainText("needs air");
    await page.keyboard.press("Escape");
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});

// Selects words in the paragraph that holds them, with the keyboard, from its end.
async function selectWords(
  page: Page,
  paragraph: ReturnType<Page["locator"]>,
  words: string,
  fromEnd: number,
) {
  await paragraph.click();
  await page.keyboard.press("End");
  for (let i = 0; i < fromEnd; i++) await page.keyboard.press("ArrowLeft");
  for (let i = 0; i < words.length; i++)
    await page.keyboard.press("Shift+ArrowLeft");
  await expect
    .poll(() => page.evaluate(() => String(getSelection())))
    .toBe(words);
}

test("the agent is asked for a change at a selection or at the caret: a request discussion, answered by a proposal accepted in it", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const wishId = await openWish(
    page,
    `Ask the lamp ${randomUUID().slice(0, 8)}`,
  );
  try {
    djinn(
      "tilasm",
      "create",
      "Lamp",
      "--wish",
      wishId,
      "--markdown",
      "The wick carries the oil.\n\nThe flame needs air.\n\nThe glass keeps the wind out.",
    );
    await expect(page.locator('[data-wish-section="tilasms"]')).toBeVisible();
    djinn("tilasm", "open", "L01", "--wish", wishId);
    const panel = workspacePanel(page, "tilasms");
    const body = panel.locator(".tilasm-prose");
    await expect(body).toContainText("The flame needs air.");

    // The bar asks the agent at a selection: a small input by the passage, which stays marked.
    await selectWords(
      page,
      body.locator("p", { hasText: "The flame needs air." }),
      "needs air",
      1,
    );
    await page
      .getByRole("toolbar", { name: "Formatting" })
      .getByRole("button", { name: "Ask the agent" })
      .click();
    const ask = page.getByRole("dialog", { name: "Ask the agent" });
    const field = ask.getByRole("textbox", {
      name: "Your request to the agent",
    });
    await expect(field).toBeFocused();
    await expect(body.locator(".tilasm-mark")).toHaveText("needs air");
    // Shift+Enter starts a new line; Escape folds the input into a dot, its draft kept.
    await field.pressSequentially("Say why");
    await page.keyboard.press("Shift+Enter");
    await field.pressSequentially("in a few words");
    await expect(field).toHaveValue("Say why\nin a few words");
    await page.screenshot({
      path: path.join(shots, "documents-request-input.png"),
    });
    await page.keyboard.press("Escape");
    await expect(ask).toHaveCount(0);
    const draftDot = panel.locator(".tilasm-dot.is-draft");
    await expect(draftDot).toHaveAttribute(
      "aria-label",
      "Request not sent yet",
    );
    await draftDot.click();
    await expect(field).toHaveValue("Say why\nin a few words");
    // Enter sends: a discussion marked as a request, on the passage.
    await field.press("Enter");
    const thread = page.getByRole("dialog", { name: "Request to the agent" });
    await expect(thread).toBeVisible();
    await expect(thread.locator(".tilasm-thread-kind")).toHaveText("Request");
    await expect(thread).toContainText("in a few words");
    await expect(body.locator(".tilasm-mark")).toHaveText("needs air");
    const opened = discussionsOf("L01", wishId).threads ?? [];
    expect(opened).toHaveLength(1);
    expect(opened[0]!.kind).toBe("TILASM_THREAD_KIND_REQUEST");
    expect(opened[0]!.anchor?.quote).toBe("needs air");
    const threadId = opened[0]!.id;

    // The document agent answers with a proposal linked to the request: its diff shows in the text, and the request
    // accepts it.
    djinn(
      "doc",
      "propose",
      "L01",
      "--wish",
      wishId,
      "--thread",
      threadId,
      "--replacement",
      "needs oxygen to burn",
      "--note",
      "Fire burns oxygen",
      "--author",
      "document",
    );
    await expect(body.locator(".tilasm-old")).toHaveText("needs air");
    await expect(body.locator(".tilasm-new")).toContainText(
      "needs oxygen to burn",
    );
    const nested = thread.locator(".tilasm-proposal");
    await expect(nested).toContainText("Fire burns oxygen");
    await expect(nested).toContainText("Document agent");
    await nested.getByRole("button", { name: "Accept" }).click();
    await expect
      .poll(() => markdownOf("L01", wishId))
      .toContain("The flame needs oxygen to burn.");
    await expect(body).toContainText("The flame needs oxygen to burn.");
    await expect(body.locator(".tilasm-new")).toHaveCount(0);

    // At the caret, the "/" menu asks the agent: a request at a place, with no quote.
    await page.keyboard.press("Escape");
    await body.locator("p", { hasText: "The wick carries the oil." }).click();
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    await page.keyboard.type("/ask");
    const slash = page.getByRole("listbox", { name: "Blocks" });
    await expect(slash.getByRole("option")).toHaveText(["Ask the agent"]);
    await page.keyboard.press("Enter");
    await expect(ask).toBeVisible();
    await expect(body).not.toContainText("/ask");
    await expect(body.locator(".tilasm-point.is-active")).toHaveCount(1);
    await field.pressSequentially("Add a sentence on the wick's cotton");
    await field.press("Enter");
    await expect(thread).toContainText("Request at this place");
    await expect
      .poll(() => (discussionsOf("L01", wishId).threads ?? []).length)
      .toBe(2);
    const atCaret = (discussionsOf("L01", wishId).threads ?? []).find(
      (x) => x.id !== threadId,
    )!;
    expect(atCaret.kind).toBe("TILASM_THREAD_KIND_REQUEST");
    expect(atCaret.anchor?.quote ?? "").toBe("");
    expect(
      (atCaret.anchor?.prefix ?? "") + (atCaret.anchor?.suffix ?? ""),
    ).toContain("The flame");

    // Cmd+J (Ctrl+J) opens the same input at the caret; Escape with nothing written leaves nothing behind.
    await page.keyboard.press("Escape");
    await body
      .locator("p", { hasText: "The glass keeps the wind out." })
      .click();
    await page.keyboard.press("ControlOrMeta+j");
    await expect(field).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(ask).toHaveCount(0);
    await expect(panel.locator(".tilasm-dot.is-draft")).toHaveCount(0);
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "pause", wishId);
  }
});

// A djinn of its own with question workers on, which wakes a document agent: the project's settings run it on the
// fake provider, which plays its prompt; the request's text, "wait", has it wait for a message, so that it works on the
// request while the test looks. The test answers as the agent would, with the command line.
test("while the document agent works on a request, its discussion and its dot say so; its answer shows in place", async ({
  page,
}) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-doc-"));
  const project = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-docp-"));
  fs.mkdirSync(path.join(project, ".agents"));
  fs.writeFileSync(
    path.join(project, ".agents", "settings.txtpb"),
    "provider: PROVIDER_FAKE\n",
  );
  // No real agent can run: a fake one for each, first on the PATH.
  const bin = path.join(home, "bin");
  fs.mkdirSync(bin);
  if (process.platform !== "win32")
    for (const agent of ["claude", "codex", "agy"])
      fs.writeFileSync(path.join(bin, agent), "#!/bin/sh\nexec cat\n", {
        mode: 0o755,
      });
  const env = {
    ...process.env,
    DJINN_HOME: home,
    DJINN_QUESTION_WORKERS: "on",
    PATH: bin + path.delimiter + process.env.PATH,
  };
  const own = (...args: string[]) =>
    execFileSync(binary, args, { env, encoding: "utf8" });
  const proc: ChildProcess = spawn(binary, ["up", "--browser"], {
    stdio: ["ignore", "pipe", "inherit"],
    env,
  });
  const exited = new Promise((resolve) => proc.once("exit", resolve));
  try {
    const url = await new Promise<string>((resolve, reject) => {
      const timer = setTimeout(
        () => reject(new Error("djinn printed no URL")),
        10_000,
      );
      let out = "";
      proc.stdout!.on("data", (chunk: Buffer) => {
        out += chunk.toString();
        const match = out.match(/http:\/\/127\.0\.0\.1:\d+\/\?token=[0-9a-f]+/);
        if (match) {
          clearTimeout(timer);
          resolve(match[0]);
        }
      });
      proc.once("exit", (code) => reject(new Error(`djinn exited: ${code}`)));
    });
    const projectId = JSON.parse(own("project", "add", project, "--json"))
      .project.id as string;
    const title = `Agent at work ${randomUUID().slice(0, 8)}`;
    const wishId = JSON.parse(
      own("wish", "make", title, "--project-ids", projectId, "--json"),
    ).wish.id as string;
    own(
      "tilasm",
      "create",
      "Lamp",
      "--wish",
      wishId,
      "--markdown",
      "The wick carries the oil.\n\nThe flame needs air.",
    );
    await page.goto(url);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(page.locator('[data-wish-section="tilasms"]')).toBeVisible();
    own("tilasm", "open", "L01", "--wish", wishId);
    const panel = workspacePanel(page, "tilasms");
    const body = panel.locator(".tilasm-prose");
    await expect(body).toContainText("The flame needs air.");

    await selectWords(
      page,
      body.locator("p", { hasText: "The flame needs air." }),
      "needs air",
      1,
    );
    await page.keyboard.press("ControlOrMeta+j");
    const field = page
      .getByRole("dialog", { name: "Ask the agent" })
      .getByRole("textbox", { name: "Your request to the agent" });
    await field.fill("wait");
    await field.press("Enter");
    const thread = page.getByRole("dialog", { name: "Request to the agent" });
    const writing = thread.getByRole("status");
    await expect(writing).toHaveText("Document agent is writing…", {
      timeout: 20_000,
    });
    const asked = (JSON.parse(
      own("doc", "discussions", "L01", "--wish", wishId, "--json"),
    ).threads ?? [])[0] as { id: string; anchor?: { quote?: string } };
    expect(asked.anchor?.quote).toBe("needs air");
    const threadId = asked.id;
    await page.screenshot({
      path: path.join(shots, "documents-agent-writing.png"),
    });

    // Folded, its dot says so too; the answer marks it, without opening it.
    await body.locator("p").first().click();
    await expect(thread).toHaveCount(0);
    const dot = panel.locator(".tilasm-dot");
    await expect(dot).toHaveClass(/is-working/);
    await expect(dot).toHaveAttribute(
      "aria-label",
      /Document agent is writing…/,
    );
    own(
      "doc",
      "propose",
      "L01",
      "--wish",
      wishId,
      "--thread",
      threadId,
      "--replacement",
      "needs oxygen",
      "--author",
      "document",
    );
    own(
      "doc",
      "reply",
      threadId,
      "--text",
      "Proposed: oxygen.",
      "--author",
      "document",
    );
    await expect(body.locator(".tilasm-new")).toContainText("needs oxygen");
    await expect(dot).not.toHaveClass(/is-working/);
    await expect(dot).toHaveClass(/is-unread/);
    await expect(thread).toHaveCount(0);
    await dot.click();
    await expect(thread).toContainText("Proposed: oxygen.");
    await expect(writing).toHaveCount(0);
    // The request points to its proposal in the text.
    await thread.getByRole("button", { name: "Show in the text" }).click();
    await expect(
      page.getByRole("dialog", { name: "Proposed change" }),
    ).toBeVisible();

    // The agent's task is named as the document agent, and opens its document.
    const card = page
      .locator(".wish-task")
      .filter({ hasText: "Document agent of L01" });
    await openWorkspaceSection(page, "tasks");
    await expect(card).toHaveCount(1);
    expect(errors).toEqual([]);
  } finally {
    if (proc.exitCode === null) {
      proc.kill(process.platform === "win32" ? undefined : "SIGINT");
      await exited;
    }
    for (const dir of [home, project])
      fs.rmSync(dir, {
        recursive: true,
        force: true,
        maxRetries: 20,
        retryDelay: 250,
      });
  }
});
