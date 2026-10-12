// Djinn's links, djinn://tilasm/<id> and djinn://wish/<id>, read as internal/link reads them. One clicked inside the
// window opens what it names in place: the tilasm in its wish's Tilasms tab, or the wish. Outside, the system runs
// djinn open, which does the same through djinn up.
import type { Clients } from "./client";
import type { DjinnFocus } from "./focus";
import { linksOf } from "./text";

export interface DjinnLink {
  kind: "tilasm" | "wish";
  // The identifier, in lower case.
  id: string;
}

// The hosts a link may have, by what they name: "talisman" is a tilasm too.
const kinds: Record<string, DjinnLink["kind"]> = {
  tilasm: "tilasm",
  talisman: "tilasm",
  wish: "wish",
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

// isDjinnLink tells a link of Djinn's scheme, known or not.
export const isDjinnLink = (href: string) => /^\s*djinn:/i.test(href);

// parseDjinnLink reads a link; undefined for one Djinn does not know. A slash after the identifier, a query or a
// fragment are allowed.
export function parseDjinnLink(href: string): DjinnLink | undefined {
  const m = /^djinn:\/\/([^/?#]*)\/([^/?#]*)\/?(?:[?#].*)?$/is.exec(
    href.trim(),
  );
  const kind = m && kinds[m[1].toLowerCase()];
  const id = m?.[2].toLowerCase() ?? "";
  return kind && uuid.test(id) ? { kind, id } : undefined;
}

// openDjinnLink shows what a link names in the window: a tilasm's wish on its Tilasms tab, or the wish. A link it
// does not know, or whose tilasm is not on this machine, the window says so.
export async function openDjinnLink(
  djinn: { clients: Clients; focus: Pick<DjinnFocus, "show"> },
  href: string,
): Promise<void> {
  const link = parseDjinnLink(href);
  if (link?.kind === "wish") return djinn.focus.show({ wishId: link.id });
  if (link?.kind === "tilasm") {
    try {
      const res = await djinn.clients.tilasms.get({
        tilasm: { ref: { case: "id", value: link.id } },
      });
      const wishId = res.tilasm?.wishId ?? "";
      if (wishId) return djinn.focus.show({ wishId, tilasmId: link.id });
    } catch {
      // Not on this machine: said below.
    }
  }
  djinn.focus.show({ unknownLink: href });
}

// What a link names, for the button that opens it: a pull or merge request, an issue, a CI run, a Djinn link, or
// else its host.
export type LinkName =
  | { kind: "pr" | "mr" | "issue" | "pipeline"; number: string }
  | { kind: "run" | "tilasm" | "wish" }
  | { kind: "host"; host: string };

// linkName reads what a link names from its address, as GitHub and GitLab write them.
export function linkName(href: string): LinkName {
  const djinn = parseDjinnLink(href);
  if (djinn) return { kind: djinn.kind };
  let url: URL;
  try {
    url = new URL(href);
  } catch {
    return { kind: "host", host: href };
  }
  const path = url.pathname;
  const n = (re: RegExp) => re.exec(path)?.[1];
  const mr = n(/\/-\/merge_requests\/(\d+)/);
  if (mr) return { kind: "mr", number: mr };
  const pr = n(/\/pull\/(\d+)/);
  if (pr) return { kind: "pr", number: pr };
  const issue = n(/\/(?:-\/)?issues\/(\d+)/);
  if (issue) return { kind: "issue", number: issue };
  const pipeline = n(/\/-\/pipelines\/(\d+)/);
  if (pipeline) return { kind: "pipeline", number: pipeline };
  if (/\/actions\/runs\/\d+|\/-\/jobs\/\d+/.test(path)) return { kind: "run" };
  return { kind: "host", host: url.host };
}

// mainLink is where a move question sends the developer to act: the first link of its title, its context, its
// recommendation, then its options.
export function mainLink(q: {
  text: string;
  context?: string;
  recommendation?: string;
  options?: readonly string[];
}): string | undefined {
  for (const text of [
    q.text,
    q.context,
    q.recommendation,
    ...(q.options ?? []),
  ])
    if (text) {
      const [link] = linksOf(text);
      if (link) return link;
    }
  return undefined;
}
