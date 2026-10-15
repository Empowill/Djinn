import { ChevronDown, ChevronRight, X } from "lucide-react";
import {
  type KeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
  isValidElement,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { t } from "./i18n";
import {
  normalizeWorkspaceLayout,
  workspaceDefaultLayout,
  workspacePanel,
  workspacePanelIds,
  type WorkspaceLayoutState,
} from "./wish-workspace-layout";
import {
  readWishWorkspace,
  writeWishWorkspace,
} from "./wish-workspace-storage";
import { useKeepPlace } from "./scroll-anchor";

import "./wish-workspace.css";

export type {
  WorkspaceDirection,
  WorkspaceGridPlacement,
  WorkspaceGridPlan,
  WorkspaceLayoutState,
  WorkspaceNode,
  WorkspacePlacement,
} from "./wish-workspace-layout";

export interface WishWorkspacePanel {
  id: string;
  title: ReactNode;
  /** A compact line available to the navigation card's accessible name. */
  preview?: ReactNode;
  count?: number;
  icon?: ReactNode;
  /** Optional controls rendered in the panel header immediately before the close action. */
  headerActions?: ReactNode;
  children: ReactNode;
  /** Empty optional sections do not appear once the detail has loaded. */
  hidden?: boolean;
  /** The summary is the fixed navigation card and cannot be closed. */
  pinned?: boolean;
}

export interface WishWorkspaceProps {
  wishId: string;
  panels: readonly WishWorkspacePanel[];
  /** The wish heading stays in the left reading column beside the fixed navigation card. */
  head?: ReactNode;
  /** Optional user attention lane placed between the wish heading and content panels. */
  attention?: ReactNode;
  /** Optional content kept above the content stage for callers that use the generic workspace. */
  global?: ReactNode;
  /** Detail data is ready; only then reconcile optional panels and persist the layout. */
  ready?: boolean;
  /** A deep link can select a panel on first render. */
  initialPanel?: string;
  /** Changes when navigation requests the same panel again. */
  initialPanelRequest?: string;
  onPanelChange?: (panelId?: string) => void;
}

const SUMMARY_ID = "summary";
const NAV_COMPACT_THRESHOLD = 1180;
// Keep these values in sync with the panel animation durations in wish-workspace.css. The exit and enter phases are
// deliberately separate so a replacement panel never overlaps the outgoing one for longer than necessary.
const PANEL_EXIT_MS = 70;
const PANEL_ENTER_MS = 100;

type PanelTransition = "idle" | "exiting" | "entering";

function panelText(panel: WishWorkspacePanel): string {
  return typeof panel.title === "string" ? panel.title : panel.id;
}

function nodeText(node: ReactNode): string {
  if (node === null || node === undefined || typeof node === "boolean")
    return "";
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(nodeText).join(" ");
  if (isValidElement(node))
    return nodeText((node.props as { children?: ReactNode }).children);
  return "";
}

function activityKey(panel: WishWorkspacePanel): string {
  return String(panel.count ?? "") + "|" + nodeText(panel.preview);
}

function isTypingTarget(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    !!target.closest("input, textarea, select, [contenteditable='true']")
  );
}

function contentPanelIds(panelIds: readonly string[]): string[] {
  return panelIds.filter((id) => id !== SUMMARY_ID);
}

/**
 * Convert a legacy composed tree to the single-panel representation. The maximized panel wins; otherwise the last
 * leaf is the most recent stable choice in the old tree. All other leaves remain closed in storage, while their React
 * children stay mounted by the workspace renderer.
 */
function activePanelFromLayout(
  state: WorkspaceLayoutState,
  panelIds: readonly string[],
): string | undefined {
  const allowed = new Set(contentPanelIds(panelIds));
  if (state.maximized && allowed.has(state.maximized)) return state.maximized;
  return workspacePanelIds(state.root)
    .filter((id) => allowed.has(id))
    .at(-1);
}

