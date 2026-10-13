import {
  create,
  toBinary,
  type DescMessage,
  type MessageInitShape,
} from "@bufbuild/protobuf";
import { expect, test, type Page } from "@playwright/test";
import {
  ProjectServiceListResponseSchema,
  Provider,
  WishSchema,
  WishServiceListResponseSchema,
  WishServiceMakeResponseSchema,
  WishServiceResumeResponseSchema,
  WishState,
} from "../gen/ts/plan/v1/plan_pb";
import {
  ProviderState,
  UiServiceGetEnvironmentResponseSchema,
  UiServiceShowResponseSchema,
} from "../gen/ts/ui/v1/ui_pb";
import { portable } from "./portable";
import { disableWishSmokeWebGL } from "./wish-smoke-fixture";

portable();

const madeId = "99999999-9999-4999-8999-999999999999";
const CLOCK_START = Date.parse("2026-01-01T00:00:00.000Z");

function protoResponse<Desc extends DescMessage>(
  schema: Desc,
  value: MessageInitShape<Desc>,
) {
  return {
    status: 200,
    contentType: "application/proto",
    body: Buffer.from(toBinary(schema, create(schema, value))),
  };
}

async function openCreation(
  page: Page,
  reducedMotion = false,
  smokeRenderer: "disabled" | "main" | "worker" = "disabled",
) {
  if (smokeRenderer === "disabled") await disableWishSmokeWebGL(page);
  else if (smokeRenderer === "main")
    await page.addInitScript(() => {
      Object.defineProperty(globalThis, "Worker", {
        configurable: true,
        value: undefined,
      });
    });
  await page.emulateMedia({
    reducedMotion: reducedMotion ? "reduce" : "no-preference",
  });
  await page.addInitScript(() => {
    localStorage.setItem("djinn.agents.offered", "1");
    localStorage.removeItem("djinn.provider");
  });
  await page.route("**/ui.v1.UiService/GetEnvironment", (route) =>
    route.fulfill(
      protoResponse(UiServiceGetEnvironmentResponseSchema, {
        providers: [
          { id: "claude", name: "Claude Code", state: ProviderState.READY },
        ],
      }),
    ),
  );
  await page.route("**/plan.v1.ProjectService/List", (route) =>
    route.fulfill(
      protoResponse(ProjectServiceListResponseSchema, { projects: [] }),
    ),
  );
  await page.route("**/plan.v1.WishService/List", (route) =>
    route.fulfill(
      protoResponse(WishServiceListResponseSchema, {
        wishes: Array.from({ length: 3 }, (_, index) => ({
          id: `33333333-3333-4333-8333-33333333333${index}`,
          title: `Active ${index + 1}`,
          state: WishState.ACTIVE,
          rank: index + 1,
        })),
      }),
    ),
  );
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();
  await page.locator(".sidebar .new-mission").click();
  const region = page.locator('[data-wish-entry="creation"]');
  await expect(region).toBeVisible();
  return { region, editor: region.locator(".wish-composer-input") };
}

function madeResponse() {
  return protoResponse(WishServiceMakeResponseSchema, {
    wish: create(WishSchema, {
      id: madeId,
      title: "Ready wish",
      description: "A wish ready for its first page.",
      state: WishState.PAUSED,
      rank: 1,
    }),
  });
}

function activeMadeResponse() {
  return protoResponse(WishServiceMakeResponseSchema, {
    wish: create(WishSchema, {
      id: madeId,
      title: "Active wish",
      description: "An active wish whose lead starts after the smoke sequence.",
      state: WishState.ACTIVE,
      provider: Provider.CLAUDE,
      rank: 1,
    }),
  });
}

async function pauseTestClock(page: Page) {
  await page.clock.install({ time: CLOCK_START });
  await page.clock.pauseAt(CLOCK_START + 1000);
}

