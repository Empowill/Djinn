// Creation UI owns this spec. Requests are intercepted so these checks never launch a model, write a wish,
// or depend on automatic title generation. The page itself is served by the isolated Djinn test server.
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { expect, test, type Locator, type Page } from "@playwright/test";
import {
  Allowance,
  ProjectServiceListResponseSchema,
  WishServiceListResponseSchema,
  WishServiceMakeRequestSchema,
  WishState,
} from "../gen/ts/plan/v1/plan_pb";
import {
  ProviderState,
  UiServiceGetEnvironmentResponseSchema,
} from "../gen/ts/ui/v1/ui_pb";

const alpha = "11111111-1111-4111-8111-111111111111";
const beta = "22222222-2222-4222-8222-222222222222";
const titles = {
  en: "What would you like to make happen?",
  fr: "Que voulez-vous exaucer ?",
} as const;

async function openProjects(region: Locator) {
  const dropdown = region.locator(".wish-tag-dropdown");
  if (!(await dropdown.locator(".wish-tag-menu").isVisible())) {
    await dropdown.getByRole("button").click();
  }
}

async function chooseDropdown(region: Locator, label: string, option: string) {
  await region.getByRole("button", { name: label, exact: true }).click();
  await region.getByRole("option", { name: option, exact: true }).click();
}

async function openComposer(
  page: Page,
  active = 0,
  projects = true,
  locale = "en",
) {
  if (locale === "fr") {
    await page.emulateMedia({ reducedMotion: "reduce" });
  }
  await page.addInitScript((locale) => {
    localStorage.setItem("djinn.agents.offered", "1");
    localStorage.setItem("djinn.language", locale);
    localStorage.removeItem("djinn.provider");
  }, locale);
  await page.route("**/ui.v1.UiService/GetEnvironment", (route) =>
    route.fulfill({
      contentType: "application/proto",
      body: Buffer.from(
        toBinary(
          UiServiceGetEnvironmentResponseSchema,
          create(UiServiceGetEnvironmentResponseSchema, {
            providers: [
              { id: "claude", name: "Claude Code", state: ProviderState.READY },
              { id: "codex", name: "Codex", state: ProviderState.SIGNED_OUT },
            ],
          }),
        ),
      ),
    }),
  );
  await page.route("**/plan.v1.ProjectService/List", (route) =>
    route.fulfill({
      contentType: "application/proto",
      body: Buffer.from(
        toBinary(
          ProjectServiceListResponseSchema,
          create(ProjectServiceListResponseSchema, {
            projects: projects
              ? [
                  { id: alpha, name: "Lamp", directory: "/workspace/lamp" },
                  { id: beta, name: "Smoke", directory: "/workspace/smoke" },
                ]
              : [],
          }),
        ),
      ),
    }),
  );
  await page.route("**/plan.v1.WishService/List", (route) =>
    route.fulfill({
      contentType: "application/proto",
      body: Buffer.from(
        toBinary(
          WishServiceListResponseSchema,
          create(WishServiceListResponseSchema, {
            wishes: Array.from({ length: active }, (_, index) => ({
              id: `33333333-3333-4333-8333-33333333333${index}`,
              title: `Active ${index + 1}`,
              state: WishState.ACTIVE,
              rank: index + 1,
            })),
          }),
        ),
      ),
    }),
  );
  await page.goto(process.env.DJINN_URL!);
  const liveLabel = locale === "fr" ? "En direct" : "Live";
  await expect(
    page.locator(".app-statusbar").getByText(liveLabel),
  ).toBeVisible();
  await page.locator(".sidebar .new-mission").click();
  const region = page.getByRole("region", {
    name: titles[locale as keyof typeof titles],
    exact: true,
  });
  await expect(region).toBeVisible();
  const editor = region.getByRole("textbox", {
    name: titles[locale as keyof typeof titles],
  });
  await expect(editor).toBeFocused();
  return { region, editor };
}

test("focus, animated prompt, smoke and minimal tags preserve the request payload", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page);
  await expect(region.locator(".wish-composer-placeholder")).toContainText(
    /.+/,
  );
  await expect(region.locator(".wish-composer-tools")).toHaveCount(0);
  await expect(region.locator(".wish-smoke-canvas")).toHaveAttribute(
    "data-zoom",
    "10",
  );

  const requests: ReturnType<
    typeof fromBinary<typeof WishServiceMakeRequestSchema>
  >[] = [];
  await page.route("**/plan.v1.WishService/Make", async (route) => {
    requests.push(
      fromBinary(
        WishServiceMakeRequestSchema,
        route.request().postDataBuffer()!,
      ),
    );
    await route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ code: "unavailable", message: "Try again" }),
    });
  });
  await editor.fill("Build a thoughtful export.\nKeep every detail.");
  await openProjects(region);
  await region.getByRole("checkbox", { name: "Lamp", exact: true }).check();
  await expect(
    region.getByRole("checkbox", { name: "Lamp", exact: true }),
  ).toHaveAccessibleDescription("/workspace/lamp");
  await chooseDropdown(
    region,
    "Permissions in the selected projects",
    "Edit the files",
  );
  await region
    .getByRole("button", { name: "Make the wish", exact: true })
    .click();
  await expect(region.getByRole("alert")).toHaveText("Try again");
  expect(requests).toHaveLength(1);
  expect(requests[0].title).toBe("");
  expect(requests[0].prompt).toBe(
    "Build a thoughtful export.\nKeep every detail.",
  );
  expect(requests[0].projectIds).toEqual([alpha]);
  expect(requests[0].allowance).toBe(Allowance.EDIT);
  await expect(editor).toHaveValue(
    "Build a thoughtful export.\nKeep every detail.",
  );
});

