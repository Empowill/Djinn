// The Djinn's eye (Decision Q38 A, tilasm L13 v2): the seal of the side panel opens into an eye that rests where to
// look now. Drawn from the seal: its khatam star for the iris, its saffron flame for the pupil, its malachite line;
// solar and wide open in the light theme, a mocking demon with a lowered lid in the dark (the Djinn's two tones, W156).
// It rests by the element that has the focus, and only there (W259): in the margin left of the question that has it,
// reading with you; in the socket of Write to the Djinn, folded or open; under the tab that has it. With the focus on
// nothing that has a place, it listens on Write to the Djinn; home in the seal with no such place shown. It never takes
// the focus itself: a click on a wish in the side panel gives it to the wish's first question (question-column.tsx).
// It sleeps home while the lead is stopped, and pales there while Djinn's stream is cut. Its flame shines while
// something waits for you: a question that holds up a worker, a worker waiting, a lit ifrit.
// From one place to the next it flies on a curve, eased, long enough to be followed (data/eye.ts), above the page in a
// layer of its own; at rest it covers nothing and scrolls with its perch. A calm gate holds it while you type, press,
// scroll, read a dialog, or the window rests; while you type, it goes nowhere but to the field you type in. It never takes the focus nor a click. It moves by transform and opacity
// only, composed by the engine, and nothing of it loops: at rest, the window draws nothing for it.
import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";

import {
  AFTER_KEY,
  AFTER_SCROLL,
  calmWait,
  clampTo,
  type EyeTarget,
  eyeTarget,
  flight,
  type Presence,
  presences,
} from "./data/eye";
import { onStillness, still } from "./stillness";
import "./djinn-eye.css";

// The presence of the Djinn, a choice of the personal space, kept in the page's storage as the theme is.
const KEY = "djinn.presence";
const presenceListeners = new Set<() => void>();

export function chosenPresence(): Presence {
  try {
    const value = window.localStorage.getItem(KEY) ?? "";
    return (presences as readonly string[]).includes(value)
      ? (value as Presence)
      : "alive";
  } catch {
    return "alive";
  }
}

export function setPresence(presence: Presence) {
  try {
    if (presence === "alive") window.localStorage.removeItem(KEY);
    else window.localStorage.setItem(KEY, presence);
  } catch {
    // Private window or blocked storage: the choice lasts for this page only.
  }
  shownPresence = presence;
  presenceListeners.forEach((listener) => listener());
  signalled();
}

let shownPresence: Presence | undefined;
const presenceNow = () => (shownPresence ??= chosenPresence());

export const usePresence = () =>
  useSyncExternalStore(
    (listener) => {
      presenceListeners.add(listener);
      return () => presenceListeners.delete(listener);
    },
    presenceNow,
    () => "alive" as const,
  );

// What the pages tell the eye: whether a question holds up a worker, whether the lead is stopped, what else waits for
// the developer, and Djinn's stream. One window, one eye: they live here, not in a context.
const signals: {
  blocking: boolean;
  stopped?: boolean;
  needs: boolean;
  live: boolean;
} = { blocking: false, needs: false, live: true };
const signalListeners = new Set<() => void>();
const signalled = () => signalListeners.forEach((listener) => listener());

function signal<K extends keyof typeof signals>(
  key: K,
  value: (typeof signals)[K],
) {
  if (signals[key] === value) return;
  signals[key] = value;
  signalled();
}

// useEyeBlocking tells the eye that a question of the column shown holds up a worker: its flame shines.
export function useEyeBlocking(blocking: boolean) {
  useEffect(() => signal("blocking", blocking), [blocking]);
  useEffect(() => () => signal("blocking", false), []);
}

// useEyeLead tells the eye whether the page's lead is stopped.
export function useEyeLead(stopped: boolean) {
  useEffect(() => signal("stopped", stopped), [stopped]);
  useEffect(() => () => signal("stopped", undefined), []);
}

// useEyeNeeds tells the eye that something on the page waits for the developer: its flame shines.
export function useEyeNeeds(needs: boolean) {
  useEffect(() => signal("needs", needs), [needs]);
  useEffect(() => () => signal("needs", false), []);
}

// The perches shown, by name: "home", "pill", "q:<question id>", "tab:<view>".
const perches = new Map<string, HTMLElement>();
// What the eye does when its perch leaves the page under it.
let perchLeaves: ((perch: HTMLElement) => void) | undefined;

