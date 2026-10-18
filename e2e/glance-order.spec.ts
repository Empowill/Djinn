// The glance in one order (W219): on a wish's page and on the questions page, the panel "What runs" lists every worker
// by state, whatever its kind: what is in progress on top, the one begun first above (work, the investigator Enlighten
// me starts, a lit ifrit, an urgent task with its label), then a worker stopped and kept (a circled pause, why on
// hover), then what waits, then what is planned; below them the watchers, each with what it said last, since when and
// the request it watches, a link out. The wish holds 60 finished tasks first: its waiting work sits past the first page
// of its tasks, and shows on arriving, without visiting the Tasks tab. A stopped worker continued goes back among the
// running. The workers are fakes (the fake provider, the fake claude, a shell script for the watcher): no model is
// called. Screenshots, dark and light, go to test-results/e2e/.
import { type Locator, type Page, expect, test } from "./test";
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

type Task = {
  id: string;
  code: string;
  status: string;
  role?: string;
  ifrit?: { state?: string };
  lastLine?: string;
  last_line?: string;
};

const get = (id: string) =>
  JSON.parse(djinn("task", "get", id, "--json")).task as Task;

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

// order is what a glance lists, in its order: each worker's code and the classes of its line.
const order = (glance: Locator) =>
  glance
    .locator(".glance-list:not(.glance-flow) .glance-row")
    .evaluateAll((rows) =>
      rows.map(
        (row) =>
          `${row.querySelector(".glance-code")?.textContent ?? ""} ${row.className.replace("glance-row ", "")}`,
      ),
    );

