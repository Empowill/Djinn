// Creation UI owns this spec. Requests are intercepted so these checks never launch a model, write a wish,
// or depend on automatic title generation. The page itself is served by the isolated Djinn test server.
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { expect, test, type Locator, type Page } from "@playwright/test";
import {
  Allowance,
  ProjectServiceListResponseSchema,
  Provider,
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

async function openComposer(page: Page, active = 0, projects = true) {
  await page.addInitScript(() => {
    localStorage.setItem("djinn.agents.offered", "1");
    localStorage.setItem("djinn.language", "en");
    localStorage.removeItem("djinn.provider");
  });
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
  await expect(page.locator(".app-statusbar").getByText("Live")).toBeVisible();
  await page.locator(".sidebar .new-mission").click();
  const region = page.getByRole("region", { name: "Make a wish", exact: true });
  await expect(region).toBeVisible();
  const editor = region.getByRole("textbox", { name: "What do you wish?" });
  await expect(editor).toBeFocused();
  return { region, editor };
}

async function selectText(editor: Locator, text: string) {
  await editor.evaluate((element, text) => {
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    let node: Node | null;
    while ((node = walker.nextNode())) {
      const start = node.textContent!.indexOf(text);
      if (start === -1) continue;
      const range = document.createRange();
      range.setStart(node, start);
      range.setEnd(node, start + text.length);
      const selection = window.getSelection()!;
      selection.removeAllRanges();
      selection.addRange(range);
      return;
    }
    throw new Error(`Text not found: ${text}`);
  }, text);
}

test("formatting and multiline editing send the complete Markdown prompt; errors keep the draft", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page);
  const requests: ReturnType<
    typeof fromBinary<typeof WishServiceMakeRequestSchema>
  >[] = [];
  await page.route("**/plan.v1.WishService/Make", async (route) => {
    expect(route.request().headers()["accept-language"]).toBe("en");
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
  await editor.fill("Build a thoughtful export with context and requirements.");
  await selectText(editor, "thoughtful");
  await region.getByRole("button", { name: "Bold", exact: true }).click();
  await selectText(editor, "requirements");
  await editor.press("ControlOrMeta+i");
  await editor.press("ArrowRight");
  await editor.press("End");
  await editor.press("Enter");
  await editor.pressSequentially("Keep every detail.");
  await region.getByRole("checkbox", { name: "Lamp", exact: true }).check();
  await expect(
    region.getByRole("checkbox", { name: "Lamp", exact: true }),
  ).toHaveAccessibleDescription("/workspace/lamp");
  await region
    .getByLabel("Permissions in the selected projects")
    .selectOption(String(Allowance.EDIT));
  await page.screenshot({ path: "test-results/e2e/wish-creation-dark.png" });
  await page.evaluate(() => {
    document.documentElement.dataset.theme = "light";
  });
  await expect(
    region.getByRole("button", { name: "Cancel", exact: true }).last(),
  ).toHaveCSS("color", "rgb(28, 28, 26)");
  await page.screenshot({ path: "test-results/e2e/wish-creation-light.png" });
  await page.evaluate(() => {
    document.documentElement.dataset.theme = "dark";
  });
  await region
    .getByRole("button", { name: "Make the wish", exact: true })
    .click();
  await expect(region.getByRole("alert")).toHaveText("Try again");
  expect(requests).toHaveLength(1);
  expect(requests[0].title).toBe("");
  expect(requests[0].prompt).toContain("**thoughtful**");
  expect(requests[0].prompt).toContain("*requirements*");
  expect(requests[0].prompt).toContain("\n\n");
  expect(requests[0].prompt).toContain("Keep every detail.");
  expect(requests[0].projectIds).toEqual([alpha]);
  expect(requests[0].allowance).toBe(Allowance.EDIT);
  await expect(editor).toContainText("Keep every detail.");
  await editor.press("ControlOrMeta+Enter");
  await expect.poll(() => requests.length).toBe(2);
});

test("lists serialize as Markdown and plain paste cannot insert HTML", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page);
  let prompt = "";
  await page.route("**/plan.v1.WishService/Make", async (route) => {
    prompt = fromBinary(
      WishServiceMakeRequestSchema,
      route.request().postDataBuffer()!,
    ).prompt;
    await route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ code: "unavailable", message: "Try again" }),
    });
  });
  await editor.fill("First\nSecond");
  await editor.press("ControlOrMeta+a");
  await region.getByRole("button", { name: "Bulleted list" }).click();
  await expect(editor.locator("ul li")).toHaveCount(2);
  await region
    .getByRole("button", { name: "Make the wish", exact: true })
    .click();
  await expect.poll(() => prompt).toBe("- First\n- Second");
  await expect(region.getByRole("alert")).toBeVisible();
  await editor.press("ControlOrMeta+a");
  await region.getByRole("button", { name: "Numbered list" }).click();
  await expect(editor.locator("ol li")).toHaveCount(2);
  await region
    .getByRole("button", { name: "Make the wish", exact: true })
    .click();
  await expect.poll(() => prompt).toBe("1. First\n2. Second");
  await expect(region.getByRole("alert")).toBeVisible();
  await editor.fill("");
  await editor.evaluate((element) => {
    const data = new DataTransfer();
    data.setData(
      "text/plain",
      "Literal <img src=x onerror=alert(1)> and **stars**",
    );
    data.setData(
      "text/html",
      '<img src=x onerror="alert(1)"><strong>Danger</strong>',
    );
    element.dispatchEvent(
      new ClipboardEvent("paste", {
        clipboardData: data,
        bubbles: true,
        cancelable: true,
      }),
    );
  });
  await expect(editor.locator("img,strong,script")).toHaveCount(0);
  await region
    .getByRole("button", { name: "Make the wish", exact: true })
    .click();
  await expect.poll(() => prompt).toContain("\\<img src=x onerror=alert(1)\\>");
  expect(prompt).toContain("\\*\\*stars\\*\\*");
});

