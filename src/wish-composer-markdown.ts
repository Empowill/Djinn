// The request is sent as typed, Markdown or not: the textarea's text crosses the API boundary, never its DOM. Its one
// limit is WishServiceMakeRequest.prompt's.
export const MAX_PROMPT_CHARACTERS = 1_000_000;

export function promptTooLong(prompt: string): boolean {
  // Protobuf string limits count Unicode code points, rather than UTF-16 code units.
  if (prompt.length <= MAX_PROMPT_CHARACTERS) return false;
  let count = 0;
  for (const _character of prompt) {
    if (++count > MAX_PROMPT_CHARACTERS) return true;
  }
  return false;
}