function singlePanelLayout(
  state: WorkspaceLayoutState,
  activeId: string | undefined,
  panelIds: readonly string[],
): WorkspaceLayoutState {
  const contentIds = contentPanelIds(panelIds);
  const active =
    activeId && contentIds.includes(activeId) ? activeId : undefined;
  return {
    ...state,
    root: active
      ? workspacePanel(active)
      : workspaceDefaultLayout(panelIds).root,
    closed: contentIds.filter((id) => id !== active),
    maximized: undefined,
  };
}

function migrateToSinglePanel(
  state: WorkspaceLayoutState,
  panelIds: readonly string[],
): WorkspaceLayoutState {
  return singlePanelLayout(
    state,
    activePanelFromLayout(state, panelIds),
    panelIds,
  );
}

function reducedMotion(): boolean {
  return (
    typeof window !== "undefined" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

function panelClass(phase: PanelTransition, focused: boolean): string {
  return [
    "workspace-panel",
    phase === "entering" ? "is-entering" : "",
    phase === "exiting" ? "is-exiting" : "",
    focused ? "is-focused" : "",
  ]
    .filter(Boolean)
    .join(" ");
}

export function WishWorkspace({
  wishId,
  panels,
  head,
  attention,
  global,
  ready = true,
  initialPanel,
  initialPanelRequest,
  onPanelChange,
}: WishWorkspaceProps) {
  const panelDefinitions = useMemo(() => {
    const byId = new Map<string, WishWorkspacePanel>();
    for (const panel of panels) {
      const existing = byId.get(panel.id);
      if (!existing || (existing.hidden && !panel.hidden))
        byId.set(panel.id, panel);
    }
    return [...byId.values()];
  }, [panels]);
  const visiblePanels = useMemo(
    () => panelDefinitions.filter((panel) => !panel.hidden),
    [panelDefinitions],
  );
  const navigationPanels = ready ? visiblePanels : panelDefinitions;
  const knownPanelIds = useMemo(
    () => panelDefinitions.map((panel) => panel.id),
    [panelDefinitions],
  );
  const visiblePanelIds = useMemo(
    () => visiblePanels.map((panel) => panel.id),
    [visiblePanels],
  );
  const panelIds = useMemo(
    () => (ready ? visiblePanelIds : knownPanelIds),
    [knownPanelIds, ready, visiblePanelIds],
  );
  const panelKey = panelIds.join(",");
  const byId = useMemo(
    () => new Map(panelDefinitions.map((panel) => [panel.id, panel])),
    [panelDefinitions],
  );
  const [layout, setLayout] = useState<WorkspaceLayoutState>(() =>
    migrateToSinglePanel(readWishWorkspace(wishId, panelIds), panelIds),
  );
  const [activePanelId, setActivePanelId] = useState<string | undefined>(() =>
    activePanelFromLayout(layout, panelIds),
  );
  const [layoutOwner, setLayoutOwner] = useState(wishId);
  const [layoutPanelKey, setLayoutPanelKey] = useState(panelKey);
  const [navCompact, setNavCompact] = useState(false);
  const [transition, setTransition] = useState<PanelTransition>("idle");
  const [pendingPanelId, setPendingPanelId] = useState<string | undefined>();
  const [focused, setFocused] = useState("");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [unread, setUnread] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(
      panelDefinitions
        .filter((panel) => panel.id !== SUMMARY_ID)
        .map((panel) => [panel.id, true]),
    ),
  );
  const previousWish = useRef(wishId);
  const previousPanels = useRef(panelKey);
  const initialPanelRef = useRef<string | undefined>(undefined);
  const activityRef = useRef(new Map<string, string>());
  const workspaceRootRef = useRef<HTMLDivElement | null>(null);
  const transitionTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined,
  );
  const transitionTokenRef = useRef(0);
  const activePanelRef = useRef<string | undefined>(activePanelId);
  const transitionRef = useRef<PanelTransition>(transition);
  const pendingPanelRef = useRef<string | undefined>(pendingPanelId);
  const panelIdsRef = useRef(panelIds);
  const onPanelChangeRef = useRef(onPanelChange);
  const navButtonRefs = useRef(new Map<string, HTMLButtonElement>());
  const mouseDownOpenRef = useRef(false);
  const userIntentVersionRef = useRef(0);
  const initialRequestUserVersionRef = useRef(new Map<string, number>());
  activePanelRef.current = activePanelId;
  transitionRef.current = transition;
  pendingPanelRef.current = pendingPanelId;
  panelIdsRef.current = panelIds;
  onPanelChangeRef.current = onPanelChange;

  const clearTransitionTimer = () => {
    transitionTokenRef.current += 1;
    if (transitionTimerRef.current !== undefined) {
      clearTimeout(transitionTimerRef.current);
      transitionTimerRef.current = undefined;
    }
  };

  const scheduleTransition = (callback: () => void, delay: number) => {
    clearTransitionTimer();
    const token = transitionTokenRef.current;
    transitionTimerRef.current = setTimeout(() => {
      transitionTimerRef.current = undefined;
      if (token === transitionTokenRef.current) callback();
    }, delay);
  };

  const commitPanel = (id: string | undefined) => {
    const next =
      id && contentPanelIds(panelIdsRef.current).includes(id) ? id : undefined;
    activePanelRef.current = next;
    setActivePanelId(next);
    setLayout((current) =>
      singlePanelLayout(current, next, panelIdsRef.current),
    );
    setFocused(next ?? "");
    onPanelChangeRef.current?.(next);
  };

  const requestPanel = (
    id?: string,
    immediate = false,
    initialRequestKey?: string,
  ) => {
    const next =
      id && visiblePanelIds.includes(id) && byId.has(id) ? id : undefined;
    const current = activePanelRef.current;
    const phase = transitionRef.current;
    const initialRequestVersion = initialRequestKey
      ? initialRequestUserVersionRef.current.get(initialRequestKey)
      : undefined;
    const staleInitialRequest =
      immediate &&
      initialRequestVersion !== undefined &&
      initialRequestVersion < userIntentVersionRef.current;

    // A parent render can replay an older initialPanel while a newer user click is already transitioning. The replay
    // must not cancel that newer intent; a genuinely new deep-link key records the current intent version below.
    if (staleInitialRequest) return;

    if (immediate && current !== next) {
      clearTransitionTimer();
      pendingPanelRef.current = undefined;
      setPendingPanelId(undefined);
      setTransition(next && !reducedMotion() ? "entering" : "idle");
      commitPanel(next);
      if (next && !reducedMotion())
        scheduleTransition(() => setTransition("idle"), PANEL_ENTER_MS);
      return;
    }

    if (current === next && phase !== "exiting") {
      pendingPanelRef.current = undefined;
      setPendingPanelId(undefined);
      // WishView mirrors the committed panel back through initialPanel. During the enter phase that mirror is an
      // idempotent deep-link replay; leave the transition timer intact so it cannot cut the visual handoff short.
      if (phase === "entering") return;
      setTransition("idle");
      if (next) {
        setFocused(next);
        onPanelChangeRef.current?.(next);
      }
      return;
    }
    if (phase === "exiting") {
      if (next === current) {
        clearTransitionTimer();
        pendingPanelRef.current = undefined;
        setPendingPanelId(undefined);
        setTransition("idle");
        return;
      }
      pendingPanelRef.current = next;
      setPendingPanelId(next);
      return;
    }

    clearTransitionTimer();
    pendingPanelRef.current = next;
    setPendingPanelId(next);
    if (!current || reducedMotion()) {
      pendingPanelRef.current = undefined;
      setPendingPanelId(undefined);
      setTransition(next && !reducedMotion() ? "entering" : "idle");
      commitPanel(next);
      if (next && !reducedMotion())
        scheduleTransition(() => setTransition("idle"), PANEL_ENTER_MS);
      return;
    }

    setTransition("exiting");
    scheduleTransition(() => {
      const committed = pendingPanelRef.current;
      pendingPanelRef.current = undefined;
      setPendingPanelId(undefined);
      commitPanel(committed);
      if (committed && !reducedMotion()) {
        setTransition("entering");
        scheduleTransition(() => setTransition("idle"), PANEL_ENTER_MS);
      } else {
        setTransition("idle");
      }
    }, PANEL_EXIT_MS);
  };
  const requestPanelRef = useRef(requestPanel);
  requestPanelRef.current = requestPanel;

  useLayoutEffect(() => {
    const root = workspaceRootRef.current;
    if (!root) return;
    const updateNavCompact = () => {
      setNavCompact(root.getBoundingClientRect().width < NAV_COMPACT_THRESHOLD);
    };
    updateNavCompact();
    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", updateNavCompact);
      return () => window.removeEventListener("resize", updateNavCompact);
    }
    const observer = new ResizeObserver(updateNavCompact);
    observer.observe(root);
    return () => observer.disconnect();
  }, []);

  // A wish's saved split/maximized tree is reconciled once into the single-panel model. Every panel remains rendered
  // below, so switching views never remounts task/tilasm drafts.
  useEffect(() => {
    if (previousWish.current !== wishId) {
      previousWish.current = wishId;
      previousPanels.current = panelKey;
      clearTransitionTimer();
      setLayoutOwner(wishId);
      setLayoutPanelKey(panelKey);
      const nextLayout = migrateToSinglePanel(
        readWishWorkspace(wishId, panelIds),
        panelIds,
      );
      const nextActive = activePanelFromLayout(nextLayout, panelIds);
      activePanelRef.current = nextActive;
      setLayout(nextLayout);
      setActivePanelId(nextActive);
      setTransition("idle");
      pendingPanelRef.current = undefined;
      setPendingPanelId(undefined);
      setFocused("");
      setExpanded({});
      setUnread(
        Object.fromEntries(
          panelDefinitions
            .filter((panel) => panel.id !== SUMMARY_ID)
            .map((panel) => [panel.id, true]),
        ),
      );
      activityRef.current.clear();
      initialRequestUserVersionRef.current.clear();
      initialPanelRef.current = undefined;
      return;
    }
    if (previousPanels.current === panelKey) return;
    previousPanels.current = panelKey;
    setLayoutPanelKey(panelKey);
    setLayout((current) =>
      migrateToSinglePanel(
        normalizeWorkspaceLayout(current, panelIds),
        panelIds,
      ),
    );
    if (
      activePanelRef.current &&
      !contentPanelIds(panelIds).includes(activePanelRef.current)
    ) {
      activePanelRef.current = undefined;
      setActivePanelId(undefined);
      setTransition("idle");
    }
  }, [panelDefinitions, panelIds, panelKey, wishId]);

  useEffect(() => {
    if (!ready || layoutOwner !== wishId || layoutPanelKey !== panelKey) return;
    writeWishWorkspace(wishId, layout);
  }, [layout, layoutOwner, layoutPanelKey, panelKey, ready, wishId]);

  useEffect(
    () => () => {
      clearTransitionTimer();
    },
    [],
  );

  const panelRequest = initialPanel
    ? wishId + ":" + initialPanel + ":" + (initialPanelRequest ?? "default")
    : undefined;
  useEffect(() => {
    if (
      !ready ||
      !initialPanel ||
      !visiblePanelIds.includes(initialPanel) ||
      !byId.has(initialPanel)
    )
      return;
    if (initialPanelRef.current === panelRequest) return;
    initialPanelRef.current = panelRequest;
    if (panelRequest)
      initialRequestUserVersionRef.current.set(
        panelRequest,
        userIntentVersionRef.current,
      );
    requestPanelRef.current(initialPanel, true, panelRequest);
  }, [byId, initialPanel, panelRequest, ready, visiblePanelIds]);

  const summary = byId.get(SUMMARY_ID) ?? visiblePanels[0];
  const contentPanels = navigationPanels.filter(
    (panel) => panel.id !== summary?.id,
  );

  const clearUnread = (id: string) =>
    setUnread((current) =>
      current[id] ? { ...current, [id]: false } : current,
    );

  const toggleAccordion = (id: string) => {
    clearUnread(id);
    setExpanded((current) => ({ ...current, [id]: !current[id] }));
  };

  const open = (id: string) => {
    if (
      !id ||
      id === summary?.id ||
      !visiblePanelIds.includes(id) ||
      !byId.has(id)
    )
      return;
    userIntentVersionRef.current += 1;
    clearUnread(id);
    requestPanel(id);
  };

  const openFromMouseDown = (
    event: ReactMouseEvent<HTMLButtonElement>,
    id: string,
  ) => {
    if (
      event.button !== 0 ||
      event.detail === 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    ) {
      mouseDownOpenRef.current = false;
      return;
    }
    open(id);
    mouseDownOpenRef.current = true;
  };

  const openFromClick = (
    event: ReactMouseEvent<HTMLButtonElement>,
    id: string,
  ) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      mouseDownOpenRef.current = false;
      return;
    }
    if (event.detail > 0 && mouseDownOpenRef.current) {
      mouseDownOpenRef.current = false;
      return;
    }
    mouseDownOpenRef.current = false;
    open(id);
  };

  const close = (id: string) => {
    if (id === summary?.id || activePanelRef.current !== id) return;
    const navButton = navButtonRefs.current.get(id);
    userIntentVersionRef.current += 1;
    // Clear the parent-controlled initial panel before the exit animation starts. WishView mirrors the committed
    // panel into `initialPanel`; waiting for the delayed commit leaves the old value live for one render and can
    // replay it as a fresh deep link while this panel is closing.
    onPanelChangeRef.current?.(undefined);
    requestPanel(undefined);
    setFocused("");
    if (navButton) {
      window.setTimeout(() => navButton.focus({ preventScroll: true }), 0);
    }
  };

  const keyDown = (event: KeyboardEvent<HTMLElement>, id: string) => {
    if (event.defaultPrevented || isTypingTarget(event.target)) return;
    if (
      event.target !== event.currentTarget &&
      !(
        event.target instanceof HTMLElement &&
        event.target.closest(".workspace-panel-heading")
      )
    )
      return;
    if (
      event.key === "Escape" ||
      event.key === "Delete" ||
      event.key === "Backspace"
    ) {
      const current = panelDefinitions.find((item) => item.id === id);
      if (!current?.pinned) {
        event.preventDefault();
        close(id);
      }
    }
  };

  const activityKeys = useMemo(
    () => new Map(contentPanels.map((item) => [item.id, activityKey(item)])),
    [contentPanels],
  );
  useEffect(() => {
    setUnread((current) => {
      const next = { ...current };
      let changed = false;
      for (const [id, key] of activityKeys) {
        const previous = activityRef.current.get(id);
        if (next[id] === undefined) {
          next[id] = true;
          changed = true;
        }
        if (
          previous !== undefined &&
          previous !== key &&
          activePanelRef.current !== id
        ) {
          next[id] = true;
          changed = true;
        }
        activityRef.current.set(id, key);
      }
      for (const id of activityRef.current.keys()) {
        if (!activityKeys.has(id)) activityRef.current.delete(id);
      }
      return changed ? next : current;
    });
  }, [activityKeys]);

  const renderPanel = (id: string) => {
    const definition = byId.get(id);
    if (!definition || definition.id === summary?.id) return null;
    const isActive = activePanelId === id;
    const phase = isActive ? transition : "idle";
    return (
      <WorkspacePanelFrame
        key={wishId + ":" + id}
        panel={definition}
        hidden={!isActive}
        phase={phase}
        focused={focused === id}
        onFocus={() => {
          if (!isActive) return;
          setFocused(id);
          clearUnread(id);
        }}
        onKeyDown={(event) => keyDown(event, id)}
        onClose={() => close(id)}
      >
        {definition.children}
      </WorkspacePanelFrame>
    );
  };

  const renderSummary = () => {
    if (!summary) return null;
    const selectedPanelId =
      transition === "exiting" ? pendingPanelId : activePanelId;
    return (
      <aside
        className="workspace-summary-card"
        data-wish-panel={summary.id}
        aria-label={t("workspace.sections")}
      >
        <nav
          className="workspace-section-list"
          aria-label={t("workspace.sections")}
        >
          {contentPanels.map((definition) => {
            const isExpanded = !!expanded[definition.id];
            const disabled = !ready && !!definition.hidden;
            const sectionLabel = panelText(definition);
            const sectionTooltip =
              definition.count === undefined
                ? sectionLabel
                : sectionLabel + " (" + definition.count + ")";
            const isSelected = selectedPanelId === definition.id;
            return (
              <div
                className={
                  "workspace-section-row" + (isSelected ? " is-active" : "")
                }
                key={definition.id}
                data-wish-section={definition.id}
                data-wish-selected={isSelected ? "true" : undefined}
                data-wish-unread={unread[definition.id] ? "true" : undefined}
              >
                <button
                  ref={(element) => {
                    if (element)
                      navButtonRefs.current.set(definition.id, element);
                    else navButtonRefs.current.delete(definition.id);
                  }}
                  type="button"
                  className={
                    "workspace-section-toggle" +
                    (isSelected ? " is-active" : "")
                  }
                  aria-label={sectionTooltip}
                  aria-current={isSelected ? "true" : undefined}
                  title={sectionTooltip}
                  disabled={disabled}
                  onMouseDown={(event) =>
                    openFromMouseDown(event, definition.id)
                  }
                  onClick={(event) => openFromClick(event, definition.id)}
                >
                  <span className="workspace-section-icon" aria-hidden="true">
                    {definition.icon}
                  </span>
                  <span className="workspace-section-row-copy">
                    <span className="workspace-section-row-title">
                      <strong>{definition.title}</strong>
                      {definition.count !== undefined && (
                        <span className="count">{definition.count}</span>
                      )}
                    </span>
                    {isExpanded && definition.preview && (
                      <span className="workspace-section-row-preview">
                        {definition.preview}
                      </span>
                    )}
                  </span>
                </button>
                <button
                  type="button"
                  className="workspace-section-preview-toggle"
                  aria-expanded={isExpanded}
                  aria-label={t(
                    isExpanded
                      ? "workspace.preview_hide"
                      : "workspace.preview_show",
                    { section: sectionLabel },
                  )}
                  title={t(
                    isExpanded
                      ? "workspace.preview_hide"
                      : "workspace.preview_show",
                    { section: sectionLabel },
                  )}
                  tabIndex={navCompact ? -1 : undefined}
                  disabled={disabled || !definition.preview}
                  onClick={(event) => {
                    event.stopPropagation();
                    toggleAccordion(definition.id);
                  }}
                >
                  {isExpanded ? (
                    <ChevronDown size={14} aria-hidden="true" />
                  ) : (
                    <ChevronRight size={14} aria-hidden="true" />
                  )}
                </button>
              </div>
            );
          })}
        </nav>
      </aside>
    );
  };

  const hasActivePanel = !!activePanelId;
  return (
    <div className="wish-workspace-shell">
      {global && <div className="workspace-inline-global">{global}</div>}
      <div
        className={["wish-workspace", attention ? "has-attention" : ""]
          .filter(Boolean)
          .join(" ")}
        data-wish-workspace="stage"
        data-wish-nav-compact={navCompact ? "true" : undefined}
        data-wish-active-panel={activePanelId}
        ref={workspaceRootRef}
      >
        {head && <div className="workspace-head-slot">{head}</div>}
        {attention && (
          <div className="workspace-attention-slot">{attention}</div>
        )}
        <main
          className={
            "workspace-content-stage" + (hasActivePanel ? "" : " is-empty")
          }
          data-wish-active={activePanelId}
          aria-label={t("workspace.main_content")}
        >
          {panelDefinitions
            .filter((definition) => definition.id !== summary?.id)
            .map((definition) => renderPanel(definition.id))}
        </main>
        <div className="workspace-summary-slot">{renderSummary()}</div>
      </div>
    </div>
  );
}

