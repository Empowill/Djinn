// Screenshots of opened azimas in the Tasks tab of a wish of real size (bigwish.ts), dark and light, wide, narrowed
// and phone-wide, into test-results/azima-shots/ (or AZIMA_SHOTS): to look at an azima's card before and after a
// change. Its azimas get a Markdown description, one long, the last none; one waits for another. Run with
// `go tool task screenshots` (it also retakes the README's).
import { type Page, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import path from "node:path";

import { importBigWish, removeBigWish } from "./bigwish";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const shots =
  process.env.AZIMA_SHOTS ??
  path.join(__dirname, "../test-results/azima-shots");

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

const short = [
  "Split the **orchestrator** in two: the scheduler picks the next task, the runner starts its worker.",
  "",
  "- the scheduler never blocks on a worker",
  "- a runner that dies leaves its task *waiting*, never lost",
  "",
  "See `internal/orchestrator` and [the design](https://example.com/design).",
].join("\n");

const long = [
  "## Why",
  "",
  "The window reads every event of every task at each change: on a wish of real size it stalls for a second.",
  "",
  "## How",
  "",
  "1. Keep the last event of each task in the store, apart from the others.",
  "2. Send the window a change, not the whole wish.",
  "3. Measure it on the real wish, and on ten times that.",
  "",
  "```go",
  "func (s *Store) Last(task string) (*Event, error)",
  "```",
  "",
  "## Proof",
  "",
  "- opening the wish under 300 ms",
  "- a change shown under 500 ms",
  "- nothing lost after a restart",
  "",
  "## Not this azima",
  "",
  "The journal and the tree view: they read the same events but have their own azimas, their own budgets and their own measures.",
].join("\n");

async function shoot(page: Page, name: string, width: number) {
  await page.setViewportSize({ width, height: 1000 });
  for (const theme of ["dark", "light"] as const) {
    await page.evaluate((v) => localStorage.setItem("djinn.theme", v), theme);
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    await page.locator("#view-tab-tasks").click();
    const card = page.locator(".azima-card.open").first();
    await expect(card).toBeVisible();
    // Below the attention bar, which stays at the top.
    await card.evaluate((el) => {
      el.style.scrollMarginTop = "120px";
      el.scrollIntoView({ block: "start" });
    });
    await page.screenshot({
      path: path.join(shots, `${name}-${theme}.png`),
      style: ".app-statusbar > span:nth-child(2) { visibility: hidden }",
    });
  }
}

test("opened azimas in the Tasks tab", async ({ page }) => {
  test.setTimeout(180_000);
  const wishId = importBigWish("real");
  try {
    const tasks = JSON.parse(
      djinn("task", "list", "--wish-id", wishId, "--json"),
    ).tasks as {
      id: string;
      code: string;
      kind?: string;
      part_of?: string;
      azima?: { state?: string };
    }[];
    // The azimas the Tasks tab shows as cards, opened: of no other, their work not done.
    const open = tasks
      .filter(
        (x) =>
          x.kind === "TASK_KIND_AZIMA" &&
          !x.part_of &&
          x.azima?.state !== "AZIMA_STATE_DONE",
      )
      .sort((a, b) => a.code.localeCompare(b.code, "en", { numeric: true }));
    // Short descriptions, the last azima none.
    for (const x of open.slice(0, -1))
      djinn("task", "describe", x.id, "--text", short);
    // One waits for another.
    if (open.length > 2)
      djinn("task", "depend", open[1].id, "--after", open[0].id);

    await page.goto(process.env.DJINN_URL!);
    await page.evaluate((id) => {
      localStorage.setItem("djinn.wish", id);
      localStorage.setItem("djinn.terminal.collapsed", "1");
    }, wishId);
    await page.reload();
    await page.locator("#view-tab-tasks").click();
    // The first card the tab opens gets the long one.
    const first = await page
      .locator(".azima-card.open .azima-heading .agent-code")
      .first()
      .textContent();
    const shown = open.find((x) => x.code === first)!;
    djinn("task", "describe", shown.id, "--text", long);
    await shoot(page, "wide", 1440);
    await shoot(page, "narrow", 1000);
    await shoot(page, "phone", 420);
  } finally {
    removeBigWish(wishId);
  }
});
