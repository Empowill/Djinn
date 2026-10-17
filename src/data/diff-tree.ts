// The files a task's diff changes, as a tree of folders: a reader gets the overview first and opens the folders that
// matter. Each folder counts the files under it and the lines they add and remove; a chain of folders that each hold
// one folder and no file reads as one row (a/b/c). Folders come before files, each by name; the diff shows the files
// in the tree's order, so the tree follows it as it scrolls.
import type { TaskDiffFile } from "../../gen/ts/plan/v1/plan_pb";

export interface DiffFolder {
  kind: "folder";
  // Its path from the repository's root, the last folder of a chain: what keeps it open or closed.
  path: string;
  // What its row shows: its name, or the names of its chain joined by a slash.
  name: string;
  // The files under it, at any depth, and the lines they add and remove.
  files: number;
  added: number;
  deleted: number;
  children: DiffNode[];
}

export interface DiffLeaf {
  kind: "file";
  path: string;
  // The file's name, its folders left to the rows above.
  name: string;
  file: TaskDiffFile;
}

export type DiffNode = DiffFolder | DiffLeaf;

// A row of the tree as it shows: its node, how deep, and the folder that holds it ("" at the top).
export interface DiffRow {
  node: DiffNode;
  depth: number;
  parent: string;
}

// Up to this many files, every folder starts open: the whole tree reads at a glance. Beyond, they start closed.
export const OPEN_UP_TO = 20;

interface Dir {
  dirs: Map<string, Dir>;
  files: DiffLeaf[];
}

const byName = (a: { name: string }, b: { name: string }) =>
  a.name < b.name ? -1 : a.name > b.name ? 1 : 0;

// diffTree groups the files by folder, the single-child chains compacted.
export function diffTree(files: readonly TaskDiffFile[]): DiffNode[] {
  const root: Dir = { dirs: new Map(), files: [] };
  for (const file of files) {
    const parts = file.path.split("/");
    const name = parts.pop() ?? file.path;
    let dir = root;
    for (const part of parts) {
      let next = dir.dirs.get(part);
      if (!next) {
        next = { dirs: new Map(), files: [] };
        dir.dirs.set(part, next);
      }
      dir = next;
    }
    dir.files.push({ kind: "file", path: file.path, name, file });
  }
  return nodesOf(root, "");
}

function nodesOf(dir: Dir, at: string): DiffNode[] {
  const folders = [...dir.dirs].map(([name, sub]) => folderOf(name, sub, at));
  return [...folders.sort(byName), ...[...dir.files].sort(byName)];
}

function folderOf(name: string, dir: Dir, at: string): DiffFolder {
  let path = at ? `${at}/${name}` : name;
  // A folder that holds one folder and no file reads as one row with it.
  while (dir.files.length === 0 && dir.dirs.size === 1) {
    const [[next, sub]] = dir.dirs;
    name = `${name}/${next}`;
    path = `${path}/${next}`;
    dir = sub;
  }
  const children = nodesOf(dir, path);
  const folder: DiffFolder = {
    kind: "folder",
    path,
    name,
    files: 0,
    added: 0,
    deleted: 0,
    children,
  };
  for (const child of children) {
    if (child.kind === "folder") {
      folder.files += child.files;
      folder.added += child.added;
      folder.deleted += child.deleted;
    } else {
      folder.files++;
      folder.added += child.file.added;
      folder.deleted += child.file.deleted;
    }
  }
  return folder;
}

// diffFiles lists the files in the tree's order: the order the diff shows them in.
export function diffFiles(nodes: readonly DiffNode[]): TaskDiffFile[] {
  return nodes.flatMap((n) =>
    n.kind === "file" ? [n.file] : diffFiles(n.children),
  );
}

// diffRows lists the rows the tree shows: a closed folder hides what it holds.
export function diffRows(
  nodes: readonly DiffNode[],
  open: (folder: DiffFolder) => boolean,
  depth = 0,
  parent = "",
): DiffRow[] {
  return nodes.flatMap((node) => [
    { node, depth, parent },
    ...(node.kind === "folder" && open(node)
      ? diffRows(node.children, open, depth + 1, node.path)
      : []),
  ]);
}

// rowShowing is the index of the row that shows a file: its own, or the closed folder that holds it; -1 if none.
export function rowShowing(rows: readonly DiffRow[], path: string): number {
  let found = -1;
  rows.forEach((row, i) => {
    const holds =
      row.node.kind === "file"
        ? row.node.path === path
        : path.startsWith(`${row.node.path}/`);
    if (holds && (found < 0 || row.depth > rows[found].depth)) found = i;
  });
  return found;
}
