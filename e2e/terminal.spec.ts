// The terminal pinned at the bottom of the window, against the real djinn up --browser (see global-setup.ts): a
// POSIX shell on a pseudo-terminal of djinn, reached through the shim.
import { expect, test, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

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

async function call(method: string, body: object) {
  const url = new URL(process.env.DJINN_URL!);
  const res = await fetch(
    new URL(`/terminal.v1.TerminalService/${method}`, url),
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${url.searchParams.get("token")}`,
      },
      body: JSON.stringify(body),
    },
  );
  expect(res.ok).toBe(true);
  return res.json();
}

test.describe.configure({ mode: "serial" });
test.skip(
  process.platform === "win32",
  "the terminal runs PowerShell on Windows; these tests drive a POSIX shell",
);

test.beforeAll(async () => {
  await call("Open", { name: "main" });
});

test.afterAll(async () => {
  const { terminals = [] } = await call("List", {});
  for (const t of terminals) {
    await call("Close", { id: t.id });
  }
});

test("the terminal shows at the bottom and runs a command", async ({
  page,
}) => {
  await page.goto(djinnURL());
  const terminal = page.getByRole("region", { name: "Terminal" });
  await expect(terminal).toBeVisible();
  // Pinned at the bottom of the window.
  const box = (await terminal.boundingBox())!;
  expect(box.y + box.height).toBeCloseTo(1000, -1);
  // $SHELL, which global-setup.ts sets; a login shell on macOS, as Terminal starts it (terminal.Shell).
  await expect(terminal.locator(".lead-terminal-command").first()).toHaveText(
    process.platform === "darwin" ? "/bin/sh -l" : "/bin/sh",
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
    "stty raw -echo; echo raw-ready; n=$(dd bs=1 count=20 2>/dev/null | od -v -An -tx1 | tr -s ' ' '\\n' | grep -c '^20$'); stty sane; echo spaces=$n",
  );
  // The program reads its keys raw from now on.
  await expect(line(page, "raw-ready")).toHaveCount(1);
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
  try {
    await again.goto(djinnURL());
    // The same shell, its output read again from the part djinn keeps.
    await expect(line(again, "still-here")).toHaveCount(1);
    await expect(line(again, "hello")).toHaveCount(1);
  } finally {
    await again.close();
  }
});

test(
  "a key reaches the terminal in well under a key repeat",
  { tag: "@budget" },
  async ({ page }) => {
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
  },
);

// agy (Antigravity) 1.3.2 opens with queries to the terminal (XTVERSION, DA1, DECRQM 2026 and 2027, OSC 11), then
// draws its logo, and goes on once the answers come. A fake agy prints the first frame the real one wrote in Djinn's
// terminal (agy-first-frame.bin), keeps the answers it reads back, then writes the line agy writes next. The minified
// xterm.js threw on DECRQM ("n is not defined", vite.config.ts): the terminal stayed blank and answered DA1 only.
test("agy's first frame shows, and its queries are answered", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const dir = fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "agy-"));
  const replies = path.join(dir, "replies");
  const agy = path.join(dir, "agy");
  // The answers are 54 bytes: DA1 (7), DECRQM 2026 and 2027 (11 each), OSC 11 with rgb:rrrr/gggg/bbbb (25). A key
  // ends it, and it leaves the screen as it found it.
  fs.writeFileSync(
    agy,
    `#!/bin/sh
stty raw -echo
cat '${path.join(__dirname, "agy-first-frame.bin")}'
head -c 54 > '${replies}'
printf '\\033[H\\033[33;1mAccessing workspace:\\033[m\\r\\n'
head -c 1 > /dev/null
printf '\\033[?2004l\\033[?25h\\033[?1049l'
stty sane
`,
    { mode: 0o755 },
  );
  await page.goto(djinnURL());
  await expect(page.locator(".lead-terminal .xterm")).toBeVisible();
  await typeLine(page, agy);
  await expect(line(page, "Accessing workspace:")).toHaveCount(1);
  // The rest of the first frame: the logo, in half blocks, and the lines under it.
  const rows = page.locator(".lead-terminal .xterm-rows");
  await expect(rows).toContainText("▀");
  await expect(
    line(page, "Welcome back! You are currently not signed in."),
  ).toHaveCount(1);
  expect(fs.readFileSync(replies, "latin1")).toMatch(
    // eslint-disable-next-line no-control-regex -- the answers are escape sequences
    /^\x1b\[\?1;2c\x1b\[\?2026;2\$y\x1b\[\?2027;0\$y\x1b\]11;rgb:[0-9a-f]{4}\/[0-9a-f]{4}\/[0-9a-f]{4}\x1b\\$/,
  );
  // A window opened again reads that frame again, and answers none of its queries: they would land on the shell's
  // command line, ahead of what is typed there.
  await page.keyboard.press("q");
  await typeLine(page, "echo agy-''ended");
  await expect(line(page, "agy-ended")).toHaveCount(1);
  await page.reload();
  // Read again past agy's queries.
  await expect(line(page, "agy-ended")).toHaveCount(1);
  await typeLine(page, "echo agy-''gone");
  await expect(line(page, "agy-gone")).toHaveCount(1);
  expect(errors).toEqual([]);
});

test("direct and dead-key accented input reaches the program without extra spaces", async ({
  page,
}) => {
  const dir = fs.mkdtempSync(path.join(process.env.DJINN_E2E_HOME!, "ime-"));
  const out = path.join(dir, "typed");
  await page.goto(djinnURL());
  await expect(page.locator(".lead-terminal .xterm")).toBeVisible();
  await typeLine(
    page,
    `IFS= read -r line; printf '%s' "$line" > '${out}'; echo ime-''done`,
  );
  await page.locator(".lead-terminal .xterm").click();
  // Replay the exact DOM event sequences WebKitGTK fires on a French layout:
  // - Space keys whose keypress inserts into the hidden textarea (and WebKit's non-breaking space rebalancing) unless
  //   prevented;
  // - IBus direct accented keys (\u00e9, \u00e0, \u00e7, \u00f9), which fire keydown(229),
  //   beforeinput(insertFromComposition), input and compositionend without compositionstart;
  // - IBus dead-key composition (^ then e -> \u00ea), which preedits "^", deletes it, ends composition and commits
  //   \u00ea in a second composition;
  // - gtk-im-context-simple dead-key composition (diaeresis then i -> \u00ef), which fires compositionend("") on the
  //   dead key and commits \u00ef without compositionstart.
  await page.evaluate(async () => {
    const ta = document.querySelector<HTMLTextAreaElement>(
      ".lead-terminal .xterm-helper-textarea",
    )!;
    ta.focus();
    const tick = () => new Promise((r) => setTimeout(r, 10));
    const ascii = async (ch: string) => {
      const code = ch.toUpperCase().charCodeAt(0);
      ta.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: ch,
          keyCode: code,
          which: code,
          bubbles: true,
          cancelable: true,
        }),
      );
      await tick();
    };
    const space = async () => {
      ta.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: " ",
          code: "Space",
          keyCode: 32,
          which: 32,
          bubbles: true,
          cancelable: true,
        }),
      );
      const kp = new KeyboardEvent("keypress", {
        key: " ",
        code: "Space",
        keyCode: 32,
        charCode: 32,
        which: 32,
        bubbles: true,
        cancelable: true,
      });
      if (ta.dispatchEvent(kp)) {
        ta.value = (ta.value + " ").replace(/ {2}$/u, "\u00a0 ");
        ta.dispatchEvent(
          new InputEvent("input", {
            data: " ",
            inputType: "insertText",
            bubbles: true,
            composed: true,
          }),
        );
      }
      await tick();
    };
    const directAccent = async (ch: string) => {
      ta.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Unidentified",
          keyCode: 229,
          which: 229,
          bubbles: true,
          cancelable: true,
        }),
      );
      ta.dispatchEvent(
        new InputEvent("beforeinput", {
          data: ch,
          inputType: "insertFromComposition",
          bubbles: true,
          cancelable: true,
          composed: true,
        }),
      );
      ta.value = ta.value.replace(/\u00a0 $/u, "  ") + ch;
      ta.dispatchEvent(
        new InputEvent("input", {
          data: ch,
          inputType: "insertFromComposition",
          bubbles: true,
          composed: true,
        }),
      );
      ta.dispatchEvent(
        new CompositionEvent("compositionend", {
          data: ch,
          bubbles: true,
        }),
      );
      await tick();
    };
    const ibusDeadKey = async (dead: string, ch: string) => {
      ta.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Unidentified",
          keyCode: 229,
          which: 229,
          bubbles: true,
          cancelable: true,
        }),
      );
      ta.dispatchEvent(
        new CompositionEvent("compositionstart", {
          data: "",
          bubbles: true,
          cancelable: true,
        }),
      );
      ta.dispatchEvent(
        new CompositionEvent("compositionupdate", {
          data: dead,
          bubbles: true,
        }),
      );
      ta.dispatchEvent(
        new InputEvent("beforeinput", {
          data: dead,
          inputType: "insertCompositionText",
          bubbles: true,
          composed: true,
        }),
      );
      ta.value += dead;
      ta.dispatchEvent(
        new InputEvent("input", {
          data: dead,
          inputType: "insertCompositionText",
          bubbles: true,
          composed: true,
        }),
      );
      await tick();
      ta.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Unidentified",
          keyCode: 229,
          which: 229,
          bubbles: true,
          cancelable: true,
        }),
      );
      ta.dispatchEvent(
        new InputEvent("beforeinput", {
          data: null,
          inputType: "deleteCompositionText",
          bubbles: true,
          composed: true,
        }),
      );
      ta.value = ta.value.slice(0, -dead.length);
      ta.dispatchEvent(
        new InputEvent("input", {
          data: null,
          inputType: "deleteCompositionText",
          bubbles: true,
          composed: true,
        }),
      );
      ta.dispatchEvent(
        new CompositionEvent("compositionend", {
          data: "",
          bubbles: true,
        }),
      );
      ta.dispatchEvent(
        new CompositionEvent("compositionstart", {
          data: "",
          bubbles: true,
          cancelable: true,
        }),
      );
      ta.dispatchEvent(
        new InputEvent("beforeinput", {
          data: ch,
          inputType: "insertFromComposition",
          bubbles: true,
          cancelable: true,
          composed: true,
        }),
      );
      ta.value += ch;
      ta.dispatchEvent(
        new InputEvent("input", {
          data: ch,
          inputType: "insertFromComposition",
          bubbles: true,
          composed: true,
        }),
      );
      ta.dispatchEvent(
        new CompositionEvent("compositionend", {
          data: ch,
          bubbles: true,
        }),
      );
      await tick();
    };
    const simpleDeadKey = async (ch: string) => {
      ta.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Unidentified",
          keyCode: 229,
          which: 229,
          bubbles: true,
          cancelable: true,
        }),
      );
      ta.dispatchEvent(
        new CompositionEvent("compositionend", {
          data: "",
          bubbles: true,
        }),
      );
      await tick();
      await directAccent(ch);
    };

    // Type two spaces then Backspace x2 first (leaves spaces in textarea without the fix), then:
    // "d\u00e9cision d\u00e9j\u00e0 \u00eate \u00e7a o\u00f9 \u00ef"
    await space();
    await space();
    for (let i = 0; i < 2; i++) {
      ta.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Backspace",
          keyCode: 8,
          which: 8,
          bubbles: true,
          cancelable: true,
        }),
      );
      await tick();
    }
    await ascii("d");
    await directAccent("\u00e9");
    for (const c of "cision") await ascii(c);
    await space();
    await ascii("d");
    await directAccent("\u00e9");
    await ascii("j");
    await directAccent("\u00e0");
    await space();
    await ibusDeadKey("^", "\u00ea");
    await ascii("t");
    await ascii("e");
    await space();
    await directAccent("\u00e7");
    await ascii("a");
    await space();
    await ascii("o");
    await directAccent("\u00f9");
    await space();
    await simpleDeadKey("\u00ef");
  });
  await page.keyboard.press("Enter");
  await expect(line(page, "ime-done")).toHaveCount(1);
  expect(fs.readFileSync(out, "utf8")).toBe(
    "d\u00e9cision d\u00e9j\u00e0 \u00eate \u00e7a o\u00f9 \u00ef",
  );
});
