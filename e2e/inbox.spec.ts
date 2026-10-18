// The inbox and wish templates, against the real djinn up --browser (see global-setup.ts): a project's skill declares
// a source and a wish template. Routing a request that matches the template proposes its wish. The source runs nothing
// until plugged in, from its project's panel: the inbox never lists the sources. Plugged in, it prints a merge request
// assigned to you, which becomes a card in the flying carpet, saying what it is, what Djinn will do and what each
// choice changes, proposing the template's wish; a click makes the wish, with its watcher, whose done line asks
// whether to grant the wish, and rubbing the lamp grants it. A second source fails: its card says why, and unplugs it.
// The sources and the watcher are fakes: shell scripts.
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

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

const skill = `---
name: babysit-fake-mr
description: Babysit a fake merge request, for the end-to-end tests.
metadata:
  djinn:
    wish:
      title: "Babysit !{mr}"
      match: '(?i)babysit.*!(?P<mr>\\d+)'
      watch: "sh fake-watch.sh {mr}"
      done_when: MERGED
    source:
      watch: "sh fake-source.sh"
      summary: "The fake merge requests assigned to you."
---

# Babysit a fake merge request
`;

const broken = `---
name: fake-broken
description: A source that fails, for the end-to-end tests.
metadata:
  djinn:
    source:
      watch: "sh fake-broken.sh"
---

# A source that fails
`;

test.skip(
  process.platform === "win32",
  "the fake claude, the fake source and the fake watcher are shell scripts",
);

