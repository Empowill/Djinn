// The window's status language: one colour, one icon and one word per state, never the colour alone. Done is green,
// running blue with a live dot, waiting for you orange, investigating violet, planned grey, failed red, interrupted
// amber, stopped and paused muted, watching teal with an eye; a decision the developer took is human, rose with a person;
// an azima whose work is done and that awaits its proof is olive, with a clipboard to check.
// The colours are tokens of review.css, checked for contrast in both themes.
import {
  CheckCircle2,
  CircleDashed,
  CircleOff,
  CirclePause,
  ClipboardCheck,
  CircleStop,
  CircleUserRound,
  Eye,
  Hand,
  type LucideIcon,
  Search,
  TriangleAlert,
  XCircle,
} from "lucide-react";
import type { ReactNode } from "react";

import type { Tone } from "./data/format";

const icons: Record<Tone, LucideIcon | null> = {
  done: CheckCircle2,
  running: null,
  waiting: Hand,
  investigating: Search,
  planned: CircleDashed,
  failed: XCircle,
  interrupted: TriangleAlert,
  stopped: CircleStop,
  paused: CirclePause,
  watching: Eye,
  human: CircleUserRound,
  proof: ClipboardCheck,
};

// ToneIcon is a state's icon; running is a live dot.
export function ToneIcon({ tone, size = 13 }: { tone: Tone; size?: number }) {
  const Icon = icons[tone] ?? CircleOff;
  if (tone === "running")
    return <span className="live-dot" aria-hidden="true" />;
  return <Icon size={size} aria-hidden="true" />;
}

// StatusBadge says a state: its icon and its word, in its colour.
export function StatusBadge({
  tone,
  label,
  title,
}: {
  tone: Tone;
  label: string;
  title?: string;
}) {
  return (
    <span className={`status-badge tone-${tone}`} title={title}>
      <ToneIcon tone={tone} />
      <span>{label}</span>
    </span>
  );
}

// CountPill is a count in a state's colour: its icon, the number, and its words for whoever cannot see the colour.
export function CountPill({
  tone,
  count,
  label,
  icon: Icon,
  children,
}: {
  tone: Tone;
  count: number;
  // What the count is, in words: "2 questions wait for your answer".
  label: string;
  // What it counts, when the state's icon does not say it: the inbox.
  icon?: LucideIcon;
  // The words shown beside the number; only the icon and the number when none.
  children?: ReactNode;
}) {
  return (
    <span
      className={`count-pill tone-${tone}`}
      title={label}
      aria-label={label}
      role="img"
    >
      {Icon ? (
        <Icon size={12} aria-hidden="true" />
      ) : (
        <ToneIcon tone={tone} size={12} />
      )}
      <b>{count}</b>
      {children}
    </span>
  );
}
