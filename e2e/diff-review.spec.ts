// A task's diffs open full size beside the side panel, as a tilasm does: its committed work on one tab, what its
// worktree holds not committed on the other, read only. The files it changes make a tree of folders on the left, their
// diffs follow one another on the right: a click or Enter in the tree scrolls the diff to a file, the tree follows the
// diff as it scrolls. Escape closes the review, back where the developer was; a click in the side panel closes it and
// goes where it leads. The worker is the fake agent: no model is called. While it waits, the spec commits files in the
// task's worktree, then leaves a change and a new file there without committing them. Screenshots of the review, wide
// and narrow, in both themes.
import { expect, type Page, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { portable } from "./portable";

portable();

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

function git(dir: string, ...args: string[]): string {
  return execFileSync(
    "git",
    ["-c", "user.name=e2e", "-c", "user.email=e2e@example.com", ...args],
    { cwd: dir, encoding: "utf8" },
  );
}

function write(dir: string, file: string, text: string) {
  fs.mkdirSync(path.dirname(path.join(dir, file)), { recursive: true });
  fs.writeFileSync(path.join(dir, file), text);
}

const lines = (word: string, n: number) =>
  Array.from({ length: n }, (_, i) => `${word} line ${i + 1}`).join("\n") +
  "\n";

const SHOTS = Boolean(process.env.DIFF_SHOTS);

// shoot takes the window in both themes, once their transitions end.
async function shoot(page: Page, name: string) {
  if (!SHOTS) return;
  for (const theme of ["dark", "light"]) {
    await page.evaluate(async (theme) => {
      document.documentElement.dataset.theme = theme;
      await Promise.all(
        document
          .getAnimations()
          .filter((a) => a instanceof CSSTransition)
          .map((a) => a.finished.catch(() => {})),
      );
    }, theme);
    await page.screenshot({
      path: path.join(shots, `diff-review-${name}-${theme}.png`),
    });
  }
  await page.evaluate(() => {
    document.documentElement.dataset.theme = "dark";
  });
}

test("a task's diffs open full size beside the side panel, its files as a tree of folders, uncommitted tab, narrow layout, and closing", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const { title, worktree, clean } = await setupTask();
  try {
    await page.setViewportSize({ width: 1600, height: 1000 });
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    await page.getByRole("button", { name: /Rub the lamp/ }).click();
    const scroller = page.locator(".wish-view .mission-scroll");
    await scroller.evaluate((el) => (el.scrollTop = 120));
    const before = await scroller.evaluate((el) => el.scrollTop);

    // Full size: the review covers everything right of the side panel, which stays in reach; the view under it is
    // out of the keyboard's reach.
    const see = page.getByRole("button", { name: "See the diff" });
    await see.click();
    const review = page.getByRole("region", { name: /^Diff of / });
    await expect(review).toBeVisible();
    const shell = (await page.locator(".main-shell").boundingBox())!;
    const box = (await review.boundingBox())!;
    for (const side of ["x", "y", "width", "height"] as const)
      expect(Math.abs(box[side] - shell[side])).toBeLessThan(1);
    const sidebar = (await page.locator("#djinn-sidebar").boundingBox())!;
    expect(sidebar.x + sidebar.width).toBeLessThanOrEqual(box.x + 1);
    await expect(
      review.getByRole("button", { name: "Close the diff" }),
    ).toBeFocused();
    await expect(page.locator(".main-shell > .wish-view")).toHaveJSProperty(
      "inert",
      true,
    );
    const committed = review.getByRole("tab", {
      name: "Committed",
      exact: true,
    });
    await expect(committed).toHaveAttribute("aria-selected", "true");

    // The tree: folders first, each with its count of files and lines; a chain of single folders as one row.
    const tree = review.getByRole("tree", { name: "Changed files" });
    const rows = tree.getByRole("treeitem");
    await expect(rows).toHaveText([
      "lamp3 files+301",
      "brass2 files+151",
      "oil.txt+150",
      "wick.txt+1",
      "glass.txt+150",
      "wishes/kept/in/a1 file+150",
      "jar.txt+150",
      "notes.md+1",
    ]);
    const row = (name: string) => rows.filter({ hasText: name });
    await expect(row("oil.txt")).toHaveAttribute("aria-selected", "true");
    await expect(review.locator(".diff-bar")).toContainText("5 files+452");
    await shoot(page, "wide");

    // A click on a file scrolls the diff to it.
    const column = review.locator(".diff-files");
    const part = (file: string) =>
      review.locator(`.diff-file[data-path="${file}"]`);
    const atTop = async (file: string) => {
      const top = (await column.boundingBox())!.y;
      return Math.abs((await part(file).boundingBox())!.y - top) < 2;
    };
    await row("jar.txt").click();
    await expect.poll(() => atTop("wishes/kept/in/a/jar.txt")).toBe(true);
    await expect(row("jar.txt")).toHaveAttribute("aria-selected", "true");
    await expect(
      review
        .locator(".diff-line-row")
        .filter({ hasText: "jar line 1" })
        .first(),
    ).toBeVisible();

    // The tree follows the diff as it scrolls.
    await part("lamp/glass.txt").evaluate((el) => {
      const col = el.closest(".diff-files")!;
      col.scrollTop +=
        el.getBoundingClientRect().top - col.getBoundingClientRect().top + 40;
    });
    await expect(row("glass.txt")).toHaveAttribute("aria-selected", "true");

    // A folder closes on a click: the row of the file read is its folder's now.
    await row("lamp").click();
    await expect(rows).toHaveCount(4);
    await expect(row("lamp")).toHaveAttribute("aria-expanded", "false");
    await expect(row("lamp")).toHaveAttribute("aria-selected", "true");

    // The keyboard: Right opens a folder then enters it, the arrows move, Enter opens a file, Left goes up and closes.
    await expect(row("lamp")).toBeFocused();
    await page.keyboard.press("ArrowRight");
    await expect(row("lamp")).toHaveAttribute("aria-expanded", "true");
    await page.keyboard.press("ArrowRight");
    await expect(row("brass")).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("ArrowDown");
    await expect(row("wick.txt")).toBeFocused();
    await page.keyboard.press("Enter");
    await expect.poll(() => atTop("lamp/brass/wick.txt")).toBe(true);
    await expect(row("wick.txt")).toHaveAttribute("aria-selected", "true");
    await page.keyboard.press("ArrowLeft");
    await expect(row("brass")).toBeFocused();
    await page.keyboard.press("ArrowLeft");
    await expect(row("brass")).toHaveAttribute("aria-expanded", "false");
    // The keys stayed in the tree: the review is still open.
    await expect(review).toBeVisible();

    // The worktree's tab: the change and the new file, not the commit, and no comment on them.
    await review.getByRole("tab", { name: "Not committed" }).click();
    await expect(rows).toHaveText(["lamp.txt+1-1", "smoke.md+1"]);
    const smoke = review
      .locator(".diff-line-row")
      .filter({ hasText: "a wisp of smoke" });
    await expect(smoke).toBeVisible();
    await smoke.hover();
    await expect(review.locator(".diff-add-comment-btn")).toHaveCount(0);
    // Back on the committed work, the folder closed is still closed.
    await committed.click();
    await expect(row("brass")).toHaveAttribute("aria-expanded", "false");

    // Narrow, the tree goes above the diffs.
    await page.setViewportSize({ width: 900, height: 800 });
    await expect
      .poll(async () => {
        const t = (await tree.boundingBox())!;
        const c = (await column.boundingBox())!;
        return t.y + t.height <= c.y + 1;
      })
      .toBe(true);
    await shoot(page, "narrow");
    await page.setViewportSize({ width: 1600, height: 1000 });

    // Escape closes it, the focus back on the button that opened it, the view under it as it was.
    await page.keyboard.press("Escape");
    await expect(review).toHaveCount(0);
    await expect(see).toBeFocused();
    await expect(page.locator(".main-shell > .wish-view")).toHaveJSProperty(
      "inert",
      false,
    );
    expect(await scroller.evaluate((el) => el.scrollTop)).toBe(before);

    // The card's second button opens the worktree's tab; once the worktree is clean, refresh says so.
    await page.getByRole("button", { name: "See uncommitted files" }).click();
    await expect(
      review.getByRole("tab", { name: "Not committed" }),
    ).toHaveAttribute("aria-selected", "true");
    await expect(rows).toHaveCount(2);
    git(worktree, "checkout", "--", "lamp.txt");
    fs.rmSync(path.join(worktree, "smoke.md"));
    await review.getByRole("button", { name: "Read the diff again" }).click();
    // In the drawer, the sentence under the drawing of raked sand (empty-art.tsx).
    await expect(review.locator(".diff-state .empty-state-text")).toHaveText(
      "The worktree is clean: nothing left uncommitted.",
    );
    await expect(review.locator(".diff-state svg.empty-art")).toBeVisible();

    // A click in the side panel closes it and goes where it leads: the wish shown, on its questions, then the Hourglass.
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    await expect(review).toHaveCount(0);
    await expect(page.getByRole("tab", { name: "Wish" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    await page.getByRole("button", { name: /Rub the lamp/ }).click();
    await see.click();
    await expect(review).toBeVisible();
    await page.locator("#djinn-sidebar .hourglass-nav").click();
    await expect(review).toHaveCount(0);
    await expect(page.locator(".hourglass-nav")).toHaveClass(/\bselected\b/);
    expect(errors).toEqual([]);
  } finally {
    clean();
  }
});

let project: { folder: string; id: string } | undefined;

test.afterAll(() => {
  if (project) fs.rmSync(project.folder, { recursive: true, force: true });
  project = undefined;
});

async function setupTask() {
  if (!project) {
    const suffix = randomUUID().slice(0, 8);
    const folder = fs.realpathSync(
      fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "review-")),
    );
    git(folder, "init", "--quiet", "-b", "main");
    fs.writeFileSync(path.join(folder, "lamp.txt"), "a brass lamp\n");
    git(folder, "add", ".");
    git(folder, "commit", "--quiet", "-m", "A lamp");
    const id = JSON.parse(
      djinn("project", "add", folder, "--name", `review-${suffix}`, "--json"),
    ).project.id as string;
    project = { folder, id };
  }
  const suffix = randomUUID().slice(0, 8);
  const existing = (JSON.parse(djinn("wish", "list", "--json")).wishes ??
    []) as {
    id: string;
    state: string;
  }[];
  for (const w of existing)
    if (w.state === "WISH_STATE_ACTIVE") djinn("wish", "pause", w.id);
  const title = `Polish the lamp ${suffix}`;
  const wishId = JSON.parse(
    djinn("wish", "make", title, "--project-ids", project.id, "--json"),
  ).wish.id as string;
  const taskId = JSON.parse(
    djinn(
      "task",
      "spawn",
      wishId,
      "--project-id",
      project.id,
      "--title",
      "Rub the lamp",
      "--provider",
      "fake",
      "--prompt",
      "text working\nwait",
      "--json",
    ),
  ).task.id as string;
  const clean = () => {
    djinn("task", "stop", taskId);
    djinn("wish", "delete", wishId);
  };
  try {
    let worktree = "";
    await expect
      .poll(() => {
        const task = JSON.parse(djinn("task", "get", taskId, "--json")).task;
        worktree = task.status === "TASK_STATUS_RUNNING" ? task.worktree : "";
        return worktree;
      })
      .not.toBe("");
    write(worktree, "lamp/brass/wick.txt", "a committed wick\n");
    write(worktree, "lamp/brass/oil.txt", lines("oil", 150));
    write(worktree, "lamp/glass.txt", lines("glass", 150));
    write(worktree, "wishes/kept/in/a/jar.txt", lines("jar", 150));
    write(worktree, "notes.md", "a note\n");
    git(worktree, "add", ".");
    git(worktree, "commit", "--quiet", "-m", "A wick, oil, glass and a jar");
    fs.writeFileSync(path.join(worktree, "lamp.txt"), "a shining lamp\n");
    fs.writeFileSync(path.join(worktree, "smoke.md"), "a wisp of smoke\n");
    return { title, worktree, clean };
  } catch (err) {
    clean();
    throw err;
  }
}
