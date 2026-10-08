"use strict";

// Preloaded by `npm test` (node --require): the Node tests render the interface in French, the
// language their assertions were written in. src/i18n.ts reads the language from the navigator,
// as the window and the browser report it.
const { userAgent } = globalThis.navigator ?? {};
Object.defineProperty(globalThis, "navigator", {
  value: { userAgent, language: "fr-FR", languages: ["fr-FR"] },
  configurable: true,
  writable: true,
});
