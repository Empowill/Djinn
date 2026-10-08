import type { AppState, Artifact } from "./types";
import { language, t } from "./i18n";

/** Original demo, inspired by T3's inline HTML replies. No external requests. */
export const demoVisualization = String.raw`<!doctype html><html lang="${language}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>
:root{color-scheme:dark;--bg:var(--background,#111);--panel:var(--surface,#191919);--line:var(--border,#333);--text:var(--foreground,#e7e7e7);--muted:#aaa;--series:#b9c9e0}*{box-sizing:border-box}body{margin:0;padding:20px;background:var(--bg);color:var(--text);font:12px/1.6 system-ui,sans-serif}h1{font-size:22px;line-height:1.3;margin:0 0 8px}h2{font-size:18px;margin:0 0 12px}p{color:var(--muted);margin:0 0 12px}button,input,select{font:inherit;color:inherit}button,select,input[type=search]{border:1px solid #555;border-radius:7px;background:#222;padding:7px 10px}button{cursor:pointer}button[aria-pressed=true]{background:#e7e7e7;color:#111}button:focus-visible,input:focus-visible,select:focus-visible{outline:2px solid #eee;outline-offset:3px}.toolbar{display:flex;flex-wrap:wrap;align-items:center;gap:8px;margin:12px 0}.panel{padding:18px;border:1px solid var(--line);border-radius:10px;background:var(--panel);margin-top:16px}.plot{height:180px;border-bottom:1px solid #555;position:relative}.bars{height:100%;display:flex;align-items:end;gap:14px;padding:0 12px}.bar{flex:1;max-width:80px;border-radius:4px 4px 0 0;background:var(--series)}.labels{display:grid;grid-template-columns:repeat(6,1fr);text-align:center;margin-top:6px}.line{height:100%;width:100%;display:block}table{border-collapse:collapse;width:100%}th,td{padding:8px;border-bottom:1px solid var(--line);text-align:left;font-size:12px}th{font-weight:600}caption{text-align:left;color:var(--muted);padding-bottom:8px}output{display:block;margin-top:10px}.cards{display:grid;grid-template-columns:repeat(3,1fr);gap:10px}.card{padding:12px;border:1px solid #444;border-radius:8px}.card strong{display:block;margin-bottom:6px}.state{color:var(--muted)}details{margin-top:12px}summary{cursor:pointer}.flow{display:grid;grid-template-columns:1fr auto 1fr auto 1fr;gap:10px;align-items:center}.branch{display:grid;gap:8px}.arrow{text-align:center;color:var(--muted)}[hidden]{display:none!important}@media(max-width:600px){body{padding:12px}.panel{padding:12px}.cards,.flow{grid-template-columns:1fr}.arrow{transform:rotate(90deg)}input[type=search]{max-width:100%}}
</style></head><body><h1>${t("demo_supports.headline")}</h1><p>${t("demo_supports.intro")}</p>
<section class="panel"><h2>${t("demo_supports.chart_title")}</h2><div class="toolbar" role="group" aria-label="${t("demo_supports.chart_type")}"><button type="button" id="barsMode" aria-pressed="true">${t("demo_supports.bars")}</button><button type="button" id="lineMode" aria-pressed="false">${t("demo_supports.curve")}</button><button type="button" id="reset">${t("demo_supports.reset")}</button></div><div class="toolbar"><label for="volume">${t("demo_supports.volume")}</label><input id="volume" type="range" min="50" max="150" step="10" value="100"><span id="factor">100 %</span></div><p>${t("demo_supports.chart_hint")}</p><div class="plot" aria-hidden="true"><div class="bars" id="bars"></div><svg id="curve" class="line" viewBox="0 0 540 180" preserveAspectRatio="none" hidden><path d="M20 20H520 M20 80H520 M20 140H520" stroke="#333" fill="none"></path><polyline id="series" fill="none" stroke="#b9c9e0" stroke-width="3" vector-effect="non-scaling-stroke"></polyline></svg></div><div class="labels" aria-hidden="true"><span>${t("demo_supports.day_short_mon")}</span><span>${t("demo_supports.day_short_tue")}</span><span>${t("demo_supports.day_short_wed")}</span><span>${t("demo_supports.day_short_thu")}</span><span>${t("demo_supports.day_short_fri")}</span><span>${t("demo_supports.day_short_sat")}</span></div><output id="total" aria-live="polite"></output><details><summary>${t("demo_supports.chart_data")}</summary><table><caption>${t("demo_supports.chart_caption")}</caption><thead><tr><th scope="col">${t("demo_supports.day")}</th><th scope="col">${t("demo_supports.tasks")}</th></tr></thead><tbody id="chartRows"></tbody></table></details></section>
<section class="panel"><h2>${t("demo_supports.table_title")}</h2><div class="toolbar"><label for="search">${t("demo_supports.search")}</label><input id="search" type="search" placeholder="${t("demo_supports.search_placeholder")}"><label for="status">${t("demo_supports.status")}</label><select id="status"><option value="all">${t("demo_supports.all")}</option><option value="active">${t("demo_supports.in_progress")}</option><option value="review">${t("demo_supports.in_review")}</option><option value="done">${t("demo_supports.done")}</option></select></div><table><caption>${t("demo_supports.projects")}</caption><thead><tr><th scope="col">${t("demo_supports.project")}</th><th scope="col">${t("demo_supports.status")}</th><th scope="col">${t("demo_supports.team")}</th></tr></thead><tbody id="projects"></tbody></table><output id="count" aria-live="polite"></output></section>
<section class="panel"><h2>${t("demo_supports.mini_title")}</h2><p>${t("demo_supports.mini_hint")}</p><div class="cards"><div class="card"><strong>${t("demo_supports.card_nav")}</strong><span class="state" id="state-nav">${t("demo_supports.todo")}</span><div class="toolbar"><button type="button" data-advance="nav">${t("demo_supports.advance")}</button></div></div><div class="card"><strong>${t("demo_supports.card_ui")}</strong><span class="state" id="state-ui">${t("demo_supports.todo")}</span><div class="toolbar"><button type="button" data-advance="ui">${t("demo_supports.advance")}</button></div></div><div class="card"><strong>${t("demo_supports.card_check")}</strong><span class="state" id="state-check">${t("demo_supports.todo")}</span><div class="toolbar"><button type="button" data-advance="check">${t("demo_supports.advance")}</button></div></div></div><output id="progress" aria-live="polite">${t("demo_supports.progress_initial")}</output></section>
<section class="panel"><h2>${t("demo_supports.graph_title")}</h2><p>${t("demo_supports.graph_hint")}</p><div class="flow" role="group" aria-label="${t("demo_supports.graph_label")}"><button type="button" data-node="mission" aria-pressed="true">${t("demo_supports.node_wish")}</button><span class="arrow" aria-hidden="true">→</span><div class="branch"><button type="button" data-node="design" aria-pressed="false">${t("demo_supports.node_design")}</button><button type="button" data-node="build" aria-pressed="false">${t("demo_supports.node_build")}</button></div><span class="arrow" aria-hidden="true">→</span><button type="button" data-node="review" aria-pressed="false">${t("demo_supports.card_check")}</button></div><output id="role" aria-live="polite">${t("demo_supports.role_wish")}</output></section>
<p style="margin-top:16px">${t("demo_supports.footer")}</p>
<script>
const days=[${JSON.stringify(t("demo_supports.day_monday"))},${JSON.stringify(t("demo_supports.day_tuesday"))},${JSON.stringify(t("demo_supports.day_wednesday"))},${JSON.stringify(t("demo_supports.day_thursday"))},${JSON.stringify(t("demo_supports.day_friday"))},${JSON.stringify(t("demo_supports.day_saturday"))}],base=[12,19,17,26,24,31],slider=document.getElementById('volume'),bars=document.getElementById('bars'),curve=document.getElementById('curve');
function chart(){const v=base.map(n=>Math.round(n*Number(slider.value)/100));document.getElementById('factor').textContent=slider.value+' %';bars.innerHTML=v.map(n=>'<div class="bar" style="height:'+n*3.3+'px"></div>').join('');document.getElementById('series').setAttribute('points',v.map((n,i)=>(20+i*100)+','+(174-n*3.3)).join(' '));document.getElementById('chartRows').innerHTML=v.map((n,i)=>'<tr><th scope="row">'+days[i]+'</th><td>'+n+'</td></tr>').join('');document.getElementById('total').textContent=${JSON.stringify(t("demo_supports.total"))}.replace('{total}',v.reduce((a,b)=>a+b,0))}
function mode(line){bars.hidden=line;curve.toggleAttribute('hidden',!line);document.getElementById('barsMode').setAttribute('aria-pressed',String(!line));document.getElementById('lineMode').setAttribute('aria-pressed',String(line))}
slider.addEventListener('input',chart);document.getElementById('barsMode').onclick=()=>mode(false);document.getElementById('lineMode').onclick=()=>mode(true);
const data=[{name:'Atlas',status:'active',team:${JSON.stringify(t("demo_supports.team_product"))}},{name:'Nova',status:'review',team:${JSON.stringify(t("demo_supports.team_interface"))}},{name:'Echo',status:'done',team:${JSON.stringify(t("demo_supports.team_quality"))}},{name:'Orion',status:'active',team:${JSON.stringify(t("demo_supports.team_interface"))}}],labels={active:${JSON.stringify(t("demo_supports.in_progress"))},review:${JSON.stringify(t("demo_supports.in_review"))},done:${JSON.stringify(t("demo_supports.done"))}};
function filter(){const q=document.getElementById('search').value.toLocaleLowerCase('${language}'),s=document.getElementById('status').value,rows=data.filter(p=>p.name.toLocaleLowerCase('${language}').includes(q)&&(s==='all'||s===p.status));document.getElementById('projects').innerHTML=rows.map(p=>'<tr><th scope="row">'+p.name+'</th><td>'+labels[p.status]+'</td><td>'+p.team+'</td></tr>').join('');document.getElementById('count').textContent=rows.length?${JSON.stringify(t("demo_supports.count"))}.replace('{count}',rows.length):${JSON.stringify(t("demo_supports.no_match"))}}
document.getElementById('search').addEventListener('input',filter);document.getElementById('status').addEventListener('change',filter);
const states={nav:0,ui:0,check:0},steps=[${JSON.stringify(t("demo_supports.todo"))},${JSON.stringify(t("demo_supports.in_progress"))},${JSON.stringify(t("demo_supports.done"))}];function progress(){Object.keys(states).forEach(k=>document.getElementById('state-'+k).textContent=steps[states[k]]);document.getElementById('progress').textContent=${JSON.stringify(t("demo_supports.progress"))}.replace('{count}',Object.values(states).filter(s=>s===2).length)}document.querySelectorAll('[data-advance]').forEach(b=>b.onclick=()=>{const k=b.dataset.advance;states[k]=(states[k]+1)%3;progress()});
const roles={mission:${JSON.stringify(t("demo_supports.role_wish"))},design:${JSON.stringify(t("demo_supports.role_design"))},build:${JSON.stringify(t("demo_supports.role_build"))},review:${JSON.stringify(t("demo_supports.role_review"))}};document.querySelectorAll('[data-node]').forEach(b=>b.onclick=()=>{document.querySelectorAll('[data-node]').forEach(x=>x.setAttribute('aria-pressed',String(x===b)));document.getElementById('role').textContent=roles[b.dataset.node]});
document.getElementById('reset').onclick=()=>{slider.value='100';chart();mode(false);document.getElementById('search').value='';document.getElementById('status').value='all';filter();Object.keys(states).forEach(k=>states[k]=0);progress()};chart();filter();
</script></body></html>`;

