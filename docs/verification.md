# Vérification — parallélisme par périmètre et reconnexion native (6 octobre 2026)

Correction structurelle intégrée aux sources existantes : le plafond configuré limite les sous-agents simultanés ; les écrivains déclarés disjoints peuvent travailler ensemble. Les conflits de fichiers/dossiers, prérequis et limites sont signalés par des événements natifs. Le chef supervise en lecture seule, puis intègre après fermeture effective des écrivains. Aucune session utilisateur ni application installée n’a été modifiée.

| Commande                                                                                                                             | Résultat                   | Durée            | Exit | Log                                    |
| ------------------------------------------------------------------------------------------------------------------------------------ | -------------------------- | ---------------- | ---- | -------------------------------------- |
| `npm test`                                                                                                                           | PASS — 100 réussis, 3 SKIP | 1,084 s (runner) | 0    | `/tmp/djinn-parallel-final-tests.log`  |
| `npm run build`                                                                                                                      | PASS                       | 2,828 s          | 0    | `/tmp/djinn-parallel-final-build.log`  |
| `node --test tests/native-orchestration.test.cjs tests/mission-view.test.cjs tests/mission-render.test.cjs tests/scheduler.test.cjs` | PASS — 34 réussis          | 0,577 s (runner) | 0    | `/tmp/djinn-parallel-native-final.log` |

Preuves : deux écrivains indépendants démarrent ensemble ; une place libérée démarre immédiatement le travail suivant sans attendre toute une vague ; un conflit de fichier attend seulement son propriétaire ; une dépendance n’empêche pas un travail indépendant ; une indication ciblant un worker terminé provoque sa reprise pendant que son pair indépendant continue. Une indication pendant l’intégration ferme ce passage avant le retour du worker. Les inspecteurs gardent la lecture seule même dans une étape execute, et les événements conservent le stepId natif. Les chemins canoniques détectent alias de liens symboliques, sorties de racine et liens pendants. Un second pipeline dans un dossier identique, parent ou enfant est refusé jusqu’à la fin effective du propriétaire.

Le nouveau snapshot reflète uniquement les passages vivants du runtime. Sa lecture ne lance aucun fournisseur. La restauration rattache une mission/étape correspondante, rétablit les agents réellement actifs même si leurs événements de début étaient déjà enregistrés et conserve la sélection historique. Le journal borné et les eventId persistés empêchent le double traitement des événements. Les tests utilisent les vrais modules natifs avec Electron/processus/JSON-RPC simulés ; ils ne prouvent pas une reprise observée dans une fenêtre réelle.

