import {
  parseWorkspaceLayout,
  serializeWorkspaceLayout,
  workspaceStorageKey,
  type WorkspaceLayoutState,
} from "./wish-workspace-layout";

type WorkspaceStorage = Pick<Storage, "getItem" | "setItem">;

function browserStorage(): WorkspaceStorage | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    // WebKit private windows and embedded surfaces can expose localStorage while refusing access to it.
    return undefined;
  }
}

export function readWishWorkspace(
  wishId: string,
  panelIds: readonly string[],
  storage = browserStorage(),
): WorkspaceLayoutState {
  if (!storage || !wishId) return parseWorkspaceLayout(undefined, panelIds);
  try {
    return parseWorkspaceLayout(
      storage.getItem(workspaceStorageKey(wishId)),
      panelIds,
    );
  } catch {
    return parseWorkspaceLayout(undefined, panelIds);
  }
}

export function writeWishWorkspace(
  wishId: string,
  state: WorkspaceLayoutState,
  storage = browserStorage(),
): void {
  if (!storage || !wishId) return;
  try {
    storage.setItem(
      workspaceStorageKey(wishId),
      serializeWorkspaceLayout(state),
    );
  } catch {
    // A layout is a preference. A blocked or full store must never prevent the wish from rendering or saving work.
  }
}
