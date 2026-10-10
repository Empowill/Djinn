// The mark of a spec that checks the interface alone, the same Chromium on every system: the Windows job of CI sets
// DJINN_TEST_SYSTEM_ONLY=1 and skips it, as Linux proves it (CONTRIBUTING.md). Never mark a spec that reaches what
// differs there: djinn's own processes, the command line's files and paths, the terminal.
import { test } from "@playwright/test";

// portable skips the spec, called at the top of its file, or the test, called first in its body, where only the
// tests that target the system run.
export function portable(): void {
  test.skip(
    process.env.DJINN_TEST_SYSTEM_ONLY === "1",
    "portable: the interface alone, proven on Linux; skipped where DJINN_TEST_SYSTEM_ONLY=1, the Windows job (CONTRIBUTING.md)",
  );
}
