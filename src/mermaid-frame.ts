// The document a Mermaid diagram draws in. The build writes it next to index.html, so the server and the native
// window serve it like any page, from the page's own origin: the page allows only 'self' in frames.
//
// A blob: or srcdoc frame inherits the page's policy, which forbids inline scripts: Mermaid never ran there. A
// document loaded from a URL has its own policy, and this one allows exactly its two scripts, by their hashes. The
// frame is sandboxed with scripts only: an opaque origin, no access to the page, no network.
export const MERMAID_FRAME = "mermaid-frame.html";

// The height is the body's, margins included: the document's would never fall below the frame's.
// The frame asks for nothing until the page posts a render request. It answers with the request's token: the height
// of what it drew, once drawn, or an error: "engine" when Mermaid did not load, "render" when the source does not
// draw.
const bootstrap = `(()=>{if(self.origin!=="null")return;let token="",drawn="";const send=(type,data)=>parent.postMessage({type,...data},"*");const report=()=>drawn&&send("djinn:visualization-height",{token:drawn,height:Math.min(4000,Math.max(120,document.body.scrollHeight+24))});new ResizeObserver(report).observe(document.body);addEventListener("message",async(event)=>{const m=event.data;if(event.source!==parent||!m||m.type!=="djinn:mermaid-render"||typeof m.token!=="string"||typeof m.source!=="string")return;token=m.token;document.documentElement.style.colorScheme=m.scheme;document.body.style.background=m.ground;document.body.style.color=m.text;if(typeof mermaid==="undefined")return send("djinn:visualization-error",{token,message:"engine"});try{mermaid.initialize({...m.config,startOnLoad:false,securityLevel:"strict",fontFamily:"system-ui",flowchart:{htmlLabels:false},maxTextSize:30000,suppressErrorRendering:true});const {svg}=await mermaid.render("diagram",m.source);if(token!==m.token)return;document.getElementById("render").innerHTML=svg;drawn=token;report()}catch{if(token===m.token)send("djinn:visualization-error",{token,message:"render"})}})})();`;

async function sha256(text: string) {
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(text),
  );
  return `'sha256-${btoa(String.fromCharCode(...new Uint8Array(digest)))}'`;
}

// framePolicy allows the scripts of hashes and nothing else to run. It names no connect-src: default-src 'none'
// forbids every connection.
export const framePolicy = (hashes: string[]) =>
  [
    "default-src 'none'",
    `script-src ${hashes.join(" ")}`,
    "style-src 'unsafe-inline'",
    "img-src data:",
    "font-src data:",
    "frame-src 'none'",
    "object-src 'none'",
    "base-uri 'none'",
    "form-action 'none'",
  ].join("; ");

// mermaidFrame returns the frame's document with runtime, Mermaid's standalone bundle, inline. A runtime that would
// end its script element early is refused: escaping it would change its hash and its code.
export async function mermaidFrame(runtime: string) {
  if (/<\/?script/i.test(runtime))
    throw new Error("the Mermaid runtime contains a script tag");
  const policy = framePolicy([await sha256(runtime), await sha256(bootstrap)]);
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${policy}"><meta name="referrer" content="no-referrer"><style>body{margin:12px;font:12px/1.6 system-ui}svg{max-width:100%;height:auto;display:block;margin:auto}#render{overflow:auto}</style></head><body><div id="render"></div><script>${runtime}</script><script>${bootstrap}</script></body></html>`;
}
