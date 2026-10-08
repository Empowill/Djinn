// The terminal pinned at the bottom of the window, against the real djinn up --browser (see global-setup.ts): a
// POSIX shell on a pseudo-terminal of djinn, reached through the shim.
import { expect, test, type Page } from "@playwright/test";

function djinnURL(): string {
  const url = new URL(process.env.DJINN_URL!);
  url.pathname = "/";
  return url.toString();
}

// line finds a row of the terminal screen whose text is exactly text.
function line(page: Page, text: string) {
  return page
    .locator(".lead-terminal .xterm-rows > div")
    .filter({ hasText: new RegExp(`^\\s*${text}\\s*$`) });
}

async function typeLine(page: Page, text: string) {
  await page.locator(".lead-terminal .xterm").click();
  await page.keyboard.type(text);
  await page.keyboard.press("Enter");
}

test.describe.configure({ mode: "serial" });

test("the terminal shows at the bottom and runs a command", async ({
  page,
}) => {
  await page.goto(djinnURL());
  const terminal = page.getByRole("region", { name: "Terminal" });
  await expect(terminal).toBeVisible();
  // Pinned at the bottom of the window.
  const box = (await terminal.boundingBox())!;
  expect(box.y + box.height).toBeCloseTo(1000, -1);
  await expect(terminal.locator(".lead-terminal-command").first()).toHaveText(
    "/bin/sh",
  );
  // The quotes keep the echoed command line from matching.
  await typeLine(page, "echo hel''lo");
  await expect(line(page, "hello")).toHaveCount(1);
});

test("a held Space reaches the program as a run of spaces", async ({
  page,
}) => {
  await page.goto(djinnURL());
  await expect(page.locator(".lead-terminal .xterm")).toBeVisible();
  // In raw mode, as Claude Code reads its keys, count the spaces that arrive.
  await typeLine(
    page,
    "stty raw -echo; n=$(dd bs=1 count=20 2>/dev/null | od -v -An -tx1 | tr -s ' ' '\\n' | grep -c '^20$'); stty sane; echo spaces=$n",
  );
  await page.waitForTimeout(500);
  // Count the key downs the page sees as repeats, as a real key repeat sends them.
  await page.evaluate(() => {
    const w = window as unknown as { repeats: number };
    w.repeats = 0;
    document.addEventListener(
      "keydown",
      (e) => {
        if (e.key === " " && e.repeat) w.repeats++;
      },
      true,
    );
  });
  // Hold Space: the first press, then the key repeat of the system, one every 30 ms.
  const started = Date.now();
  for (let i = 0; i < 20; i++) {
    await page.keyboard.down(" ");
    await page.waitForTimeout(30);
  }
  await page.keyboard.up(" ");
  const held = Date.now() - started;
  await expect(line(page, "spaces=20")).toHaveCount(1);
  const repeats = await page.evaluate(
    () => (window as unknown as { repeats: number }).repeats,
  );
  expect(repeats).toBe(19);
  console.log(`held Space: 1 press and ${repeats} repeats in ${held} ms`);
});

test("a reopened window finds the terminal and its output", async ({
  page,
}) => {
  await page.goto(djinnURL());
  await typeLine(page, "echo still-''here");
  await expect(line(page, "still-here")).toHaveCount(1);
  await page.close({ runBeforeUnload: false });
  const again = await page.context().newPage();
  await again.goto(djinnURL());
  // The same shell, its output read again from the part djinn keeps.
  await expect(line(again, "still-here")).toHaveCount(1);
  await expect(line(again, "hello")).toHaveCount(1);
});

test("a key reaches the terminal in well under a key repeat", async ({
  page,
}) => {
  await page.goto(djinnURL());
  await expect(page.locator(".lead-terminal .xterm")).toBeVisible();
  // The page's own requests, one after the other as the terminal sends keys: Connect in JSON, same origin, the
  // session cookie.
  const mean = await page.evaluate(async () => {
    const call = async (method: string, body: object) => {
      const res = await fetch(`/terminal.v1.TerminalService/${method}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) throw new Error(`${method}: ${res.status}`);
      return res.json();
    };
    const { terminal } = await call("Open", {
      name: "main",
      cols: 80,
      rows: 24,
    });
    const n = 200;
    const start = performance.now();
    for (let i = 0; i < n; i++) await call("Write", { id: terminal.id });
    return (performance.now() - start) / n;
  });
  console.log(`write round trip over loopback HTTP: ${mean.toFixed(2)} ms`);
  expect(mean).toBeLessThan(15);
});
