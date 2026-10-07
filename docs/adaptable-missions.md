# Missions adaptables et expérience simplifiée

Décisions du 7 octobre 2026, consolidées avec Clément.

## Intention

Djinn pilote une équipe d’agents vers le résultat utilisable le plus rapidement possible. L’utilisateur peut changer de priorité, demander une correction, tester un ticket ou interrompre le travail à tout moment. La timeline suit le travail ; elle ne sert pas de système d’autorisation technique.

## Autorisations

- Demander les autorisations techniques lorsqu’elles sont nécessaires ; `never` ne constitue plus le réglage par défaut.
- Présenter les demandes natives Codex et Claude Code dans la mission : agent, fournisseur, raison, action exacte et périmètre.
- Accepter une fois, refuser, ou autoriser pour la session lorsque le fournisseur le permet. Les réponses reprennent la demande native en attente.
- Notifier une intervention utile et ouvrir directement la carte concernée.
- Les demandes annulées, déjà résolues ou appartenant à un passage arrêté ne peuvent pas être acceptées.
- Une review peut accueillir une correction ou un lancement local demandé explicitement. Le fournisseur reste responsable de ses permissions et du sandbox.

## Travail et vérification

- Conserver les décisions acquises, les instructions humaines et les résultats lors des reprises.
- Proposer le plus petit workflow utile et limiter les questions aux arbitrages métier qui changent le résultat.
- Maintenir les sous-agents avec un objectif borné et une propriété explicite des fichiers ; les travaux indépendants avancent séparément.
- Le chef reste joignable pendant leur travail ; une supervision supplémentaire ne démarre qu’en réponse à une intervention humaine. L’intégration conserve son rôle à la fin.
- Envoyer des extraits compacts à chaque passage et rendre les documents texte complets accessibles à la demande dans le stockage privé de Djinn.
- Vérifier les zones affectées selon leur risque. Les preuves existantes restent utiles tant que le périmètre pertinent n’a pas changé.
- Distinguer passage terminé, résultat prêt, besoin d’une réponse et blocage. Un passage achevé ne prouve pas la satisfaction des critères de sortie.
- Conserver la validation humaine de la recette et de la livraison. Les actions locales réversibles ne nécessitent pas de validation de timeline supplémentaire.
- Résoudre le dossier explicitement demandé pour les serveurs, y compris les worktrees cachés ou profonds, en conservant le confinement du chemin.

## Interface

- Avatars à droite de la barre d’onglets, équipe courante et chef inclus.
- Un à trois agents : tous visibles. Quatre ou plus : trois avatars et `+X` pour les autres.
- Nom complet et statut au survol/focus ; clic sur un avatar ouvre le chat existant. `+X` ouvre une liste de sélection dans un drawer.
- Les agents gardent un ordre stable. Leurs détails et leur historique restent accessibles à la demande.
- « À toi de jouer » rassemble les autorisations, les décisions ouvertes et les tests disponibles. Les mises à jour ordinaires restent dans le journal.

## Recette

- Chaque action précise le résultat à tester, sa provenance, ce qu’il faut faire et le comportement attendu.
- Avant disponibilité vérifiée : préparer le test ou réessayer, avec une cause d’échec visible.
- Lorsque le serveur est disponible : ouvrir le test. L’utilisateur peut signaler que le résultat fonctionne, décrire un problème ou reporter sa recette.
- Le retour est conservé avec l’action et son étape ; un problème est transmis au chef avec son contexte.
- Une notification demande une intervention ou annonce un résultat testable ; elle ne répète pas chaque progression interne.

## Critères d’acceptation

1. Une demande native reste en attente jusqu’à la réponse humaine, puis reprend ou refuse l’opération correspondante sans recréer la mission.
2. Une permission expirée ou une réponse en double est rejetée ; un rechargement ne ressuscite pas une permission sans requête native active.
3. Un worktree caché contenant un script valide peut être lancé directement ; un chemin hors du projet est refusé.
4. Un résultat explicitement bloqué ou des critères non satisfaits n’entraînent pas une recette automatique.
5. Les avatars et le drawer fonctionnent au clavier et respectent exactement la règle de dépassement.
6. Les retours de recette survivent à une sauvegarde/restauration et restent associés au résultat concerné.
7. La reprise utilise un contexte compact, privilégiant les sources de vérité et les dernières instructions humaines.