function WorkspacePanelFrame({
  panel,
  hidden,
  phase,
  focused,
  onFocus,
  onKeyDown,
  onClose,
  children,
}: {
  panel: WishWorkspacePanel;
  hidden: boolean;
  phase: PanelTransition;
  focused: boolean;
  onFocus: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLElement>) => void;
  onClose: () => void;
  children: ReactNode;
}) {
  const label = panelText(panel);
  const contentRef = useRef<HTMLDivElement | null>(null);
  const keepPlace = useKeepPlace(contentRef);
  return (
    <section
      className={panelClass(phase, focused)}
      data-wish-panel={panel.id}
      hidden={hidden}
      aria-hidden={hidden || phase === "exiting"}
      inert={hidden || phase === "exiting" ? true : undefined}
      aria-labelledby={"workspace-panel-heading-" + panel.id}
      tabIndex={hidden || phase === "exiting" ? -1 : 0}
      onFocus={onFocus}
      onKeyDown={onKeyDown}
    >
      <header className="workspace-panel-header">
        <div className="workspace-panel-heading is-fixed">
          <h2 id={"workspace-panel-heading-" + panel.id}>{panel.title}</h2>
        </div>
        {(panel.headerActions !== undefined || !panel.pinned) && (
          <div className="workspace-panel-actions">
            {panel.headerActions !== undefined && (
              <div className="workspace-panel-header-actions">
                {panel.headerActions}
              </div>
            )}
            {!panel.pinned && (
              <button
                type="button"
                className="workspace-panel-action"
                aria-label={t("workspace.close_panel", { panel: label })}
                title={t("workspace.close")}
                onClick={onClose}
              >
                <X size={14} aria-hidden="true" />
              </button>
            )}
          </div>
        )}
      </header>
      <div className="workspace-panel-content" ref={keepPlace}>
        {children}
      </div>
    </section>
  );
}

