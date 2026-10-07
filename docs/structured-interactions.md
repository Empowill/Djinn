# Mission : questions, comptes rendus et recettes indépendantes

## Diagnostic

Le gain de vitesse de la mission multi-ticket est à conserver. Le problème est la fiabilité de la restitution et des interactions : des informations produites par les agents ne devenaient pas toujours des éléments utilisables dans Mission.

Le parcours comportait plusieurs ruptures identifiées dans le code :

- Les questions pouvaient utiliser `question` au lieu de `context`, ou des choix sous forme de chaînes. Les descriptions n’étaient alors pas correctement conservées. Les questions sans choix ne proposaient pas immédiatement une réponse libre.
- Le rapport du chef pouvait être traité comme celui d’un sous-agent, car son passage natif possède aussi un parent. Le résultat d’étape n’était alors pas appliqué à la mission.
- Un artefact de type `markdown`, comme le `DJINN_EVENT` fourni dans la conversation, n’était pas reconnu comme document.
- Le texte `DJINN_EVENT` servait simultanément de message et de contrat de données. Une publication pouvait sembler réussie dans une trace sans être enregistrée comme interaction exploitable.
- Les actions étaient trop liées à l’étape consultée. Un ticket testable ne disposait pas d’une vue globale de recette pendant que l’autre continuait.
- L’état natif « running » pouvait masquer une autorisation en attente. « Instruction livrée » pouvait aussi être interprété comme une réponse de l’agent, alors qu’il ne s’agissait que d’une remise au runtime.
- Une fin de passage pouvait faire avancer une étape automatique sans compte rendu explicite. Une question bloquante d’un worker pouvait arrêter les travaux indépendants.

## Comportement livré

| Retour demandé | Comportement |
| --- | --- |
| Questions complètes et répondables dans Mission | Outil `publish_question`, contexte obligatoire, choix normalisés et réponse libre ; auteur et périmètre conservés. |
| Permissions et questions seulement quand pertinentes | Autorisations natives en attente uniquement ; sections actives retirées après résolution. Les décisions restent dans l’historique. |
| État fiable des agents | Mission, avatars, conversation et Timeline indiquent une autorisation ou une réponse attendue ; une instruction remise n’est pas présentée comme une réponse confirmée. |
| Vue globale des tickets testables | Liste compacte « Tâches et recette » dans Mission, avec propriétaire et détails pliables ; `update_task` lie chaque ticket ou sous-partie à une recette. |
| Tester B pendant les retours de A | Actions disponibles indépendamment du stade de leurs voisins ; « Prête à tester », « En cours de test », « Retour à traiter », « Correction en cours », « Validé par vous », « Test reporté ». |
| Recette sans serveur | Les recettes manuelles ou accessibles par lien peuvent être commencées et recevoir un résultat, comme les prévisualisations locales. |
| Nouvelle correction testable | La dernière version de recette devient la référence ; le retour d’une ancienne version ne masque pas la correction. Les résultats humains et le test commencé survivent au rechargement pour la même version. |
| Rapports clairs à chaque étape | `publish_report` pour le worker, `publish_step_report` pour le chef : statut, résumé, fait, reste et preuves. Une étape automatique issue du plan de l’agent exige son rapport explicite. |
| Rapports et artefacts réellement enregistrés | `publish_artifact`, journal natif et projection atomique persistante avant l’accusé de réception de l’outil. Le contenu complet des documents est conservé. |
| Moins d’autorisations routinières | `inspect_test_environment` lit les noms de scripts, liste les conteneurs Docker locaux et vérifie des URL loopback explicites. Commande fixe, pas de shell fourni par le modèle ; cache bref pour éviter les répétitions. |
| Maintenir la vélocité | Question de worker limitée par défaut à son agent ; les travaux indépendants continuent. Publication seulement aux changements utiles, contexte compact, vérifications ciblées réutilisées. |

Il n’y a pas de nouvelle vue « état vivant ». Mission reste le lieu des décisions et des actions, Timeline celui de l’exécution, et les rapports utilisent les surfaces existantes.

## Contrats et reprise

Les outils natifs valident leurs arguments côté Electron et vérifient l’appartenance du thread et du passage actifs à la mission. Un worker ne peut pas publier le résultat final du chef. Les sous-agents observés du fournisseur utilisent la même vérification d’ascendance.

Le journal conserve les événements. Une projection bornée des publications structurées permet la reprise après fermeture du renderer, sans relancer de travail ni approuver d’étape. Une prévisualisation restaurée reste arrêtée tant que le runtime n’a pas confirmé son serveur. Les réponses humaines déjà enregistrées ne sont pas rouvertes par cette reprise.

Le protocole texte reste compatible pour Claude et les anciens événements : alias `question`/`context`, choix textuels et type `markdown`. Les rapports complets déjà présents sous forme de marqueurs dans les notes d’agent peuvent être récupérés. Les fragments incomplets et les citations humaines ne deviennent pas automatiquement des interactions.

Codex attache les outils à la création du thread, pas à `thread/resume`. Une clé de session versionnée ouvre donc une fois une conversation équipée de ces outils ; les décisions, résumés et contexte de mission sont transmis, et les anciennes références de conversation sont conservées. Les passages suivants réutilisent cette session.

Les permissions générales du fournisseur ne sont pas désactivées. Le diagnostic local borné évite les escalades de lecture courantes ; les commandes arbitraires et les opérations sensibles restent soumises à leur autorisation native.

## Vérification et activation

