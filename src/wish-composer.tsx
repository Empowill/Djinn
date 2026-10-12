import { memo, useEffect, useRef, useState } from "react";

import { t } from "./i18n";

const PLACEHOLDER_KEYS = [
  "make.placeholder_build",
  "make.placeholder_plan",
  "make.placeholder_research",
] as const;

const PLACEHOLDER_PROMPTS = PLACEHOLDER_KEYS.map((key) => t(key));
const DELETE_DELAY = 8;
const TYPE_DELAY = 48;
const HOLD_DELAY = 1700;
const MAX_CATCH_UP_STEPS = 32;

type PlaceholderAnimation = {
  promptIndex: number;
  character: number;
  deleting: boolean;
  dueAt: number;
};

const AnimatedPlaceholder = memo(function AnimatedPlaceholder({
  empty,
}: {
  empty: boolean;
}) {
  const [value, setValue] = useState(PLACEHOLDER_PROMPTS[0]);
  const animation = useRef<PlaceholderAnimation | null>(null);

  useEffect(() => {
    if (!empty) {
      animation.current = null;
      setValue(PLACEHOLDER_PROMPTS[0]);
      return;
    }
    const media = window.matchMedia("(prefers-reduced-motion: reduce)");
    let frame = 0;
    let cancelled = false;
    let lastPublished = PLACEHOLDER_PROMPTS[0];

    const clearFrame = () => {
      if (!frame) return;
      window.cancelAnimationFrame(frame);
      frame = 0;
    };
    const publish = (next: string) => {
      if (next === lastPublished) return;
      lastPublished = next;
      setValue(next);
    };
    const currentDelay = () => {
      const state = animation.current;
      if (!state) return HOLD_DELAY;
      if (state.deleting) {
        return state.character === PLACEHOLDER_PROMPTS[state.promptIndex].length
          ? HOLD_DELAY
          : DELETE_DELAY;
      }
      return TYPE_DELAY;
    };
    const schedule = () => {
      if (frame || cancelled || !empty || media.matches || document.hidden)
        return;
      frame = window.requestAnimationFrame(step);
    };
    const reset = (now: number) => {
      clearFrame();
      const first = PLACEHOLDER_PROMPTS[0];
      animation.current = {
        promptIndex: 0,
        character: first.length,
        deleting: true,
        dueAt: now + HOLD_DELAY,
      };
      lastPublished = first;
      setValue(first);
      schedule();
    };
    const pause = () => clearFrame();
    const resume = () => {
      if (cancelled || !empty || media.matches || document.hidden) return;
      const now = performance.now();
      const state = animation.current;
      if (state) state.dueAt = now + currentDelay();
      schedule();
    };
    const step = (now: number) => {
      frame = 0;
      if (cancelled || !empty || media.matches || document.hidden) return;
      const state = animation.current;
      if (!state) return;

      let steps = 0;
      while (now >= state.dueAt && steps < MAX_CATCH_UP_STEPS) {
        const prompt = PLACEHOLDER_PROMPTS[state.promptIndex];
        if (state.deleting) {
          state.character = Math.max(0, state.character - 1);
          if (state.character === 0) {
            state.deleting = false;
            state.promptIndex =
              (state.promptIndex + 1) % PLACEHOLDER_PROMPTS.length;
            state.dueAt += TYPE_DELAY;
          } else {
            state.dueAt += DELETE_DELAY;
          }
        } else {
          state.character = Math.min(prompt.length, state.character + 1);
          if (state.character === prompt.length) {
            state.deleting = true;
            state.dueAt += HOLD_DELAY;
          } else {
            state.dueAt += TYPE_DELAY;
          }
        }
        steps++;
      }
      if (steps === MAX_CATCH_UP_STEPS && now >= state.dueAt)
        state.dueAt = now + currentDelay();
      publish(PLACEHOLDER_PROMPTS[state.promptIndex].slice(0, state.character));
      schedule();
    };
    const restart = () => {
      const now = performance.now();
      if (media.matches) {
        clearFrame();
        const first = PLACEHOLDER_PROMPTS[0];
        animation.current = {
          promptIndex: 0,
          character: first.length,
          deleting: true,
          dueAt: Number.POSITIVE_INFINITY,
        };
        lastPublished = first;
        setValue(first);
        return;
      }
      reset(now);
    };

    restart();
    const onMotionPreferenceChange = () => restart();
    const onVisibilityChange = () => {
      if (document.hidden) pause();
      else resume();
    };
    media.addEventListener?.("change", onMotionPreferenceChange);
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      cancelled = true;
      clearFrame();
      media.removeEventListener?.("change", onMotionPreferenceChange);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [empty]);

  if (!empty) return null;
  return (
    <span className="wish-composer-placeholder" aria-hidden="true">
      <span className="wish-composer-caret" />
      {value}
    </span>
  );
});

// The request is deliberately a native textarea: it keeps the browser's caret, selection, undo stack and paste
// behavior while sending plain Markdown-compatible text to the API. Formatting controls are intentionally absent.
export function WishComposer({
  id,
  describedBy,
  disabled,
  invalid = false,
  onChange,
}: {
  id: string;
  describedBy?: string;
  disabled: boolean;
  invalid?: boolean;
  onChange: (markdown: string, hasText: boolean) => void;
}) {
  const [empty, setEmpty] = useState(true);

  const update = (value: string) => {
    const hasText = Boolean(value.replace(/[\s\p{Cf}]/gu, ""));
    setEmpty(!hasText);
    onChange(value, hasText);
  };

  return (
    <div
      className={`wish-composer ${disabled ? "is-disabled" : ""}`}
      data-empty={empty}
    >
      <AnimatedPlaceholder empty={empty} />
      <textarea
        id={id}
        name="prompt"
        rows={8}
        aria-multiline="true"
        aria-required="true"
        aria-disabled={disabled}
        aria-invalid={invalid || undefined}
        aria-labelledby={`${id}-label`}
        aria-describedby={describedBy}
        disabled={disabled}
        className="wish-composer-input"
        placeholder=""
        onChange={(event) => update(event.target.value)}
      />
    </div>
  );
}
