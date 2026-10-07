import type { AppState, Artifact } from "./types";

/** Original demo, inspired by T3's inline HTML replies. No external requests. */
export const demoVisualization = String.raw`<!doctype html><html lang="fr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>
:root{color-scheme:dark;--bg:var(--background,#111);--panel:var(--surface,#191919);--line:var(--border,#333);--text:var(--foreground,#e7e7e7);--muted:#aaa;--series:#b9c9e0}*{box-sizing:border-box}body{margin:0;padding:20px;background:var(--bg);color:var(--text);font:12px/1.6 system-ui,sans-serif}h1{font-size:22px;line-height:1.3;margin:0 0 8px}h2{font-size:18px;margin:0 0 12px}p{color:var(--muted);margin:0 0 12px}button,input,select{font:inherit;color:inherit}button,select,input[type=search]{border:1px solid #555;border-radius:7px;background:#222;padding:7px 10px}button{cursor:pointer}button[aria-pressed=true]{background:#e7e7e7;color:#111}button:focus-visible,input:focus-visible,select:focus-visible{outline:2px solid #eee;outline-offset:3px}.toolbar{display:flex;flex-wrap:wrap;align-items:center;gap:8px;margin:12px 0}.panel{padding:18px;border:1px solid var(--line);border-radius:10px;background:var(--panel);margin-top:16px}.plot{height:180px;border-bottom:1px solid #555;position:relative}.bars{height:100%;display:flex;align-items:end;gap:14px;padding:0 12px}.bar{flex:1;max-width:80px;border-radius:4px 4px 0 0;background:var(--series)}.labels{display:grid;grid-template-columns:repeat(6,1fr);text-align:center;margin-top:6px}.line{height:100%;width:100%;display:block}table{border-collapse:collapse;width:100%}th,td{padding:8px;border-bottom:1px solid var(--line);text-align:left;font-size:12px}th{font-weight:600}caption{text-align:left;color:var(--muted);padding-bottom:8px}output{display:block;margin-top:10px}.cards{display:grid;grid-template-columns:repeat(3,1fr);gap:10px}.card{padding:12px;border:1px solid #444;border-radius:8px}.card strong{display:block;margin-bottom:6px}.state{color:var(--muted)}details{margin-top:12px}summary{cursor:pointer}.flow{display:grid;grid-template-columns:1fr auto 1fr auto 1fr;gap:10px;align-items:center}.branch{display:grid;gap:8px}.arrow{text-align:center;color:var(--muted)}[hidden]{display:none!important}@media(max-width:600px){body{padding:12px}.panel{padding:12px}.cards,.flow{grid-template-columns:1fr}.arrow{transform:rotate(90deg)}input[type=search]{max-width:100%}}
</style></head><body><h1>Une réponse peut devenir une petite application</h1><p>Démo Djinn · toutes les données sont fictives. Graphiques, filtres et interactions fonctionnent localement. Texte courant et contrôles : 12 px.</p>
<section class="panel"><h2>Graphique interactif</h2><div class="toolbar" role="group" aria-label="Type de graphique"><button type="button" id="barsMode" aria-pressed="true">Histogramme</button><button type="button" id="lineMode" aria-pressed="false">Courbe</button><button type="button" id="reset">Réinitialiser</button></div><div class="toolbar"><label for="volume">Volume du scénario</label><input id="volume" type="range" min="50" max="150" step="10" value="100"><span id="factor">100 %</span></div><p>Tâches fictives terminées par jour. Échelle fixe : de 0 à 50 tâches.</p><div class="plot" aria-hidden="true"><div class="bars" id="bars"></div><svg id="curve" class="line" viewBox="0 0 540 180" preserveAspectRatio="none" hidden><path d="M20 20H520 M20 80H520 M20 140H520" stroke="#333" fill="none"></path><polyline id="series" fill="none" stroke="#b9c9e0" stroke-width="3" vector-effect="non-scaling-stroke"></polyline></svg></div><div class="labels" aria-hidden="true"><span>Lun.</span><span>Mar.</span><span>Mer.</span><span>Jeu.</span><span>Ven.</span><span>Sam.</span></div><output id="total" aria-live="polite"></output><details><summary>Données du graphique</summary><table><caption>Valeurs simulées, identiques au graphique</caption><thead><tr><th scope="col">Jour</th><th scope="col">Tâches</th></tr></thead><tbody id="chartRows"></tbody></table></details></section>
<section class="panel"><h2>Tableau filtrable</h2><div class="toolbar"><label for="search">Rechercher un projet</label><input id="search" type="search" placeholder="Nom du projet"><label for="status">Statut</label><select id="status"><option value="all">Tous</option><option value="active">En cours</option><option value="review">En review</option><option value="done">Terminé</option></select></div><table><caption>Projets fictifs</caption><thead><tr><th scope="col">Projet</th><th scope="col">Statut</th><th scope="col">Équipe</th></tr></thead><tbody id="projects"></tbody></table><output id="count" aria-live="polite"></output></section>
<section class="panel"><h2>Mini-interface manipulable</h2><p>Simule une progression ; les choix restent temporaires et ne lancent aucun agent.</p><div class="cards"><div class="card"><strong>Navigation</strong><span class="state" id="state-nav">À faire</span><div class="toolbar"><button type="button" data-advance="nav">Faire avancer</button></div></div><div class="card"><strong>Composants</strong><span class="state" id="state-ui">À faire</span><div class="toolbar"><button type="button" data-advance="ui">Faire avancer</button></div></div><div class="card"><strong>Vérification</strong><span class="state" id="state-check">À faire</span><div class="toolbar"><button type="button" data-advance="check">Faire avancer</button></div></div></div><output id="progress" aria-live="polite">0 tâche terminée sur 3.</output></section>
<section class="panel"><h2>Graphe de contributions</h2><p>Deux contributions indépendantes peuvent se chevaucher. La vérification attend leurs résultats. Ce graphe est une simulation, la timeline Djinn montre les exécutions réelles.</p><div class="flow" role="group" aria-label="Dépendances entre contributions"><button type="button" data-node="mission" aria-pressed="true">Mission</button><span class="arrow" aria-hidden="true">→</span><div class="branch"><button type="button" data-node="design" aria-pressed="false">Design</button><button type="button" data-node="build" aria-pressed="false">Réalisation</button></div><span class="arrow" aria-hidden="true">→</span><button type="button" data-node="review" aria-pressed="false">Vérification</button></div><output id="role" aria-live="polite">Mission : intention, périmètres et décisions humaines.</output></section>
<p style="margin-top:16px">La source de ce support est sauvegardée dans la session. Les filtres et actions de cette page sont temporaires ; rouvrir le support restaure le scénario initial. Le support « Graphe Mermaid » démontre aussi le rendu natif du Markdown.</p>
<script>
const days=['Lundi','Mardi','Mercredi','Jeudi','Vendredi','Samedi'],base=[12,19,17,26,24,31],slider=document.getElementById('volume'),bars=document.getElementById('bars'),curve=document.getElementById('curve');
function chart(){const v=base.map(n=>Math.round(n*Number(slider.value)/100));document.getElementById('factor').textContent=slider.value+' %';bars.innerHTML=v.map(n=>'<div class="bar" style="height:'+n*3.3+'px"></div>').join('');document.getElementById('series').setAttribute('points',v.map((n,i)=>(20+i*100)+','+(174-n*3.3)).join(' '));document.getElementById('chartRows').innerHTML=v.map((n,i)=>'<tr><th scope="row">'+days[i]+'</th><td>'+n+'</td></tr>').join('');document.getElementById('total').textContent='Total simulé : '+v.reduce((a,b)=>a+b,0)+' tâches.'}
function mode(line){bars.hidden=line;curve.toggleAttribute('hidden',!line);document.getElementById('barsMode').setAttribute('aria-pressed',String(!line));document.getElementById('lineMode').setAttribute('aria-pressed',String(line))}
slider.addEventListener('input',chart);document.getElementById('barsMode').onclick=()=>mode(false);document.getElementById('lineMode').onclick=()=>mode(true);
const data=[{name:'Atlas',status:'active',team:'Produit'},{name:'Nova',status:'review',team:'Interface'},{name:'Echo',status:'done',team:'Qualité'},{name:'Orion',status:'active',team:'Interface'}],labels={active:'En cours',review:'En review',done:'Terminé'};
function filter(){const q=document.getElementById('search').value.toLocaleLowerCase('fr'),s=document.getElementById('status').value,rows=data.filter(p=>p.name.toLocaleLowerCase('fr').includes(q)&&(s==='all'||s===p.status));document.getElementById('projects').innerHTML=rows.map(p=>'<tr><th scope="row">'+p.name+'</th><td>'+labels[p.status]+'</td><td>'+p.team+'</td></tr>').join('');document.getElementById('count').textContent=rows.length?rows.length+' projet(s) fictif(s).':'Aucun projet correspondant.'}
document.getElementById('search').addEventListener('input',filter);document.getElementById('status').addEventListener('change',filter);
const states={nav:0,ui:0,check:0},steps=['À faire','En cours','Terminé'];function progress(){Object.keys(states).forEach(k=>document.getElementById('state-'+k).textContent=steps[states[k]]);document.getElementById('progress').textContent=Object.values(states).filter(s=>s===2).length+' tâche(s) terminée(s) sur 3.'}document.querySelectorAll('[data-advance]').forEach(b=>b.onclick=()=>{const k=b.dataset.advance;states[k]=(states[k]+1)%3;progress()});
const roles={mission:'Mission : intention, périmètres et décisions humaines.',design:'Design : contribution indépendante sur les parcours.',build:'Réalisation : contribution indépendante sur les composants.',review:'Vérification : attend le design et la réalisation. Le résultat est ensuite présenté à l’humain.'};document.querySelectorAll('[data-node]').forEach(b=>b.onclick=()=>{document.querySelectorAll('[data-node]').forEach(x=>x.setAttribute('aria-pressed',String(x===b)));document.getElementById('role').textContent=roles[b.dataset.node]});
document.getElementById('reset').onclick=()=>{slider.value='100';chart();mode(false);document.getElementById('search').value='';document.getElementById('status').value='all';filter();Object.keys(states).forEach(k=>states[k]=0);progress()};chart();filter();
</script></body></html>`;