test("agent setup preserves text, formatting, projects and permissions and restores editor focus", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page);
  await editor.fill("Keep this draft through setup");
  await selectText(editor, "draft");
  await region.getByRole("button", { name: "Bold", exact: true }).click();
  await selectText(editor, "setup");
  await region.getByRole("button", { name: "Italic", exact: true }).click();
  await editor.press("ArrowRight");
  await editor.press("Enter");
  await region.getByRole("button", { name: "Bulleted list" }).click();
  await editor.pressSequentially("First detail");
  await editor.press("Enter");
  await editor.pressSequentially("Second detail");
  await region.getByRole("checkbox", { name: "Lamp", exact: true }).check();
  await region
    .getByLabel("Permissions in the selected projects")
    .selectOption(String(Allowance.AUTO));
  await region
    .getByLabel("Agent", { exact: true })
    .selectOption(String(Provider.CODEX));
  await region.getByRole("button", { name: "Set it up" }).click();
  await expect(
    page.getByRole("heading", { name: "Set up the agents" }),
  ).toBeVisible();
  await expect(editor).toBeHidden();
  await page.getByRole("button", { name: "Back to the wish" }).click();
  await expect(editor).toBeFocused();
  await expect(editor).toContainText("Keep this draft through setup");
  await expect(editor.locator("b,strong")).toHaveText("draft");
  await expect(editor.locator("i,em").first()).toHaveText("setup");
  await expect(editor.locator("ul li")).toHaveText([
    "First detail",
    "Second detail",
  ]);
  await expect(
    region.getByRole("checkbox", { name: "Lamp", exact: true }),
  ).toBeChecked();
  await expect(region.getByLabel("Agent", { exact: true })).toHaveValue(
    String(Provider.CODEX),
  );
  await expect(
    region.getByLabel("Permissions in the selected projects"),
  ).toHaveValue(String(Allowance.AUTO));
});