async function settleTerminalHandoff(page: Page) {
  const creation = page.locator('[data-wish-entry="creation"]');
  const wish = page.locator('[data-wish-entry="wish"]');
  // The terminal dissolve callback uses bounded RAFs so the renderer can paint its endpoint before React removes the
  // smoke. Advance those frames until the DOM commit is observable rather than assuming one clock duration flushes it.
  for (let frame = 0; frame < 12; frame++) {
    if ((await creation.count()) === 0) break;
    await page.clock.runFor(16);
  }
  await expect(creation).toHaveCount(0);
  await expect(wish).toHaveAttribute("data-wish-transition", "revealing");
}

async function settleWishFocus(page: Page) {
  const wish = page.locator('[data-wish-entry="wish"]');
  // WishApp schedules focus after the handoff commit. Let that frame run only after the removal/reveal commit is visible.
  for (let frame = 0; frame < 8; frame++) {
    if (
      (await wish.count()) > 0 &&
      (await wish.evaluate((element) => document.activeElement === element))
    )
      return;
    await page.clock.runFor(16);
  }
  await expect(wish).toBeFocused();
}

async function startDissipation(page: Page) {
  const smoke = page.locator('[data-wish-entry="creation"] .wish-smoke');
  for (let frame = 0; frame < 8; frame++) {
    const progress = Number(
      await smoke.getAttribute("data-wish-smoke-dissolve"),
    );
    if (Number.isFinite(progress) && progress > 0) return;
    await page.clock.runFor(16);
  }
  await expect
    .poll(async () =>
      Number(await smoke.getAttribute("data-wish-smoke-dissolve")),
    )
    .toBeGreaterThan(0);
}

