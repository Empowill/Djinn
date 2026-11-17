# Security policy

Djinn starts coding agents that run commands on your machine, in your projects. A flaw in Djinn can reach your
code, your credentials or your system: please tell us privately before anyone else.

## Report a vulnerability

Use GitHub's private reporting: the **Security** tab of [Empowill/Djinn](https://github.com/Empowill/Djinn), then
**Report a vulnerability** ([direct link](https://github.com/Empowill/Djinn/security/advisories/new)). Only the
maintainers read it. Never open a public issue, a pull request or a discussion for a vulnerability.

Say, as far as you know:

- the version (`djinn version`) and the system (Linux distribution, macOS or Windows version, which build);
- what an attacker can do, and what they need first (a local account, a crafted repository, a malicious web page, a
  prompt an agent reads…);
- the steps to reproduce it, or a proof of concept;
- whether it is already public.

Never send a real secret, nor personal data about anyone, in a report: a placeholder does.

## What happens next

- We acknowledge your report within **5 business days**.
- We tell you within **10 business days** whether we take it as a vulnerability, how severe we judge it, and what we
  plan.
- We fix it in a release, then publish a GitHub security advisory that credits you, unless you prefer not. We ask you
  to wait for that release, or **90 days** from your report, whichever comes first, before you disclose it; we agree
  on another date with you when a fix needs more time.

## Supported versions

Djinn is in beta: only the [latest release](https://github.com/Empowill/Djinn/releases/latest) gets security fixes.
A build from `main` gets them as they land.

## Scope

In scope: the `djinn` binary and its window, its local server and command line, the install scripts
(`scripts/install.sh`, `scripts/install.ps1`) and the release archives.

Out of scope, to report to their own projects: the agents Djinn starts (Claude Code, Codex, Antigravity) and their
models, Git and your forge (GitHub, GitLab), and the third-party libraries listed in
[`docs/THIRD_PARTY_NOTICES.md`](docs/THIRD_PARTY_NOTICES.md), unless Djinn uses them unsafely.