test("French empty creation keeps the title, caret and smoke visible", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const { region, editor } = await openComposer(page, 0, true, "fr");
  const heading = region.getByRole("heading", {
    name: titles.fr,
    exact: true,
  });
  await expect(heading).toBeVisible();
  await expect(heading).toHaveCSS("white-space", "nowrap");
  await expect(editor).toBeFocused();
  await expect(editor).toHaveAttribute("placeholder", "");
  await expect(region.locator(".wish-composer-placeholder")).toBeVisible();
  await expect(region.locator(".wish-composer-caret")).toBeVisible();
  await expect(editor).toHaveCSS("font-size", "16px");
  await expect(editor).toHaveCSS("line-height", "26.4px");
  await expect(
    region.locator(".wish-creation-footer .button.secondary"),
  ).toHaveCount(0);
  const headingBox = await heading.boundingBox();
  const controlsBox = await region
    .locator(".wish-creation-controls")
    .boundingBox();
  const editorBox = await editor.boundingBox();
  const requestBox = await region
    .locator(".wish-creation-request")
    .boundingBox();
  const regionBox = await region.boundingBox();
  const submitBox = await region
    .getByRole("button", { name: "Faire le souhait", exact: true })
    .boundingBox();
  const footerBox = await region.locator(".wish-creation-footer").boundingBox();
  expect(headingBox).not.toBeNull();
  expect(controlsBox).not.toBeNull();
  expect(editorBox).not.toBeNull();
  expect(requestBox).not.toBeNull();
  expect(regionBox).not.toBeNull();
  expect(submitBox).not.toBeNull();
  expect(footerBox).not.toBeNull();
  expect(
    Math.round(controlsBox!.y - (headingBox!.y + headingBox!.height)),
  ).toBe(20);
  expect(
    Math.round(editorBox!.y - (controlsBox!.y + controlsBox!.height)),
  ).toBe(20);
  expect(requestBox!.width / regionBox!.width).toBeGreaterThan(0.38);
  expect(requestBox!.width / regionBox!.width).toBeLessThan(0.42);
  expect(submitBox!.x).toBeLessThan(footerBox!.x + footerBox!.width / 3);
  const smoke = region.locator(".wish-smoke-canvas");
  await expect(smoke).toHaveAttribute("data-zoom", "10");
  await expect(smoke).toHaveAttribute("data-ready", "true", {
    timeout: 60_000,
  });
  await expect(smoke).toHaveAttribute("data-visible", "true", {
    timeout: 30_000,
  });
  const asciiLayers = region.locator(".wish-smoke-ascii pre");
  await expect(asciiLayers).toHaveCount(8);
  const hasAscii = async () =>
    (await asciiLayers.allTextContents()).some((text) => /\S/.test(text));
  await expect.poll(hasAscii).toBe(true);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.waitForTimeout(250);
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(asciiLayers).toHaveCount(8);
  await expect.poll(hasAscii, { timeout: 10_000 }).toBe(true);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await expect.poll(hasAscii, { timeout: 10_000 }).toBe(true);
  await page.screenshot({ path: "test-results/e2e/wish-creation-fr.png" });
});

test("native textarea keeps pasted markup literal", async ({ page }) => {
  const { region, editor } = await openComposer(page);
  await editor.fill("Literal <img src=x onerror=alert(1)> and **stars**");
  await expect(editor).toHaveValue(
    "Literal <img src=x onerror=alert(1)> and **stars**",
  );
  await expect(region.locator("img,strong,script")).toHaveCount(0);
});

