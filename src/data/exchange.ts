// Importing a wish from a file the page reads: WishService.ImportData takes the content, binary or JSON, as
// `djinn wish export` writes it. A wish already here is shown as djinn holds it, not imported twice.
import { fromBinary } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";

import { WishExportSchema } from "../../gen/ts/plan/v1/plan_pb";
import type { Clients } from "./client";

// The largest file the import reads, as the server does.
export const MAX_FILE = 100 * 1024 * 1024;

export interface Imported {
  wishId: string;
  // The wish was here already: nothing was imported.
  already: boolean;
  // What djinn says about the import, such as a wish imported paused.
  note: string;
}

export async function importWish(
  clients: Clients,
  file: File,
): Promise<Imported> {
  if (file.size > MAX_FILE) throw new Error("The file is larger than 100 MB");
  const data = new Uint8Array(await file.arrayBuffer());
  try {
    const res = await clients.wishes.importData({ data });
    return { wishId: res.wish?.id ?? "", already: false, note: res.note };
  } catch (error) {
    if (!(error instanceof ConnectError) || error.code !== Code.AlreadyExists)
      throw error;
    return { wishId: wishIdOf(data), already: true, note: "" };
  }
}

// wishIdOf reads the wish's identifier from an export, JSON or binary.
export function wishIdOf(data: Uint8Array): string {
  const first = new TextDecoder().decode(data.subarray(0, 1)).trim();
  if (first === "{")
    return JSON.parse(new TextDecoder().decode(data))?.wish?.id ?? "";
  return fromBinary(WishExportSchema, data).wish?.id ?? "";
}