// Kept exportable for screen-level tests and small previews that want a compact navigation row.
export function WorkspaceSectionCard({
  panel,
  onOpen,
}: {
  panel: WishWorkspacePanel;
  onOpen: () => void;
}) {
  const mouseDownOpenRef = useRef(false);
  const openFromMouseDown = (event: ReactMouseEvent<HTMLButtonElement>) => {
    if (
      event.button !== 0 ||
      event.detail === 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    ) {
      mouseDownOpenRef.current = false;
      return;
    }
    onOpen();
    mouseDownOpenRef.current = true;
  };
  const openFromClick = (event: ReactMouseEvent<HTMLButtonElement>) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      mouseDownOpenRef.current = false;
      return;
    }
    if (event.detail > 0 && mouseDownOpenRef.current) {
      mouseDownOpenRef.current = false;
      return;
    }
    mouseDownOpenRef.current = false;
    onOpen();
  };
  return (
    <button
      type="button"
      className="workspace-section-row workspace-section-card"
      data-wish-section={panel.id}
      aria-label={t("workspace.open_section", {
        section: panelText(panel),
      })}
      onMouseDown={openFromMouseDown}
      onClick={openFromClick}
    >
      <ChevronRight size={13} aria-hidden="true" />
      <span className="workspace-section-row-copy">
        <span className="workspace-section-row-title">
          <strong>{panel.title}</strong>
          {panel.count !== undefined && (
            <span className="count">{panel.count}</span>
          )}
        </span>
        {panel.preview && (
          <span className="workspace-section-row-preview">{panel.preview}</span>
        )}
      </span>
    </button>
  );
}
