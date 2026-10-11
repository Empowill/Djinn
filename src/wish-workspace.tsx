import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  Columns2,
  Maximize2,
  Minimize2,
  X,
} from "lucide-react";
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
  type WorkspaceDirection,
  type WorkspacePlacement,
  workspaceHasPanel,
  workspacePanelIds,
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
  /** A short line shown in the sparse summary card. */
  preview?: ReactNode;
  count?: number;
  icon?: ReactNode;
  children: ReactNode;
  /** Empty optional sections do not appear in the summary or storage layout. */
  hidden?: boolean;
  /** The summary is the one panel that cannot be closed. */
  pinned?: boolean;
}

export interface WishWorkspaceProps {
  wishId: string;
  panels: readonly WishWorkspacePanel[];
  /** Content that must remain actionable while a panel is maximized. */
  global?: ReactNode;
  /** Detail data is ready; only then reconcile optional panels and persist the layout. */
  ready?: boolean;
  /** A deep link can select a panel on first render. */
  initialPanel?: string;
  /** Changes when navigation requests the same panel again (for example, a repeated tilasm link). */
  initialPanelRequest?: string;
  onPanelChange?: (panelId?: string) => void;
}

const SUMMARY_ID = "summary";
const PLACEMENTS: readonly WorkspacePlacement[] = [
  "left",
  "right",
  "top",
  "bottom",
];

const SPLIT_CLASSES: Record<WorkspaceDirection, string> = {
  horizontal: "workspace-split-horizontal",
  vertical: "workspace-split-vertical",
};

const DROP_CLASSES: Record<WorkspacePlacement, string> = {
  left: "workspace-drop-left",
  right: "workspace-drop-right",
  top: "workspace-drop-top",
  bottom: "workspace-drop-bottom",
};

const PLACEMENT_LABELS: Record<WorkspacePlacement, Parameters<typeof t>[0]> = {
  left: "workspace.left",
  right: "workspace.right",
  top: "workspace.top",
  bottom: "workspace.bottom",
};

const placementLabel = (placement: WorkspacePlacement): string =>
  t(PLACEMENT_LABELS[placement]);

const placementIcon = (placement: WorkspacePlacement) => {
  const props = { size: 13, "aria-hidden": true } as const;
  switch (placement) {
    case "left":
      return <ArrowLeft {...props} />;
    case "right":
      return <ArrowRight {...props} />;
    case "top":
      return <ArrowUp {...props} />;
    case "bottom":
      return <ArrowDown {...props} />;
  }
};