test("project search includes folders, empty results retain selection, Escape returns focus", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page);
  await expect(region).toHaveRole("region");
  await expect(page.locator(".sidebar")).not.toHaveAttribute("inert", "");
  await expect(
    region.getByRole("button", { name: "Make the wish", exact: true }),
  ).toBeDisabled();
  await editor.fill("   \n  ");
  await expect(
    region.getByRole("button", { name: "Make the wish", exact: true }),
  ).toBeDisabled();
  await editor.fill("\u00a0\u200b\u200c\u200d\u200e\u2060\ufeff");
  await expect(
    region.getByRole("button", { name: "Make the wish", exact: true }),
  ).toBeDisabled();
  await region.getByRole("checkbox", { name: "Lamp", exact: true }).check();
  const search = region.getByRole("searchbox");
  await search.fill("/workspace/smoke");
  await expect(region.getByRole("checkbox")).toHaveCount(1);
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
  // Some browsers consume the first Escape to clear a search input; the next closes creation.
  if (await region.isVisible()) await search.press("Escape");
  await expect(region).toHaveCount(0);
  await expect(page.locator(".sidebar .new-mission")).toBeFocused();
});

test("three active wishes submit paused, without project permissions, and busy state prevents duplicate submission", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page, 3, false);
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
  await editor.fill(
    "A request much longer than the old title limit. ".repeat(20),
  );
  await editor.press("ControlOrMeta+Enter");
  await expect.poll(() => count).toBe(1);
  await expect(
    region.getByRole("button", { name: "Creating…" }),
  ).toBeDisabled();
  await expect(editor).toHaveAttribute("contenteditable", "false");
  const dismissal = region.getByRole("button", {
    name: /^(Cancel|Back to wishes)$/,
  });
  await expect(dismissal).toHaveCount(2);
  await expect(dismissal.first()).toBeDisabled();
  await expect(dismissal.last()).toBeDisabled();
  await expect(page.locator(".sidebar")).toHaveAttribute("inert", "");
  await expect(region).toBeFocused();
  await region.press("Escape");
  await expect(region).toBeVisible();
  await region.press("Tab");
  await expect(region).toBeFocused();
  await editor.press("ControlOrMeta+Enter");
  expect(count).toBe(1);
  release();
  await expect(region.getByRole("alert")).toHaveText("Try again");
  await expect(
    region.getByRole("button", { name: "Make it paused" }),
  ).toBeEnabled();
});

test("the full Markdown character limit keeps oversized drafts editable and accepts the boundary", async ({
  page,
}) => {
  const { region, editor } = await openComposer(page);
  const limit = 1_000_000;
  let sentLength = 0;
  await page.route("**/plan.v1.WishService/Make", async (route) => {
    sentLength = fromBinary(
      WishServiceMakeRequestSchema,
      route.request().postDataBuffer()!,
    ).prompt.length;
    await route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ code: "unavailable", message: "Try again" }),
    });
  });
  // Literal asterisks become escaped Markdown, so validation must count the serialized request.
  await editor.fill("a".repeat(limit - 1) + "*");
  await expect(editor).toHaveAttribute("aria-invalid", "true");
  await expect(region.getByRole("alert")).toHaveText(
    "Your request exceeds 1,000,000 characters after formatting. Shorten it to make the wish.",
  );
  await expect(
    region.getByRole("button", { name: "Make the wish", exact: true }),
  ).toBeDisabled();
  await editor.press("ControlOrMeta+Enter");
  expect(sentLength).toBe(0);
  await editor.press("Backspace");
  await editor.pressSequentially("b");
  await expect(editor).not.toHaveAttribute("aria-invalid", "true");
  await expect(
    region.getByRole("button", { name: "Make the wish", exact: true }),
  ).toBeEnabled();
  await region
    .getByRole("button", { name: "Make the wish", exact: true })
    .click();
  await expect.poll(() => sentLength).toBe(limit);
});

test("creation fits a narrow window and keeps the editor and submit action reachable", async ({
  page,
}) => {
  await page.setViewportSize({ width: 760, height: 800 });
  const { region, editor } = await openComposer(page);
  await editor.fill("Create a focused experience for a narrow window.");
  await expect(region.getByRole("checkbox", { name: /Lamp/ })).toBeVisible();
  const submit = region.getByRole("button", {
    name: "Make the wish",
    exact: true,
  });
  await submit.scrollIntoViewIfNeeded();
  await expect(submit).toBeInViewport();
  expect(
    await region.evaluate(
      (element) => element.scrollWidth <= element.clientWidth + 1,
    ),
  ).toBe(true);
  await editor.scrollIntoViewIfNeeded();
  await expect(editor).toBeInViewport();
  await page.screenshot({ path: "test-results/wish-creation-narrow.png" });
});