// EyePerch is a place the eye may rest: an empty point, laid out by its stylesheet, that the eye is centred on. It is
// the place of the element that holds it, its parent, or of the one around marked data-eye-place: the eye rests there
// while the focus is in that element. A perch that opens for it (the socket of Write to the Djinn) shows it behind a porthole that opens as it lands: the eye blinks
// once it is open.
export function EyePerch({ name, opens }: { name: string; opens?: boolean }) {
  const ref = useCallback(
    (element: HTMLSpanElement | null) => {
      if (!element) return;
      perches.set(name, element);
      signalled();
      return () => {
        if (perches.get(name) === element) perches.delete(name);
        perchLeaves?.(element);
        signalled();
      };
    },
    [name],
  );
  return (
    <span
      ref={ref}
      className="djinn-eye-perch"
      data-perch={name}
      data-opens={opens ? "" : undefined}
      aria-hidden="true"
    />
  );
}

// A perch counts while it is laid out and not under a page made inert (a tilasm open over it).
function shown(element: HTMLElement): boolean {
  return (
    element.isConnected &&
    element.getClientRects().length > 0 &&
    !element.closest("[inert]")
  );
}

const reduced = () =>
  document.documentElement.dataset.motion === "reduced" ||
  (typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches);

const scaleOf = (element: Element | null) =>
  (element &&
    parseFloat(
      getComputedStyle(element).getPropertyValue("--djinn-eye-scale"),
    )) ||
  1;

const centre = (rect: DOMRect) => ({
  x: rect.left + rect.width / 2,
  y: rect.top + rect.height / 2,
});

// typing says whether a field has the focus: the developer writes.
function typing(): boolean {
  const active = document.activeElement;
  return (
    active instanceof HTMLElement &&
    (active.isContentEditable ||
      active.matches(
        "textarea, select, input:not([type=checkbox], [type=radio], [type=button], [type=submit], [type=range])",
      ))
  );
}

// focusPerch is the perch of the element that has the focus, if it has one: the nearest whose place holds it.
function focusPerch(): string | undefined {
  const active = document.activeElement;
  if (!active || active === document.body) return undefined;
  let found: { name: string; place: Element } | undefined;
  for (const [name, element] of perches) {
    if (!shown(element)) continue;
    const place =
      element.parentElement?.closest("[data-eye-place]") ??
      element.parentElement;
    if (place?.contains(active) && (!found || found.place.contains(place)))
      found = { name, place };
  }
  return found?.name;
}

const dialogOpen = () =>
  !!document.querySelector(
    '.modal-backdrop, [role="dialog"][aria-modal="true"]',
  );

// The quiet before the first flight: the page settles before the eye leaves the seal.
const ARRIVAL = 700;
// A blink now and then, never more often than every 9 s.
const BLINK_MIN = 9_000;
const BLINK_SPREAD = 11_000;
const BLINK = 110;
const FADE = 120;
// The porthole of a perch that opens takes this long to open (djinn-eye.css, .lead-socket), then the eye blinks.
const OPENED = 640;

// passUnder clips the flight layer out of the surfaces the eye flies under (workspace-action.tsx, data-eye-under): a
// path of the window with a hole in each one's rounded shape, read once as the flight starts, which none of them leaves.
function passUnder(layer: HTMLElement) {
  const holes = [...document.querySelectorAll<HTMLElement>("[data-eye-under]")]
    .filter(shown)
    .map((element) => {
      const {
        left: x,
        top: y,
        width: w,
        height: h,
      } = element.getBoundingClientRect();
      const r = Math.min(
        parseFloat(getComputedStyle(element).borderTopLeftRadius) || 0,
        w / 2,
        h / 2,
      );
      const arc = (toX: number, toY: number) =>
        `A${r} ${r} 0 0 1 ${toX} ${toY}`;
      return `M${x + r} ${y}H${x + w - r}${arc(x + w, y + r)}V${y + h - r}${arc(x + w - r, y + h)}H${x + r}${arc(x, y + h - r)}V${y + r}${arc(x + r, y)}Z`;
    });
  layer.style.clipPath = holes.length
    ? `path(evenodd, "M0 0H${window.innerWidth}V${window.innerHeight}H0Z${holes.join("")}")`
    : "";
}

// Eye moves one element, built once, between the perches and the flight layer.
class Eye {
  private at = "";
  private parked?: { scale: number };
  private flying?: Animation;
  private flyingUntil = 0;
  private quietUntil = performance.now() + ARRIVAL;
  private pressed = false;
  private timer?: ReturnType<typeof setTimeout>;
  private blinkTimer?: ReturnType<typeof setTimeout>;
  private openTimer?: ReturnType<typeof setTimeout>;
  private readonly layer: HTMLDivElement;
  private readonly stops: (() => void)[] = [];

