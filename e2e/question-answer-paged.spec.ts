// Incident Q253: in a wish of more questions than the first page holds, a question answered in the window must turn
// answered and stay so. The window read its questions again after the answer, the first page and the open ones: the
// answered question, on neither, left the screens, and the card that stayed open made the developer answer again and
// again. The read now goes as far as the questions shown went, and the card takes the answer the server returned.
import { expect, test } from "./test";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import path from "node:path";
import { portable } from "./portable";

portable();

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

test("a question answered past the first page turns answered, and stays", async ({
  page,
}) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();

  // Three wishes at a time: the earlier specs' wishes make way.
  const listed = JSON.parse(djinn("wish", "list", "--json")).wishes ?? [];
  for (const w of listed.filter(
    (w: { state: string }) => w.state === "WISH_STATE_ACTIVE",
  ))
    djinn("wish", "pause", w.id);

  const title = `Paged ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;
  try {
    // Fifty questions decided, the first page of the window's read; two more open past it.
    for (let i = 1; i <= 50; i++) {
      const asked = JSON.parse(
        djinn("question", "ask", `Old question ${i}?`, wishId, "--json"),
      );
      djinn(
        "question",
        "answer",
        asked.question.code,
        "yes",
        "--wish-id",
        wishId,
      );
    }
    djinn("question", "ask", "Which name for the page?", wishId);
    djinn("question", "ask", "Which icon for the page?", wishId);

    await page.locator(".wish-nav").filter({ hasText: title }).click();
    const name = page
      .locator(".question-card")
      .filter({ hasText: "Which name for the page?" });
    const icon = page
      .locator(".question-card")
      .filter({ hasText: "Which icon for the page?" });
    await expect(name).toBeVisible();
    await expect(icon).toBeVisible();

    await name.getByLabel("Note with the answer").fill("The hourglass.");
    await name.getByRole("button", { name: "Rub the lamp" }).click();
    await expect(name.locator(".lamp-rub")).toHaveCount(0);
    // Another answer, another read of the questions: the first one stays decided.
    await icon.getByRole("button", { name: "Rub the lamp" }).click();
    await expect(icon.locator(".lamp-rub")).toHaveCount(0);

    await page.getByRole("tab", { name: /^Decisions/ }).click();
    for (const text of ["Which name for the page?", "Which icon for the page?"])
      await expect(
        page.locator(".decision-row").filter({ hasText: text }),
      ).toBeVisible();
    expect(errors).toEqual([]);
  } finally {
    djinn("wish", "delete", wishId);
  }
});
