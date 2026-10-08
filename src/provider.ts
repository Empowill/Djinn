// The agent a wish runs: its lead's and, unless a task names another, its tasks'. The one chosen by default for a new
// wish is a preference of this page, kept in its storage like the theme.
import { Provider } from "../gen/ts/plan/v1/plan_pb";

const KEY = "djinn.provider";

// The agents a wish can be made with, by their names.
export const wishProviders: { provider: Provider; name: string }[] = [
  { provider: Provider.CLAUDE, name: "Claude" },
  { provider: Provider.CODEX, name: "Codex" },
  { provider: Provider.ANTIGRAVITY, name: "Antigravity" },
];

// providerName names the agent of a wish; a wish made before the choice runs Claude.
export function providerName(provider: Provider): string {
  return (
    wishProviders.find((p) => p.provider === provider)?.name ??
    (provider === Provider.FAKE ? "Fake" : "Claude")
  );
}

export function defaultProvider(): Provider {
  try {
    const value = Number(window.localStorage.getItem(KEY));
    return wishProviders.some((p) => p.provider === value)
      ? (value as Provider)
      : Provider.CLAUDE;
  } catch {
    return Provider.CLAUDE;
  }
}

export function setDefaultProvider(provider: Provider) {
  try {
    if (provider === Provider.CLAUDE) window.localStorage.removeItem(KEY);
    else window.localStorage.setItem(KEY, String(provider));
  } catch {
    // Private window or blocked storage: the choice lasts for this page only.
  }
}