test("a source runs once plugged in from its project's panel; its assigned merge request becomes a card that makes the template's wish with its watcher, whose done line offers to grant it; a failing source gets its own card", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way, and one stays active for the flying carpet.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);
  const here = JSON.parse(djinn("wish", "make", "Inbox from here", "--json"))
    .wish.id as string;

  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "bell-")),
  );
  const link = "https://gitlab.example.com/acme/bell/-/merge_requests/12";
  fs.mkdirSync(path.join(folder, ".agents", "skills", "babysit-fake-mr"), {
    recursive: true,
  });
  fs.writeFileSync(
    path.join(folder, ".agents", "skills", "babysit-fake-mr", "SKILL.md"),
    skill,
  );
  const runs = path.join(folder, "source-runs");
  fs.writeFileSync(
    path.join(folder, "fake-source.sh"),
    `echo run >> "${runs}"\necho "Babysit !12 · Fix the wick, assigned to you by alice"\necho "${link}"\n`,
  );
  fs.mkdirSync(path.join(folder, ".agents", "skills", "fake-broken"), {
    recursive: true,
  });
  fs.writeFileSync(
    path.join(folder, ".agents", "skills", "fake-broken", "SKILL.md"),
    broken,
  );
  fs.writeFileSync(
    path.join(folder, "fake-broken.sh"),
    'echo "fake-forge: not logged in" >&2\nexit 4\n',
  );
  fs.writeFileSync(
    path.join(folder, "fake-watch.sh"),
    'echo "!$1 · checks: pending"\nsleep 1\necho "MERGED: !$1 is merged."\n',
  );
  djinn("project", "add", folder, "--name", "bell");
  const skills = JSON.parse(
    djinn("skill", "list", "--project", "bell", "--json"),
  ).skills;
  const fakeSkill = skills.find(
    (k: { name: string }) => k.name === "babysit-fake-mr",
  );
  expect(fakeSkill.template).toBe("Babysit !{mr}");
  expect(fakeSkill.inbox_source).toBe("sh fake-source.sh");

  // Routing a request that matches the skill's template proposes the template's wish.
  const asked = JSON.parse(
    djinn(
      "wish",
      "route",
      "babysit the bell MR !7",
      "--wish-id",
      here,
      "--ask",
      "--json",
    ),
  );
  expect(asked.question.options[0]).toBe(
    "New wish “Babysit !7”, in bell, from the skill babysit-fake-mr",
  );

  let madeId: string | undefined;
  let taskId: string | undefined;
  try {
    // Declared, the source is not plugged in: it never runs, and the inbox does not list it; its project's panel does,
    // with "Plug in" and what that does.
    const sources = JSON.parse(djinn("inbox", "sources", "--json")).sources;
    expect(sources).toContainEqual(
      expect.objectContaining({
        name: "bell/babysit-fake-mr",
        watch: "sh fake-source.sh",
      }),
    );
    expect(
      sources.find((s: { name: string }) => s.name === "bell/babysit-fake-mr")
        .plugged,
    ).toBeFalsy();
    await page.goto(process.env.DJINN_URL!);
    await expect(page.locator(".hero h1")).toBeVisible();
    await expect(page.locator(".inbox .inbox-source-row")).toHaveCount(0);
    await page
      .locator(".project-list")
      .getByRole("button", { name: "bell", exact: true })
      .click();
    const panel = page.getByRole("dialog", { name: "bell" });
    const row = panel
      .locator(".inbox-source-row")
      .filter({ hasText: "babysit-fake-mr" });
    await expect(row).toContainText("The fake merge requests assigned to you.");
    await expect(row).toContainText("Unplugged: runs nothing");
    await expect(row).toContainText("each item it prints becomes a card");
    expect(fs.existsSync(runs)).toBe(false);
    expect(
      JSON.parse(djinn("inbox", "list", "--json")).items ?? [],
    ).toHaveLength(0);
    const wishes = JSON.parse(djinn("wish", "list", "--json")).wishes.length;

    // Plugged in, it runs: its item waits, and nothing is made. The card says its kind and the fact, what Djinn will
    // do, and under each choice what it changes.
    await row.getByRole("button", { name: "Plug in" }).click();
    await expect(row.getByRole("button", { name: "Unplug" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(panel).toBeHidden();
    const card = page
      .locator(".inbox-card")
      .filter({ hasText: "Babysit !12 · Fix the wick" });
    await expect(card.locator(".question-meta")).toContainText("Request");
    await expect(card.locator(".question-meta")).toContainText(
      "read by babysit-fake-mr on this machine",
    );
    await expect(card.locator(".inbox-will")).toContainText(
      "a wish follows it with the skill babysit-fake-mr: its watcher starts at once",
    );
    const first = card.locator(".option").first();
    await expect(first).toContainText(
      "New wish “Babysit !12”, in bell, from the skill babysit-fake-mr",
    );
    await expect(first.locator(".inbox-effect")).toContainText(
      "Its watcher starts at once",
    );
    await expect(card.locator(".option").last()).toContainText(
      "It will not come back here",
    );
    expect(JSON.parse(djinn("wish", "list", "--json")).wishes).toHaveLength(
      wishes,
    );
    await card.getByRole("button", { name: "Rub the lamp" }).click();

    // The wish, its template recorded, its watcher started in the project; the item routed, its card gone.
    await expect(page.locator(".hero h1")).toHaveText("Babysit !12");
    const made = JSON.parse(djinn("wish", "list", "--json")).wishes.find(
      (w: { title: string }) => w.title === "Babysit !12",
    );
    madeId = made.id;
    expect(made.template).toMatchObject({
      skill: "babysit-fake-mr",
      project_id: expect.any(String),
      watch: "sh fake-watch.sh 12",
      done_when: "MERGED",
    });
    const tasks = JSON.parse(
      djinn("task", "list", "--wish-id", made.id, "--json"),
    ).tasks;
    expect(tasks).toHaveLength(1);
    taskId = tasks[0].id;
    expect(tasks[0].provider).toBe("PROVIDER_WATCH");
    const items = JSON.parse(djinn("inbox", "list", "--all", "--json")).items;
    expect(items[0]).toMatchObject({
      state: "INBOX_STATE_ROUTED",
      wish_id: made.id,
    });
    expect(
      JSON.parse(djinn("inbox", "list", "--json")).items ?? [],
    ).toHaveLength(0);

    // The watcher's done line: a card asks whether to grant the wish; rubbing the lamp grants it.
    const grantCard = page
      .locator(".question-card")
      .filter({ hasText: "grant “Babysit !12”?" });
    await expect(grantCard).toContainText("MERGED: !12 is merged.", {
      timeout: 20_000,
    });
    expect(
      JSON.parse(djinn("wish", "list", "--json")).wishes.find(
        (w: { id: string }) => w.id === made.id,
      ).state,
    ).toBe("WISH_STATE_ACTIVE");
    await grantCard.getByRole("button", { name: "Rub the lamp" }).click();
    await expect
      .poll(
        () =>
          JSON.parse(djinn("wish", "list", "--json")).wishes.find(
            (w: { id: string }) => w.id === made.id,
          ).state,
      )
      .toBe("WISH_STATE_GRANTED");

    djinn("inbox", "unplug", "bell/babysit-fake-mr");
    expect(
      JSON.parse(djinn("inbox", "sources", "--json")).sources.find(
        (s: { name: string }) => s.name === "bell/babysit-fake-mr",
      ).plugged,
    ).toBeFalsy();

    // A source that fails: once plugged in, its card says why and since when; unplugging it from the card takes it
    // away.
    djinn("inbox", "plug", "bell/fake-broken");
    await page.locator(".sidebar .plan-nav").click();
    const failing = page
      .locator(".inbox-source-card")
      .filter({ hasText: "bell/fake-broken" });
    await expect(failing.locator(".question-meta")).toContainText(
      "Djinn's configuration",
    );
    await expect(failing.locator("h3")).toHaveText(
      "The source bell/fake-broken fails on this machine",
    );
    await expect(failing.locator(".inbox-error")).toContainText(
      "fake-forge: not logged in",
    );
    await expect(failing.locator(".inbox-fix")).toContainText("Recommended");
    await failing
      .getByRole("button", { name: /Unplug it on this machine/ })
      .click();
    await expect(failing).toHaveCount(0);
    expect(
      JSON.parse(djinn("inbox", "sources", "--json")).sources.find(
        (s: { name: string }) => s.name === "bell/fake-broken",
      ).plugged,
    ).toBeFalsy();
  } finally {
    for (const source of ["bell/babysit-fake-mr", "bell/fake-broken"]) {
      try {
        djinn("inbox", "unplug", source);
      } catch {}
    }
    if (taskId) {
      try {
        djinn("task", "stop", taskId);
      } catch {}
    }
    if (madeId) {
      try {
        djinn("wish", "delete", madeId);
      } catch {}
    }
    try {
      djinn("wish", "delete", here);
    } catch {}
  }
});
