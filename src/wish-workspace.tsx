import { Maximize2, Minimize2, X } from "lucide-react";
import { LayoutGroup } from "motion/react";
import {
  type DragEvent,
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { t } from "./i18n";
import {
  closeWorkspacePanel,
  composeWorkspace,
  maximizeWorkspacePanel,
  moveWorkspacePanel,
  normalizeWorkspaceLayout,
  openWorkspacePanel,
  restoreWorkspace,
  type WorkspaceLayoutState,
  type WorkspaceNode,
  type WorkspacePlacement,
  workspaceHasPanel,
} from "./wish-workspace-layout";
import {
  readWishWorkspace,
  writeWishWorkspace,
} from "./wish-workspace-storage";
import { useKeepPlace } from "./scroll-anchor";

import "./wish-workspace.css";

export type {
  WorkspaceDirection,
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
  children: ReactNode;
  /** Empty optional sections do not appear once the detail has loaded. */
  hidden?: boolean;
  /** The summary is the fixed navigation card and cannot be closed or moved. */
  pinned?: boolean;
}

export interface WishWorkspaceProps {
  wishId: string;
  panels: readonly WishWorkspacePanel[];
  /** The wish heading stays in the left reading column beside the fixed navigation card. */
  head?: ReactNode;
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

interface WorkspacePanelRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

/**
 * Flatten the saved tree into reading rows while keeping every React frame as a direct child of one stage.
 * Vertical splits append rows. Horizontal splits align their child rows and then chunk each row to two columns, so a
 * legacy nested horizontal tree can never produce a four-column strip or shrink a panel into an unreadable sliver.
 */
function workspaceLeafRows(node: WorkspaceNode, compact = false): string[][] {
  if (node.type === "panel") return [[node.id]];
  if (!node.children.length) return [];
  if (compact || node.direction === "vertical")
    return node.children.flatMap((child) => workspaceLeafRows(child, compact));

  const childRows = node.children.map((child) =>
    workspaceLeafRows(child, compact),
  );
  const rowCount = Math.max(...childRows.map((rows) => rows.length), 0);
  const rows: string[][] = [];
  for (let row = 0; row < rowCount; row += 1) {
    const aligned = childRows.flatMap((child) => child[row] ?? []);
    for (let index = 0; index < aligned.length; index += 2)
      rows.push(aligned.slice(index, index + 2));
  }
  return rows;
}

/** Convert reading rows into percentage rectangles without nesting the React frames. */
function workspacePanelRects(
  rows: readonly (readonly string[])[],
): Map<string, WorkspacePanelRect> {
  const result = new Map<string, WorkspacePanelRect>();
  if (!rows.length) return result;
  const rowHeight = 100 / rows.length;
  rows.forEach((row, rowIndex) => {
    const columnCount = row.length;
    if (!columnCount) return;
    row.forEach((id, columnIndex) => {
      result.set(id, {
        left: (100 / columnCount) * columnIndex,
        top: rowHeight * rowIndex,
        width: 100 / columnCount,
        height: rowHeight,
      });
    });
  });
  return result;
}

function panelText(panel: WishWorkspacePanel): string {
  return typeof panel.title === "string" ? panel.title : panel.id;
}

function isTypingTarget(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    !!target.closest("input, textarea, select, [contenteditable='true']")
  );
}

function dropPlacement(
  event: DragEvent<HTMLElement>,
): WorkspacePlacement | undefined {
  const rect = event.currentTarget.getBoundingClientRect();
  if (!rect.width || !rect.height) return undefined;
  const x = (event.clientX - rect.left) / rect.width;
  const y = (event.clientY - rect.top) / rect.height;
  // The nearest edge chooses the split direction. This keeps a side-by-side panel's tall rectangle composable by
  // dropping on its left or right edge, without adding a second row of arrows or placement buttons to every panel.
  const edges: [number, WorkspacePlacement][] = [
    [x, "left"],
    [1 - x, "right"],
    [y, "top"],
    [1 - y, "bottom"],
  ];
  return edges.reduce((nearest, edge) =>
    edge[0] < nearest[0] ? edge : nearest,
  )[1];
}

export function WishWorkspace({
  wishId,
  panels,
  head,
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
    readWishWorkspace(wishId, panelIds),
  );
  const [compact, setCompact] = useState(
    () => typeof window !== "undefined" && window.innerWidth <= 900,
  );
  const [layoutOwner, setLayoutOwner] = useState(wishId);
  const [layoutPanelKey, setLayoutPanelKey] = useState(panelKey);
  const [dragging, setDragging] = useState("");
  const [focused, setFocused] = useState("");
  const previousWish = useRef(wishId);
  const previousPanels = useRef(panelKey);
  const initialPanelRef = useRef<string | undefined>(undefined);

  useEffect(() => {
    const media = window.matchMedia("(max-width: 900px)");
    const update = () => setCompact(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  // Keep a wish's saved composition isolated when React reuses this screen for another wish. Optional sections are
  // reconciled only after detail loading, so a saved journal, rights or instruction panel is not lost on first paint.
  useEffect(() => {
    if (previousWish.current !== wishId) {
      previousWish.current = wishId;
      previousPanels.current = panelKey;
      setLayoutOwner(wishId);
      setLayoutPanelKey(panelKey);
      setLayout(readWishWorkspace(wishId, panelIds));
      setFocused("");
      return;
    }
    if (previousPanels.current === panelKey) return;
    previousPanels.current = panelKey;
    setLayoutPanelKey(panelKey);
    setLayout((current) => normalizeWorkspaceLayout(current, panelIds));
  }, [panelIds, panelKey, wishId]);

  useEffect(() => {
    if (!ready || layoutOwner !== wishId || layoutPanelKey !== panelKey) return;
    writeWishWorkspace(wishId, layout);
  }, [layout, layoutOwner, layoutPanelKey, panelKey, ready, wishId]);

  const panelRequest = initialPanel
    ? `${wishId}:${initialPanel}:${initialPanelRequest ?? "default"}`
    : undefined;
  useLayoutEffect(() => {
    if (
      !ready ||
      !initialPanel ||
      !visiblePanelIds.includes(initialPanel) ||
      !byId.has(initialPanel)
    )
      return;
    if (initialPanelRef.current === panelRequest) return;
    initialPanelRef.current = panelRequest;
    setLayout((current) => openWorkspacePanel(current, initialPanel));
  }, [byId, initialPanel, panelRequest, ready, visiblePanelIds]);

  const summary = byId.get(SUMMARY_ID) ?? visiblePanels[0];
  const contentPanels = navigationPanels.filter(
    (panel) => panel.id !== summary?.id,
  );
  const panel = (id: string) => byId.get(id);
  const update = (
    next: (current: WorkspaceLayoutState) => WorkspaceLayoutState,
  ) => setLayout((current) => next(current));

  const open = (id: string) => {
    if (
      !id ||
      id === summary?.id ||
      !visiblePanelIds.includes(id) ||
      !byId.has(id)
    )
      return;
    update((current) => {
      // Selecting a row gives that section the whole reading area. Any prior split remains in the saved tree and
      // comes back when the panel is restored; composition itself is reserved for a deliberate content drag.
      return openWorkspacePanel(current, id);
    });
    onPanelChange?.(id);
  };

  const close = (id: string) => {
    if (id === summary?.id) return;
    update((state) => closeWorkspacePanel(state, id));
    onPanelChange?.();
  };

  const maximize = (id: string) => {
    if (id === summary?.id) return;
    update((current) => maximizeWorkspacePanel(current, id));
    onPanelChange?.(id);
  };

  const restore = () => {
    update(restoreWorkspace);
    onPanelChange?.();
  };

  const compose = (
    id: string,
    target: string,
    placement: WorkspacePlacement,
  ) => {
    if (
      !id ||
      !target ||
      id === target ||
      id === summary?.id ||
      target === summary?.id ||
      !byId.has(id) ||
      !byId.has(target)
    )
      return;
    update((current) => composeWorkspace(current, id, target, placement));
    setFocused(id);
    onPanelChange?.();
  };

  const dragStart = (event: DragEvent<HTMLElement>, id: string) => {
    if (!byId.has(id) || id === summary?.id) return;
    setDragging(id);
    event.currentTarget
      .closest<HTMLElement>(".wish-workspace")
      ?.classList.add("is-dragging");
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("application/x-djinn-wish-panel", id);
    event.dataTransfer.setData("text/plain", id);
  };

  const dragEnd = (event?: DragEvent<HTMLElement>) => {
    event?.currentTarget
      .closest<HTMLElement>(".wish-workspace")
      ?.classList.remove("is-dragging");
    setDragging("");
  };

  const dragOver = (event: DragEvent<HTMLElement>, id: string) => {
    if (!dragging || id === summary?.id || dragging === id) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
  };

  const drop = (event: DragEvent<HTMLElement>, target: string) => {
    event.preventDefault();
    const id = event.dataTransfer.getData("application/x-djinn-wish-panel");
    if (
      target === summary?.id ||
      !dragging ||
      !id ||
      id !== dragging ||
      id === target ||
      !byId.has(id) ||
      !byId.has(target)
    ) {
      setDragging("");
      return;
    }
    const placement = dropPlacement(event);
    if (placement) compose(id, target, placement);
    setDragging("");
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
    if (event.key === "Escape" && layout.maximized) {
      event.preventDefault();
      restore();
      return;
    }
    if (event.key === "Delete" || event.key === "Backspace") {
      const current = panel(id);
      if (!current?.pinned) {
        event.preventDefault();
        close(id);
      }
      return;
    }
    if (!event.altKey || id === summary?.id) return;
    const directions: Record<string, WorkspacePlacement | undefined> = {
      ArrowLeft: "left",
      ArrowRight: "right",
      ArrowUp: "top",
      ArrowDown: "bottom",
    };
    const placement = directions[event.key];
    if (!placement) return;
    event.preventDefault();
    update((current) => moveWorkspacePanel(current, id, placement));
    setFocused(id);
  };

  const renderPanel = (id: string, rect?: WorkspacePanelRect) => {
    const definition = panel(id);
    if (!definition || definition.id === summary?.id) return null;
    const maximized = layout.maximized === id;
    const positioned = rect
      ? maximized
        ? { left: 0, top: 0, width: 100, height: 100 }
        : rect
      : undefined;
    return (
      <WorkspacePanelFrame
        key={`${wishId}:${id}`}
        panel={definition}
        maximized={maximized}
        rect={positioned}
        hidden={!positioned || (!!layout.maximized && !maximized)}
        dragging={dragging === id}
        focused={focused === id}
        onFocus={() => setFocused(id)}
        onKeyDown={(event) => keyDown(event, id)}
        onDragStart={(event) => dragStart(event, id)}
        onDragEnd={dragEnd}
        onDragOver={(event) => dragOver(event, id)}
        onDrop={(event) => drop(event, id)}
        onMaximize={() => maximize(id)}
        onRestore={restore}
        onClose={() => close(id)}
      >
        {definition.children}
      </WorkspacePanelFrame>
    );
  };

  const panelRows = useMemo(
    () => workspaceLeafRows(layout.root, compact),
    [compact, layout.root],
  );
  const panelRects = useMemo(() => workspacePanelRects(panelRows), [panelRows]);
  // A maximized section owns the stage, so hidden siblings must not make its reading area arbitrarily tall. Otherwise
  // each visible row still gets a generous minimum before the stage grows for scrolling content.
  const renderedRows = layout.maximized ? 1 : panelRows.length;
  const stageMinimumHeight =
    renderedRows > 0
      ? `${renderedRows * 520 + (renderedRows - 1) * 16}px`
      : undefined;
  // Every section remains a direct child of one persistent stage. Closed sections have no rectangle and stay hidden,
  // which keeps their React state mounted while the serialised tree changes around them.
  const contentTree = panelDefinitions
    .filter((definition) => definition.id !== summary?.id)
    .map((definition) =>
      renderPanel(definition.id, panelRects.get(definition.id)),
    );

  const renderSummary = () => {
    if (!summary) return null;
    const opened = (id: string) =>
      id !== summary.id && workspaceHasPanel(layout.root, id);
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
            const isOpen = opened(definition.id);
            return (
              <button
                type="button"
                className={`workspace-section-row${isOpen ? " is-open" : ""}`}
                key={definition.id}
                data-wish-section={definition.id}
                aria-expanded={isOpen}
                aria-label={t("workspace.open_section", {
                  section: panelText(definition),
                })}
                disabled={!ready && !!definition.hidden}
                onClick={() => open(definition.id)}
              >
                <span className="workspace-section-row-main">
                  {definition.icon}
                  <span className="workspace-section-row-copy">
                    <strong>{definition.title}</strong>
                    {definition.preview && (
                      <span className="workspace-section-row-preview">
                        {definition.preview}
                      </span>
                    )}
                  </span>
                </span>
                {definition.count !== undefined && (
                  <span className="count">{definition.count}</span>
                )}
              </button>
            );
          })}
        </nav>
      </aside>
    );
  };

  return (
    <div className="wish-workspace-shell">
      {global && <div className="workspace-inline-global">{global}</div>}
      <LayoutGroup id={`wish-workspace-${wishId}`}>
        <div
          className={`wish-workspace${layout.maximized ? " is-maximized" : ""}${dragging ? " is-dragging" : ""}`}
          data-wish-workspace="stage"
          data-wish-maximized={layout.maximized ?? undefined}
          onDragEnd={dragEnd}
        >
          {head && <div className="workspace-head-slot">{head}</div>}
          <section
            className="workspace-content-stage"
            aria-label={t("workspace.main_content")}
            style={
              stageMinimumHeight ? { minHeight: stageMinimumHeight } : undefined
            }
          >
            {contentTree}
          </section>
          <div className="workspace-summary-slot">{renderSummary()}</div>
        </div>
      </LayoutGroup>
    </div>
  );
}