function WorkspaceComposeMenu({
  candidates,
  onCompose,
}: {
  candidates: readonly WishWorkspacePanel[];
  onCompose: (target: string, placement: WorkspacePlacement) => void;
}) {
  const candidateKey = candidates.map((candidate) => candidate.id).join(",");
  const [target, setTarget] = useState(candidates[0]?.id ?? "");
  const [placement, setPlacement] = useState<WorkspacePlacement>("right");

  useEffect(() => {
    if (candidates.some((candidate) => candidate.id === target)) return;
    setTarget(candidates[0]?.id ?? "");
  }, [candidateKey, candidates, target]);

  if (candidates.length === 0) return null;
  const label = t("workspace.compose");
  return (
    <details className="workspace-compose-menu">
      <summary aria-label={label} title={label}>
        <Columns2 size={14} aria-hidden="true" />
      </summary>
      <div className="workspace-compose-controls">
        <label>
          <span>{t("workspace.target")}</span>
          <select
            value={target}
            aria-label={t("workspace.target")}
            onChange={(event) => setTarget(event.target.value)}
          >
            {candidates.map((candidate) => (
              <option key={candidate.id} value={candidate.id}>
                {panelText(candidate)}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>{t("workspace.direction")}</span>
          <select
            value={placement}
            aria-label={t("workspace.direction")}
            onChange={(event) =>
              setPlacement(event.target.value as WorkspacePlacement)
            }
          >
            {PLACEMENTS.map((option) => (
              <option key={option} value={option}>
                {placementLabel(option)}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          onClick={() => onCompose(target, placement)}
          disabled={!target}
        >
          {t("workspace.compose_action")}
        </button>
      </div>
    </details>
  );
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

export function WishWorkspace({
  wishId,
  panels,
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
  const visiblePanels = useMemo(() => {
    return panelDefinitions.filter((panel) => !panel.hidden);
  }, [panelDefinitions]);
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
    () => new Map(visiblePanels.map((panel) => [panel.id, panel])),
    [visiblePanels],
  );
  const [layout, setLayout] = useState<WorkspaceLayoutState>(() =>
    readWishWorkspace(wishId, panelIds),
  );
  const [layoutOwner, setLayoutOwner] = useState(wishId);
  const [layoutPanelKey, setLayoutPanelKey] = useState(panelKey);
  const [dragging, setDragging] = useState("");
  const [focused, setFocused] = useState("");
  const previousWish = useRef(wishId);
  const previousPanels = useRef(panelKey);
  const initialPanelRef = useRef<string | undefined>(undefined);

  // A wish's composition never leaks into another wish, even when React keeps this screen mounted while selection
  // changes. A changing set of optional panels is normalized in place, preserving the user's existing tree.
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
    if (!ready || !initialPanel || !byId.has(initialPanel)) return;
    if (initialPanelRef.current === panelRequest) return;
    initialPanelRef.current = panelRequest;
    setLayout((current) => openWorkspacePanel(current, initialPanel));
  }, [byId, initialPanel, panelRequest, ready]);

  const panel = (id: string) => byId.get(id);
  const update = (
    next: (current: WorkspaceLayoutState) => WorkspaceLayoutState,
  ) => setLayout((current) => next(current));

  const open = (id: string) => {
    // A card click opens the panel into the composition so the remaining cards stay available. Deep links use the
    // initialPanel effect above when they need an immediately selected/maximized target.
    update((current) => {
      const existing = workspacePanelIds(current.root);
      const opened = restoreWorkspace(openWorkspacePanel(current, id));
      const target = existing.at(-1);
      return target ? composeWorkspace(opened, id, target, "right") : opened;
    });
  };
  const close = (id: string) => {
    const current = layout;
    update((state) => closeWorkspacePanel(state, id));
    if (current.maximized === id) onPanelChange?.();
  };
  const maximize = (id: string) => {
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
    if (!id || id === target || !byId.has(id) || !byId.has(target)) return;
    update((current) => {
      // A section card is a valid target even while its panel is closed. Open it into the restored tree first, then
      // compose the incoming panel beside it; the target must never be a dead affordance.
      const targetOpen = workspaceHasPanel(current.root, target)
        ? current
        : restoreWorkspace(openWorkspacePanel(current, target));
      return composeWorkspace(targetOpen, id, target, placement);
    });
    setFocused(id);
    onPanelChange?.();
  };

  const dragStart = (event: DragEvent<HTMLElement>, id: string) => {
    if (!byId.has(id)) return;
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
  const drop = (
    event: DragEvent<HTMLElement>,
    target: string,
    placement: WorkspacePlacement,
  ) => {
    event.preventDefault();
    const id =
      event.dataTransfer.getData("application/x-djinn-wish-panel") ||
      event.dataTransfer.getData("text/plain");
    compose(id, target, placement);
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
    if (!event.altKey) return;
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

  const renderDropZones = (target: string) => (
    <div className="workspace-drop-zones" aria-label={t("workspace.compose")}>
      {PLACEMENTS.map((placement) => (
        <button
          key={placement}
          type="button"
          className={`workspace-drop-zone ${DROP_CLASSES[placement]}`}
          data-wish-drop={placement}
          aria-label={t("workspace.move_panel", {
            panel: panel(target) ? panelText(panel(target)!) : target,
            direction: placementLabel(placement),
          })}
          onDragOver={(event) => event.preventDefault()}
          onDrop={(event) => drop(event, target, placement)}
          onClick={() => {
            if (focused) compose(focused, target, placement);
          }}
        >
          {placementIcon(placement)}
        </button>
      ))}
    </div>
  );

  const renderPanel = (id: string, key: string) => {
    const definition = panel(id);
    if (!definition) return null;
    const maximized = layout.maximized === id;
    return (
      <WorkspacePanelFrame
        key={key}
        panel={definition}
        maximized={maximized}
        dragging={dragging === id}
        focused={focused === id}
        onFocus={() => setFocused(id)}
        onKeyDown={(event) => keyDown(event, id)}
        onDragStart={(event) => dragStart(event, id)}
        onDragEnd={dragEnd}
        onMaximize={() => maximize(id)}
        onRestore={restore}
        onClose={() => close(id)}
        composeCandidates={visiblePanels.filter(
          (candidate) => candidate.id !== id,
        )}
        onCompose={(target, placement) => compose(id, target, placement)}
      >
        {definition.children}
        {renderDropZones(id)}
      </WorkspacePanelFrame>
    );
  };

  const renderNode = (node: WorkspaceNode, key = "root"): ReactNode => {
    if (node.type === "panel")
      return node.id === summary?.id
        ? renderSummary()
        : renderPanel(node.id, key);
    return (
      <div
        key={key}
        className={`workspace-split ${SPLIT_CLASSES[node.direction]}`}
        data-wish-split={node.direction}
      >
        {node.children.map((child, index) =>
          renderNode(child, `${key}-${index}`),
        )}
      </div>
    );
  };

  const summary = panel(SUMMARY_ID) ?? visiblePanels[0];
  const cards = visiblePanels.filter(
    (definition) => definition.id !== summary?.id,
  );
  const renderSectionCards = () => (
    <div
      className="workspace-section-grid"
      aria-label={t("workspace.sections")}
    >
      {cards.map((definition) => (
        <div
          className={`workspace-section-card-wrap${dragging === definition.id ? " is-dragging" : ""}`}
          key={definition.id}
          data-wish-section={definition.id}
          draggable
          onDragStart={(event) => dragStart(event, definition.id)}
          onDragEnd={dragEnd}
        >
          <button
            type="button"
            className="workspace-section-card"
            draggable
            onClick={() => open(definition.id)}
            onDragStart={(event) => dragStart(event, definition.id)}
            onDragEnd={dragEnd}
            aria-label={t("workspace.open_section", {
              section: panelText(definition),
            })}
          >
            <span className="workspace-section-card-title">
              {definition.icon}
              <strong>{definition.title}</strong>
              {definition.count !== undefined && (
                <span className="count">{definition.count}</span>
              )}
            </span>
            {definition.preview && (
              <span className="workspace-section-card-preview">
                {definition.preview}
              </span>
            )}
          </button>
          <WorkspaceComposeMenu
            candidates={visiblePanels.filter(
              (candidate) => candidate.id !== definition.id,
            )}
            onCompose={(target, placement) =>
              compose(definition.id, target, placement)
            }
          />
          {renderDropZones(definition.id)}
        </div>
      ))}
    </div>
  );

  const renderSummary = () => {
    if (!summary) return null;
    const children = (
      <>
        {summary.children}
        {renderSectionCards()}
      </>
    );
    return (
      <WorkspacePanelFrame
        key="summary"
        panel={summary}
        maximized={layout.maximized === summary.id}
        dragging={dragging === summary.id}
        focused={focused === summary.id}
        onFocus={() => setFocused(summary.id)}
        onKeyDown={(event) => keyDown(event, summary.id)}
        onDragStart={(event) => dragStart(event, summary.id)}
        onDragEnd={dragEnd}
        onMaximize={() => maximize(summary.id)}
        onRestore={restore}
        onClose={() => close(summary.id)}
        composeCandidates={cards}
        onCompose={(target, placement) =>
          compose(summary.id, target, placement)
        }
      >
        {children}
        {renderDropZones(summary.id)}
      </WorkspacePanelFrame>
    );
  };

  const maximizedPanel = layout.maximized && panel(layout.maximized);
  const tree = !ready
    ? renderSummary()
    : maximizedPanel
      ? maximizedPanel.id === summary?.id
        ? renderSummary()
        : renderPanel(maximizedPanel.id, `max-${maximizedPanel.id}`)
      : renderNode(layout.root);

  return (
    <div className="wish-workspace-shell" data-wish-workspace={wishId}>
      {global && <div className="workspace-global">{global}</div>}
      <LayoutGroup id={`wish-workspace-${wishId}`}>
        <div
          className={`wish-workspace${layout.maximized ? " is-maximized" : ""}${dragging ? " is-dragging" : ""}`}
          data-wish-workspace="stage"
          data-wish-maximized={layout.maximized ?? undefined}
          onDragEnd={dragEnd}
        >
          {tree ?? renderSummary()}
        </div>
      </LayoutGroup>
    </div>
  );
}

function WorkspacePanelFrame({
  panel,
  maximized,
  dragging,
  focused,
  onFocus,
  onKeyDown,
  onDragStart,
  onDragEnd,
  onMaximize,
  onRestore,
  onClose,
  composeCandidates,
  onCompose,
  children,
}: {
  panel: WishWorkspacePanel;
  maximized: boolean;
  dragging: boolean;
  focused: boolean;
  onFocus: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLElement>) => void;
  onDragStart: (event: DragEvent<HTMLElement>) => void;
  onDragEnd: () => void;
  onMaximize: () => void;
  onRestore: () => void;
  onClose: () => void;
  composeCandidates?: readonly WishWorkspacePanel[];
  onCompose?: (target: string, placement: WorkspacePlacement) => void;
  children: ReactNode;
}) {
  const label = panelText(panel);
  const contentRef = useRef<HTMLDivElement | null>(null);
  const keepPlace = useKeepPlace(contentRef);
  return (
    <section
      className={`workspace-panel${panel.pinned ? " is-pinned" : ""}${maximized ? " is-maximized" : ""}${dragging ? " is-dragging" : ""}${focused ? " is-focused" : ""}`}
      data-wish-panel={panel.id}
      aria-labelledby={`workspace-panel-heading-${panel.id}`}
      tabIndex={0}
      onFocus={onFocus}
      onKeyDown={onKeyDown}
      onDragOver={(event) => event.preventDefault()}
    >
      <header className="workspace-panel-header">
        <div
          className="workspace-panel-heading"
          draggable
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          title={t("workspace.drag_detail")}
        >
          {panel.icon}
          <h2 id={`workspace-panel-heading-${panel.id}`}>{panel.title}</h2>
          {panel.count !== undefined && (
            <span className="count">{panel.count}</span>
          )}
        </div>
        <div className="workspace-panel-actions">
          {composeCandidates && onCompose && (
            <WorkspaceComposeMenu
              candidates={composeCandidates}
              onCompose={onCompose}
            />
          )}
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
      </header>
      <div className="workspace-panel-content" ref={keepPlace}>
        {children}
      </div>
      <p className="workspace-keyboard-hint">{t("workspace.keyboard_hint")}</p>
    </section>
  );
}

// Kept exportable for screen-level tests and small previews that want a card without constructing the whole workspace.
export function WorkspaceSectionCard({
  panel,
  onOpen,
}: {
  panel: WishWorkspacePanel;
  onOpen: () => void;
}) {
  return (
    <button
      type="button"
      className="workspace-section-card"
      data-wish-section={panel.id}
      onClick={onOpen}
    >
      <span className="workspace-section-card-title">
        {panel.icon}
        <strong>{panel.title}</strong>
        {panel.count !== undefined && (
          <span className="count">{panel.count}</span>
        )}
      </span>
      {panel.preview && (
        <span className="workspace-section-card-preview">{panel.preview}</span>
      )}
    </button>
  );
}
