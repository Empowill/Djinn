import { useEffect, useState } from "react";

const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

/**
 * Reads both motion preferences used by the renderer. The document attribute
 * is controlled by Djinn's persisted setting; the media query reflects the
 * operating system preference.
 */
export function readReducedMotion(): boolean {
  const documentReduced =
    typeof document !== "undefined" &&
    document.documentElement?.dataset.motion === "reduced";
  const systemReduced =
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia(REDUCED_MOTION_QUERY).matches;
  return documentReduced || systemReduced;
}

/**
 * Subscribes to the two preference sources and returns a complete cleanup
 * function. The media-query listener supports both browser APIs because the
 * desktop shell can run on older Electron versions as well.
 */
export function subscribeReducedMotion(
  onChange: (reduced: boolean) => void,
): () => void {
  const update = () => onChange(readReducedMotion());
  let media: MediaQueryList | undefined;
  let modernMediaListener = false;

  if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
    media = window.matchMedia(REDUCED_MOTION_QUERY);
    if (typeof media.addEventListener === "function") {
      media.addEventListener("change", update);
      modernMediaListener = true;
    } else if (typeof media.addListener === "function") {
      media.addListener(update);
    }
  }

  const root = typeof document !== "undefined" ? document.documentElement : null;
  const observer =
    root && typeof MutationObserver !== "undefined"
      ? new MutationObserver(update)
      : undefined;
  observer?.observe(root!, {
    attributes: true,
    attributeFilter: ["data-motion"],
  });

  update();

  return () => {
    observer?.disconnect();
    if (!media) return;
    if (modernMediaListener) media.removeEventListener("change", update);
    else if (typeof media.removeListener === "function")
      media.removeListener(update);
  };
}

/** Reactive reduced-motion preference for Motion transitions. */
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(readReducedMotion);
  useEffect(() => subscribeReducedMotion(setReduced), []);
  return reduced;
}