test("successful make exits content before smoke dissipation and reveal", async ({
  page,
}) => {
  const { region, editor } = await openCreation(page);
  await pauseTestClock(page);
  const form = region.locator("form");
  const smokeCanvas = region.locator(".wish-smoke-canvas");
  await smokeCanvas.evaluate((canvas) =>
    canvas.setAttribute("data-test-canvas-token", "creation-renderer"),
  );
  let release!: () => void;
  const responseReady = new Promise<void>((resolve) => {
    release = resolve;
  });
  let makeCalls = 0;
  await page.route("**/plan.v1.WishService/Make", async (route) => {
    makeCalls += 1;
    await responseReady;
    await route.fulfill(madeResponse());
  });

  await editor.fill("Keep the smoke until the new wish is ready.");
  const beforeMake = await page.locator(".main-shell").evaluate((shell) => {
    const creation = shell.querySelector<HTMLElement>(
      '[data-wish-entry="creation"]',
    );
    if (!creation) throw new Error("creation surface is missing");
    const rect = creation.getBoundingClientRect();
    return {
      left: rect.left,
      top: rect.top,
      width: rect.width,
      height: rect.height,
    };
  });
  await form.evaluate((element) => element.requestSubmit());
  await expect.poll(() => makeCalls).toBe(1);
  // A repeated form submission during the network and transition phases must remain a no-op.
  await form.evaluate((element) => element.requestSubmit());
  await expect.poll(() => makeCalls).toBe(1);
  await expect(region).toHaveAttribute("aria-busy", "true");
  await expect(region.locator(".wish-smoke")).toHaveCount(1);
  release();

  await expect(page.locator('[data-wish-transition="departing"]')).toHaveCount(
    1,
  );
  await expect(
    page.locator('[data-wish-transition="departing"] .wish-smoke-canvas'),
  ).toHaveAttribute("data-test-canvas-token", "creation-renderer");
  await expect(
    page.locator('[data-wish-transition="departing"] .wish-smoke'),
  ).toHaveAttribute("data-wish-smoke-state", "surface");
  await expect(
    page.locator(
      '[data-wish-transition="departing"] [data-wish-section="title"]',
    ),
  ).toHaveCSS("animation-name", "wish-exit-up-fade");
  await expect(
    page.locator('[data-wish-transition="departing"]'),
  ).not.toHaveCSS("position", "fixed");
  const departureLayout = await page
    .locator('[data-wish-transition="departing"]')
    .evaluate((creation) => {
      const shell = creation.parentElement;
      const staged = shell?.querySelector<HTMLElement>(
        '[data-wish-entry="wish"][data-wish-transition="preparing"]',
      );
      if (!shell || !staged) throw new Error("staged wish is missing");
      const readRect = (element: Element) => {
        const rect = element.getBoundingClientRect();
        return {
          left: rect.left,
          top: rect.top,
          width: rect.width,
          height: rect.height,
        };
      };
      return {
        creation: readRect(creation),
        shell: readRect(shell),
        staged: {
          ...readRect(staged),
          position: getComputedStyle(staged).position,
          pointerEvents: getComputedStyle(staged).pointerEvents,
          inert: staged.hasAttribute("inert"),
        },
      };
    });
  expect(departureLayout.creation).toEqual(beforeMake);
  expect(departureLayout.staged).toMatchObject({
    ...departureLayout.shell,
    position: "absolute",
    pointerEvents: "none",
    inert: true,
  });
  await page.clock.runFor(360);
  await expect(page.locator('[data-wish-transition="opening"]')).toHaveCount(1);
  await expect(
    page
      .locator('[data-wish-transition="opening"] [data-wish-section]')
      .first(),
  ).toHaveCSS("opacity", "0");
  await expect(
    page.locator('[data-wish-transition="opening"] .wish-smoke'),
  ).toHaveAttribute("data-wish-smoke-state", "expanded");
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveCSS(
    "opacity",
    "0",
  );
  await page.clock.runFor(730);
  await expect
    .poll(async () =>
      page.locator('[data-wish-transition="opening"]').evaluate((element) => {
        const rect = element.getBoundingClientRect();
        return {
          left: rect.left,
          top: rect.top,
          width: rect.width,
          height: rect.height,
          position: getComputedStyle(element).position,
        };
      }),
    )
    .toMatchObject({ left: 0, top: 0, position: "fixed" });
  const viewport = await page.evaluate(() => ({
    width: window.innerWidth,
    height: window.innerHeight,
  }));
  await expect
    .poll(async () =>
      page.locator('[data-wish-transition="opening"]').evaluate((element) => {
        const rect = element.getBoundingClientRect();
        return { width: rect.width, height: rect.height };
      }),
    )
    .toEqual({ width: viewport.width, height: viewport.height });
  await expect
    .poll(async () =>
      page
        .locator('[data-wish-transition="opening"] .wish-smoke')
        .evaluate((element) => {
          const rect = element.getBoundingClientRect();
          return {
            left: rect.left,
            top: rect.top,
            width: rect.width,
            height: rect.height,
          };
        }),
    )
    .toEqual({
      left: 0,
      top: 0,
      width: viewport.width,
      height: viewport.height,
    });
  // The pending wish is mounted beneath the smoke before the opening root is removed: there is no blank frame.
  await expect(page.locator('[data-wish-entry="wish"]')).toBeVisible();
  await page.clock.runFor(90);
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveAttribute(
    "data-wish-opening-phase",
    "dissipating",
  );
  await startDissipation(page);
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(1);
  await expect(
    page.locator('[data-wish-entry="creation"] .wish-smoke'),
  ).toHaveAttribute("data-wish-smoke-state", "dissipating");
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCSS(
    "opacity",
    "1",
  );
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCSS(
    "animation-name",
    "none",
  );
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveAttribute(
    "data-wish-transition",
    "preparing",
  );
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveCSS(
    "opacity",
    "0",
  );
  const canvasToken = await page
    .locator('[data-wish-entry="creation"] .wish-smoke-canvas')
    .getAttribute("data-test-canvas-token");
  expect(canvasToken).toBe("creation-renderer");
  await page.clock.runFor(400);
  await expect
    .poll(async () =>
      Number(
        await page
          .locator('[data-wish-entry="creation"] .wish-smoke')
          .getAttribute("data-wish-smoke-dissolve"),
      ),
    )
    .toBeGreaterThan(0.35);
  await expect
    .poll(async () =>
      Number(
        await page
          .locator('[data-wish-entry="creation"] .wish-smoke')
          .getAttribute("data-wish-smoke-dissolve"),
      ),
    )
    .toBeLessThan(0.75);
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(1);
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveAttribute(
    "data-wish-transition",
    "preparing",
  );
  await page.clock.runFor(400);
  await settleTerminalHandoff(page);
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveAttribute(
    "data-wish-transition",
    "revealing",
  );
  await expect(page.locator('[data-wish-entry="wish"]')).not.toHaveAttribute(
    "inert",
  );
  await expect(
    page.locator('[data-wish-entry="wish"] .wish-smoke'),
  ).toHaveCount(0);
  const revealContract = await page
    .locator('[data-wish-entry="wish"]')
    .evaluate((entry) => {
      const rootStyle = getComputedStyle(entry);
      const sections = Array.from(
        entry.querySelectorAll<HTMLElement>("[data-wish-reveal-section]"),
      );
      const timeMs = (value: string) => {
        const match = value.trim().match(/^([0-9.]+)(ms|s)$/);
        if (!match) return Number.NaN;
        return Number(match[1]) * (match[2] === "s" ? 1_000 : 1);
      };
      return {
        distance: rootStyle.getPropertyValue("--wish-motion-distance").trim(),
        duration: timeMs(rootStyle.getPropertyValue("--wish-motion-duration")),
        stagger: timeMs(rootStyle.getPropertyValue("--wish-motion-stagger")),
        sections: sections.map((section) => ({
          marker: section.getAttribute("data-wish-reveal-section"),
          duration: timeMs(getComputedStyle(section).animationDuration),
          delay: timeMs(getComputedStyle(section).animationDelay),
        })),
      };
    });
  expect(revealContract.distance).toBe("28px");
  expect(revealContract.duration).toBe(520);
  expect(revealContract.stagger).toBe(82);
  expect(revealContract.sections).toEqual([
    { marker: "head", duration: 520, delay: 0 },
    { marker: "content", duration: 520, delay: 82 },
    { marker: "summary", duration: 520, delay: 164 },
  ]);
  await settleWishFocus(page);
  await page.clock.runFor(900);
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveAttribute(
    "data-wish-transition",
    "ready",
  );
});

