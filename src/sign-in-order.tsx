// The sign-ins of every agent in the developer's order of priority (decision Q295 B, tilasm L49 v2): the first free one
// of its agent takes each new task, and each batch of the Djinn unless it is pinned. The developer ranks them as the
// azimas (azima-rank.tsx): each line is dragged by its grip, its rank beside it, or moved one step with Alt+Arrow; while
// one is dragged, a thin line shows where it would land. Each move sends the whole order (LeadService.Order), and a
// live region says the new rank.
import { GripVertical } from "lucide-react";
import {
  type DragEvent,
  type KeyboardEvent,
  type ReactNode,
  useRef,
  useState,
} from "react";

import { t } from "./i18n";

// A sign-in in the order: its key as djinn lead order takes it (claude, claude/work), and its name for a reader.
export interface Ranked {
  key: string;
  name: string;
}

// Where the dragged line would land: the gap it falls into (0 before the first line, the list's length after the last),
// and the line's top in the list.
interface Landing {
  gap: number;
  top: number;
}

// moved is order with the line at index from put into gap (0 before the first, order.length after the last).
export function moved<T>(order: readonly T[], from: number, gap: number): T[] {
  const out = [...order];
  const [item] = out.splice(from, 1);
  out.splice(gap > from ? gap - 1 : gap, 0, item);
  return out;
}

// reordered is the whole order (every sign-in Djinn knows, shown or not) once the ones shown take the order shown: each
// takes the place of one shown before, so a sign-in the screen does not show keeps its rank.
export function reordered(
  whole: readonly string[],
  shown: readonly string[],
): string[] {
  const visible = new Set(shown);
  const out = [...whole];
  let next = 0;
  whole.forEach((key, i) => {
    if (visible.has(key)) out[i] = shown[next++];
  });
  return out;
}

export function RankedSignIns({
  items,
  render,
  onOrder,
}: {
  // The sign-ins shown, in their order.
  items: readonly Ranked[];
  // Renders a sign-in's lines, its grip on the left of the first.
  render: (item: Ranked, grip: ReactNode) => ReactNode;
  // Writes the order shown, after a move.
  onOrder: (keys: string[]) => void;
}) {
  const [dragged, setDragged] = useState("");
  const [landing, setLanding] = useState<Landing>();
  const [said, setSaid] = useState("");
  // Each sign-in's lines by its key: where a drop line goes.
  const rows = useRef(new Map<string, HTMLElement>());

  const move = (from: number, gap: number) => {
    const next = moved(items, from, gap);
    const at = next.indexOf(items[from]);
    setSaid(
      t("agents.order_moved", {
        name: items[from].name,
        rank: at + 1,
        count: items.length,
      }),
    );
    onOrder(next.map((item) => item.key));
  };
  // Where the dragged line lands in gap, or nothing when it would not move: in the middle of the space between two.
  const land = (gap: number): Landing | undefined => {
    const at = items.findIndex((item) => item.key === dragged);
    if (at < 0 || gap === at || gap === at + 1) return;
    const next = items[gap] && rows.current.get(items[gap].key);
    const prev = gap > 0 ? rows.current.get(items[gap - 1].key) : undefined;
    const bottom = prev ? prev.offsetTop + prev.offsetHeight : undefined;
    const top =
      next && bottom !== undefined
        ? (bottom + next.offsetTop) / 2
        : (next?.offsetTop ?? bottom ?? 0);
    return { gap, top: Math.max(top, 1) };
  };
  // Over a line, the pointer's half says which gap it points at: above it, or below it.
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
  const drop = (event: DragEvent) => {
    event.preventDefault();
    const at = items.findIndex((item) => item.key === dragged);
    if (at >= 0 && landing) move(at, landing.gap);
    setDragged("");
    setLanding(undefined);
  };

  return (
    <div
      className="sign-in-order"
      onDragOver={(event) => {
        if (dragged && landing) event.preventDefault();
      }}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node))
          setLanding(undefined);
      }}
      onDrop={drop}
    >
      {items.map((item, index) => {
        // Alt+Arrow moves a sign-in one rank: before the one above it, after the one below it.
        const keys = (event: KeyboardEvent) => {
          if (!event.altKey) return;
          if (event.key === "ArrowUp" && index > 0) move(index, index - 1);
          else if (event.key === "ArrowDown" && index < items.length - 1)
            move(index, index + 2);
          else return;
          event.preventDefault();
        };
        const label = t("agents.order_rank", {
          name: item.name,
          rank: index + 1,
          count: items.length,
        });
        const grip = (
          <button
            type="button"
            className="sign-in-grip"
            title={label}
            aria-label={label}
            draggable
            onKeyDown={keys}
            onDragStart={(event) => {
              event.dataTransfer.setData("text/plain", item.key);
              event.dataTransfer.effectAllowed = "move";
              // The sign-in's lines follow the pointer, not its grip alone.
              const lines = rows.current.get(item.key);
              if (lines) event.dataTransfer.setDragImage(lines, 16, 16);
              setDragged(item.key);
            }}
            onDragEnd={() => {
              setDragged("");
              setLanding(undefined);
            }}
          >
            <GripVertical size={14} aria-hidden="true" />
            <span className="sign-in-rank">{index + 1}</span>
          </button>
        );
        return (
          <div
            key={item.key}
            className={`sign-in-ranked${dragged === item.key ? " dragged" : ""}`}
            data-sign-in={item.key}
            ref={(el) => {
              if (el) rows.current.set(item.key, el);
              else rows.current.delete(item.key);
            }}
            onDragOver={(event) => over(event, index)}
          >
            {render(item, grip)}
          </div>
        );
      })}
      {landing && (
        <div
          className="sign-in-drop-line"
          style={{ top: landing.top }}
          aria-hidden="true"
        />
      )}
      <p className="sign-in-said" aria-live="polite">
        {said}
      </p>
    </div>
  );
}
