import { memo, useState } from "react";

import { language, t } from "./i18n";
import { OperatingLoad } from "./operating-load";
import { PageHeader } from "./page-header";
import { shownTextSize } from "./text-size";
import { shownTheme } from "./theme";

// DocsPath is where djinn serves its documentation, the Grimoire (docs/site): its navigation on the left, beside it the
// splash page, then the book, Getting started, a page per concept, and the command line.
export const DocsPath = "/docs/";

// GRIMOIRE is the page of the window that shows the Grimoire, as the side panel's entry asks. A wish's id is a UUID:
// never this.
export const GRIMOIRE = "grimoire";

// Docs is the Grimoire as a page of the window, like the Hourglass: its header, then the site in the window's theme,
// text size and language, its navigation shown from the first view. It is a page of djinn's own server, in a frame
// that fills the page: each page of the book has its address there, and back and forward move between them.
export const Docs = memo(function Docs() {
  // The frame's address is set once: a new one would reload the site, its place and its search lost.
  const [src] = useState(() => {
    const text = shownTextSize();
    return `${DocsPath}?theme=${shownTheme()}&lang=${language}&embedded=1${text ? `&text=${text}` : ""}`;
  });
  return (
    <div className="wish-view grimoire">
      <PageHeader middle={<OperatingLoad />}>
        <strong>{t("docs.title")}</strong>
      </PageHeader>
      <iframe className="grimoire-frame" title={t("docs.title")} src={src} />
    </div>
  );
});
