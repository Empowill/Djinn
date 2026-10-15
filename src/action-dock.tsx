import { ChevronLeft, ChevronRight, X } from "lucide-react";
import {
  createContext,
  type KeyboardEvent,
  type Ref,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
  use,
} from "react";
import { createPortal } from "react-dom";
import { useReducedMotion } from "motion/react";

import { t } from "./i18n";
import "./action-dock.css";

export interface ActionDockItem {
  id: string;
  /** External Djinn focus target, such as question-<id> or lead-prompt-<wish>. */
  targetId?: string;
  summary: string;
  content: ReactNode;
}

type ActionDockItemContextValue = {
  active: boolean;
  headerTarget: HTMLElement | null;
};

type DockTransition = {
  fromId: string;
  fromSummary: string;
  direction: "next" | "previous";
};

const ActionDockItemContext = createContext<ActionDockItemContextValue | null>(
  null,
);

/** Mounts actions from the active decision into the dock's shared header slot. */
export function ActionDockHeader({ children }: { children: ReactNode }) {
  const context = use(ActionDockItemContext);
  if (!context?.active || !context.headerTarget) return null;
  return createPortal(children, context.headerTarget);
}

/**
 * One pending wish decision at a time, anchored in the shared floating action.
 * Every item stays mounted while navigation hides it, so question drafts keep
 * their local state without putting hidden controls into the accessibility tree.
 */
