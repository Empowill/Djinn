// The wish workspace is a small, serialisable tree. The tree deliberately knows nothing about React or a wish's
// data: panels can be composed, closed and restored without coupling layout to the content they show.

export const WORKSPACE_LAYOUT_VERSION = 1;

export type WorkspaceDirection = "horizontal" | "vertical";
export type WorkspacePlacement = "left" | "right" | "top" | "bottom";

export interface WorkspacePanelNode {
  type: "panel";
  id: string;
}

export interface WorkspaceSplitNode {
  type: "split";
  direction: WorkspaceDirection;
  children: WorkspaceNode[];
}

export type WorkspaceNode = WorkspacePanelNode | WorkspaceSplitNode;

export interface WorkspaceLayoutState {
  version: typeof WORKSPACE_LAYOUT_VERSION;
  root: WorkspaceNode;
  closed: string[];
  maximized?: string;
}

const panel = (id: string): WorkspacePanelNode => ({ type: "panel", id });

const directionFor = (placement: WorkspacePlacement): WorkspaceDirection =>
  placement === "left" || placement === "right" ? "horizontal" : "vertical";

const isPlacement = (value: unknown): value is WorkspacePlacement =>
  value === "left" ||
  value === "right" ||
  value === "top" ||
  value === "bottom";

const isDirection = (value: unknown): value is WorkspaceDirection =>
  value === "horizontal" || value === "vertical";

const unique = (values: readonly string[]): string[] => {
  const seen = new Set<string>();
  return values.filter((value) => {
    if (!value || seen.has(value)) return false;
    seen.add(value);
    return true;
  });
};

export function workspacePanel(id: string): WorkspacePanelNode {
  return panel(id);
}

export function workspaceSplit(
  direction: WorkspaceDirection,
  children: readonly WorkspaceNode[],
): WorkspaceSplitNode {
  return { type: "split", direction, children: [...children] };
}

export function workspaceDefaultLayout(
  panelIds: readonly string[],
  summaryId = "summary",
): WorkspaceLayoutState {
  const ids = unique(panelIds);
  const summary = ids.includes(summaryId) ? summaryId : (ids[0] ?? summaryId);
  return {
    version: WORKSPACE_LAYOUT_VERSION,
    root: panel(summary),
    closed: ids.filter((id) => id !== summary),
  };
}

export function workspacePanelIds(node: WorkspaceNode): string[] {
  if (node.type === "panel") return [node.id];
  return node.children.flatMap(workspacePanelIds);
}

export function workspaceHasPanel(node: WorkspaceNode, id: string): boolean {
  return workspacePanelIds(node).includes(id);
}

function normalizedNode(
  value: unknown,
  allowed: ReadonlySet<string>,
  seen: Set<string>,
): WorkspaceNode | undefined {
  if (!value || typeof value !== "object") return undefined;
  const candidate = value as {
    type?: unknown;
    id?: unknown;
    direction?: unknown;
    children?: unknown;
  };
  if (candidate.type === "panel") {
    if (typeof candidate.id !== "string" || !allowed.has(candidate.id))
      return undefined;
    if (seen.has(candidate.id)) return undefined;
    seen.add(candidate.id);
    return panel(candidate.id);
  }
  if (
    candidate.type !== "split" ||
    !isDirection(candidate.direction) ||
    !Array.isArray(candidate.children)
  )
    return undefined;
  const children = candidate.children
    .map((child) => normalizedNode(child, allowed, seen))
    .filter((child): child is WorkspaceNode => !!child);
  if (children.length === 0) return undefined;
  if (children.length === 1) return children[0];
  return { type: "split", direction: candidate.direction, children };
}

/**
 * Sanitize data read from storage. Unknown panel IDs and duplicate leaves are dropped, while every known panel not
 * present in the tree is kept closed. This lets old layouts survive when a panel is renamed or a wish has no data.
 */
export function normalizeWorkspaceLayout(
  value: unknown,
  panelIds: readonly string[],
  summaryId = "summary",
): WorkspaceLayoutState {
  const ids = unique(panelIds);
  const allowed = new Set(ids);
  const fallback = workspaceDefaultLayout(ids, summaryId);
  if (!value || typeof value !== "object") return fallback;

  const candidate = value as {
    version?: unknown;
    root?: unknown;
    closed?: unknown;
    maximized?: unknown;
  };
  if (candidate.version !== WORKSPACE_LAYOUT_VERSION) return fallback;

  const seen = new Set<string>();
  const parsedRoot = normalizedNode(candidate.root, allowed, seen);
  if (!parsedRoot) return fallback;
  const pinnedId = ids.includes(summaryId) ? summaryId : (ids[0] ?? summaryId);
  let root = parsedRoot;
  if (!seen.has(pinnedId)) {
    seen.add(pinnedId);
    root = workspaceSplit("horizontal", [panel(pinnedId), parsedRoot]);
  }
  const closed = unique(
    Array.isArray(candidate.closed)
      ? candidate.closed.filter((id): id is string => typeof id === "string")
      : [],
  ).filter((id) => allowed.has(id) && !seen.has(id));
  const missing = ids.filter((id) => !seen.has(id) && !closed.includes(id));
  const maximized =
    typeof candidate.maximized === "string" && seen.has(candidate.maximized)
      ? candidate.maximized
      : undefined;
  return {
    version: WORKSPACE_LAYOUT_VERSION,
    root,
    closed: [...closed, ...missing],
    ...(maximized ? { maximized } : {}),
  };
}

