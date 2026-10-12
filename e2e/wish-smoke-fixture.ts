import type { Page } from "@playwright/test";

// Creation tests exercise the form contract, not the decorative shader. Keep canvas 2D (and the terminal) intact while
// making WebGL2 fail quickly in software-only workers; the French visual test deliberately leaves the solver enabled.
export async function disableWishSmokeWebGL(page: Page) {
  await page.addInitScript(() => {
    const originalGetContext = HTMLCanvasElement.prototype.getContext;
    HTMLCanvasElement.prototype.getContext = function (
      contextId: string,
      options?: unknown,
    ) {
      if (contextId === "webgl2") return null;
      const passthrough = originalGetContext as unknown as {
        call: (
          canvas: HTMLCanvasElement,
          contextId: string,
          options?: unknown,
        ) => RenderingContext | null;
      };
      return passthrough.call(this, contextId, options);
    } as typeof originalGetContext;
    HTMLCanvasElement.prototype.transferControlToOffscreen = function () {
      throw new Error("test-disabled");
    };
  });
}