## Périmètre de cette livraison

Implémentation sur Djinn ; aucune modification automatique des worktrees Dolmen, des missions déjà enregistrées, de leurs statuts Notion ou des permissions du système d’exploitation. Les changements de politique s’appliquent aux nouveaux passages lancés par cette version.

## Ce que l’audit a montré

La mission « Trois tickets Empowill en worktrees, puis MR commune » cumulait un contexte de plus de 53 000 caractères, 10 questions dont 7 bloquantes et 46 vérifications représentant environ 401 secondes, dont un timeout de 360 secondes. Un incident de quota s’ajoutait aux attentes. La lenteur venait donc de plusieurs causes ; simplifier l’interface seule n’aurait pas suffi.

Cette livraison retire la supervision de démarrage systématique, évite la double transmission au chef d’un message adressé à un agent, réutilise les conversations Codex d’une mission et privilégie le contexte utile. Les règles de vérification proportionnée guident les agents ; leur respect et le gain de temps réel devront être observés sur les prochains tickets.

## Validation et intégration

- Implémentation principale et intégration : Codex ; trois sous-tâches bornées confiées à des workers `gpt-5.6-luna` avec effort `max` (autorisations, workflow/runtime des actions, interface).
- Validation globale : agent `verification-runner`. Contrôles ciblés après corrections, puis une passe globale finale.
- Parcours Electron avec un profil temporaire et des fournisseurs simulés : permissions Codex et Claude, réponse à la requête d’origine, doublons et annulations, notification vers la bonne carte, avatars et drawer, serveur dans un worktree caché, retour de recette conservé sans approuver l’étape, supports complets accessibles à la demande.
- Relecture de l’intégration par Codex. Les modèles externes Fable et le reviewer Console n’étaient pas disponibles dans cette session ; aucun modèle ne leur a été substitué et aucune revue externe n’est revendiquée.
- Aucun appel fournisseur payant pour ces tests, aucune interruption de la mission réelle et aucun commit/push. Durées, résultats globaux et paquet local consignés après validation ci-dessous ; coûts et compteurs de tokens non disponibles.

Résultats finaux :

- `npm test` : PASS, 269 tests dont 266 passés et 3 ignorés, 11,539 secondes, code 0 ; journal `/private/tmp/djinn-final-validated-tests.log`.
- `git diff --check` : PASS, 0,006 seconde, code 0 ; journal `/private/tmp/djinn-final-validated-diff.log`.
- `DJINN_PACKAGE_SUFFIX=adaptable-missions npm run package:local` : PASS, compilation TypeScript/Vite et paquet macOS, code 0 ; journal `/private/tmp/djinn-package-adaptable-final.log`.
- `verify:local-package` : PASS, sources Electron et build identiques dans le paquet, code 0 ; journal `/private/tmp/djinn-package-verify-adaptable-final.log`.
- Parcours Electron exécuté sur le paquet livré avec profil temporaire et fournisseurs simulés : PASS, code 0 ; journal `/private/tmp/djinn-packaged-smoke-final.log`.

Paquet : [Djinn.app](/Users/clementfauvelle/CODE/djinn/release/v0.2.1-adaptable-missions/mac-arm64/Djinn.app). Quitter l’ancienne application après la mission en cours, puis lancer ce paquet pour bénéficier du nouveau runtime.

Travail dans le checkout local, sans worktree supplémentaire : 49 fichiers concernés, dont 15 nouveaux et 17 dans les tests. Environ 7 700 lignes ajoutées et 1 000 retirées, incluant les modules générés et la mise en forme. Deux passes globales ont identifié des contrats obsolètes et le problème du contexte JSON ; la troisième est verte. Les corrections intermédiaires ont été revérifiées sur les zones affectées.

Recommandation de routage : garder les sous-agents bornés, intégrer une fois leurs résultats, lancer la supervision sur demande et ne refaire les contrôles globaux qu’après un changement transversal. Les durées mesurées ci-dessus concernent la validation de cette livraison, pas un benchmark de missions réelles.