export function ActionDock({
  items,
  collapsed,
  onCollapsedChange,
  focusRequest = 0,
  focusTarget = "",
  ref,
}: {
  items: ActionDockItem[];
  collapsed: boolean;
  onCollapsedChange: (collapsed: boolean) => void;
  focusRequest?: number;
  focusTarget?: string;
  ref?: Ref<HTMLElement>;
}) {
  const ids = items.map((item) => item.id);
  const idsKey = ids.join("\u0000");
  const previousIds = useRef<string[]>([]);
  const root = useRef<HTMLElement>(null);
  const collapseToggle = useRef<HTMLButtonElement>(null);
  const wasCollapsed = useRef(collapsed);
  const previousItems = useRef<ActionDockItem[]>([]);
  const transitionTimer = useRef<number | undefined>(undefined);
  const [headerTarget, setHeaderTarget] = useState<HTMLDivElement | null>(null);
  const [activeId, setActiveId] = useState(ids[0] ?? "");
  const [direction, setDirection] = useState<"next" | "previous">("next");
  const [carousel, setCarousel] = useState<DockTransition | null>(null);
  const [leavingItem, setLeavingItem] = useState<ActionDockItem | null>(null);
  const [opening, setOpening] = useState(false);
  const handledFocusRequest = useRef(0);
  const protectedFocusTarget = useRef({ request: 0, target: "" });
  const reducedMotion = useReducedMotion() ?? false;

  const beginTransition = useCallback(
    (from: ActionDockItem, transitionDirection: "next" | "previous") => {
      if (transitionTimer.current !== undefined) {
        window.clearTimeout(transitionTimer.current);
      }
      if (reducedMotion) {
        setCarousel(null);
        setLeavingItem(null);
        return;
      }
      setCarousel({
        fromId: from.id,
        fromSummary: from.summary,
        direction: transitionDirection,
      });
      transitionTimer.current = window.setTimeout(() => {
        setCarousel(null);
        setLeavingItem(null);
        transitionTimer.current = undefined;
      }, 240);
    },
    [reducedMotion],
  );

  useEffect(
    () => () => {
      if (transitionTimer.current !== undefined) {
        window.clearTimeout(transitionTimer.current);
      }
    },
    [],
  );

  useEffect(() => {
    const openedFromPill = wasCollapsed.current && !collapsed;
    wasCollapsed.current = collapsed;
    if (!openedFromPill || reducedMotion) {
      if (!openedFromPill) setOpening(false);
      return;
    }
    setOpening(true);
    const timer = window.setTimeout(() => setOpening(false), 240);
    return () => window.clearTimeout(timer);
  }, [collapsed, reducedMotion]);

  useEffect(() => {
    if (
      protectedFocusTarget.current.request !== focusRequest ||
      protectedFocusTarget.current.target !== focusTarget
    ) {
      protectedFocusTarget.current = {
        request: focusRequest,
        target: focusTarget,
      };
    }
  }, [focusRequest, focusTarget]);

  useEffect(() => {
    const previous = previousIds.current;
    const previousItemsSnapshot = previousItems.current;
    const added = ids.find((id) => !previous.includes(id));
    const protectedTarget =
      protectedFocusTarget.current.request === focusRequest &&
      protectedFocusTarget.current.target
        ? items.find(
            (item) =>
              item.id === protectedFocusTarget.current.target ||
              item.targetId === protectedFocusTarget.current.target,
          )
        : undefined;
    const currentItem = previousItemsSnapshot.find(
      (item) => item.id === activeId,
    );
    const currentIndex = previous.indexOf(activeId);
    const nextIndex = Math.min(
      Math.max(currentIndex < 0 ? 0 : currentIndex, 0),
      Math.max(ids.length - 1, 0),
    );
    const nextId =
      protectedTarget?.id ??
      added ??
      (ids.includes(activeId) ? activeId : (ids[nextIndex] ?? ""));
    const nextItem = items.find((item) => item.id === nextId);
    if (currentItem && nextItem && currentItem.id !== nextItem.id) {
      const nextItemIndex = items.findIndex((item) => item.id === nextItem.id);
      const previousItemIndex = previousItemsSnapshot.findIndex(
        (item) => item.id === currentItem.id,
      );
      const transitionDirection =
        nextItemIndex >= previousItemIndex ? "next" : "previous";
      if (!ids.includes(currentItem.id)) setLeavingItem(currentItem);
      beginTransition(currentItem, transitionDirection);
    }
    setActiveId(nextId);
    if (added) {
      setDirection("next");
      onCollapsedChange(false);
    }
    previousIds.current = ids;
    previousItems.current = items;
  }, [
    activeId,
    beginTransition,
    focusRequest,
    ids,
    idsKey,
    items,
    onCollapsedChange,
  ]);

  useEffect(() => {
    if (!focusRequest || handledFocusRequest.current === focusRequest) return;
    const targetItem = focusTarget
      ? items.find(
          (item) => item.id === focusTarget || item.targetId === focusTarget,
        )
      : undefined;
    if (focusTarget && !targetItem) return;
    if (targetItem && targetItem.id !== activeId) {
      protectedFocusTarget.current = {
        request: focusRequest,
        target: focusTarget,
      };
      const current = items.find((item) => item.id === activeId);
      if (current) {
        const currentIndex = items.findIndex((item) => item.id === current.id);
        const targetIndex = items.findIndex(
          (item) => item.id === targetItem.id,
        );
        beginTransition(
          current,
          targetIndex >= currentIndex ? "next" : "previous",
        );
        setDirection(targetIndex >= currentIndex ? "next" : "previous");
      }
      setActiveId(targetItem.id);
      onCollapsedChange(false);
      return;
    }
    let timer: number | undefined;
    let stopped = false;
    const deadline = Date.now() + 1_000;
    const focus = () => {
      if (stopped) return;
      const current = root.current;
      if (!current) {
        if (Date.now() < deadline) timer = window.setTimeout(focus, 40);
        return;
      }
      current.scrollIntoView({
        behavior: reducedMotion ? "auto" : "smooth",
        block: "nearest",
      });
      const activeItem = items.find((item) => item.id === activeId);
      const itemNode = activeItem
        ? Array.from(
            current.querySelectorAll<HTMLElement>("[data-action-dock-item]"),
          ).find((element) => element.dataset.actionDockItem === activeItem.id)
        : undefined;
      const target =
        itemNode?.querySelector<HTMLElement>(
          "button:not(:disabled), textarea:not(:disabled), input:not(:disabled), select:not(:disabled), [tabindex='0']",
        ) ??
        itemNode ??
        current.querySelector<HTMLElement>(
          "h3.workspace-action-attention-title",
        ) ??
        current;
      target.focus({ preventScroll: true });
      if (
        document.activeElement === target ||
        target.contains(document.activeElement)
      ) {
        // Mark only after the deferred focus actually landed. Parent renders can replace `items` and cancel the
        // RAF before this point; leaving the request unhandled lets the next effect retry it.
        handledFocusRequest.current = focusRequest;
      } else if (Date.now() < deadline) {
        timer = window.setTimeout(focus, 40);
      }
    };
    const frame = window.requestAnimationFrame(() => {
      timer = window.setTimeout(focus, 0);
    });
    return () => {
      stopped = true;
      window.cancelAnimationFrame(frame);
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [
    activeId,
    beginTransition,
    focusRequest,
    focusTarget,
    items,
    onCollapsedChange,
    reducedMotion,
  ]);

  const setRoot = (node: HTMLElement | null) => {
    root.current = node;
    if (typeof ref === "function") ref(node);
    else if (ref) ref.current = node;
  };

  const activeIndex = Math.max(
    0,
    items.findIndex((item) => item.id === activeId),
  );
  const move = (delta: -1 | 1) => {
    const nextIndex = activeIndex + delta;
    if (nextIndex < 0 || nextIndex >= items.length) return;
    const current = items[activeIndex];
    if (current) beginTransition(current, delta > 0 ? "next" : "previous");
    protectedFocusTarget.current = { request: focusRequest, target: "" };
    handledFocusRequest.current = focusRequest;
    setLeavingItem(null);
    setDirection(delta > 0 ? "next" : "previous");
    setActiveId(items[nextIndex].id);
    onCollapsedChange(false);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key !== "Escape") return;
    if (
      event.target instanceof HTMLElement &&
      event.target.matches("input, textarea, select, [contenteditable='true']")
    )
      return;
    event.preventDefault();
    onCollapsedChange(true);
  };

  return (
    <section
      ref={setRoot}
      className={`workspace-action-attention${collapsed ? " is-collapsed" : ""}${opening ? " is-opening" : ""}`}
      data-wish-action-dock="true"
      tabIndex={-1}
      aria-label={t("attention.dock_label")}
      onKeyDown={onKeyDown}
    >
      <nav
        className="workspace-action-attention-nav"
        aria-label={t("attention.navigation")}
      >
        <div className="workspace-action-attention-arrows">
          <button
            type="button"
            className="workspace-action-attention-arrow"
            onClick={() => move(-1)}
            disabled={activeIndex <= 0}
            aria-label={t("attention.previous")}
            title={t("attention.previous")}
          >
            <ChevronLeft size={15} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="workspace-action-attention-arrow"
            onClick={() => move(1)}
            disabled={activeIndex >= items.length - 1}
            aria-label={t("attention.next")}
            title={t("attention.next")}
          >
            <ChevronRight size={15} aria-hidden="true" />
          </button>
        </div>
        <div
          className="workspace-action-attention-title-viewport"
          aria-live="polite"
        >
          <h3
            className={`workspace-action-attention-title${carousel ? ` is-entering is-${carousel.direction}` : ""}`}
            tabIndex={-1}
            title={items[activeIndex]?.summary ?? ""}
          >
            <span className="workspace-action-attention-title-text">
              {items[activeIndex]?.summary ?? ""}
            </span>
          </h3>
          {carousel && (
            <span
              className={`workspace-action-attention-title is-leaving is-${carousel.direction}`}
              aria-hidden="true"
            >
              <span className="workspace-action-attention-title-text">
                {carousel.fromSummary}
              </span>
            </span>
          )}
        </div>
        <div
          ref={setHeaderTarget}
          className="workspace-action-attention-header-actions"
        />
        <button
          ref={collapseToggle}
          type="button"
          className="workspace-action-attention-close"
          onClick={() => onCollapsedChange(true)}
          aria-label={t("attention.close")}
          title={t("attention.close")}
        >
          <X size={15} aria-hidden="true" />
        </button>
      </nav>
      <div className="workspace-action-attention-items" aria-live="polite">
        {items.map((item, index) => (
          <ActionDockItemContext
            key={item.id}
            value={{
              active: !collapsed && index === activeIndex,
              headerTarget,
            }}
          >
            <div
              className={`workspace-action-attention-item${!collapsed && index === activeIndex ? ` is-active${carousel?.fromId !== item.id && carousel ? ` is-${carousel.direction}` : ""}` : ""}${carousel?.fromId === item.id && index !== activeIndex ? ` is-leaving is-${carousel.direction}` : ""}`}
              id={
                !collapsed && index === activeIndex
                  ? (item.targetId ?? item.id)
                  : undefined
              }
              data-action-dock-item={item.id}
              aria-hidden={collapsed || index !== activeIndex}
              inert={collapsed || index !== activeIndex ? true : undefined}
              tabIndex={!collapsed && index === activeIndex ? -1 : undefined}
            >
              {item.content}
            </div>
          </ActionDockItemContext>
        ))}
        {leavingItem && !items.some((item) => item.id === leavingItem.id) && (
          <ActionDockItemContext
            value={{ active: false, headerTarget }}
            key={`leaving-${leavingItem.id}`}
          >
            <div
              className={`workspace-action-attention-item is-leaving is-${carousel?.direction ?? direction}`}
              aria-hidden="true"
              inert
            >
              {leavingItem.content}
            </div>
          </ActionDockItemContext>
        )}
      </div>
    </section>
  );
}
