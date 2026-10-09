// Djinn's links, djinn://tilasm/<id> and djinn://wish/<id>, read as internal/link reads them. One clicked inside the
// window opens what it names in place: the tilasm in its wish's Tilasms tab, or the wish. Outside, the system runs
// djinn open, which does the same through djinn up.
import type { Clients } from "./client";
import type { DjinnFocus } from "./focus";

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
