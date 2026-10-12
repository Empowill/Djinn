---
id: 01a126be-b111-75c6-864c-605f3be2c459
code: T32
phase: 3
status: draft
---

# T32 · Desktop integration ideas

**Goal.** Enhance desktop integration with a tray icon showing active work, detached windows, autostart, and drag-and-drop project imports.

## Ideas from the Wails v3 study
From the Wails v3 study (08/10/2026), features Wails offers that Djinn has not taken up; each needs a decision before any work:
- A tray icon that shows the count of running workers and open questions (today the tray has Show and Quit only; GNOME needs an extension for a tray).
- Several windows: a wish or a worker detached into its own window, on a second screen.
- Djinn started when the user session opens.
- Add a project by dropping its folder on the window (T13 has the folder picker); whether a dropped folder gives its path is not checked on any system.

## Open questions
- Tray icon on Linux: how to handle environments like standard GNOME that do not show StatusNotifierItem trays without user extensions?
- Multi-window state: does a detached window maintain its own WebSocket or Connect client or share an in-memory state?
- Autostart mechanisms across Linux (xdg autostart desktop entry), macOS (LaunchAgents or Login Items), and Windows (Registry Run key or Startup folder).
- Does drag-and-drop provide absolute folder paths on Wayland and macOS WKWebView?

## Done when
- [ ] A decision is made and implemented for the candidate desktop integrations. (needs: desktop environment testing and decision)