  constructor(
    private readonly el: HTMLElement,
    layer: HTMLDivElement,
  ) {
    this.layer = layer;
    document.body.appendChild(layer);
    signalListeners.add(this.schedule);
    perchLeaves = this.leave;
    const on = <E extends Event>(
      target: EventTarget,
      type: string,
      handle: (e: E) => void,
      capture = true,
    ) => {
      target.addEventListener(type, handle as EventListener, {
        capture,
        passive: true,
      });
      this.stops.push(() =>
        target.removeEventListener(type, handle as EventListener, capture),
      );
    };
    on(window, "keydown", () => {
      if (!typing()) this.quiet(AFTER_KEY);
    });
    on(document, "scroll", () => this.quiet(AFTER_SCROLL));
    // A flight cannot follow a page the developer scrolls under it: it lands now. The page's own small scrolls (its
    // scroll anchoring keeping your place) let it fly on.
    const scrolled = () => {
      this.quiet(AFTER_SCROLL);
      this.flying?.finish();
    };
    on(window, "wheel", scrolled);
    on(window, "touchmove", scrolled);
    on(window, "pointerdown", () => (this.pressed = true));
    const release = () => {
      if (!this.pressed) return;
      this.pressed = false;
      this.quiet(250);
      this.schedule();
    };
    on(window, "pointerup", release);
    on(window, "pointercancel", release);
    on(window, "blur", release, false);
    const later = () => setTimeout(this.schedule, 0);
    on(document, "focusin", later);
    on(document, "focusout", later);
    this.stops.push(onStillness(this.schedule));
    this.schedule();
    this.blinkLater();
  }

  stop() {
    clearTimeout(this.timer);
    clearTimeout(this.blinkTimer);
    clearTimeout(this.openTimer);
    this.flying?.cancel();
    this.stops.forEach((stop) => stop());
    signalListeners.delete(this.schedule);
    if (perchLeaves === this.leave) perchLeaves = undefined;
    this.el.remove();
    this.layer.remove();
  }

  private quiet(ms: number) {
    this.quietUntil = Math.max(this.quietUntil, performance.now() + ms);
  }

  private target(focus: string | undefined): EyeTarget {
    const names = new Set<string>();
    for (const [name, element] of perches) if (shown(element)) names.add(name);
    return eyeTarget({
      presence: presenceNow(),
      live: signals.live,
      stopped: signals.stopped,
      blocking: signals.blocking,
      needs: signals.needs,
      perches: names,
      focus,
    });
  }

  // schedule moves the eye to its target as soon as the calm gate lets it; the last target wins.
  readonly schedule = () => {
    clearTimeout(this.timer);
    this.timer = undefined;
    const focus = focusPerch();
    const target = this.target(focus);
    if (this.flying) return;
    // Not shown yet: it opens in the seal, and leaves it once the gate lets it.
    const home = perches.get("home");
    if (
      target.perch &&
      !this.el.isConnected &&
      !this.parked &&
      home &&
      shown(home)
    ) {
      this.at = "home";
      this.land(home, {
        ...target,
        perch: "home",
        mood: target.perch === "home" ? target.mood : "resting",
      });
    }
    if (target.perch === this.at && !this.parked) {
      this.show(target);
      return;
    }
    const wait = calmWait({
      now: performance.now(),
      // A field that gets the focus takes the eye to its place; once there, nothing else moves it while you type.
      typing: typing() && target.perch !== focus,
      pressed: this.pressed,
      dialog: dialogOpen(),
      still: still(),
      quietUntil: this.quietUntil,
      flyingUntil: this.flyingUntil,
    });
    // Held until a field loses the focus, the button comes up, the dialog closes or the window wakes: each calls back.
    if (wait === Infinity) {
      this.el.toggleAttribute("data-shine", this.shines(target));
      return;
    }
    if (wait > 0) {
      this.timer = setTimeout(this.schedule, wait + 10);
      return;
    }
    this.move(target);
  };

  private shines(target: EyeTarget) {
    return target.shine && target.mood !== "asleep";
  }

  private show(target: EyeTarget) {
    this.el.dataset.mood = target.mood;
    this.el.toggleAttribute("data-shine", this.shines(target));
  }

