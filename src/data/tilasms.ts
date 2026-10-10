// A tilasm dropped on the Tilasms tab: a folder, its files read by their paths in it, or a .zip, read whole. The page
// sends them to TilasmService.PutData, which puts them as a tilasm of the wish, or imports a tilasm's export.

// The most a drop reads, its files summed: twice a tilasm's ceiling, which the server checks file by file.
export const MAX_DROP = 100 * 1024 * 1024;

// TooLarge is the error of a drop over MAX_DROP: the screen says it in its words.
export class TooLarge extends Error {}

export interface DroppedFile {
  path: string;
  content: Uint8Array;
}

export interface Dropped {
  // The folder's name or the .zip's: the title of a tilasm whose page has none.
  name: string;
  files: DroppedFile[];
}

// The parts of the File and Directory Entries API a drop uses (DataTransferItem.webkitGetAsEntry), apart for the tests.
export interface DropEntry {
  name: string;
  isFile: boolean;
  isDirectory: boolean;
  file?: (ok: (file: Blob) => void, fail: (error: unknown) => void) => void;
  createReader?: () => {
    readEntries: (
      ok: (entries: DropEntry[]) => void,
      fail: (error: unknown) => void,
    ) => void;
  };
}

// entriesOf takes the entries of a drop at once: the browser empties its items once the drop event returns.
export function entriesOf(data: DataTransfer | null): DropEntry[] {
  return [...(data?.items ?? [])]
    .filter((item) => item.kind === "file")
    .map((item) => item.webkitGetAsEntry() as DropEntry | null)
    .filter((entry): entry is DropEntry => !!entry);
}

// readDrop reads what was dropped: one folder, its files under it; one file, itself (a .zip); several, side by side.
// Hidden files (.DS_Store, .git) stay out.
export async function readDrop(entries: DropEntry[]): Promise<Dropped> {
  const files: DroppedFile[] = [];
  let size = 0;
  const add = async (entry: DropEntry, path: string) => {
    if (entry.name.startsWith(".")) return;
    if (entry.isDirectory) {
      for (const child of await children(entry))
        await add(child, path ? `${path}/${child.name}` : child.name);
      return;
    }
    const blob = await new Promise<Blob>((ok, fail) => entry.file!(ok, fail));
    size += blob.size;
    if (size > MAX_DROP) throw new TooLarge();
    files.push({
      path,
      content: new Uint8Array(await blob.arrayBuffer()),
    });
  };
  const [first] = entries;
  if (entries.length === 1 && first?.isDirectory) {
    await add(first, "");
    return { name: first.name, files };
  }
  for (const entry of entries) await add(entry, entry.name);
  return { name: entries.length === 1 ? first!.name : "", files };
}

// children reads a folder's entries: readEntries gives them a batch at a time, then an empty one.
async function children(folder: DropEntry): Promise<DropEntry[]> {
  const reader = folder.createReader!();
  const out: DropEntry[] = [];
  for (;;) {
    const batch = await new Promise<DropEntry[]>((ok, fail) =>
      reader.readEntries(ok, fail),
    );
    if (batch.length === 0) return out;
    out.push(...batch);
  }
}
