// The buttons at the top of a wish, against the real djinn up --browser (see global-setup.ts): Share writes the wish's
// export (binary protobuf), a file the command line imports again; Talk to the Djinn on the flying carpet's header
// resumes the one lead, the Djinn, in its terminal, on no wish (on a wish, lead-switch.spec.ts checks it). The lead is
// a fake claude on djinn's PATH that repeats what it reads. The chip beside the push strategy says where the wish's
// work lands, in a real Git repository, and changes its branch. The trash has its own spec (wish-lifecycle.spec.ts).
import { create, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";
import {
  LeadBatchState,
  LeadServiceGetResponseSchema,
  Provider,
} from "../gen/ts/plan/v1/plan_pb";
import { dropLead, leadFolder, leadRows, setLead } from "./lead";

const binary = path.resolve(
  __dirname,
  "../bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);
const session = "6c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e4f";

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

// open makes a wish set aside, and shows it in the window.
async function open(page: import("@playwright/test").Page, title: string) {
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;
  await page.goto(process.env.DJINN_URL!);
  await page.locator(".wish-nav").filter({ hasText: title }).click();
  await expect(page.locator(".hero h1")).toHaveText(title);
  return wishId;
}

test("Share writes the wish's export, which imports again", async ({
  page,
}) => {
  const title = `Share the lamp ${randomUUID().slice(0, 8)}`;
  const wishId = await open(page, title);
  djinn(
    "block",
    "put",
    wishId,
    "--kind",
    "note",
    "--title",
    "Oil",
    "--content",
    "Whale oil, no more.",
  );

  await page
    .locator(".topbar-actions")
    .getByRole("button", { name: "Share" })
    .click();
  const toast = page.locator(".toast");
  await expect(toast).toContainText("Wish exported to ");
  const file = (await toast.innerText())
    .replace(/^.*Wish exported to /s, "")
    .trim();
  expect(fs.statSync(file).size).toBeGreaterThan(0);

  // The file is the wish: gone, then imported again from it, with its title and its block.
  djinn("wish", "delete", wishId);
  djinn("wish", "import", file);
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes as {
    id: string;
    title: string;
  }[];
  expect(listed.find((w) => w.id === wishId)?.title).toBe(title);
  const blocks = JSON.parse(djinn("block", "list", wishId, "--json"))
    .blocks as { title: string }[];
  expect(blocks.map((b) => b.title)).toContain("Oil");
  djinn("wish", "delete", wishId);
});

test.describe("Lead", () => {
  test.skip(process.platform === "win32", "the fake claude is a shell script");
  test.afterEach(dropLead);

  test("Talk to the Djinn on the flying carpet opens the Djinn's terminal, on no wish", async ({
    page,
  }) => {
    // One wish active, so that the window opens on the flying carpet: the earlier specs' wishes make way.
    for (const w of JSON.parse(djinn("wish", "list", "--json")).wishes ?? [])
      if (w.state === "WISH_STATE_ACTIVE") djinn("wish", "pause", w.id);
    const folder = leadFolder();
    const title = `The Djinn from the carpet ${randomUUID().slice(0, 8)}`;
    const wishId = JSON.parse(djinn("wish", "make", title, "--json")).wish
      .id as string;
    setLead(session, folder);
    await page.goto(process.env.DJINN_URL!);
    await expect(page.locator(".hero h1")).toHaveText("Flying carpet");

    // Seen at once: in the header, beside the pill that writes to the Djinn.
    const talk = page
      .locator(".page-header .topbar-actions")
      .getByRole("button", { name: "Talk to the Djinn", exact: true });
    await expect(talk).toBeVisible();
    await expect(talk).toHaveAttribute("title", "Opens the Djinn's terminal");
    await expect(
      page.getByRole("button", { name: "Write to the Djinn" }),
    ).toBeVisible();
    await talk.click();
    const terminal = page.getByRole("region", { name: "Terminal" });
    await expect(
      terminal.getByRole("tab", { name: "The Djinn", exact: true }),
    ).toHaveAttribute("aria-selected", "true");
    await expect(
      terminal.locator(".lead-terminal-command").first(),
    ).toContainText(`claude --resume ${session}`);
    await expect(
      leadRows(page, `fake-claude --resume ${session} in ${folder}`),
    ).toHaveCount(1);
    // On no wish: nothing is typed to focus it.
    await expect(leadRows(page, "Focus on the wish")).toHaveCount(0);
    djinn("wish", "delete", wishId);
  });
});

function git(dir: string, ...args: string[]): string {
  return execFileSync(
    "git",
    ["-c", "user.name=e2e", "-c", "user.email=e2e@example.com", ...args],
    { cwd: dir, encoding: "utf8" },
  ).trim();
}

// contrast is the ratio between an element's text colour and the first ground behind it that is not transparent.
async function contrast(
  page: import("@playwright/test").Page,
  selector: string,
) {
  return page
    .locator(selector)
    .first()
    .evaluate((el) => {
      const rgb = (c: string) => (c.match(/[\d.]+/g) ?? []).map(Number);
      const lum = ([r, g, b]: number[]) =>
        [r, g, b]
          .map((v) => v / 255)
          .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
          .reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
      let ground: Element | null = el;
      let back = [0, 0, 0, 0];
      while (ground && (back.length < 3 || back[3] === 0)) {
        back = rgb(getComputedStyle(ground).backgroundColor);
        ground = ground.parentElement;
      }
      const [a, b] = [lum(rgb(getComputedStyle(el).color)), lum(back)].sort(
        (x, y) => y - x,
      );
      return (a + 0.05) / (b + 0.05);
    });
}

test("the head says where the wish's work lands, and a click changes the branch", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const suffix = randomUUID().slice(0, 8);
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "lands-")),
  );
  git(folder, "init", "--quiet", "-b", "main");
  fs.writeFileSync(path.join(folder, "README.md"), "A lamp.\n");
  git(folder, "add", ".");
  git(folder, "commit", "--quiet", "-m", "A lamp");
  git(folder, "branch", "release");
  const projectId = JSON.parse(
    djinn("project", "add", folder, "--name", `lands-${suffix}`, "--json"),
  ).project.id as string;
  const title = `Where it lands ${suffix}`;
  const wishId = JSON.parse(
    djinn(
      "wish",
      "make",
      title,
      "--project-ids",
      projectId,
      "--paused",
      "--json",
    ),
  ).wish.id as string;
  const landsIn = () =>
    (
      JSON.parse(djinn("wish", "list", "--json")).wishes as {
        id: string;
        integration_branches?: { branch: string }[];
      }[]
    ).find((w) => w.id === wishId)?.integration_branches?.[0]?.branch;
  try {
    // The show of the last minute is this wish's, not the Lead test's: the window gets that one again as it connects
    // (UiService.Watch), and may get it after the click below.
    djinn("open", `djinn://wish/${wishId}`);
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const chip = page.locator(".hero-meta button.wish-lands");
    await expect(chip).toHaveText("Lands in main");

    // Read in both themes, switched as the settings do, once the colours have changed: a reload within a minute of the
    // Lead test above would show its wish again (UiService.Watch), deleted since, and open the first wish instead.
    for (const theme of ["dark", "light"] as const) {
      await page.evaluate((v) => {
        localStorage.setItem("djinn.theme", v);
        document.documentElement.dataset.theme = v;
        return Promise.all(
          document
            .getAnimations()
            .filter((a) => a instanceof CSSTransition)
            .map((a) => a.finished.catch(() => {})),
        );
      }, theme);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      await expect(chip).toBeVisible();
      // Polled: the colours fade from one theme to the other.
      await expect
        .poll(() => contrast(page, ".hero-meta .wish-lands code"))
        .toBeGreaterThanOrEqual(4.5);
      await expect
        .poll(() => contrast(page, ".hero-meta .wish-lands > span"))
        .toBeGreaterThanOrEqual(4.5);
    }

    // The picker lists the project's branches; a new name is made from the current one.
    await chip.click();
    const picker = page.getByRole("dialog", {
      name: "Where the wish's work lands",
    });
    await expect(picker.locator(".branch-option")).toHaveCount(2);
    await expect(picker.getByRole("button", { name: "main" })).toHaveAttribute(
      "aria-current",
      "true",
    );
    await expect(picker.getByRole("button", { name: "release" })).toBeVisible();
    await picker.getByRole("textbox", { name: "Branch" }).fill("feat/lamp");
    await picker
      .getByRole("button", { name: "New branch feat/lamp from main" })
      .click();
    await expect(page.locator(".toast")).toContainText(
      "The wish's work now lands in feat/lamp.",
    );
    await expect(chip).toHaveText("Lands in feat/lamp");
    await expect(picker).toHaveCount(0);
    expect(landsIn()).toBe("feat/lamp");
    expect(git(folder, "rev-parse", "feat/lamp")).toBe(
      git(folder, "rev-parse", "main"),
    );

    // An existing branch, picked from the list.
    await chip.click();
    await picker.getByRole("button", { name: "release" }).click();
    await expect(chip).toHaveText("Lands in release");
    expect(landsIn()).toBe("release");
    expect(errors).toEqual([]);
  } finally {
    await page.evaluate(() => localStorage.removeItem("djinn.theme"));
    djinn("wish", "delete", wishId);
  }
});