test("the glance lists every worker in one order by state, its waiting work past the first page included", async ({
  page,
}) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  const tag = randomUUID().slice(0, 8);
  const home = process.env.DJINN_E2E_HOME!;
  // A project in Git, with an identity, and a watch command its permissions allow: it says GREEN, then watches on.
  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(home, "glance-order-")),
  );
  git(folder, "init", "--quiet", "-b", "main");
  git(folder, "config", "user.name", "e2e");
  git(folder, "config", "user.email", "e2e@example.com");
  const watch = path.join(folder, "watch.sh");
  fs.writeFileSync(watch, "echo GREEN\nexec sleep 600\n");
  fs.mkdirSync(path.join(folder, ".agents"));
  fs.writeFileSync(
    path.join(folder, ".agents", "permissions.txtpb"),
    `commands: "sh ${watch}"\n`,
  );
  fs.writeFileSync(path.join(folder, "lamp.txt"), "a brass lamp\n");
  git(folder, "add", ".");
  git(folder, "commit", "--quiet", "-m", "A lamp");
  const name = `glance-order-${tag}`;
  const projectId = JSON.parse(
    djinn("project", "add", folder, "--name", name, "--json"),
  ).project.id as string;

  // The wish as another Djinn left it: 60 tasks done, an azima whose pull request is open, W61 waiting on it, W62
  // on W61, and W63 planned. Its ids come before any task spawned here: the waiting ones sit past the first page.
  const at = "2026-10-17T08:00:00Z";
  const wishId = randomUUID();
  const title = `Order the lamp ${tag}`;
  const idOf = (n: number) =>
    `01a10000-0000-7000-8000-${tag}${String(n).padStart(4, "0")}`;
  const azima = idOf(0);
  const done = Array.from({ length: 60 }, (_, i) => ({
    id: idOf(i + 1),
    wish_id: wishId,
    code: `W${i + 1}`,
    title: `Old work ${i + 1}`,
    status: "TASK_STATUS_DONE",
    create_time: at,
    end_time: at,
  }));
  const planned = (n: number, after: string[]) => ({
    id: idOf(n),
    wish_id: wishId,
    project_id: "p",
    code: `W${n}`,
    title: n === 63 ? "Polish the brass" : `Wick part ${n - 60}`,
    status: "TASK_STATUS_PENDING",
    depends_on: after,
    create_time: at,
  });
  const exported = {
    version: 1,
    create_time: at,
    projects: [{ id: "p", name }],
    wish: { id: wishId, title, create_time: at, project_ids: ["p"] },
    tasks: [
      {
        id: azima,
        wish_id: wishId,
        code: "T1",
        title: "The wick",
        kind: "TASK_KIND_AZIMA",
        status: "TASK_STATUS_PENDING",
        azima: {
          state: "AZIMA_STATE_IN_PROGRESS",
          pr: {
            url: "https://github.com/acme/lamp/pull/40",
            state: "AZIMA_PR_STATE_OPEN",
          },
        },
        create_time: at,
      },
      ...done,
      planned(61, [azima]),
      planned(62, [idOf(61)]),
      planned(63, []),
    ],
  };
  const file = path.join(folder, "..", `glance-order-${tag}.json`);
  fs.writeFileSync(file, JSON.stringify(exported));
  djinn("wish", "import", file);
  fs.rmSync(file);
  djinn("wish", "move", wishId, "--to", "1");

  const spawned: string[] = [];
  const spawn = (...args: string[]) => {
    const task = JSON.parse(
      djinn(
        "task",
        "spawn",
        wishId,
        "--project-id",
        projectId,
        ...args,
        "--json",
      ),
    ).task as Task;
    spawned.push(task.id);
    return task;
  };
  // Each worker is waited for at work before the next starts: they begin in this order. One that waits for a slot
  // (the machine counts the workers it runs), or that Djinn resumes once one frees, is started anyway (task start);
  // one Djinn paused to make room for the investigator is resumed by hand (task resume). Either way it is forced: no
  // later worker pauses it, however few slots the machine has (W343).
  const running = async (id: string, state = "TASK_STATUS_RUNNING") =>
    expect
      .poll(
        () => {
          const t = get(id) as Task & { wait_reason?: string };
          if (
            state === "TASK_STATUS_RUNNING" &&
            (t.status === "TASK_STATUS_PENDING" ||
              t.status === "TASK_STATUS_RESUMING")
          )
            try {
              djinn("task", "start", id);
            } catch {
              // It waits for something else: said below.
            }
          if (
            state === "TASK_STATUS_RUNNING" &&
            t.status === "TASK_STATUS_PAUSED"
          )
            try {
              djinn("task", "resume", id);
            } catch {
              // Resumed already: read again below.
            }
          return t.status === state
            ? state
            : `${t.status} ${t.wait_reason ?? ""}`;
        },
        { timeout: 30_000 },
      )
      .toBe(state);
  try {
    const work = spawn(
      "--title",
      "Trim the wick",
      "--provider",
      "fake",
      "--prompt",
      "text trimming\nwait",
    );
    await running(work.id);
    // The investigator Enlighten me starts: the fake claude, which stays at work.
    djinn(
      "question",
      "ask",
      "Which wick for the lamp?",
      wishId,
      "--options",
      "Cotton",
      "--options",
      "Linen",
    );
    djinn(
      "question",
      "enlighten",
      "Q01",
      "--wish-id",
      wishId,
      "--note",
      "Which lasts?",
    );
    let investigator = "";
    let investigatorId = "";
    await expect
      .poll(
        () => {
          const tasks: Task[] =
            JSON.parse(djinn("task", "list", "--wish-id", wishId, "--json"))
              .tasks ?? [];
          const found = tasks.find((t) => t.role === "TASK_ROLE_INVESTIGATOR");
          investigator = found?.code ?? "";
          investigatorId = found?.id ?? "";
          return investigator;
        },
        { timeout: 30_000 },
      )
      .not.toBe("");
    spawned.push(investigatorId);
    // On a machine of few slots, the investigator takes the work's slot and pauses it (preemptForSlot), or, the work
    // started by hand, waits for a slot: both are at work, the work keeping its first start, the first of the glance.
    await running(investigatorId);
    await running(work.id);
    const ifrit = spawn(
      "--kind",
      "ifrit",
      "--title",
      "Front of the lamp",
      "--provider",
      "fake",
      "--prompt",
      "text The front is up.",
    );
    await expect
      .poll(() => get(ifrit.id).ifrit?.state ?? "", { timeout: 30_000 })
      .toBe("IFRIT_STATE_LISTENING");
    const urgent = spawn(
      "--title",
      "Unblock the lamp",
      "--urgent",
      "--provider",
      "fake",
      "--prompt",
      "text unblocking\nwait",
    );
    await running(urgent.id);
    // A worker stopped on request: kept, paused.
    const stopped = spawn(
      "--title",
      "Fill the oil",
      "--provider",
      "fake",
      "--prompt",
      "text filling\nwait",
    );
    await running(stopped.id);
    djinn("task", "stop", stopped.id);
    await running(stopped.id, "TASK_STATUS_STOPPED");
    // The watcher of the azima's pull request: it says GREEN, then watches on.
    const watcher = spawn(
      "--title",
      "Watch PR #40",
      "--provider",
      "watch",
      "--part-of",
      "T1",
      "--prompt",
      `sh ${watch}`,
    );
    await expect
      .poll(() => {
        const t = get(watcher.id);
        return t.lastLine ?? t.last_line ?? "";
      })
      .toBe("GREEN");

    // Straight onto the wish's page: never through the Tasks tab.
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.goto(process.env.DJINN_URL!);
    await theme(page, "dark");
    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const glance = page.locator(".wish-view .glance-wide");
    const expected = [
      `${work.code} run`,
      `${investigator} investigate`,
      `${ifrit.code} run`,
      `${urgent.code} run`,
      `${stopped.code} paused`,
      "W61 wait",
      "W62 wait child",
      "W63 plan",
      `${watcher.code} watch watch`,
    ];
    await expect
      .poll(() => order(glance), { timeout: 15_000 })
      .toEqual(expected);
    const row = (code: string) =>
      glance.locator(".glance-row").filter({
        has: page.locator(".glance-code", { hasText: new RegExp(`^${code}$`) }),
      });
    // The urgent task's label, as in the tasks view; the ifrit's flame; the paused worker's pause and why.
    await expect(row(urgent.code).locator(".urgent-label")).toHaveText(
      "Urgent",
    );
    await expect(row(ifrit.code).locator(".glance-flame")).toBeVisible();
    await expect(row(stopped.code).locator(".glance-pause")).toBeVisible();
    await expect(row(stopped.code)).toHaveAttribute(
      "title",
      new RegExp(`^Stopped · ${stopped.code} Fill the oil\\n.+`),
    );
    await expect(row("W61")).toContainText("waits on T1");
    // The watcher: GREEN, since when, and its request, a link out.
    const watching = glance.locator(".glance-watcher");
    await expect(watching.locator(".glance-watch-line")).toHaveText("GREEN");
    await expect(watching.locator(".glance-since")).toHaveText(/^\d+[sm]/);
    await expect(watching.locator("a.glance-link")).toHaveAttribute(
      "href",
      "https://github.com/acme/lamp/pull/40",
    );
    await expect(watching.locator("a.glance-link")).toHaveText("PR #40");
    await glance.evaluate((el) => el.scrollIntoView({ block: "start" }));
    await page.screenshot({
      path: path.join(shots, "glance-order-wish-dark.png"),
    });
    await theme(page, "light");
    await page.screenshot({
      path: path.join(shots, "glance-order-wish-light.png"),
    });
    await theme(page, "dark");
    // A click on the paused worker goes to its task.
    await row(stopped.code).click();
    await expect(page.locator(`[id="task-${stopped.id}"]`)).toBeVisible();

    // The questions page: the same order under the wish's title, its counts, each worker once.
    await page
      .locator(".plan-nav")
      .filter({ hasText: "Flying carpet" })
      .click();
    const carpet = page
      .locator(".flight-plan .glance-wide .glance-wish")
      .filter({ has: page.locator(".glance-head", { hasText: title }) });
    await expect.poll(() => order(carpet)).toEqual(expected);
    await expect(carpet.locator(".glance-count")).toHaveText(
      "1 ifrit lit · 2 running · 1 investigating · 1 paused · 3 to come · 1 watching",
    );
    // The side panel counts the paused worker too, on its second line and on the folded rail.
    const nav = page.locator(".wish-nav").filter({ hasText: title });
    await expect(nav.locator(".wish-counts .wish-sign.tone-paused")).toHaveText(
      "1",
    );
    await carpet.scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(shots, "glance-order-carpet-dark.png"),
    });
    await theme(page, "light");
    await page.screenshot({
      path: path.join(shots, "glance-order-carpet-light.png"),
    });
    await theme(page, "dark");

    // Continued, the stopped worker is at work again: among the running, by when it first began.
    djinn("task", "continue", stopped.id, "--prompt", "text filling on\nwait");
    await running(stopped.id);
    await expect
      .poll(() => order(carpet))
      .toEqual([
        `${work.code} run`,
        `${investigator} investigate`,
        `${ifrit.code} run`,
        `${urgent.code} run`,
        `${stopped.code} run`,
        "W61 wait",
        "W62 wait child",
        "W63 plan",
        `${watcher.code} watch watch`,
      ]);
    await expect(
      nav.locator(".wish-counts .wish-sign.tone-paused"),
    ).toHaveCount(0);
    expect(errors).toEqual([]);
  } finally {
    for (const id of spawned.reverse())
      try {
        djinn("task", "cancel", id);
      } catch {
        // Ended already.
      }
    djinn("wish", "pause", wishId);
  }
});
