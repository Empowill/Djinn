# Timeline personnalisable — intégration finale

Passage du 6 octobre 2026, réalisé directement par le chef, sans sous-agent supplémentaire. Les modifications existantes ont été conservées. Aucun commit, push, déploiement, remplacement de l’application installée ou lancement de serveur permanent.

## Résultat local

Le code contient les projets réutilisables et leurs conventions, les workflows personnalisables, la timeline sous le header et les espaces filtrés par étape. Les étapes futures sont désactivées. La création n’impose plus de titre ; le harnais peut le produire, avec priorité au renommage humain. La configuration permet de 1 à 16 sous-agents. Les messages démarrent ou reprennent l’étape courante, tandis que la validation des résultats, des transitions, de la review et de la livraison reste humaine.

Les supports utilisent Markdown GFM, conservent les révisions humaines et proposent des visualisations HTML isolées. Les prompts encouragent les illustrations pertinentes et demandent de vérifier la disponibilité des skills, dont `grill-me`. Le suivi affiche les agents réellement actifs, leur tâche, le chef en attente des sous-agents, la dernière activité horodatée, l’accès au chat et la durée du silence.

## Corrections de ce passage

- `src/use-djinn.ts` : un reçu `prevented/run_cancelled` programme la reprise même si l’interface n’est pas encore en pause. Si la fermeture arrive avant le reçu, la reprise démarre immédiatement depuis l’état courant. Un reçu tardif appartenant à un ancien passage ne relance pas son remplacement. Les erreurs `run_inactive` de la transmission différée conservent l’indication pour la reprise.
- `electron/main.cjs` : une question bloquante du superviseur interrompt proprement les passages concernés. La pipeline garde ses verrous jusqu’à la fin native, puis attend les réponses sans démarrer l’intégration ni un nouveau superviseur. Les navigations des sous-frames sont filtrées : seul l’hôte peut charger un support enregistré. Les redirections des sous-frames sont refusées.
- `electron/visualization.cjs` : le registre accepte uniquement ses URL de documents isolés, avec un protocole, un chemin et des paramètres contrôlés.
- `src/renderer-policy.ts` et `vite.config.ts` : CSP parent actif en développement et en production ; les frames sont limitées aux documents isolés. Le développement conserve les capacités nécessaires à Vite.
- Tests : hook réel avec bridge et ordonnancement contrôlés, question du superviseur avec faux app-server, navigations natives et CSP. Les régressions existantes couvrent les avertissements Codex suivis d’une poursuite normale et le ciblage d’un worker sans écrivains concurrents.

## Journal et preuves

1. Réception confirmée par un événement `note` ; continuation exclusive retenue selon la décision humaine.
2. Reproduction : dans les deux ordres reçu/fermeture, une indication conservée ne relançait pas l’étape. Une question publiée par le superviseur laissait les tours actifs. Une navigation de frame n’était pas filtrée.
3. Corrections puis vérification : ces régressions passent. Les réponses bloquantes arrivant avant la fermeture reprennent une seule fois, après toutes les décisions, sans approbation automatique.
4. Empreintes SHA-256 des sources, du runtime, des tests et de la configuration identiques avant et après les contrôles finaux.

| Commande                                                                                                 | Résultat                                                                          | Durée   | Exit | Log                                                |
| -------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- | ------- | ---- | -------------------------------------------------- |
| `npm test`                                                                                               | PASS — 104 tests, 101 réussis, 3 ignorés                                          | 1,139 s | 0    | `/tmp/codex-verification/djinn-final-test.log`     |
| `npm run build`                                                                                          | PASS                                                                              | 3,227 s | 0    | `/tmp/codex-verification/djinn-final-build.log`    |
| Transformation HTML Vite en mode middleware, sans socket d’écoute                                        | PASS — CSP de développement réellement injecté                                    | 0,055 s | 0    | `/tmp/codex-verification/djinn-dev-csp.html`       |
| `DJINN_OFFLINE_E2E=1 npx playwright test tests/workflow.spec.ts tests/supports.spec.ts --max-failures=1` | Empêché avant le parcours : lancement Chromium refusé, `Permission denied (1100)` | 0,983 s | 1    | `/tmp/codex-verification/djinn-final-e2e.log`      |
| `DJINN_SMOKE_PRODUCTION=1 node scripts/electron-smoke.cjs`                                               | Empêché au lancement : `Process failed to launch!`                                | 0,285 s | 1    | `/tmp/codex-verification/djinn-final-electron.log` |

Les trois tests ignorés nécessitent des sockets loopback refusées dans cet environnement. Les tests natifs utilisent des processus contrôlés, sans appeler un modèle. Aucune review visuelle, isolation dynamique dans un véritable navigateur ou réussite du smoke Electron n’est revendiquée. Ces vérifications restent à effectuer dans l’environnement local de Djinn ; la validation humaine reste ouverte.

## Prévisualisation et validation humaine

Script de serveur identifié : **`dev`**, paquet **`.`**. Djinn doit le démarrer et le gérer après le passage. Son URL ne doit être présentée comme prête qu’après la vérification HTTP native. `desktop` est le lanceur Electron de développement pour vérifier le pont natif et les agents ; la prévisualisation Vite seule ne lance pas de fournisseurs réels.

Parcours à vérifier : créer une mission dans un projet enregistré avec huit agents ; envoyer un message ; répondre à toutes les questions ; consulter et valider la carte résultat ; lancer l’étape suivante ; consulter une étape passée ; manipuler un support Markdown et une visualisation ; vérifier les états du chef, du worker et les avertissements. Tester à 1440 et 1024 px, puis au clavier. L’export de session v2 se fait depuis Djinn et conserve les supports et décisions.

Classification : changement structurel, intégration supervisée directement par Codex. Aucun reviewer externe utilisé. Le dépôt étant entièrement non suivi avant ce passage, aucune statistique fiable du patch initial ni mesure de coût des modèles n’est disponible.
