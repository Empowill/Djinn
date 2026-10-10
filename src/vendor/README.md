# Mermaid local bundle

The npm registry was inaccessible (`ENOTFOUND`) in the execution environment. This is the already installed open-source Mermaid 11.16.1 standalone bundle from Claude.app `ion-dist/_frame-rt/_runtime/mermaid-11.16.1.min.js`, copied unchanged, not application source. No Anthropic fonts or application code were copied.

SHA-256: `18327bef70d96fb505fe7287d9f6a7362ebf07ff6576ddfaffb1a06f3e1a2954`.

Upstream version and MIT license verified against `https://github.com/mermaid-js/mermaid/tree/mermaid%4011.16.1`. Included license banners are preserved in the JS and copied to `BUNDLED-NOTICES.txt`; Djinn's packaged notices also include them.

Inlined by the build into `mermaid-frame.html` (`src/mermaid-frame.ts`), which the page loads on demand in a scripts-only sandboxed iframe: strict Mermaid security, no network, scripts allowed by their SHA-256 hashes only, and an authenticated bounded height/error channel. The 3.6 MB bundle stays outside the application's chunks. The source reaches the frame by message, never interpolated.

Future update: replace this bundle with a reproducible upstream package build when registry access is available, retain the notices and rerun the Mermaid interaction/isolation tests.