Corrections pendant vérification : attentes de fixtures alignées sur les chemins canoniques `/private` ; polling de fixtures borné en durée plutôt qu’en nombre arbitraire de ticks ; garde native des navigations de visualisation consolidée, avec refus des sorties réseau même si leur hostname correspond à un support enregistré. Les tests de navigation vérifient la politique des événements [will-frame-navigate d’Electron](https://github.com/electron/electron/blob/main/docs/api/web-contents.md). Le recheck ciblé couvre les derniers ajustements ; le build a été refait après intégration. Le seul avertissement de build concerne la taille du bundle.

Fichiers : nouveau `electron/scheduler.cjs`, orchestration `electron/runtime.cjs`, `electron/main.cjs`, `electron/codex-app-server.cjs`, `electron/preload.cjs` ; contrat/persistance `src/types.ts`, `src/session-validation.ts` et module natif généré ; reconnexion et intégration `src/use-djinn.ts`, `src/mission-view.ts` ; configuration et suivi `src/step-timeline.tsx`, `src/mission-panels.tsx`, `src/app.tsx`, `src/styles.css` ; suites scheduler/native/workflow/vue/rendu/runtime ; README et protocole.

Délégation bornée autorisée : un worker Codex `gpt-5.6-luna`, raisonnement `max`, possédait exclusivement les quatre fichiers UI. Le parent possédait runtime/reconnexion/tests et a intégré la correction des indications du chef. Aucun worker supplémentaire, commit, push, publication ou appel payant aux fournisseurs. Le build UI du worker est enregistré dans `/tmp/djinn-parallel-ui-build.log` (PASS, 2,81 s). Aucun worktree créé ; statistiques fiables avant/après et coût/token des modèles indisponibles, le dépôt étant initialement entièrement non suivi.

Limites : les trois SKIP sont les tests de sockets loopback interdits dans cet environnement. Les restrictions Playwright/Electron décrites ci-dessous persistent. Le contrôle natif de l’app Djinn a retourné « Computer Use was not approved to use Djinn » ; ce refus n’a pas été contourné. La fenêtre installée n’a donc pas été relancée ni annoncée comme active. Les scopes Codex sont transmis dans la sandbox mais n’ont pas été essayés avec un fournisseur réel ; Claude conserve ses permissions et un contrat de périmètre dans le prompt, sans nouvelle isolation OS par fichier. La vérification visuelle et une reprise réelle restent à faire dans un environnement autorisé. Aucune conversation Codex extérieure ni donnée sauvegardée n’est transformée en activité native fictive.

## Vérification du passage précédent

# Vérification — intégration finale de la timeline (6 octobre 2026)

Les corrections finales de reprise pendant l’annulation, de question du superviseur et de navigation des visualisations sont intégrées. `npm test` PASS (1,139 s, exit 0, 104 tests dont 101 réussis et 3 ignorés), `npm run build` PASS (3,227 s, exit 0). Logs : `/tmp/codex-verification/djinn-final-test.log` et `/tmp/codex-verification/djinn-final-build.log`. Les empreintes des sources sont restées stables pendant ces contrôles.

Playwright reste empêché au lancement de Chromium (`Permission denied (1100)`, 0,983 s, exit 1, `/tmp/codex-verification/djinn-final-e2e.log`). Le smoke Electron échoue avant le parcours (`Process failed to launch!`, 0,285 s, exit 1, `/tmp/codex-verification/djinn-final-electron.log`). Aucune review visuelle réussie n’est déclarée. Le résultat, les corrections, le journal et les étapes manuelles sont documentés dans [timeline-integration.md](timeline-integration.md).

## État contrôlé précédent — workflow v2 et runtime durable

Changement structurel : projets et copies datées, workflows personnalisables, espaces par étape, reprise sur message/réponse, titre du harnais, Markdown/HTML isolé et orchestration Codex durable. Intégration des changements déjà présents ; aucune délégation supplémentaire et aucun modèle de fournisseur substitué. Les tests utilisent des faux fournisseurs et des dossiers temporaires ; aucune inférence payante ni mission utilisateur modifiée.

| Commande                                                              | Résultat | Durée mesurée | Exit | Log                             |
| --------------------------------------------------------------------- | -------- | ------------- | ---- | ------------------------------- |
| `npm test`                                                            | PASS     | 1.248 s       | 0    | `/tmp/djinn-final-tests.log`    |
| `npm run build`                                                       | PASS     | 2.833 s       | 0    | `/tmp/djinn-final-build.log`    |
| `node --test tests/agent-chat.test.cjs tests/mission-render.test.cjs` | PASS     | 0.463 s       | 0    | `/tmp/djinn-final-targeted.log` |

`npm test` : 81 tests, 78 PASS, 3 SKIP, aucun échec. Les trois cas ignorés ouvrent de véritables sockets loopback, interdits par le sandbox. Le recheck ciblé passe après les derniers ajustements de reprise et d’accusé. Le build inclut TypeScript et Vite ; son seul avertissement est la taille du bundle, sans erreur de compilation.

Preuves couvertes : migration native v1→v2 et sauvegarde des octets v1 ; migration idempotente et conservation des données invalides ; validation renderer/native identique ; projets et chemins relatifs/symlinks ; gardes de progression et validation humaine ; 16 agents configurables avec écrivains séquentiels ; titre renommé pendant un tour actif ; stderr WARN suivi d’une poursuite normale ; indication ciblée sans double écriture ; annulation/reprise après fermeture effective ; décisions bloquantes ; conversations distinctes du chef et du worker, sandbox/model conservés, steering actif sans interruption ; streaming et identité stable du message ; perte ambiguë de transport ne libérant pas prématurément l’écrivain ; isolation/CSP de visualisation ; sélection passive, filtres par étape, historique v1 et conservation d’événements tardifs ; rendu statique de la timeline au-dessus des onglets et boutons futurs désactivés.

Cinq parcours Playwright dédiés sont enregistrés dans `tests/workflow.spec.ts`. Ils n’ont pas pu être exécutés dans cet environnement : Vite reçoit `listen EPERM` sur `127.0.0.1:4317` (`/tmp/djinn-workflow-e2e.log`). Le mode de test sans serveur échoue avant le démarrage du navigateur avec `MachPortRendezvousServer bootstrap_check_in: Permission denied (1100)` (`/tmp/djinn-workflow-e2e-offline.log`). Aucun résultat fonctionnel ni capture visuelle n’en est déduit. Le navigateur de l’application a aussi refusé l’URL locale de prévisualisation ; ce refus n’a pas été contourné. Le smoke Electron complet reste à exécuter dans un environnement autorisant l’automatisation ; son faux Codex et ses assertions sont adaptés à l’app-server et au contrat v2.

Limites d’intégration : Codex demande une CLI avec app-server ; Claude conserve une supervision CLI en lecture seule et reprend le destinataire après interruption contrôlée, sans conversation native persistante. La vérification sur de vrais fournisseurs, la review visuelle à 1440/1024 px et les parcours clavier restent à faire. Les captures ci-dessous sont historiques et ne prouvent pas ce nouveau parcours.

Fichiers : contrat et migration dans `src/types.ts`, `src/workflow.ts`, `src/session-validation.ts` et leurs modules Electron générés ; orchestration dans `electron/runtime.cjs`, `electron/main.cjs`, `electron/preload.cjs`, `electron/codex-app-server.cjs` ; intégration dans `src/data.ts`, `src/use-djinn.ts`, `src/mission-view.ts`, `src/app.tsx`, `src/step-timeline.tsx`, `src/mission-panels.tsx` et styles ; supports dans `src/markdown-body.tsx`, `src/artifact-workspace.tsx`, `src/agent-chat.tsx`, `src/visualization-frame.tsx`, `electron/visualization.cjs`, CSP Vite ; suites unitaires, parcours Playwright et faux fournisseur/smoke associés. README et protocole décrivent le contrat actuel.

Aucun commit, push, installation, publication ou remplacement de l’application Djinn déjà installée. Les sources et `dist/` contiennent l’intégration ; l’application installée reste la v0.1.3. Le dépôt initial était entièrement non suivi : statistiques fiables de patch avant/après, durée totale, coût et consommation des modèles indisponibles. Aucune review externe supplémentaire n’a été lancée ; inspection et vérification finales réalisées directement dans Codex.

## Vérifications historiques

# Vérification de Djinn v0.1.2

Agents sous le header, timeline temporelle commune, interventions humaines datées, actions de fin et serveur de développement géré par Electron. Les chats d’agents et les orbes figés après leur passage sont conservés.

| Vérification                                   | Résultat                                      | Durée       | Exit | Log                                                |
| ---------------------------------------------- | --------------------------------------------- | ----------- | ---- | -------------------------------------------------- |
| Tests unitaires séquentiels                    | PASS — 38 tests, aucun ignoré                 | 2,56 s      | 0    | `/private/tmp/djinn-v012-unit-final.log`           |
| `npm run build`                                | PASS                                          | non mesurée | 0    | `/private/tmp/djinn-v012-final-build.log`          |
| `npm run test:e2e`                             | 10 parcours passés ; tolérance CSS à corriger | 1,2 min     | 1    | `/private/tmp/djinn-v012-e2e.log`                  |
| Recheck ciblé de la timeline après corrections | PASS — dernier des 11 parcours                | non mesurée | 0    | `/private/tmp/djinn-v012-temporal-final.log`       |
| Smoke Electron en production                   | PASS                                          | non mesurée | 0    | `/private/tmp/djinn-v012-electron-smoke-final.log` |
| `electron-builder --dir`                       | PASS — macOS arm64 v0.1.2                     | non mesurée | 0    | `/private/tmp/djinn-v012-package.log`              |

Les onze parcours couvrent décisions, démo, annotations, import/export, persistance, création de mission, orbes, header compact, chats et raccourcis, périodes parallèles, interventions, actions et notifications. Des départs décalés, chevauchements, indications, réponses et retours vérifient les positions temporelles et la déduplication. Les sessions anciennes sans début/fin n’inventent pas de durées. La vue utilise toute sa largeur et permet de zoomer.

Le smoke Electron utilise une CLI déterministe et un projet HTTP temporaire. Une implémentation terminée déclenche réellement son serveur : réponse HTTP vérifiée, ouverture par le pont natif, arrêt du port et relance sur le nouveau port. Les données sont isolées des missions utilisateur. Les tests de registre couvrent aussi projets ambigus, chemins autorisés, actions invalides et réutilisation explicite d’un serveur extérieur sans le tuer.

Corrections durant les tests : ancien test aligné sur le contrat renvoyant directement l’action ; journal du serveur réinitialisé à chaque relance pour détecter son nouveau port ; tolérance visuelle alignée sur l’arrondi CSS. La relance a maintenant son test de régression.

Les notifications natives sont vérifiées avec les événements Electron simulés, y compris le clic. Cela confirme le dispatch et le ciblage, sans prouver la réception par macOS. Questions et actions ont aussi des alertes persistantes et une cloche.

Deux workers Codex `gpt-5.6-luna`, raisonnement `max`, ont eu des responsabilités séparées : runtime/serveurs natifs et géométrie/vue temporelle. Le parent a intégré actions, notifications, horodatages et tests, puis corrigé et vérifié le résultat. Les opérations lourdes ont été exécutées séquentiellement. Aucun modèle payant appelé pendant les tests.

App autonome : aucun changement Dolmen, worktree supplémentaire, commit ou push. Les fichiers sont encore non suivis ; aucune statistique fiable de patch avant/après ou de coût des modèles n’est disponible. Aucune revue Cursor/Fable n’a été exécutée ; la revue finale a été faite dans Codex.

Preuves : [timeline temporelle](screenshots/temporal-timeline.png), [actions](screenshots/actions.png), [mission](screenshots/mission.png), [chat agent](screenshots/agent-chat.png), [review](screenshots/review.png). Périmètre et lancement : [README.md](../README.md).

Djinn 0.1.2 a été relancé après vérification de l’absence d’exécution active. Les deux missions utilisateur sont conservées. Le bundle précédent est gardé dans `release-previous-1/`.

## Ajustement v0.1.3

Changement simple réalisé directement par le parent, sans délégation : panneau Activité retiré de Mission et contenu étendu sur toute la largeur disponible. Build PASS (exit 0, durée non mesurée, `/private/tmp/djinn-v013-build.log`) ; deux parcours existants de layout PASS (exit 0, `/private/tmp/djinn-v013-layout.log`) ; packaging PASS (exit 0, `/private/tmp/djinn-v013-package.log`). Inspection visuelle à 1440 px et parcours à 1024 px. La version 0.1.3 est relancée, ses deux missions conservées, sans interrompre d’exécution active. Le précédent bundle reste dans `release-previous-2/`.