test("the History tab shows the wish commands and the lead journal for that wish", async ({
  page,
}) => {
  const suffix = randomUUID().slice(0, 8);
  const title = `History of the lamp ${suffix}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;
  const exportFile = path.join(
    process.env.DJINN_E2E_HOME!,
    `history-${suffix}.json`,
  );
  try {
    djinn("wish", "describe", wishId, "--text", "Keep the wick trimmed.");
    djinn("wish", "export", wishId, "--file", exportFile);
    const exp = JSON.parse(fs.readFileSync(exportFile, "utf8"));
    const now = new Date().toISOString();
    exp.lead_batches = [
      {
        id: `batch-${suffix}`,
        state: "LEAD_BATCH_STATE_DONE",
        start_time: now,
        end_time: now,
        provider: "PROVIDER_CLAUDE",
        model: "claude-opus-4-7",
        wish_ids: [wishId],
        events: ["W1 merged"],
        commands: ["djinn task spawn W2"],
        note: "Spawned W2 to trim the wick.",
        usage: { cost_usd: 0.08 },
      },
    ];
    fs.writeFileSync(exportFile, JSON.stringify(exp));
    djinn("wish", "import", exportFile, "--replace");

    djinn("open", `djinn://wish/${wishId}`);
    await page.goto(process.env.DJINN_URL!);
    await page.locator(".wish-nav").filter({ hasText: title }).click();

    const historyTab = page.locator("#view-tab-history");
    await expect(historyTab).toContainText("History");
    await historyTab.click();

    const panel = page.getByRole("tabpanel", { name: "History" });
    await expect(panel.locator(".lead-batch-row")).toHaveCount(1);
    await expect(panel.locator(".lead-batch-row")).toContainText(
      "claude-opus-4-7",
    );
    await expect(panel.locator(".lead-batch-row")).toContainText(
      "djinn task spawn W2",
    );
    await expect(panel.locator(".lead-batch-row")).toContainText(
      "Spawned W2 to trim the wick.",
    );
    await expect(panel.locator(".lead-batch-row")).toContainText("$0.08");
    await expect(panel.locator(".journal-list tr.command")).toHaveCount(2);
    await expect(panel.locator(".journal-list")).toContainText("wish describe");
    await expect(panel.locator(".journal-list")).toContainText(
      "Keep the wick trimmed.",
    );
  } finally {
    fs.rmSync(exportFile, { force: true });
    djinn("wish", "delete", wishId);
  }
});

