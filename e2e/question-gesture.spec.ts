// A gesture on a question card shows: its button stays down a moment before the answer leaves, the card glows in the
// gesture's colour and a wisp of smoke rises from the button; with reduced motion, the glow is a plain colour flash
// and nothing moves. The states last a few hundred milliseconds: a MutationObserver in the page records them.
import { expect, test, type Page } from "./test";
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

type Seen = {
  gesture: string;
  pressed: number[];
  released?: number;
  glow: string;
  glowOpacity: string;
  wisp: string;
};

// watch records, from now on, the first frame of a gesture on the card of text: its glow and its wisp as computed,
// and when its button went down and came back up.
async function watch(page: Page, text: string) {
  await page.evaluate((text) => {
    const w = window as unknown as { seen?: Partial<Seen> };
    const seen: Partial<Seen> = { pressed: [] };
    w.seen = seen;
    const look = () => {
      const card = [...document.querySelectorAll(".question-card")].find((c) =>
        c.textContent?.includes(text),
      );
      if (!card) return;
      const gesture = card.classList.contains("gesture-rub")
        ? "rub"
        : card.classList.contains("gesture-enlighten")
          ? "enlighten"
          : "";
      if (gesture && !seen.gesture) {
        const after = getComputedStyle(card, "::after");
        seen.gesture = gesture;
        seen.glow = after.animationName;
        seen.glowOpacity = after.opacity;
        const wisp = card.querySelector(".question-wisp");
        seen.wisp = wisp ? getComputedStyle(wisp).display : "missing";
      }
      const down = !!card.querySelector(".is-pressed");
      if (down && !seen.pressed!.length) seen.pressed!.push(performance.now());
      if (!down && seen.pressed!.length && seen.released === undefined)
        seen.released = performance.now();
    };
    new MutationObserver(look).observe(document.body, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ["class"],
    });
  }, text);
}

const seen = (page: Page) =>
  page.evaluate(() => (window as unknown as { seen: Seen }).seen);

test("a gesture presses its button and the card answers back; reduced motion flashes still", async ({
  page,
}) => {
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

  const title = `Gesture ${randomUUID().slice(0, 8)}`;
  const wishId = JSON.parse(djinn("wish", "make", title, "--paused", "--json"))
    .wish.id as string;

  try {
    const item = page.locator(".wish-nav").filter({ hasText: title });
    await expect(item).toBeVisible();
    await item.click();

    djinn(
      "question",
      "ask",
      "Which oil?",
      wishId,
      "--options",
      "Olive",
      "--options",
      "Paraffin",
      "--recommendation",
      "**A**: it smells good.",
    );
    djinn("question", "ask", "Which wick?", wishId);
    const oil = page
      .locator(".question-card")
      .filter({ hasText: "Which oil?" });
    const wick = page
      .locator(".question-card")
      .filter({ hasText: "Which wick?" });
    await expect(oil).toBeVisible();
    await expect(wick).toBeVisible();

    // Ctrl+Enter rubs the lamp: the button is down a moment, the card glows in brass and a wisp rises.
    await watch(page, "Which oil?");
    await oil.focus();
    await page.keyboard.press("Control+Enter");
    await expect(oil).not.toBeVisible();
    const rub = await seen(page);
    expect(rub.gesture).toBe("rub");
    expect(rub.glow).toBe("question-glow");
    expect(rub.wisp).toBe("block");
    expect(rub.pressed).toHaveLength(1);
    expect(rub.released).toBeDefined();
    expect(rub.released! - rub.pressed[0]).toBeGreaterThanOrEqual(120);
    const [answered] = JSON.parse(
      djinn("question", "list", "--wish-id", wishId, "--json"),
    ).questions.filter((q: { text: string }) => q.text === "Which oil?");
    expect(answered.answer.choice).toBe("CHOICE_A");

    // Reduced motion, by a click on Enlighten me: a plain colour flash, no movement, no smoke.
    await page.emulateMedia({ reducedMotion: "reduce" });
    await watch(page, "Which wick?");
    await wick.getByRole("button", { name: /Enlighten me/ }).click();
    await expect(
      page
        .locator(".investigating-section .question-card")
        .filter({ hasText: "Which wick?" }),
    ).toBeVisible();
    const still = await seen(page);
    expect(still.gesture).toBe("enlighten");
    expect(still.glow).toBe("none");
    expect(still.glowOpacity).toBe("1");
    expect(still.wisp).toBe("none");
    expect(still.pressed).toHaveLength(1);

    expect(errors).toEqual([]);
  } finally {
    await page.emulateMedia({ reducedMotion: null });
    djinn("wish", "delete", wishId);
  }
});
