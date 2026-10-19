// The window's status language: one colour, one icon and one word per state, never the colour alone. Done is green,
// running blue with a live dot, waiting for you orange, a failed merge an agent fixes orange with a wrench (Djinn's
// move, not yours), investigating violet, planned grey, failed red, interrupted amber, stopped and paused muted,
// cancelled a muted stone with a ban sign, watching teal with an eye; a decision the developer took is human, rose with
// a person; an azima whose work is done and that awaits its proof is olive, with a clipboard to check; a question that
// can wait is grey, with a clock; a lit ifrit is vermilion with a flame, and one put out grey with its flame outlined.
// The colours are tokens of review.css, checked for contrast in both themes.
import {
  Ban,
  CheckCircle2,
  CircleDashed,
  CircleOff,
  CirclePause,
  ClipboardCheck,
  CircleStop,
  CircleUserRound,
  Clock3,
  Eye,
  Flame,
  Hand,
  type LucideIcon,
  PencilRuler,
  Search,
  Star,
  TriangleAlert,
  Wrench,
  XCircle,
} from "lucide-react";
import type { ReactNode } from "react";

import type { Tone } from "./data/format";

const icons: Record<Tone, LucideIcon | null> = {
  done: CheckCircle2,
  running: null,
  waiting: Hand,
  investigating: Search,
  mockup: PencilRuler,
  planned: CircleDashed,
  failed: XCircle,
  fixing: Wrench,
  interrupted: TriangleAlert,
  stopped: CircleStop,
  cancelled: Ban,
  paused: CirclePause,
  watching: Eye,
  human: CircleUserRound,
  proof: ClipboardCheck,
  later: Clock3,
  withdrawn: CircleOff,
  accent: Star,
  ifrit: Flame,
  out: Flame,
};

// ToneIcon is a state's icon; running is a live dot, and a lit ifrit's flame is filled.
export function ToneIcon({ tone, size = 13 }: { tone: Tone; size?: number }) {
  const Icon = icons[tone] ?? CircleOff;
  if (tone === "running")
    return <span className="live-dot" aria-hidden="true" />;
  if (tone === "ifrit")
    return <Flame size={size} fill="currentColor" aria-hidden="true" />;
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

// The marks on a wish's icon in the menu (L09 v6), without background: a small hand, in the colour of what waits for
// you, for the questions that wait; the logo's flame, in two tones of the ifrit's vermilion, for a lit ifrit. A halo of
// the row's colour keeps each apart from the icon (wish.css).
export function WaitingHand() {
  return (
    <svg
      className="lucide waiting-hand"
      viewBox="0 0 24 24"
      width="12"
      height="12"
      fill="currentColor"
      aria-hidden="true"
    >
      <rect x="7" y="5" width="3" height="10" rx="1.5" />
      <rect x="10.6" y="2.5" width="3" height="11" rx="1.5" />
      <rect x="14.2" y="3.5" width="3" height="11" rx="1.5" />
      <rect x="17.8" y="6.5" width="3" height="9" rx="1.5" />
      <path d="M7 12h13.8v3.5c0 4-3 7-7 7h-1c-2.6 0-4.4-1.4-5.6-3.6L3 13.4a1.7 1.7 0 0 1 2.7-2L7 13Z" />
    </svg>
  );
}

export function IfritFlame() {
  return (
    <svg
      className="lucide ifrit-flame"
      viewBox="0 0 24 24"
      width="13"
      height="14"
      aria-hidden="true"
    >
      <path
        className="ifrit-flame-out"
        d="M12 1C19 7.5 18.5 16 12 23 5.5 16 5 7.5 12 1Z"
      />
      <path
        className="ifrit-flame-core"
        d="M12 8.5c3 3.6 2.6 7.8 0 11.2-2.6-3.4-3-7.6 0-11.2Z"
      />
    </svg>
  );
}