test("failed make leaves the draft usable and keeps creation mounted", async ({
  page,
}) => {
  const { region, editor } = await openCreation(page);
  const form = region.locator("form");
  let attempts = 0;
  await page.route("**/plan.v1.WishService/Make", (route) =>
    route.fulfill(
      attempts++ === 0
        ? {
            status: 503,
            contentType: "application/json",
            body: JSON.stringify({ code: "unavailable", message: "Try again" }),
          }
        : madeResponse(),
    ),
  );
  const draft = "Keep this draft after a failed make.";
  await editor.fill(draft);
  await form.evaluate((element) => element.requestSubmit());
  await expect(region.getByRole("alert")).toHaveText("Try again");
  await expect(editor).toBeEnabled();
  await expect(editor).toHaveValue(draft);
  await expect(editor).toBeFocused();
  await expect(region).toHaveAttribute("data-wish-transition", "entering");
  await expect(region.locator('[data-wish-smoke-state="surface"]')).toHaveCount(
    1,
  );
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveCount(0);

  // The same draft can be submitted again, and the successful transition takes the normal smoke path.
  await pauseTestClock(page);
  await form.evaluate((element) => element.requestSubmit());
  await expect(page.locator('[data-wish-transition="departing"]')).toHaveCount(
    1,
  );
  await page.clock.runFor(360);
  await expect(page.locator('[data-wish-transition="opening"]')).toHaveCount(1);
  await page.clock.runFor(820);
  await expect(
    page.locator('[data-wish-opening-phase="dissipating"]'),
  ).toHaveCount(1);
  await startDissipation(page);
  await page.clock.runFor(800);
  await settleTerminalHandoff(page);
  await expect(page.locator('[data-wish-entry="wish"]')).toBeVisible();
  await settleWishFocus(page);
});