function removeNode(
  node: WorkspaceNode,
  id: string,
): { node?: WorkspaceNode; removed: boolean } {
  if (node.type === "panel")
    return node.id === id ? { removed: true } : { node, removed: false };
  const children: WorkspaceNode[] = [];
  let removed = false;
  for (const child of node.children) {
    if (removed) {
      children.push(child);
      continue;
    }
    const result = removeNode(child, id);
    removed ||= result.removed;
    if (result.node) children.push(result.node);
  }
  if (!removed) return { node, removed: false };
  if (children.length === 0) return { removed: true };
  if (children.length === 1) return { node: children[0], removed: true };
  return {
    node: { type: "split", direction: node.direction, children },
    removed: true,
  };
}

function insertNode(
  node: WorkspaceNode,
  id: string,
  target: string,
  placement: WorkspacePlacement,
): { node: WorkspaceNode; inserted: boolean } {
  if (node.type === "panel") {
    if (node.id !== target) return { node, inserted: false };
    const incoming = panel(id);
    const children =
      placement === "left" || placement === "top"
        ? [incoming, node]
        : [node, incoming];
    return {
      node: {
        type: "split",
        direction: directionFor(placement),
        children,
      },
      inserted: true,
    };
  }
  for (let index = 0; index < node.children.length; index += 1) {
    const result = insertNode(node.children[index], id, target, placement);
    if (!result.inserted) continue;
    const children = [...node.children];
    if (
      result.node.type === "split" &&
      result.node.direction === node.direction
    ) {
      children.splice(index, 1, ...result.node.children);
    } else {
      children[index] = result.node;
    }
    return {
      node: { type: "split", direction: node.direction, children },
      inserted: true,
    };
  }
  return { node, inserted: false };
}

/** Place a panel beside a target. The operation is a no-op for identical or unknown targets. */
export function composeWorkspace(
  state: WorkspaceLayoutState,
  id: string,
  target: string,
  placement: WorkspacePlacement,
): WorkspaceLayoutState {
  if (!id || !target || id === target || !isPlacement(placement)) return state;
  const removed = removeNode(state.root, id);
  if (!removed.node) return state;
  const inserted = insertNode(removed.node, id, target, placement);
  if (!inserted.inserted) return state;
  return {
    ...state,
    root: inserted.node,
    closed: state.closed.filter((panelId) => panelId !== id),
    maximized: undefined,
  };
}

/** Open a closed panel beside the summary and maximize it for immediate feedback. */
export function openWorkspacePanel(
  state: WorkspaceLayoutState,
  id: string,
  summaryId = "summary",
): WorkspaceLayoutState {
  if (!id || workspaceHasPanel(state.root, id))
    return { ...state, maximized: id || state.maximized };
  const target = workspaceHasPanel(state.root, summaryId)
    ? summaryId
    : workspacePanelIds(state.root)[0];
  if (!target) return state;
  const inserted = insertNode(state.root, id, target, "right");
  if (!inserted.inserted) return state;
  return {
    ...state,
    root: inserted.node,
    closed: state.closed.filter((panelId) => panelId !== id),
    maximized: id,
  };
}

export function closeWorkspacePanel(
  state: WorkspaceLayoutState,
  id: string,
  summaryId = "summary",
): WorkspaceLayoutState {
  if (id === summaryId) return { ...state, maximized: undefined };
  const removed = removeNode(state.root, id);
  if (!removed.removed || !removed.node) return state;
  return {
    ...state,
    root: removed.node,
    closed: state.closed.includes(id) ? state.closed : [...state.closed, id],
    maximized: state.maximized === id ? undefined : state.maximized,
  };
}

export function maximizeWorkspacePanel(
  state: WorkspaceLayoutState,
  id: string,
): WorkspaceLayoutState {
  return workspaceHasPanel(state.root, id)
    ? { ...state, maximized: id }
    : state;
}

export function restoreWorkspace(
  state: WorkspaceLayoutState,
): WorkspaceLayoutState {
  return state.maximized ? { ...state, maximized: undefined } : state;
}

/** Keyboard movement uses the stable tree order; horizontal and vertical arrows map to the nearest sibling. */
export function moveWorkspacePanel(
  state: WorkspaceLayoutState,
  id: string,
  direction: WorkspacePlacement,
): WorkspaceLayoutState {
  if (!isPlacement(direction)) return state;
  const ids = workspacePanelIds(state.root);
  const index = ids.indexOf(id);
  if (index < 0) return state;
  const forward = direction === "right" || direction === "bottom";
  const target = ids[index + (forward ? 1 : -1)];
  if (!target) return state;
  return composeWorkspace(state, id, target, direction);
}

export function serializeWorkspaceLayout(state: WorkspaceLayoutState): string {
  return JSON.stringify(state);
}

export function parseWorkspaceLayout(
  raw: string | null | undefined,
  panelIds: readonly string[],
  summaryId = "summary",
): WorkspaceLayoutState {
  if (!raw) return workspaceDefaultLayout(panelIds, summaryId);
  try {
    return normalizeWorkspaceLayout(JSON.parse(raw), panelIds, summaryId);
  } catch {
    return workspaceDefaultLayout(panelIds, summaryId);
  }
}

export function workspaceStorageKey(wishId: string): string {
  return `djinn.wish.workspace.${wishId}`;
}
