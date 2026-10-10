// The inbox, against the real djinn up --browser (see global-setup.ts): a project's skill declares a source and a wish
// template. The source runs nothing until plugged in: the empty inbox lists it with "Plug in". Plugged in, it prints a
// merge request assigned to you, which becomes a card in the flight plan, proposing the template's wish; a click makes
// the wish, with its watcher. The source and the watcher are fakes: shell scripts.
import { expect, test } from "@playwright/test";
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
---

# Babysit a fake merge request
`;

test.skip(
  process.platform === "win32",
  "the fake claude, the fake source and the fake watcher are shell scripts",
);

test("a source runs once plugged in; its assigned merge request becomes a card that proposes the babysit template; a click makes the wish with its watcher", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way, and one stays active for the flight plan.
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
  fs.writeFileSync(
    path.join(folder, "fake-watch.sh"),
    'echo "!$1 · checks: pending"\nsleep 30\n',
  );
  djinn("project", "add", folder, "--name", "bell");
  const skills = JSON.parse(
    djinn("skill", "list", "--project", "bell", "--json"),
  ).skills;
  expect(skills[0].inbox_source).toBe("sh fake-source.sh");

  // Declared, the source is not plugged in: it never runs, and the empty inbox lists it with "Plug in".
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
  const row = page
    .locator(".inbox-empty .inbox-source-row")
    .filter({ hasText: "bell/babysit-fake-mr" });
  await expect(row).toContainText("Unplugged: runs nothing");
  expect(fs.existsSync(runs)).toBe(false);
  expect(JSON.parse(djinn("inbox", "list", "--json")).items ?? []).toHaveLength(
    0,
  );
  const wishes = JSON.parse(djinn("wish", "list", "--json")).wishes.length;

  // Plugged in, it runs: its item waits, and nothing is made.
  await row.getByRole("button", { name: "Plug in" }).click();
  const card = page
    .locator(".inbox-card")
    .filter({ hasText: "Babysit !12 · Fix the wick" });
  await expect(card).toContainText("From babysit-fake-mr");
  await expect(card.locator(".option").first()).toContainText(
    "New wish “Babysit !12”, in bell, from the skill babysit-fake-mr",
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
  expect(made.template).toMatchObject({
    skill: "babysit-fake-mr",
    watch: "sh fake-watch.sh 12",
  });
  const tasks = JSON.parse(
    djinn("task", "list", "--wish-id", made.id, "--json"),
  ).tasks;
  expect(tasks).toHaveLength(1);
  expect(tasks[0].provider).toBe("PROVIDER_WATCH");
  const items = JSON.parse(djinn("inbox", "list", "--all", "--json")).items;
  expect(items[0]).toMatchObject({
    state: "INBOX_STATE_ROUTED",
    wish_id: made.id,
  });
  expect(JSON.parse(djinn("inbox", "list", "--json")).items ?? []).toHaveLength(
    0,
  );

  djinn("inbox", "unplug", "bell/babysit-fake-mr");
  expect(
    JSON.parse(djinn("inbox", "sources", "--json")).sources.find(
      (s: { name: string }) => s.name === "bell/babysit-fake-mr",
    ).plugged,
  ).toBeFalsy();
  djinn("task", "stop", tasks[0].id);
  djinn("wish", "pause", made.id);
  djinn("wish", "pause", here);
});