  // leave keeps the eye where it stood when its perch leaves the page (its question answered): it waits there, above
  // the page, for its next flight.
  private readonly leave = (perch: HTMLElement) => {
    if (!perch.contains(this.el)) return;
    const box = this.el.getBoundingClientRect();
    this.at = "";
    if (!box.width) {
      this.el.remove();
      this.parked = undefined;
      return;
    }
    const scale = scaleOf(perch);
    this.layer.appendChild(this.el);
    const { x, y } = centre(box);
    this.el.style.transform = `translate(${x - this.el.offsetWidth / 2}px, ${y - this.el.offsetHeight / 2}px) scale(${scale})`;
    this.parked = { scale };
    this.at = "";
  };

  private land(perch: HTMLElement, target: EyeTarget) {
    perch.appendChild(this.el);
    this.el.style.transform = "";
    this.el.style.removeProperty("--djinn-eye-look-x");
    this.el.style.removeProperty("--djinn-eye-look-y");
    delete this.el.dataset.flying;
    this.layer.style.clipPath = "";
    this.parked = undefined;
    this.show(target);
    clearTimeout(this.openTimer);
    if (perch.hasAttribute("data-opens"))
      this.openTimer = setTimeout(() => this.blink(), OPENED);
  }

  private move(target: EyeTarget) {
    const to = target.perch ? perches.get(target.perch) : undefined;
    this.at = target.perch;
    if (!to) {
      this.el.remove();
      this.parked = undefined;
      return;
    }
    const placed = this.el.isConnected;
    const from = placed ? centre(this.el.getBoundingClientRect()) : undefined;
    const fromScale = this.parked?.scale ?? scaleOf(this.el.parentElement);
    const end = centre(to.getBoundingClientRect());
    const view = { width: window.innerWidth, height: window.innerHeight };
    const inView =
      end.x >= 0 && end.y >= 0 && end.x <= view.width && end.y <= view.height;
    // First shown, or its place out of sight: it is there, without a flight.
    if (!from || !inView) {
      this.land(to, target);
      return;
    }
    // Less motion asked: a fade out here, a fade in there.
    if (reduced()) {
      this.flyingUntil = performance.now() + 2 * FADE;
      const out = this.el.animate([{ opacity: 1 }, { opacity: 0 }], {
        duration: FADE,
        fill: "forwards",
      });
      this.flying = out;
      out.onfinish = () => {
        this.flying = undefined;
        this.land(to, target);
        out.cancel();
        this.el.animate([{ opacity: 0 }, { opacity: 1 }], { duration: FADE });
        this.schedule();
      };
      return;
    }
    const start = clampTo(from, view, 12);
    const plan = flight(start, end, fromScale, scaleOf(to));
    // It opens to leave, and looks where it goes.
    if (this.el.dataset.mood === "asleep" || !this.el.dataset.mood)
      this.el.dataset.mood = "watching";
    const dx = end.x - start.x;
    const dy = end.y - start.y;
    const look = Math.hypot(dx, dy) || 1;
    this.el.style.setProperty("--djinn-eye-look-x", `${(2 * dx) / look}px`);
    this.el.style.setProperty("--djinn-eye-look-y", `${dy / look}px`);
    this.el.dataset.flying = "";
    passUnder(this.layer);
    this.layer.appendChild(this.el);
    this.el.style.transform = "";
    const w = this.el.offsetWidth;
    const h = this.el.offsetHeight;
    const anim = this.el.animate(
      plan.steps.map((p) => ({
        transform: `translate(${p.x - w / 2}px, ${p.y - h / 2}px) scale(${p.scale})`,
      })),
      { duration: plan.duration, easing: "linear", fill: "forwards" },
    );
    this.flying = anim;
    this.flyingUntil = performance.now() + plan.duration;
    anim.onfinish = () => {
      if (this.flying !== anim) return;
      this.flying = undefined;
      // Its perch may have gone while it flew: the next schedule finds another.
      if (to.isConnected) this.land(to, target);
      else {
        this.layer.style.clipPath = "";
        this.leave(this.layer);
      }
      anim.cancel();
      this.schedule();
    };
  }

  private blinkLater() {
    this.blinkTimer = setTimeout(
      () => {
        this.blink();
        this.blinkLater();
      },
      BLINK_MIN + Math.random() * BLINK_SPREAD,
    );
  }

  // blink closes the eye for a moment, by an attribute a timer sets: no loop. Never at rest, asleep, in flight, or with less
  // motion asked.
  private blink() {
    const mood = this.el.dataset.mood;
    if (
      still() ||
      reduced() ||
      this.flying ||
      !this.el.isConnected ||
      mood === "asleep" ||
      mood === "restarting"
    )
      return;
    this.el.toggleAttribute("data-blink", true);
    setTimeout(() => this.el.removeAttribute("data-blink"), BLINK);
  }
}

