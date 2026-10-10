// The lists that only grow, the decisions and the journal, show their latest items and fold the older ones behind a
// line that unfolds them a batch at a time (decision Q62). What is folded is not rendered: a wish of a hundred
// decisions renders thirty. The line ends the list, the latest first: what it unfolds comes below what you read, so
// the place you read stays where it is (scroll-anchor.ts).
import { ChevronDown } from "lucide-react";
import { useState } from "react";

// How many of the latest items a list shows at first.
export const RECENT = 30;
// How many more items each click on the line shows.
export const BATCH = 30;

// batches is how many items the window shows to reach n: the first window, then whole batches.
const batches = (n: number) =>
  n <= RECENT ? RECENT : RECENT + Math.ceil((n - RECENT) / BATCH) * BATCH;

// useRecent says how many items of a list of total to show, and gives the function that shows a batch more. An item
// to bring into sight, at index reach, unfolds the batches up to it.
export function useRecent(total: number, reach = -1) {
  const [shown, setShown] = useState(RECENT);
  // Set while rendering, before the effects: the one that scrolls to the item finds it in the page.
  if (reach >= shown) setShown(batches(reach + 1));
  return {
    shown: Math.min(shown, total),
    more: () => setShown((n) => n + BATCH),
  };
}

// OlderLine is the line under the latest items that unfolds the next batch. label says what it shows: count items,
// of the hidden ones.
export function OlderLine({
  hidden,
  label,
  onShow,
}: {
  hidden: number;
  label: (count: number, hidden: number) => string;
  onShow: () => void;
}) {
  if (hidden <= 0) return null;
  return (
    <button className="text-button older-line" onClick={onShow}>
      <ChevronDown size={13} aria-hidden="true" />
      {label(Math.min(BATCH, hidden), hidden)}
    </button>
  );
}
