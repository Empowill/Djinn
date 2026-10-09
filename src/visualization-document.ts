// visualizationMessage reads a message from a sandboxed frame: the height it drew or its error, for one of tokens.
// It returns null for anything else: another window or origin, a forged token, a value out of bounds.
export function visualizationMessage(
  event: MessageEvent,
  frameWindow: Window | null,
  tokens: readonly string[],
) {
  const data: unknown = event.data;
  if (
    !frameWindow ||
    event.source !== frameWindow ||
    event.origin !== "null" ||
    !data ||
    typeof data !== "object" ||
    Array.isArray(data)
  )
    return null;
  const message = data as Record<string, unknown>;
  if (typeof message.token !== "string" || !tokens.includes(message.token))
    return null;
  if (
    message.type === "djinn:visualization-height" &&
    typeof message.height === "number" &&
    Number.isFinite(message.height) &&
    message.height >= 120 &&
    message.height <= 4000
  )
    return { height: Math.ceil(message.height) };
  if (
    message.type === "djinn:visualization-error" &&
    typeof message.message === "string" &&
    message.message.length <= 300
  )
    return { error: message.message };
  return null;
}
