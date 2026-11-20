// The version of the running Djinn, as djinn version gives it, and where it comes from (L10, W61): the window reads it
// on the update stream (UiService.WatchUpdate, current) and shows it at the foot of the wishes' menu and in the
// tooltip of the EARLY ACCESS tag.
import { useEffect, useState } from "react";

import { useDjinn } from "./data/djinn";
import { t } from "./i18n";

// Origin is where a version comes from: a release's tag, the commit of a local install (go tool task install), a
// development build, or the commit of a Go pseudo-version (a build from a checkout without tags, CI's).
export type Origin =
  | { kind: "release"; tag: string }
  | { kind: "local"; commit: string; dirty: boolean }
  | { kind: "dev" }
  | { kind: "build"; commit: string }
  | { kind: "unknown" };

const lowerHex = (s: string) => /^[0-9a-f]+$/.test(s);

// localBuild reads a local install's version as harness.LocalBuild does (internal/harness/compare.go):
// local-<git describe --always --dirty>, such as local-1a2b3c4 or local-v0.1.0-3-g1a2b3c4-dirty.
export function localBuild(
  version: string,
): { commit: string; dirty: boolean } | undefined {
  if (!version.startsWith("local-")) return undefined;
  let rest = version.slice("local-".length);
  const dirty = rest.endsWith("-dirty");
  if (dirty) rest = rest.slice(0, -"-dirty".length);
  const g = rest.lastIndexOf("-g");
  if (g >= 0 && lowerHex(rest.slice(g + 2))) rest = rest.slice(g + 2);
  return rest.length >= 4 && lowerHex(rest)
    ? { commit: rest, dirty }
    : undefined;
}

// A Go pseudo-version: vX.Y.Z-[pre.]yyyymmddhhmmss-<12 hex>, perhaps +dirty.
const PSEUDO =
  /^v\d+\.\d+\.\d+-(?:[0-9A-Za-z.]+\.)?\d{14}-([0-9a-f]{12})(?:\+dirty)?$/;
const SEMVER = /^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;

// versionOrigin tells where a version comes from.
export function versionOrigin(version: string): Origin {
  if (version === "dev") return { kind: "dev" };
  const local = localBuild(version);
  if (local) return { kind: "local", ...local };
  if (version.startsWith("local-"))
    return { kind: "local", commit: "", dirty: version.endsWith("-dirty") };
  const pseudo = PSEUDO.exec(version);
  if (pseudo) return { kind: "build", commit: pseudo[1].slice(0, 7) };
  if (SEMVER.test(version)) return { kind: "release", tag: version };
  return { kind: "unknown" };
}

// originLine says where a version comes from, in a line of the tooltip; "" when Djinn cannot tell.
export function originLine(version: string): string {
  const o = versionOrigin(version);
  switch (o.kind) {
    case "release":
      return t("version.release", { tag: o.tag });
    case "local":
      if (!o.commit) return t("version.local_unknown");
      return t(o.dirty ? "version.local_dirty" : "version.local", {
        commit: o.commit,
      });
    case "dev":
      return t("version.dev");
    case "build":
      return t("version.build", { commit: o.commit });
  }
  return "";
}

// versionLines are the tooltip's lines that name a version: Djinn <version>, then where it comes from; none while the
// version is not known yet.
export function versionLines(version: string): string[] {
  if (!version) return [];
  const origin = originLine(version);
  const name = t("version.name", { version });
  return origin ? [name, origin] : [name];
}

// useDjinnVersion is the running Djinn's version, "" until the update stream first says it. Only the version is kept:
// the stream's other changes render nothing here.
export function useDjinnVersion(): string {
  const djinn = useDjinn();
  const [version, setVersion] = useState("");
  useEffect(
    () => djinn?.update.subscribe((state) => setVersion(state.current)),
    [djinn],
  );
  return version;
}