test("a new draft cancels a late active-lead transition", async ({ page }) => {
  const { region, editor } = await openCreation(page);
  await pauseTestClock(page);
  let releaseResume!: () => void;
  const resumeReady = new Promise<void>((resolve) => {
    releaseResume = resolve;
  });
  let resumeStarted!: () => void;
  const resumeSeen = new Promise<void>((resolve) => {
    resumeStarted = resolve;
  });
  let showCalls = 0;
  await page.route("**/plan.v1.WishService/Make", (route) =>
    route.fulfill(activeMadeResponse()),
  );
  await page.route("**/plan.v1.WishService/Resume", async (route) => {
    resumeStarted();
    await resumeReady;
    await route.fulfill(
      protoResponse(WishServiceResumeResponseSchema, {
        terminal: "late-terminal",
      }),
    );
  });
  await page.route("**/ui.v1.UiService/Show", async (route) => {
    showCalls += 1;
    await route.fulfill(
      protoResponse(UiServiceShowResponseSchema, { window: false }),
    );
  });

  await editor.fill("Cancel the old lead before showing its terminal.");
  await region.locator("form").evaluate((element) => element.requestSubmit());
  await resumeSeen;
  await expect(page.locator('[data-wish-transition="departing"]')).toHaveCount(
    1,
  );
  await page.clock.runFor(360);
  await expect(page.locator('[data-wish-transition="opening"]')).toHaveCount(1);
  await page.clock.runFor(820);
  await expect(
    page.locator('[data-wish-opening-phase="dissipating"]'),
  ).toHaveCount(1);
  await startDissipation(page);
  await page.clock.runFor(800);
  await settleTerminalHandoff(page);

  await page.locator(".sidebar .new-mission").click();
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(1);
  const resumeResponse = page.waitForResponse((response) =>
    response.url().endsWith("/plan.v1.WishService/Resume"),
  );
  releaseResume();
  await (await resumeResponse).body();
  await page.clock.runFor(32);
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(1);
  await expect(
    page.locator('[data-wish-entry="creation"] .wish-composer-input'),
  ).toBeFocused();
  expect(showCalls).toBe(0);
});

test("reduced motion removes the stagger and still completes the transition", async ({
  page,
}) => {
  const { region, editor } = await openCreation(page, true);
  await expect(region.locator('[data-wish-section="title"]')).toHaveCSS(
    "animation-name",
    "none",
  );
  await expect(region.locator('[data-wish-section="options"]')).toHaveCSS(
    "animation-name",
    "none",
  );
  await expect(region.locator('[data-wish-section="editor"]')).toHaveCSS(
    "animation-name",
    "none",
  );
  await expect(region.locator('[data-wish-section="action"]')).toHaveCSS(
    "animation-name",
    "none",
  );
  await expect(region.locator('[data-wish-section="title"]')).toHaveCSS(
    "transform",
    "none",
  );
  await expect(region.locator('[data-wish-section="title"]')).toHaveCSS(
    "opacity",
    "1",
  );
  await page.route("**/plan.v1.WishService/Make", (route) =>
    route.fulfill(madeResponse()),
  );
  await editor.fill("Reveal this wish without a motion sequence.");
  await region
    .getByRole("button", { name: "Make it paused", exact: true })
    .click();
  await expect(page.locator('[data-wish-entry="wish"]')).toBeVisible();
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(0);
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveCSS(
    "transform",
    "none",
  );
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveCSS(
    "opacity",
    "1",
  );
});