- `npm test` : 294 cas, 291 PASS, 3 ignorés, aucun échec, environ 12 secondes. Log : `/private/tmp/djinn-structured-unit-v4.log`.
- Construction TypeScript/Vite et paquet macOS local : PASS. Log : `/private/tmp/djinn-structured-package-final.log`.
- Parité du paquet : 33 fichiers `dist`/`electron` identiques aux fichiers vérifiés. Log : `/private/tmp/djinn-structured-package-verify-final.log`.
- Recette Electron du paquet : question complète/réponse libre, trois appels d’outils acquittés, document long intégralement conservé, permission Codex/Claude résolue dans le même passage, section retirée, notification ouvrant la bonne mission, serveur de worktree, recette indépendante et retour humain sans approbation globale. Log : `/private/tmp/djinn-structured-packaged-smoke-final.log`.
- `git diff --check` : PASS. Log : `/private/tmp/djinn-structured-diff-final.log`.

La recette Electron utilise un profil temporaire, des fournisseurs simulés et un serveur local : elle n’interrompt pas la mission Dolmen et ne consomme pas de passage fournisseur payant. La capture « Tâches et recette » a été inspectée visuellement.

La suite Playwright historique n’a pas fourni de résultat global fiable : plusieurs lancements se sont chevauchés puis ont été arrêtés. Des scénarios attendent encore d’anciens sélecteurs ou contrats de démo (cartes d’équipe, boutons d’étapes futures, export de session v1). Ce diagnostic ne vaut pas validation de tous les parcours historiques. La perte du contexte d’incident lors d’un changement de fournisseur a également été corrigée, et la nouvelle recette packagée vérifie séparément le parcours concerné par cette livraison.

L’application actuellement ouverte provient du paquet `v0.2.1-adaptable-missions`. Les corrections nécessitent le démarrage du nouveau paquet `v0.2.1-structured-interactions` une fois la mission active terminée. Aucun redémarrage de cette mission n’est effectué par cette livraison.

## Routage et limites

Le primaire a gardé l’intégration et la revue des contrats. Deux workers Codex `gpt-5.6-luna` avec raisonnement `max` ont travaillé sur des périmètres distincts (UI et protocole natif). La recherche et les vérifications ont utilisé les rôles dédiés, avec logs complets séparés. Les corrections d’intégration couvrent notamment les identités de passage, la persistance, le traitement des questions bloquantes et les versions de recette.

Les outils Cursor/Fable ne sont pas disponibles dans cette session ; aucune contre-revue externe Fable n’est revendiquée. Les contrats natifs ont été revus par le primaire et vérifiés par les tests locaux. Les coûts, tokens et accélérations quantitatives ne sont pas disponibles : aucun chiffre de vélocité n’est inventé. Une mission réelle ultérieure reste nécessaire pour comparer ces mesures et confirmer la qualité des décisions métier du fournisseur.

Le checkout existant `/Users/clementfauvelle/CODE/djinn` (branche `main`) a été conservé ; aucun nouveau worktree, commit ou push. Le patch de cette intervention, comparé à l’archive du début et hors métadonnées macOS/captures régénérées, touche 43 fichiers dont 12 nouveaux, environ +4 600/−420 lignes, tests et miroirs générés inclus. Les modifications préexistantes ont été préservées. Les corrections ont suivi trois familles de vérification : contrats de données/reprise, rendu et états de recette, puis parcours Electron packagé. Les vérifications anciennes ne doivent pas être relancées en parallèle : réutiliser un unique runner et des tests ciblés après les corrections.
# Récupération des missions anciennes

Une étape en pause ou bloquée affiche aussi son résumé historique lorsqu’elle ne possède pas encore de rapport structuré. Les publications `DJINN_EVENT` complètes laissées dans des notes natives anciennes sont récupérées avec leur provenance agent et passage, même si l’ancien runtime omettait le champ `actor`. Les citations humaines, les notes anonymes et les fragments incomplets restent ignorés.

Djinn prend un verrou par profil de données avant de charger l’état. Une seconde instance utilisant le même profil quitte et remet la première fenêtre au premier plan. Les profils temporaires des tests restent indépendants.

Pour la récupération du 7 octobre, le journal natif de la mission à deux tickets contenait un résultat plus récent que `state.json`. La réparation a conservé une copie intégrale de l’état et du journal, restauré le compte rendu explicitement bloqué et les trois indications humaines manquantes, puis préparé une recette serveur sans revendiquer un serveur déjà actif ni une validation humaine de la date ou de l’intégration commune. Les autres missions sont restées identiques au moment de la réparation.

Validation du correctif : deux régressions reproduites avant correction, puis 296 tests réussis et 3 ignorés ; compilation et parité du paquet local réussies ; parcours Electron isolé de récupération réussi. La mission réelle affiche son rapport courant, la reprise et le test ET-4020.

Intervention de portée moyenne : diagnostic, affichage, migration ponctuelle et intégration par Codex ; verrou natif et tests délégués à un worker Codex `gpt-5.6-luna`, raisonnement `max`. Fichiers du correctif : `src/app.tsx`, `src/mission-interactions.ts`, `electron/main.cjs`, leurs tests de régression, `tests/single-instance.test.cjs` et `scripts/recovery-smoke.cjs`. Deux tests échouaient avant le correctif et passent ensuite. Le premier parcours Electron nécessitait une exécution hors sandbox ; son sélecteur Restitution a ensuite été corrigé pour tenir compte du compteur. Le parcours final réussit. Suite complète : 13,62 s, code de sortie 0, log `/private/tmp/djinn-single-instance-all.log`. Paquet : `/private/tmp/djinn-recovery-package.log`. Parité : `/private/tmp/djinn-recovery-package-parity.log`. Parcours final : `/private/tmp/djinn-recovery-smoke-final.log`. Coût et tokens indisponibles.
