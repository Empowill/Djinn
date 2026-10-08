import React from "react";
import { createRoot } from "react-dom/client";
import App from "./app";
import { LeadTerminalFrame } from "./lead-terminal";
import { UpdateBanner } from "./update-banner";
import { language } from "./i18n";
import "@fontsource/dm-sans/latin-400.css";
import "@fontsource/dm-sans/latin-500.css";
import "@fontsource/dm-sans/latin-600.css";
import "@fontsource/ibm-plex-mono/latin-400.css";
import "./styles.css";
document.documentElement.lang = language;
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <UpdateBanner />
    <LeadTerminalFrame>
      <App />
    </LeadTerminalFrame>
  </React.StrictMode>,
);
