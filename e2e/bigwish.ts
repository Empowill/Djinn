// A wish of real size in the djinn of global-setup.ts, to measure the window on: tools/bigwish writes it
// (internal/testx/bigwish, the same bytes every run), and djinn wish import reads it, as for any spec's wish. "real" is
// the developer's wish today, "x10" ten times over. A spec imports it, measures, and removes it after: the other specs
// share this djinn, and a wish takes one of its three active places.
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export type BigWishSize = "real" | "x10";

const root = path.resolve(__dirname, "..");
const binary = path.join(
  root,
  "bin/djinn-e2e" + (process.platform === "win32" ? ".exe" : ""),
);

function djinn(...args: string[]): string {
  return execFileSync(binary, args, {
    env: { ...process.env, DJINN_HOME: process.env.DJINN_E2E_HOME },
    encoding: "utf8",
  });
}

// importBigWish imports the wish of that size, in place of any wish of another size, and gives its identifier.
export function importBigWish(size: BigWishSize): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-e2e-bigwish-"));
  try {
    const file = path.join(dir, `${size}.djinn`);
    execFileSync("go", ["run", "./tools/bigwish", "-size", size, "-o", file], {
      cwd: root,
      stdio: ["ignore", "ignore", "inherit"],
    });
    const imported = JSON.parse(
      djinn("wish", "import", file, "--replace", "--json"),
    ) as { wish: { id: string } };
    return imported.wish.id;
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

// removeBigWish deletes it from the djinn, with all it holds.
export function removeBigWish(wishId: string): void {
  djinn("wish", "delete", wishId);
}