test("next to Lead, the driven lead shows the running batch, the journal and a failed batch's retry", async ({
  page,
}) => {
  const suffix = randomUUID().slice(0, 8);
  const title = `Driven lead ${suffix}`;
  const start = timestampFromDate(new Date(Date.now() - 30_000));
  await page.route("**/plan.v1.LeadService/Get", (route) =>
    route.fulfill({
      contentType: "application/proto",
      body: Buffer.from(
        toBinary(
          LeadServiceGetResponseSchema,
          create(LeadServiceGetResponseSchema, {
            driven: true,
            seat: { provider: Provider.CLAUDE, model: "claude-opus-4-7" },
            batch: {
              id: "b-run",
              state: LeadBatchState.RUNNING,
              startTime: start,
              provider: Provider.CLAUDE,
              model: "claude-opus-4-7",
              events: ["W1 merged", "Q01 answered"],
              attempt: 2,
              usage: { costUsd: 0.04 },
            },
            journal: [
              {
                id: "b-done",
                state: LeadBatchState.DONE,
                startTime: start,
                endTime: start,
                provider: Provider.CLAUDE,
                model: "claude-opus-4-7",
                sessionId: "a1b2c3d4-e5f6-4789-8012-34567890abcd",
                events: ["wish created"],
                commands: ["djinn task spawn W1"],
                note: "Spawned W1 for the lamp.",
                usage: { costUsd: 0.12 },
              },
              {
                id: "b-fail",
                state: LeadBatchState.FAILED,
                startTime: start,
                endTime: start,
                provider: Provider.CLAUDE,
                model: "claude-opus-4-7",
                attempt: 3,
                error: "exit status 1",
                usage: { costUsd: 0.02 },
              },
            ],
          }),
        ),
      ),
    }),
  );
  const wishId = await open(page, title);
  try {
    const actions = page.locator(".topbar-actions");
    const status = actions.locator(".lead-batch.running");
    await expect(status).toBeVisible();
    await expect(status.locator(".lead-batch-events")).toHaveText("2 events");
    await expect(status.locator(".lead-batch-retry")).toHaveText("try 2");
    await expect(status.locator(".lead-batch-cost")).toHaveText("$0.04");

    const journalBtn = actions.locator(".lead-journal-button");
    await expect(journalBtn).toHaveText("2");
    await journalBtn.click();

    const dialog = actions.getByRole("dialog", { name: "Lead journal" });
    await expect(dialog).toBeVisible();
    const rows = dialog.locator(".lead-batch-row");
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toContainText("claude-opus-4-7");
    await expect(rows.nth(0)).toContainText("a1b2c3d4");
    await expect(rows.nth(0)).toContainText("djinn task spawn W1");
    await expect(rows.nth(0)).toContainText("Spawned W1 for the lamp.");
    await expect(rows.nth(0)).toContainText("$0.12");
    await expect(rows.nth(1)).toHaveClass(/failed/);
    await expect(rows.nth(1)).toContainText("try 3");
    await expect(rows.nth(1)).toContainText("failed");
    await expect(rows.nth(1)).toContainText("exit status 1");
  } finally {
    djinn("wish", "delete", wishId);
  }
});
