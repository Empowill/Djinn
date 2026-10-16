// The theme, set before the page paints: ?theme= (the window passes its own), else the one chosen here, else the
// system's. site.js switches it. ?embedded=1 says the window shows the page, its close button over the bar's end;
// ?text= is the window's text size, which site.css scales the page by.
(function () {
  "use strict";
  var params = new URLSearchParams(location.search);
  var asked = params.get("theme");
  if (params.has("embedded"))
    document.documentElement.classList.add("embedded");
  var text = params.get("text");
  if (text === "large" || text === "compact" || text === "very-compact") {
    document.documentElement.dataset.textSize = text;
  }
  var kept = null;
  try {
    kept = localStorage.getItem("djinn.docs.theme");
  } catch (e) {
    // A page opened from a file may have no storage.
  }
  var theme = asked || kept;
  if (theme !== "dark" && theme !== "light") {
    theme = matchMedia("(prefers-color-scheme: light)").matches
      ? "light"
      : "dark";
  }
  document.documentElement.dataset.theme = theme;
})();
