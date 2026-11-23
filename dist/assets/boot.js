let booted = false;
const boot = () => {
  if (booted) return;
  booted = true;
  const css = document.querySelector('meta[name="djinn-entry-css"]')?.getAttribute("content");
  if (css) {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.crossOrigin = "";
    link.href = css;
    document.head.appendChild(link);
  }
  const js = document.querySelector('meta[name="djinn-entry-js"]')?.getAttribute("content");
  if (js) {
    const script = document.createElement("script");
    script.type = "module";
    script.crossOrigin = "";
    script.src = js;
    document.head.appendChild(script);
  }
};
const schedule = () => {
  requestAnimationFrame(() => setTimeout(boot, 0));
  setTimeout(boot, 32);
};
if (document.readyState === "complete") schedule();
else window.addEventListener("load", schedule, { once: true });