// DjinnEye shows the eye in the window: mounted once, beside the pages; live is Djinn's stream.
export function DjinnEye({ live }: { live: boolean }) {
  const [eye] = useState(() => {
    const element = document.createElement("span");
    element.className = "djinn-eye";
    element.setAttribute("aria-hidden", "true");
    return element;
  });
  useEffect(() => signal("live", live), [live]);
  useEffect(() => {
    const layer = document.createElement("div");
    layer.className = "djinn-eye-flight";
    layer.setAttribute("aria-hidden", "true");
    const controller = new Eye(eye, layer);
    return () => controller.stop();
  }, [eye]);
  return createPortal(<EyeFace />, eye);
}

// EyeFace is the drawing, in layers each moved by transform or opacity alone: the flame's shine; the eyeball (a blink
// squeezes it); inside the almond, the iris (it looks by moving), and the lid the dark theme half lowers; the outline;
// the closed eye of its sleep, with a z.
function EyeFace() {
  return (
    <>
      <span className="djinn-eye-shine" />
      <span className="djinn-eye-ball">
        <svg viewBox="0 0 44 26">
          <g
            className="djinn-eye-rays"
            stroke="var(--eye-ray)"
            strokeWidth="1.6"
            strokeLinecap="round"
          >
            <path d="M22 2.4V-1.6M11.2 5.2L8.8 1.9M32.8 5.2L35.2 1.9" />
          </g>
          <path
            d="M2 13 C10 1.5, 34 1.5, 42 13 C34 24.5, 10 24.5, 2 13 Z"
            fill="var(--eye-white)"
          />
        </svg>
        <span className="djinn-eye-socket">
          <span className="djinn-eye-iris">
            <svg viewBox="0 0 44 26">
              <g
                fill="var(--eye-star)"
                stroke="var(--eye-line)"
                strokeWidth="1.1"
                strokeLinejoin="round"
              >
                <rect x="16.6" y="7.6" width="10.8" height="10.8" />
                <rect
                  x="16.6"
                  y="7.6"
                  width="10.8"
                  height="10.8"
                  transform="rotate(45 22 13)"
                />
              </g>
              <circle
                cx="22"
                cy="13"
                r="4.4"
                fill="var(--eye-iris)"
                stroke="var(--eye-line)"
                strokeWidth="1.1"
              />
              <g className="djinn-eye-flame">
                <path
                  d="M22 9.2 C 24.5 12, 24.1 15.6, 22 16.3 C 19.9 15.6, 19.5 12, 22 9.2 Z"
                  fill="var(--eye-flame)"
                  stroke="var(--eye-flame-edge)"
                  strokeWidth="0.5"
                  strokeLinejoin="round"
                />
                <path
                  d="M22 10.9 C 23.2 12.8, 22.95 15, 22 15.4 C 21.05 15, 20.8 12.8, 22 10.9 Z"
                  fill="var(--eye-flame-in)"
                />
              </g>
            </svg>
          </span>
          <svg className="djinn-eye-lid" viewBox="0 0 44 26">
            <path
              d="M0 0 H44 V11.4 C32 6.2, 14 6.6, 0 9.8 Z"
              fill="var(--eye-lid)"
            />
            <path
              d="M5 10.1 C16 6.9, 31 6.6, 40.5 11"
              fill="none"
              stroke="var(--eye-line)"
              strokeWidth="1.6"
              strokeLinecap="round"
            />
          </svg>
        </span>
        <svg viewBox="0 0 44 26">
          <path
            d="M2 13 C10 1.5, 34 1.5, 42 13 C34 24.5, 10 24.5, 2 13 Z"
            fill="none"
            stroke="var(--eye-line)"
            strokeWidth="2"
            strokeLinejoin="round"
          />
        </svg>
      </span>
      <svg className="djinn-eye-closed" viewBox="0 0 44 26">
        <path
          d="M2 13 C10 1.5, 34 1.5, 42 13 C34 20.5, 10 20.5, 2 13 Z"
          fill="var(--eye-white)"
        />
        <path
          d="M3 13 C12 21, 32 21, 41 13"
          fill="none"
          stroke="var(--eye-line)"
          strokeWidth="2.2"
          strokeLinecap="round"
        />
        <path
          d="M13 18.6L11.4 22.2M22 20.2V24.2M31 18.6L32.6 22.2"
          stroke="var(--eye-ray)"
          strokeWidth="1.6"
          strokeLinecap="round"
        />
        <path
          d="M41 1.5h4.2l-4.2 4.6h4.2"
          fill="none"
          stroke="var(--eye-ray)"
          strokeWidth="1.3"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </>
  );
}
