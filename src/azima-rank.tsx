// The azimas of a wish not done yet, in the Tasks tab, in their rank (Task.rank): Djinn starts the waiting work of the
// first ones first, and the gates serve it first. The developer ranks them as the wishes in the side bar
// (wish-sidebar.tsx): each azima is dragged by its grip, on the left of its heading, or moved one step with Alt+Arrow;
// while it is dragged, a thin line shows where it would land. A drop ranks it just before the azima under the line, or
// after the last one (TaskService.Rank).
import { GripVertical } from "lucide-react";
import {
  type DragEvent,
  type KeyboardEvent,
  type ReactNode,
  useRef,
  useState,
} from "react";

import type { Task } from "../gen/ts/plan/v1/plan_pb";
import type { AzimaGroup } from "./data/flight";
import { t } from "./i18n";

// Where a dragged azima ranks: just before one, or just after one.
export type RankPlace = { before: Task } | { after: Task };

// Where the dragged azima would land: the gap it falls into (0 before the first azima, the list's length after the
// last), and the line's top in the list.
interface Landing {
  gap: number;
  top: number;
}

export function RankedAzimaList({
  groups,
  render,
  onRank,
}: {
  // The azimas not done yet, in their rank, with their parts.
  groups: readonly AzimaGroup[];
  // Renders an azima, its grip in its heading.
  render: (group: AzimaGroup, grip: ReactNode) => ReactNode;
  // Ranks the azima just before or just after another.
  onRank: (azima: Task, place: RankPlace) => void;
}) {
  const [dragged, setDragged] = useState("");
  const [landing, setLanding] = useState<Landing>();
  // Each azima's card by its id: where a drop line goes.
  const rows = useRef(new Map<string, HTMLElement>());

  // Where the dragged azima lands in gap, or nothing when it would not move. The line sits in the middle of the space
  // between two cards: under the last one, or above the first one, at their edge.
  const land = (gap: number): Landing | undefined => {
    const at = groups.findIndex((g) => g.azima.id === dragged);
    if (at < 0 || gap === at || gap === at + 1) return;
    const next = groups[gap] && rows.current.get(groups[gap].azima.id);
    const prev =
      gap > 0 ? rows.current.get(groups[gap - 1].azima.id) : undefined;
    const bottom = prev ? prev.offsetTop + prev.offsetHeight : undefined;
    const top =
      next && bottom !== undefined
        ? (bottom + next.offsetTop) / 2
        : (next?.offsetTop ?? bottom ?? 0);
    return { gap, top: Math.max(top, 1) };
  };
  // Over a card, the pointer's half says which gap it points at: above the card, or below it.
  const over = (event: DragEvent<HTMLElement>, index: number) => {
    if (!dragged) return;
    const box = event.currentTarget.getBoundingClientRect();
    const next = land(
      index + (event.clientY > box.top + box.height / 2 ? 1 : 0),
    );
    if (next?.gap !== landing?.gap || next?.top !== landing?.top)
      setLanding(next);
    if (!next) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
  };
  const rank = (azima: Task, gap: number) =>
    onRank(
      azima,
      gap < groups.length
        ? { before: groups[gap].azima }
        : { after: groups[groups.length - 1].azima },
    );
  const drop = (event: DragEvent) => {
    event.preventDefault();
    const azima = groups.find((g) => g.azima.id === dragged)?.azima;
    if (azima && landing) rank(azima, landing.gap);
    setDragged("");
    setLanding(undefined);
  };

  return (
    <div
      className="azima-list ranked"
      onDragOver={(event) => {
        if (dragged && landing) event.preventDefault();
      }}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node))
          setLanding(undefined);
      }}
      onDrop={drop}
    >
      {groups.map((group, index) => {
        const { azima } = group;
        // Alt+Arrow moves an azima one step: before the one above it, after the one below it.
        const keys = (event: KeyboardEvent) => {
          if (!event.altKey) return;
          if (event.key === "ArrowUp" && index > 0) rank(azima, index - 1);
          else if (event.key === "ArrowDown" && index < groups.length - 1)
            rank(azima, index + 2);
          else return;
          event.preventDefault();
        };
        const label = t("azima.rank", { rank: index + 1, code: azima.code });
        const grip = (
          <button
            type="button"
            className="azima-grip"
            title={label}
            aria-label={label}
            draggable
            onKeyDown={keys}
            onDragStart={(event) => {
              event.dataTransfer.setData("text/plain", azima.id);
              event.dataTransfer.effectAllowed = "move";
              // The card follows the pointer, not its grip alone.
              const card = rows.current.get(azima.id);
              if (card) event.dataTransfer.setDragImage(card, 16, 16);
              setDragged(azima.id);
            }}
            onDragEnd={() => {
              setDragged("");
              setLanding(undefined);
            }}
          >
            <GripVertical size={14} aria-hidden="true" />
          </button>
        );
        return (
          <div
            key={azima.id}
            className={`azima-rank-item ${dragged === azima.id ? "dragged" : ""}`}
            ref={(el) => {
              if (el) rows.current.set(azima.id, el);
              else rows.current.delete(azima.id);
            }}
            onDragOver={(event) => over(event, index)}
          >
            {render(group, grip)}
          </div>
        );
      })}
      {landing && (
        <div
          className="azima-drop-line"
          style={{ top: landing.top }}
          aria-hidden="true"
        />
      )}
    </div>
  );
}
