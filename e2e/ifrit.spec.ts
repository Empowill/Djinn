// A lit ifrit shows plainly in the window (T51, tilasm L06 section 6): its band heads what waits for you, holding its
// own question and the lead's question about it while another question stays below; its wish bar shows the flame with
// its count and the wish's vermilion tone, the wish's head its badge and word; its card in the Tasks tab says Lit and
// offers Finish, which ends it and its band. The ifrit is the fake agent spawned with --kind ifrit: it says its line,
// then listens on its open input; no model is called. Screenshots of the band and the card, dark and light, go to
// test-results/e2e/.
import { expect, type Page, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { portable } from "./portable";

portable();

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const shots = path.join(__dirname, "../test-results/e2e");

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

function git(dir: string, ...args: string[]): string {
  return execFileSync("git", args, { cwd: dir, encoding: "utf8" });
}

// theme switches the theme in the page, without a reload, once its transitions are over.
async function theme(page: Page, value: "dark" | "light") {
  await page.evaluate(async (v) => {
    if (v === "light") localStorage.setItem("djinn.theme", v);
    else localStorage.removeItem("djinn.theme");
    document.documentElement.dataset.theme = v;
    await Promise.all(
      document
        .getAnimations()
        .filter((a) => a instanceof CSSTransition)
        .map((a) => a.finished.catch(() => {})),
    );
  }, value);
}

type Task = {
  id: string;
  code: string;
  status: string;
  ifrit?: { state?: string };
};

test("a lit ifrit shows on its wish in vermilion, its band first in what waits for you, until it is finished", async ({
  page,
}) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const suffix = randomUUID().slice(0, 8);
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "ifrit-")),
  );
  git(folder, "init", "--quiet", "-b", "main");
  // Djinn merges the ifrit's work once it is finished: the project's repository has an identity (docs/pitfalls.md).
  git(folder, "config", "user.name", "e2e");
  git(folder, "config", "user.email", "e2e@example.com");
  fs.writeFileSync(path.join(folder, "lamp.txt"), "a brass lamp\n");
  git(folder, "add", ".");
  git(folder, "commit", "--quiet", "-m", "A lamp");
  const projectId = JSON.parse(
    djinn("project", "add", folder, "--name", `ifrit-${suffix}`, "--json"),
  ).project.id as string;
  const title = `Light the lamp ${suffix}`;
  const wishId = JSON.parse(
    djinn("wish", "make", title, "--project-ids", projectId, "--json"),
  ).wish.id as string;
  let ifritId = "";
  try {
    // A question about nothing in particular, asked first: it stays below the band. Each question here is needed before
    // something, so it holds up the wish: one that can wait is in the questions column alone, never in the bar.
    djinn(
      "question",
      "ask",
      "Which wick for the lamp?",
      wishId,
      "--before",
      "the first light",
    );
    const ifrit = JSON.parse(
      djinn(
        "task",
        "spawn",
        wishId,
        "--project-id",
        projectId,
        "--kind",
        "ifrit",
        "--title",
        "Front of the lamp",
        "--provider",
        "fake",
        "--prompt",
        "text The front is up.",
        "--json",
      ),
    ).task as Task;
    ifritId = ifrit.id;
    const get = () =>
      JSON.parse(djinn("task", "get", ifritId, "--json")).task as Task;
    // Its line said, it listens on its open input.
    await expect
      .poll(() => get().ifrit?.state ?? "", { timeout: 20_000 })
      .toBe("IFRIT_STATE_LISTENING");
    const code = ifrit.code;
    djinn(
      "question",
      "ask",
      "The route needs a token: use the demo one?",
      wishId,
      "--task-id",
      ifritId,
      "--before",
      "the first light",
    );
    djinn(
      "question",
      "ask",
      "Keep this margin for every card?",
      wishId,
      "--about",
      code,
      "--before",
      "the first light",
    );

    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto(process.env.DJINN_URL!);
    const row = page.locator(".wish-nav").filter({ hasText: title });
    await row.click();

    // What waits for you: the band first, its two questions inside it, the other question below, each once.
    const bar = page.getByRole("navigation", { name: "What waits for you" });
    const band = bar.getByRole("region", {
      name: `Ifrit ${code}: Front of the lamp`,
    });
    await expect(band).toBeVisible();
    await expect(bar.locator(":scope > ul > li").first()).toContainText(
      `Ifrit${code}Front of the lamp`,
    );
    await expect(band.locator(".attention-ifrit-state")).toHaveText("Lit");
    const lines = band.locator(".attention-item");
    await expect(lines).toHaveCount(2);
    await expect(lines.nth(0)).toContainText(`asked by ${code}`);
    await expect(lines.nth(0)).toContainText("The route needs a token");
    await expect(lines.nth(1)).toContainText(`the Djinn, about ${code}`);
    await expect(lines.nth(1)).toContainText(
      "Keep this margin for every card?",
    );
    await expect(bar.locator(":scope > ul > li").nth(1)).toContainText(
      "Which wick for the lamp?",
    );
    await expect(bar.getByText("The route needs a token")).toHaveCount(1);
    await expect(bar.locator(".attention-summary")).toHaveText(
      "4 things wait for you",
    );

    // The wish bar: the logo's flame first on the second line.
    await expect(
      row.locator(".wish-counts .wish-sign.tone-ifrit"),
    ).toBeVisible();
    await expect(row.getByRole("img", { name: "1 ifrit lit" })).toBeVisible();
    // The wish's head: its badge and the word of its state.
    await expect(
      page
        .locator(".review-pills .status-badge.tone-ifrit")
        .filter({ hasText: `Ifrit ${code} · Lit` }),
    ).toBeVisible();
    await page.screenshot({ path: path.join(shots, "ifrit-band-dark.png") });
    await theme(page, "light");
    await page.screenshot({ path: path.join(shots, "ifrit-band-light.png") });
    await theme(page, "dark");

    // The band leads to its card, in the Tasks tab: Lit, in vermilion, with Finish.
    await band.locator(".attention-ifrit-title").click();
    await expect(page.getByRole("tab", { name: /^Tasks/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const card = page.locator(`#task-${ifritId}`);
    await expect(card).toHaveClass(/tone-ifrit/);
    await expect(card.locator(".status-badge").first()).toHaveText("Lit");
    const finish = card.getByRole("button", { name: "Finish" });
    await expect(finish).toBeVisible();
    await card.screenshot({ path: path.join(shots, "ifrit-card-dark.png") });
    await theme(page, "light");
    await card.screenshot({ path: path.join(shots, "ifrit-card-light.png") });
    await theme(page, "dark");

    // Finished, it ends: no band, no flame on its wish, and its card is no longer lit.
    await finish.click();
    await expect(band).toHaveCount(0, { timeout: 20_000 });
    await expect(row.locator(".wish-sign.tone-ifrit")).toHaveCount(0);
    await expect(card).not.toHaveClass(/tone-ifrit/);
    expect(errors).toEqual([]);
  } finally {
    if (ifritId && statusOf(ifritId) === "TASK_STATUS_RUNNING")
      djinn("task", "stop", ifritId);
    djinn("wish", "pause", wishId);
  }
});

function statusOf(id: string): string {
  return (JSON.parse(djinn("task", "get", id, "--json")).task as Task).status;
}