export function demoArtifacts(updatedAt: string): Artifact[] {
  return [
    {
      id: "demo-visualization",
      title: t("demo_supports.lab_title"),
      type: "visualization",
      content: demoVisualization,
      updatedAt,
    },
    {
      id: "demo-mermaid",
      title: t("demo_supports.mermaid_title"),
      type: "document",
      updatedAt,
      content: t("demo_supports.mermaid_content"),
    },
    {
      id: "demo-guide",
      title: t("demo_supports.guide_title"),
      type: "document",
      updatedAt,
      content: t("demo_supports.guide_content"),
    },
    {
      id: "demo-code",
      title: t("demo_supports.code_title"),
      type: "code",
      updatedAt,
      content: t("demo_supports.code_content"),
    },
    {
      id: "demo-screenshot",
      title: t("demo_supports.screenshot_title"),
      type: "screenshot",
      updatedAt,
      content: `data:image/svg+xml;base64,${btoa(`<svg xmlns="http://www.w3.org/2000/svg" width="720" height="300" viewBox="0 0 720 300"><rect width="720" height="300" rx="16" fill="#191919"/><text x="28" y="40" font-family="system-ui" font-size="22" fill="#eee">${t("demo_supports.screenshot_heading")}</text><text x="28" y="66" font-family="system-ui" font-size="12" fill="#aaa">${t("demo_supports.screenshot_caption")}</text><path d="M40 250H680" stroke="#555"/><g fill="#b9c9e0"><rect x="70" y="190" width="60" height="60"/><rect x="170" y="155" width="60" height="95"/><rect x="270" y="165" width="60" height="85"/><rect x="370" y="120" width="60" height="130"/><rect x="470" y="130" width="60" height="120"/><rect x="570" y="95" width="60" height="155"/></g></svg>`)}`,
    },
  ].map((a) => ({ ...a, revision: 1, editedBy: "agent" }) as Artifact);
}

/** Add missing examples to saved demos, preserving every human revision. */
export function enrichDemoState(state: AppState): AppState {
  return {
    ...state,
    tasks: state.tasks.map((task) => {
      if (!task.demo) return task;
      const existing = new Set(task.artifacts.map((a) => a.id));
      const missing = demoArtifacts(task.createdAt)
        .filter((a) => !existing.has(a.id))
        .map((a) => ({
          ...a,
          stepId: task.steps?.[0]?.id || task.activeStepId,
        }));
      const supportEvents = missing.map((a) => ({
        id: `demo-support-event:${a.id}`,
        time: a.updatedAt,
        type: "note" as const,
        title: t("chat.event_artifact_available", { title: a.title }),
        detail: t("demo_supports.event_detail"),
        agentId: "lead",
        stepId: a.stepId,
      }));
      return missing.length
        ? {
            ...task,
            artifacts: [...task.artifacts, ...missing],
            events: [...task.events, ...supportEvents],
          }
        : task;
    }),
  };
}