test("agent setup keeps the plain draft, project and allowance choices and restores focus", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page);
  await editor.fill("Keep this draft through setup\nwith its second line.");
  await openProjects(region);
  await region.getByRole("checkbox", { name: "Lamp", exact: true }).check();
  await chooseDropdown(
    region,
    "Permissions in the selected projects",
    "Edit, in auto mode",
  );
  await chooseDropdown(region, "Agent", "Codex");
  await region.getByRole("button", { name: "Set it up" }).click();
  await expect(
    page.getByRole("heading", { name: "Set up the agents" }),
  ).toBeVisible();
  await expect(editor).toBeHidden();
  const toolbarBack = page.locator(".app-toolbar-back");
  await expect(toolbarBack).toBeFocused();
  await toolbarBack.press("Escape");
  await expect(editor).toBeFocused();
  await expect(editor).toHaveValue(
    "Keep this draft through setup\nwith its second line.",
  );
  await openProjects(region);
  await expect(
    region.getByRole("checkbox", { name: "Lamp", exact: true }),
  ).toBeChecked();
  await expect(
    region.getByRole("button", { name: "Agent", exact: true }),
  ).toContainText("Codex");
  await expect(
    region.getByRole("button", {
      name: "Permissions in the selected projects",
      exact: true,
    }),
  ).toContainText("Edit, in auto mode");
});

test("project search includes folders and an empty result retains selection", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page);
  await editor.fill("A request with a project.");
  await openProjects(region);
  await region.getByRole("checkbox", { name: "Lamp", exact: true }).check();
  const search = region.getByRole("searchbox");
  await search.fill("/workspace/smoke");
  await expect(
    region.getByRole("checkbox", { name: "Smoke", exact: true }),
  ).toBeVisible();
  await search.fill("unmatched-folder");
  await expect(
    region.getByText("No matching projects. Try another name or folder."),
  ).toBeVisible();
  await search.fill("");
  await expect(
    region.getByRole("checkbox", { name: "Lamp", exact: true }),
  ).toBeChecked();
  await search.press("Escape");
  await expect(region.locator(".wish-tag-menu")).toBeHidden();
  await expect(region).toBeVisible();
  await region
    .locator(".wish-tag-dropdown")
    .getByRole("button")
    .press("Escape");
  await expect(region).toHaveCount(0);
  await expect(page.locator(".sidebar .new-mission")).toBeFocused();
});

test("three active wishes submit paused, without project permissions, and busy prevents duplicates", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page, 3, false);
  await openProjects(region);
  await expect(
    region.getByText(
      "No projects yet. You can make this wish without a project.",
    ),
  ).toBeVisible();
  await expect(region.getByText(/3 wishes are active already/)).toBeVisible();
  let count = 0;
  let release!: () => void;
  const waiting = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/plan.v1.WishService/Make", async (route) => {
    count++;
    const request = fromBinary(
      WishServiceMakeRequestSchema,
      route.request().postDataBuffer()!,
    );
    expect(request.paused).toBe(true);
    expect(request.allowance).toBe(Allowance.NONE);
    expect(request.projectIds).toEqual([]);
    await waiting;
    await route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ code: "unavailable", message: "Try again" }),
    });
  });
  await editor.fill("A paused request.");
  await editor.press("ControlOrMeta+Enter");
  await expect.poll(() => count).toBe(1);
  await expect(
    region.getByRole("button", { name: "Creating…" }),
  ).toBeDisabled();
  await expect(editor).toBeDisabled();
  await expect(page.locator(".sidebar")).toHaveAttribute("inert", "");
  await expect(region).toBeFocused();
  await editor.press("ControlOrMeta+Enter");
  expect(count).toBe(1);
  release();
  await expect(region.getByRole("alert")).toHaveText("Try again");
  await expect(
    region.getByRole("button", { name: "Make it paused" }),
  ).toBeEnabled();
});

test("the character limit keeps an oversized draft editable", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page);
  const limit = 1_000_001;
  await editor.fill("a".repeat(limit));
  await expect(editor).toHaveAttribute("aria-invalid", "true");
  await expect(region.getByRole("alert")).toHaveText(
    "Your request exceeds 1,000,000 characters. Shorten it to make the wish.",
  );
  await expect(
    region.getByRole("button", { name: "Make the wish", exact: true }),
  ).toBeDisabled();
});

test("creation fits a narrow window and keeps the editor and submit action reachable", async ({
  page,
}) => {
  await page.setViewportSize({ width: 760, height: 800 });
  const { region, editor } = await openComposer(page);
  await editor.fill("Create a focused experience for a narrow window.");
  const submit = region.getByRole("button", {
    name: "Make the wish",
    exact: true,
  });
  await submit.hover();
  await expect(page.locator('[role="tooltip"]')).toContainText("Make the wish");
  await expect(page.locator('[role="tooltip"] kbd')).toHaveText(/Enter/);
  await submit.scrollIntoViewIfNeeded();
  await expect(submit).toBeInViewport();
  expect(
    await region.evaluate(
      (element) => element.scrollWidth <= element.clientWidth + 1,
    ),
  ).toBe(true);
  await page.screenshot({ path: "test-results/wish-creation-narrow.png" });
  await editor.scrollIntoViewIfNeeded();
  await expect(editor).toBeInViewport();
});
