import fs from "node:fs";
import os from "node:os";
import path from "node:path";

// The merge review keeps a small, stable set of representative captures outside the repository. CI can point this at
// its artifact directory without changing the assertions or the names a reviewer looks for.
export const mergeVisualRoot =
  process.env.DJINN_MERGE_VISUAL_DIR ??
  path.join(os.tmpdir(), "djinn-merge-visual-20261010");

export function mergeVisual(name: string): string {
  const target = path.join(mergeVisualRoot, name);
  fs.mkdirSync(path.dirname(target), { recursive: true });
  return target;
}
