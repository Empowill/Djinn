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

test("successful make fades the form in place before revealing the created wish", async ({
  page,
}) => {
  const { region, editor } = await openCreation(page);
  await pauseTestClock(page);
  const form = region.locator("form");
  const smoke = region.locator(".wish-smoke");
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

  await editor.fill("Keep the form stable while the new wish is ready.");
  const beforeMake = await region.boundingBox();
  expect(beforeMake).toBeTruthy();
  await form.evaluate((element) => element.requestSubmit());
  await expect.poll(() => makeCalls).toBe(1);
  await form.evaluate((element) => element.requestSubmit());
  await expect.poll(() => makeCalls).toBe(1);
  await expect(region).toHaveAttribute("aria-busy", "true");
  release();

  await expect(region).toHaveAttribute("data-wish-transition", "exiting");
  await expect(region).toHaveCSS("animation-name", "wish-creation-exit-fade");
  await expect(smoke).toHaveAttribute("data-wish-smoke-state", "surface");
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveCount(1);
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveAttribute(
    "data-wish-transition",
    "preparing",
  );
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveAttribute(
    "inert",
    "",
  );
  const duringExit = await region.boundingBox();
  expect(duringExit).toEqual(beforeMake);
  await expect(region).not.toHaveCSS("position", "fixed");

  await page.clock.runFor(199);
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(1);
  await page.clock.runFor(2);
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(0);
  const wish = page.locator('[data-wish-entry="wish"]');
  await expect(wish).toHaveAttribute("data-wish-transition", "revealing");
  await expect(wish).not.toHaveAttribute("inert");
  await expect(wish.locator(".wish-smoke")).toHaveCount(0);
  await expect(wish.locator(".workspace-summary-head > h1")).toHaveCSS(
    "animation-name",
    "wish-entry-up-fade",
  );
  await expect(wish.locator(".workspace-content-stage")).toHaveCSS(
    "animation-name",
    "wish-entry-up-fade",
  );
  await expect(wish.locator(".workspace-summary-slot")).toHaveCSS(
    "animation-delay",
    "0.246s",
  );
  await page.clock.runFor(900);
  await expect(wish).toHaveAttribute("data-wish-transition", "ready");
  await settleWishFocus(page);
});

test("failed make leaves the draft usable before a later simple handoff", async ({
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

  // The same draft can be submitted again, and the successful transition is the short form fade.
  await pauseTestClock(page);
  await form.evaluate((element) => element.requestSubmit());
  await expect(page.locator('[data-wish-transition="exiting"]')).toHaveCount(1);
  await expect(
    page.locator('[data-wish-transition="exiting"] .wish-smoke'),
  ).toHaveAttribute("data-wish-smoke-state", "surface");
  await page.clock.runFor(200);
  await expect(page.locator('[data-wish-entry="wish"]')).toBeVisible();
  await settleWishFocus(page);
});

test("an active make reveals the wish before its late lead response completes", async ({
  page,
}) => {
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

  await editor.fill("Reveal the wish before showing its terminal.");
  await region.locator("form").evaluate((element) => element.requestSubmit());
  await resumeSeen;
  await expect(page.locator('[data-wish-transition="exiting"]')).toHaveCount(1);
  await page.clock.runFor(200);
  await expect(page.locator('[data-wish-entry="creation"]')).toHaveCount(0);
  await expect(page.locator('[data-wish-entry="wish"]')).toBeVisible();

  const resumeResponse = page.waitForResponse((response) =>
    response.url().endsWith("/plan.v1.WishService/Resume"),
  );
  releaseResume();
  await resumeResponse;
  await expect.poll(() => showCalls).toBe(1);
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveAttribute(
    "data-wish-transition",
    "revealing",
  );
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
  await expect(page.locator('[data-wish-entry="wish"]')).toHaveCount(0);
});
