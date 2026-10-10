import "@fontsource/dm-sans/latin-400.css";
import "@fontsource/dm-sans/latin-500.css";
import "@fontsource/dm-sans/latin-600.css";
import "@fontsource/ibm-plex-mono/latin-400.css";
import React from "react";
import { createRoot } from "react-dom/client";

import { djinnTransport } from "./data/client";
import { DjinnProvider, createDjinn } from "./data/djinn";
import { language } from "./i18n";
import { LeadTerminalFrame } from "./lead-terminal";
import "./styles.css";
import { applyTheme, followSystem } from "./theme";
import { UpdateBanner } from "./update-banner";
import { WishApp } from "./wish-app";
import { startWishSmokePrewarm } from "./wish-smoke";

const userAgent = navigator.userAgent;
const nativeMac =
  /Macintosh|Mac OS X|MacIntel|MacPPC/i.test(
    `${navigator.platform} ${userAgent}`,
  ) &&
  (/wails\.io/i.test(userAgent) || window.location.protocol === "wails:");
document.documentElement.classList.toggle("native-mac", nativeMac);
document.documentElement.lang = language;
applyTheme();
followSystem();
// djinn serves the page and its API on the same origin, over http:// in a browser or wails:// in the window; fetch
// sends the session cookie on its own.
const djinn = createDjinn(djinnTransport(window.location.origin));
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <DjinnProvider djinn={djinn}>
      <UpdateBanner />
      <LeadTerminalFrame>
        <WishApp />
      </LeadTerminalFrame>
    </DjinnProvider>
  </React.StrictMode>,
);
startWishSmokePrewarm();
