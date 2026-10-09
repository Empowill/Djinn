// Wish templates, against the real djinn up --browser (see global-setup.ts): a project's skill declares a template,
// a request that matches it becomes a card, rubbing the lamp makes the wish with its watcher, and the watcher's done
// line asks whether to grant the wish. The watcher is a fake: a shell script that prints, waits, then prints MERGED.
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
name: babysit-fake
description: Babysit a fake pull request, for the end-to-end tests.
metadata:
  djinn:
    wish:
      title: "Babysit PR #{pr}"
      match: '(?i)babysit.*#(?P<pr>\\d+)'
      watch: "sh fake-watch.sh {pr}"
      done_when: MERGED
---

# Babysit a fake pull request
`;

test.skip(
  process.platform === "win32",
  "the fake claude and the fake watcher are shell scripts",
);

test("a request that matches a skill's template makes its wish, whose watcher offers to grant it", async ({
  page,
}) => {
  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const folder = fs.realpathSync(
    fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "gong-")),
  );
  fs.mkdirSync(path.join(folder, ".agents", "skills", "babysit-fake"), {
    recursive: true,
  });
  fs.writeFileSync(
    path.join(folder, ".agents", "skills", "babysit-fake", "SKILL.md"),
    skill,
  );
  fs.writeFileSync(
    path.join(folder, "fake-watch.sh"),
    'echo "PR #$1 · checks: pending"\nsleep 2\necho "MERGED: PR #$1 is merged."\n',
  );
  djinn("project", "add", folder, "--name", "gong");
  const skills = JSON.parse(
    djinn("skill", "list", "--project", "gong", "--json"),
  ).skills;
  expect(skills[0].template).toBe("Babysit PR #{pr}");

  const here = JSON.parse(djinn("wish", "make", "Template from here", "--json"))
    .wish.id as string;
  const request = "babysit the gong PR #7";
  const asked = JSON.parse(
    djinn("wish", "route", request, "--wish-id", here, "--ask", "--json"),
  );
  const title = "Babysit PR #7";
  expect(asked.question.options[0]).toBe(
    `New wish “${title}”, in gong, from the skill babysit-fake`,
  );

  await page.goto(process.env.DJINN_URL!);
  await page
    .locator(".wish-nav")
    .filter({ hasText: "Template from here" })
    .click();
  await page
    .locator(".question-card")
    .filter({ hasText: "Where does this request go?" })
    .getByRole("button", { name: "Rub the lamp" })
    .click();

  // The new wish, its template recorded, its watcher started at once in the project.
  await expect(page.locator(".hero h1")).toHaveText(title);
  const made = JSON.parse(djinn("wish", "list", "--json")).wishes.find(
    (w: { title: string }) => w.title === title,
  );
  expect(made.template).toMatchObject({
    skill: "babysit-fake",
    project_id: expect.any(String),
    watch: "sh fake-watch.sh 7",
    done_when: "MERGED",
  });
  const tasks = JSON.parse(
    djinn("task", "list", "--wish-id", made.id, "--json"),
  ).tasks;
  expect(tasks).toHaveLength(1);
  expect(tasks[0].provider).toBe("PROVIDER_WATCH");

  // The done line: a card asks whether to grant the wish; rubbing the lamp grants it.
  const card = page
    .locator(".question-card")
    .filter({ hasText: `grant “${title}”?` });
  await expect(card).toContainText("MERGED: PR #7 is merged.", {
    timeout: 20_000,
  });
  expect(
    JSON.parse(djinn("wish", "list", "--json")).wishes.find(
      (w: { id: string }) => w.id === made.id,
    ).state,
  ).toBe("WISH_STATE_ACTIVE");
  await card.getByRole("button", { name: "Rub the lamp" }).click();
  await expect
    .poll(
      () =>
        JSON.parse(djinn("wish", "list", "--json")).wishes.find(
          (w: { id: string }) => w.id === made.id,
        ).state,
    )
    .toBe("WISH_STATE_GRANTED");

  djinn("wish", "pause", here);
});