test("reduced motion completes with the real main-thread smoke renderer", async ({
  page,
}) => {
  const { region, editor } = await openCreation(page, true, "main");
  const canvas = region.locator(".wish-smoke-canvas");
  const support = await page.evaluate(() => {
    try {
      const probe = document.createElement("canvas");
      const context = probe.getContext("webgl2");
      return {
        webgl2: Boolean(context),
        float: Boolean(context?.getExtension("EXT_color_buffer_float")),
      };
    } catch {
      return { webgl2: false, float: false };
    }
  });
  if (!support.webgl2 || !support.float) {
    test.skip(
      true,
      `Main-thread smoke renderer unsupported: webgl2=${support.webgl2}, float=${support.float}`,
    );
    return;
  }
  await expect
    .poll(
      async () => {
        const error = await canvas.getAttribute("data-error");
        if (error === "WebGL is unavailable.") return "unsupported";
        if (error) return `renderer-error:${error}`;
        return `${await canvas.getAttribute("data-ready")}/${await canvas.getAttribute("data-visible")}`;
      },
      { timeout: 20_000, intervals: [100, 250, 500, 1_000] },
    )
    .toMatch(/^(true\/true|unsupported)$/);
  const rendererError = await canvas.getAttribute("data-error");
  if (rendererError === "WebGL is unavailable.") {
    test.skip(
      true,
      `WebGL2 main-thread renderer unavailable: ${rendererError}`,
    );
    return;
  }
  if (rendererError)
    throw new Error(`Main-thread smoke renderer failed: ${rendererError}`);
  await expect(region.locator(".wish-smoke-worker-canvas")).toHaveCount(0);
  await expect(canvas).toHaveAttribute("data-ready", "true");
  await expect(canvas).toHaveAttribute("data-visible", "true");

  await page.route("**/plan.v1.WishService/Make", (route) =>
    route.fulfill(madeResponse()),
  );
  await editor.fill("Complete through the real reduced-motion renderer.");
  await region
    .getByRole("button", { name: "Make it paused", exact: true })
    .click();
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(0);
  await expect(page.locator('[data-wish-entry="wish"]')).toBeFocused();
});

test("reduced motion completes with the worker smoke renderer", async ({
  page,
}) => {
  const { region, editor } = await openCreation(page, true, "worker");
  const workerCanvas = region.locator(".wish-smoke-worker-canvas");
  const canvas = region.locator(".wish-smoke-canvas");
  const support = await page.evaluate(() => {
    const worker = typeof Worker !== "undefined";
    const offscreen = (() => {
      try {
        const probe = document.createElement("canvas");
        return Boolean(
          typeof probe.transferControlToOffscreen === "function" &&
          probe.transferControlToOffscreen(),
        );
      } catch {
        return false;
      }
    })();
    const webgl2 = (() => {
      try {
        const probe = document.createElement("canvas");
        const context = probe.getContext("webgl2");
        return Boolean(context?.getExtension("EXT_color_buffer_float"));
      } catch {
        return false;
      }
    })();
    return { worker, offscreen, webgl2 };
  });
  if (!support.worker || !support.offscreen || !support.webgl2) {
    test.skip(
      true,
      `Worker smoke renderer unsupported: worker=${support.worker}, offscreen=${support.offscreen}, webgl2=${support.webgl2}`,
    );
    return;
  }
  await expect(workerCanvas).toHaveCount(1, { timeout: 20_000 });
  await expect(workerCanvas).toHaveAttribute("data-ready", "true", {
    timeout: 20_000,
  });
  await expect(canvas).not.toHaveAttribute("data-error");
  await page.route("**/plan.v1.WishService/Make", (route) =>
    route.fulfill(madeResponse()),
  );
  await editor.fill("Complete through the reduced-motion worker renderer.");
  await region
    .getByRole("button", { name: "Make it paused", exact: true })
    .click();
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(0);
  await expect(page.locator('[data-wish-entry="wish"]')).toBeFocused();
});

test("an unmounted creation cannot reopen after its response arrives", async ({
  page,
}) => {
  const { region, editor } = await openCreation(page);
  let release!: () => void;
  const responseReady = new Promise<void>((resolve) => {
    release = resolve;
  });
  let requestSeen!: () => void;
  const requestStarted = new Promise<void>((resolve) => {
    requestSeen = resolve;
  });
  let requestFinished!: () => void;
  const requestDone = new Promise<void>((resolve) => {
    requestFinished = resolve;
  });
  await page.route("**/plan.v1.WishService/Make", async (route) => {
    requestSeen();
    await responseReady;
    try {
      await route.fulfill(madeResponse());
    } catch {
      // Navigation intentionally aborts this request; the important contract is that the old component is gone.
    } finally {
      requestFinished();
    }
  });
  await editor.fill("Unmount while the make request is pending.");
  await region.locator("form").evaluate((element) => element.requestSubmit());
  await requestStarted;
  await page.goto(process.env.DJINN_URL!);
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();
  release();
  await requestDone;
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(0);
  await expect(page.locator('[data-wish-transition="opening"]')).toHaveCount(0);
});
