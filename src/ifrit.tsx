// An ifrit in the window (T51, src/data/ifrit.ts): vermilion and a flame wherever it shows, with the word of where it
// stands; its ports as localhost links, the gates it holds, the countdown once Djinn warned it goes out, and the button
// that keeps it lit.
import { Lock, Timer } from "lucide-react";
import { useState } from "react";

import type { Gate } from "../gen/ts/machine/v1/machine_pb";
import type { Task } from "../gen/ts/plan/v1/plan_pb";
import { message } from "./data/client";
import { useDjinn } from "./data/djinn";
import {
  type IfritLink,
  ifritMinutesLeft,
  ifritOut,
  ifritWord,
} from "./data/ifrit";
import { t } from "./i18n";
import { Link } from "./link";
import { StatusBadge } from "./status";

// IfritBadge says an ifrit and where it stands, in its colour: "Ifrit W12 · Lit", its flame filled while lit, outlined
// and grey once out.
export function IfritBadge({ task, now }: { task: Task; now: number }) {
  return (
    <StatusBadge
      tone={ifritOut(task) ? "out" : "ifrit"}
      label={t("ifrit.badge", { code: task.code, state: ifritWord(task, now) })}
      title={task.title}
    />
  );
}

// IfritLinks are the ports its serves listen on, each a link the system's browser opens; one that no longer answers is
// greyed, and says so on hover.
export function IfritLinks({ links }: { links: readonly IfritLink[] }) {
  if (!links.length) return null;
  return (
    <span className="ifrit-links">
      {links.map((link) => (
        <span
          key={link.url}
          className={`ifrit-link ${link.open ? "" : "closed"}`}
          title={
            link.open ? link.url : t("ifrit.port_closed", { host: link.host })
          }
        >
          <Link href={link.url}>{link.host}</Link>
          <span className="ifrit-link-label">{link.label}</span>
        </span>
      ))}
    </span>
  );
}

// IfritGates are the gates it holds while lit: others wait for them until it goes out or ends.
export function IfritGates({ gates }: { gates: readonly string[] }) {
  if (!gates.length) return null;
  return (
    <span className="ifrit-gates" title={t("ifrit.gates_detail")}>
      {gates.map((gate) => (
        <span key={gate} className="ifrit-gate">
          <Lock size={12} aria-hidden="true" />
          {t("ifrit.gate", { gate })}
        </span>
      ))}
    </span>
  );
}

// IfritCountdown says, once Djinn warned, how long before it goes out: gold, dotted, as what waits for you.
export function IfritCountdown({ task, now }: { task: Task; now: number }) {
  return (
    <span className="ifrit-countdown" role="timer">
      <Timer size={12} aria-hidden="true" />
      {t("ifrit.state_warned", {
        count: Math.max(1, ifritMinutesLeft(task, now)),
      })}
    </span>
  );
}

// KeepLit says Djinn the developer is there (UiService.Touch): every lit ifrit's out time goes back.
export function KeepLit() {
  const djinn = useDjinn();
  const [error, setError] = useState("");
  return (
    <button
      type="button"
      className="button secondary small ifrit-keep"
      title={error || t("ifrit.keep_lit_detail")}
      disabled={!djinn}
      onClick={() => {
        setError("");
        djinn?.clients.ui.touch({}).catch((err) => setError(message(err)));
      }}
    >
      {t("ifrit.keep_lit")}
    </button>
  );
}

// HeldGates says, in the status bar, how many gates are held, and how many of them an ifrit holds, with a vermilion
// dot: it keeps them while it is lit, and others wait.
export function HeldGates({ gates }: { gates: readonly Gate[] }) {
  const held = gates.filter((g) => g.holder).length;
  const byIfrit = gates.filter((g) => g.holder && g.ifrit).length;
  return (
    <>
      {held > 0 && t("status.gates", { count: held })}
      {byIfrit > 0 && (
        <span className="status-ifrit">
          <span className="status-dot ifrit" />
          {t("status.ifrit_gates", { count: byIfrit })}
        </span>
      )}
    </>
  );
}
