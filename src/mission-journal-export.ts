import type { FlightEvent, RuntimeEnvelope, Task } from "./types";

export type MissionJournalEvent = FlightEvent | RuntimeEnvelope;

export interface MissionJournalPage {
  events: unknown[];
  nextCursor: number | null;
  hasMore: boolean;
}

export type MissionJournalPageReader = (
  taskId: string,
  cursor: number,
  limit: number,
) => Promise<MissionJournalPage>;

const DEFAULT_PAGE_SIZE = 200;
const MAX_PAGES = 10000;

type RecordValue = Record<string, unknown>;

function isRecord(value: unknown): value is RecordValue {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function text(value: unknown): string | undefined {
  return typeof value === "string" && value ? value : undefined;
}

function eventData(value: unknown): RecordValue | undefined {
  if (!isRecord(value)) return undefined;
  return isRecord(value.data) ? value.data : undefined;
}

/**
 * Returns the identity used when native records and renderer records mirror
 * the same event. Streaming text uses its message identity because each
 * partial update has a different native eventId.
 */
export function journalEventIdentity(value: unknown): string | undefined {
  if (!isRecord(value)) return undefined;
  const data = eventData(value);
  const runId = text(value.runId) || text(data?.runId);
  const messageId = text(data?.messageId);
  if (runId && messageId && (data?.streaming === true || value.type === "text"))
    return `${runId}:message:${messageId.slice(0, 256)}`;
  return text(value.eventId) || text(value.id);
}

export const stableJournalEventId = journalEventIdentity;

function isHuman(value: unknown): boolean {
  if (!isRecord(value)) return false;
  const data = eventData(value);
  return (
    value.actor === "human" ||
    value.source === "human" ||
    data?.actor === "human"
  );
}

function eventTime(value: unknown): number {
  if (!isRecord(value)) return Number.POSITIVE_INFINITY;
  const raw = text(value.timestamp) || text(value.time);
  if (!raw) return Number.POSITIVE_INFINITY;
  const parsed = Date.parse(raw);
  return Number.isFinite(parsed) ? parsed : Number.POSITIVE_INFINITY;
}

function isStreamingIdentity(identity: string): boolean {
  return identity.includes(":message:");
}

function fallbackIdentity(value: unknown, index: number): string {
  try {
    const serialized = JSON.stringify(value);
    if (serialized) return `record:${serialized}`;
  } catch {
    // Keep an otherwise exportable record instead of failing deduplication.
  }
  return `record:${index}`;
}

/**
 * Combines native JSONL records with task events, removing native/renderer
 * mirrors. Human decisions and validations win when they share an identity
 * with an agent record. Streaming updates keep the latest timestamped value.
 */
export function mergeMissionJournalEvents(
  nativeEvents: readonly unknown[],
  taskEvents: readonly unknown[],
): MissionJournalEvent[] {
  const merged = new Map<
    string,
    { value: MissionJournalEvent; order: number }
  >();
  const records = [...nativeEvents, ...taskEvents];

  records.forEach((raw, order) => {
    if (!isRecord(raw)) return;
    const value = raw as unknown as MissionJournalEvent;
    const identity =
      journalEventIdentity(value) || fallbackIdentity(value, order);
    const existing = merged.get(identity);
    if (!existing) {
      merged.set(identity, { value, order });
      return;
    }

    const currentHuman = isHuman(existing.value);
    const candidateHuman = isHuman(value);
    if (candidateHuman && !currentHuman) {
      merged.set(identity, { value, order: existing.order });
      return;
    }
    if (currentHuman && !candidateHuman) return;

    if (
      isStreamingIdentity(identity) &&
      eventTime(value) >= eventTime(existing.value)
    )
      merged.set(identity, { value, order: existing.order });
  });

  return [...merged.values()]
    .sort(
      (a, b) => eventTime(a.value) - eventTime(b.value) || a.order - b.order,
    )
    .map(({ value }) => value);
}

export const mergeJournalEvents = mergeMissionJournalEvents;

function invalidPage(message: string): Error {
  const error = new Error(`Invalid journal pagination: ${message}`);
  error.name = "MissionJournalPaginationError";
  return error;
}

/** Reads every native page and rejects malformed or non-progressive cursors. */
export async function readAllMissionJournalPages(
  taskId: string,
  readPage: MissionJournalPageReader,
  pageSize = DEFAULT_PAGE_SIZE,
): Promise<unknown[]> {
  if (!Number.isSafeInteger(pageSize) || pageSize < 1) {
    throw invalidPage("the page size must be a positive integer");
  }

  const events: unknown[] = [];
  const cursors = new Set<number>();
  let cursor = 0;

  for (let pageNumber = 0; pageNumber < MAX_PAGES; pageNumber += 1) {
    if (cursors.has(cursor)) throw invalidPage("the cursor loops");
    cursors.add(cursor);
    const page = await readPage(taskId, cursor, pageSize);
    if (!isRecord(page) || !Array.isArray(page.events))
      throw invalidPage("the page must hold an events array");
    if (page.events.some((event) => !isRecord(event)))
      throw invalidPage("events must hold objects");
    if (typeof page.hasMore !== "boolean")
      throw invalidPage("hasMore must be a boolean");
    if (
      page.nextCursor !== null &&
      (!Number.isSafeInteger(page.nextCursor) || page.nextCursor < 0)
    )
      throw invalidPage("nextCursor must be null or a positive integer");
    if (!page.hasMore) {
      if (page.nextCursor !== null)
        throw invalidPage("nextCursor must be null on the last page");
      events.push(...page.events);
      return events;
    }
    if (page.nextCursor === null || page.nextCursor <= cursor)
      throw invalidPage("the next cursor must move forward");
    if (!page.events.length)
      throw invalidPage("an intermediate page cannot be empty");
    events.push(...page.events);
    cursor = page.nextCursor;
  }

  throw invalidPage(`more than ${MAX_PAGES} pages were requested`);
}

export const readMissionJournalPages = readAllMissionJournalPages;

export function serializeMissionJournal(events: readonly unknown[]): string {
  return (
    events
      .map((value) => {
        if (!isRecord(value))
          throw new Error("Invalid journal: an event is not an object");
        return JSON.stringify(value);
      })
      .join("\n") + (events.length ? "\n" : "")
  );
}

export const serializeJournal = serializeMissionJournal;

export async function collectMissionJournal(
  task: Pick<Task, "id" | "events">,
  readPage?: MissionJournalPageReader,
): Promise<MissionJournalEvent[]> {
  const nativeEvents = readPage
    ? await readAllMissionJournalPages(task.id, readPage)
    : [];
  return mergeMissionJournalEvents(nativeEvents, task.events);
}