export function demoArtifacts(updatedAt: string): Artifact[] {
  return [
    {
      id: "demo-visualization",
      title: "Laboratoire interactif · graphiques et mini-app",
      type: "visualization",
      content: demoVisualization,
      updatedAt,
    },
    {
      id: "demo-mermaid",
      title: "Graphe Mermaid · contributions et validation",
      type: "document",
      updatedAt,
      content:
        "# Un graphe directement dans le Markdown\n\nExemple simulé : design et réalisation ont des périmètres indépendants. La vérification utilise leurs deux résultats.\n\n```mermaid\nflowchart LR\n  M[Mission] --> D[Design]\n  M --> C[Réalisation]\n  D --> V[Vérification]\n  C --> V\n  V --> H[Validation humaine]\n```\n\nLe diagramme est rendu localement, sans CDN. Affichez sa source ou agrandissez-le. Le diagramme JSON éditable reste disponible dans le support « La mission, en un regard ».",
    },
    {
      id: "demo-guide",
      title: "Explorer les capacités de Djinn",
      type: "document",
      updatedAt,
      content:
        "# Une démo à manipuler\n\nToutes les contributions et données de cette mission sont **simulées**. Aucun modèle ni service externe n’est appelé.\n\n| Capacité | Où essayer |\n| --- | --- |\n| Graphiques, tableau, mini-app | Laboratoire interactif : volume, courbe, recherche, statuts |\n| Graphe Markdown | Graphe Mermaid : source et agrandissement |\n| Diagramme éditable | La mission, en un regard : édition des nœuds |\n| Maquette de parcours | Espace projets : choix de disposition |\n| Document GFM | Ce guide : tableau, checklist, lecture/édition/aperçu |\n| Code | Contrat de réponse visuelle : source consultable |\n| Capture | Aperçu graphique : image embarquée |\n| Décision bloquante | Répondre à Q01 puis lancer la démo |\n| Contributions | Agents et timeline : chevauchement simulé explicite |\n| Review | Ajouter un retour sur un support et le résoudre |\n| Source de vérité | Marquer un support et conserver sa révision humaine |\n| Session | Exporter la mission puis importer sa copie |\n\n## Limites réelles\nLes réglages internes du laboratoire sont temporaires. La source HTML, les supports, décisions et révisions sont sauvegardés dans la session. Les notifications système et fournisseurs réels se vérifient séparément dans une application installée ; la démo ne prétend pas les avoir validés.\n\n## À explorer\n- [ ] Modifier le volume et passer en courbe\n- [ ] Filtrer le tableau et faire avancer une carte\n- [ ] Agrandir le diagramme Mermaid\n- [ ] Éditer un document puis retrouver sa révision\n- [ ] Exporter la session\n\nRéférence du concept de réponses visuelles : [T3 Code, PR #15968](https://github.com/pingdotgg/t3code/pull/15968). Cette démo est une réalisation originale pour le moteur isolé de Djinn.",
    },
    {
      id: "demo-code",
      title: "Contrat de réponse visuelle",
      type: "code",
      updatedAt,
      content:
        '// Exemple documentaire : ce code ne lance aucun agent.\nconst support = {\n  type: "visualization",\n  title: "Graphique interactif",\n  content: "<style>body{font:12px/1.6 system-ui}</style>"\n    + "<button onclick=\\"this.textContent=\'Exploré\'\\">Explorer</button>"\n};\n// L’agent émet le support via DJINN_EVENT artifact.\n// HTML/CSS/JS autonomes, sans réseau ni pont natif.\n',
    },
    {
      id: "demo-screenshot",
      title: "Aperçu graphique · image embarquée",
      type: "screenshot",
      updatedAt,
      content: `data:image/svg+xml;base64,${btoa('<svg xmlns="http://www.w3.org/2000/svg" width="720" height="300" viewBox="0 0 720 300"><rect width="720" height="300" rx="16" fill="#191919"/><text x="28" y="40" font-family="system-ui" font-size="22" fill="#eee">Graphique de démonstration</text><text x="28" y="66" font-family="system-ui" font-size="12" fill="#aaa">Image illustrative statique. Donnees fictives.</text><path d="M40 250H680" stroke="#555"/><g fill="#b9c9e0"><rect x="70" y="190" width="60" height="60"/><rect x="170" y="155" width="60" height="95"/><rect x="270" y="165" width="60" height="85"/><rect x="370" y="120" width="60" height="130"/><rect x="470" y="130" width="60" height="120"/><rect x="570" y="95" width="60" height="155"/></g></svg>')}`,
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
        title: `Support disponible : ${a.title}`,
        detail: "Exemple ajouté à la démo, données fictives.",
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