function WorkspacePanelFrame({
  panel,
  maximized,
  rect,
  hidden,
  dragging,
  focused,
  onFocus,
  onKeyDown,
  onDragStart,
  onDragEnd,
  onDragOver,
  onDrop,
  onMaximize,
  onRestore,
  onClose,
  children,
}: {
  panel: WishWorkspacePanel;
  maximized: boolean;
  rect?: WorkspacePanelRect;
  hidden: boolean;
  dragging: boolean;
  focused: boolean;
  onFocus: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLElement>) => void;
  onDragStart: (event: DragEvent<HTMLElement>) => void;
  onDragEnd: (event: DragEvent<HTMLElement>) => void;
  onDragOver: (event: DragEvent<HTMLElement>) => void;
  onDrop: (event: DragEvent<HTMLElement>) => void;
  onMaximize: () => void;
  onRestore: () => void;
  onClose: () => void;
  children: ReactNode;
}) {
  const label = panelText(panel);
  const contentRef = useRef<HTMLDivElement | null>(null);
  const keepPlace = useKeepPlace(contentRef);
  const draggable = !panel.pinned;
  return (
    <section
      className={`workspace-panel${rect ? " is-positioned" : ""}${maximized ? " is-maximized" : ""}${dragging ? " is-dragging" : ""}${focused ? " is-focused" : ""}`}
      style={
        rect
          ? {
              left: `${rect.left}%`,
              top: `${rect.top}%`,
              width: `${rect.width}%`,
              height: `${rect.height}%`,
            }
          : undefined
      }
      data-wish-panel={panel.id}
      hidden={hidden}
      aria-labelledby={`workspace-panel-heading-${panel.id}`}
      aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight Alt+ArrowUp Alt+ArrowDown"
      tabIndex={0}
      onFocus={onFocus}
      onKeyDown={onKeyDown}
      onDragOver={draggable ? onDragOver : undefined}
      onDrop={draggable ? onDrop : undefined}
    >
      <header className="workspace-panel-header">
        <div
          className={`workspace-panel-heading${draggable ? "" : " is-fixed"}`}
          draggable={draggable}
          onDragStart={draggable ? onDragStart : undefined}
          onDragEnd={draggable ? onDragEnd : undefined}
          title={draggable ? t("workspace.drag_detail") : undefined}
        >
          {panel.icon}
          <h2 id={`workspace-panel-heading-${panel.id}`}>{panel.title}</h2>
          {panel.count !== undefined && (
            <span className="count">{panel.count}</span>
          )}
        </div>
        {!panel.pinned && (
          <div className="workspace-panel-actions">
            {maximized ? (
              <button
                type="button"
                className="workspace-panel-action"
                aria-label={t("workspace.restore_panel", { panel: label })}
                title={t("workspace.restore")}
                onClick={onRestore}
              >
                <Minimize2 size={14} aria-hidden="true" />
              </button>
            ) : (
              <button
                type="button"
                className="workspace-panel-action"
                aria-label={t("workspace.maximize_panel", { panel: label })}
                title={t("workspace.maximize")}
                onClick={onMaximize}
              >
                <Maximize2 size={14} aria-hidden="true" />
              </button>
            )}
            <button
              type="button"
              className="workspace-panel-action"
              aria-label={t("workspace.close_panel", { panel: label })}
              title={t("workspace.close")}
              onClick={onClose}
            >
              <X size={14} aria-hidden="true" />
            </button>
          </div>
        )}
      </header>
      <div className="workspace-panel-content" ref={keepPlace}>
        {children}
      </div>
      {draggable && (
        <p className="workspace-keyboard-hint">
          {t("workspace.keyboard_hint")}
        </p>
      )}
    </section>
  );
}
