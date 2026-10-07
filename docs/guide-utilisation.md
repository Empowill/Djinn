# Djinn

Application Electron locale pour cadrer une mission, suivre des agents Codex ou Claude Code, prendre des décisions, annoter des supports et transmettre le contexte dans un fichier.

## Lancer

```sh
npm install
npm run desktop
```

Version compilée : `npm run build`, puis `npm start`.
Application macOS : `npm run package` (dans `release/`).
Version locale séparée : `npm run package:local` (dans `release/v<version>/`). Ce parcours conserve le bundle d’une application déjà ouverte ; quittez-la à la fin du passage, puis ouvrez le nouveau `Djinn.app`.

## Premier parcours

La mission **Espace projets** est une démonstration interactive : aucun modèle ni code de projet n’est exécuté. Répondez aux décisions, lancez la démo, puis testez la review et le partage.

Pour travailler sur un projet réel :

1. Décrivez votre intention, choisissez un projet enregistré ou son dossier et ses conventions Markdown. Le titre est proposé par le harnais ; un renommage humain reste prioritaire.
2. Choisissez le type de discussion : exploration, réflexion, spécification, prototype, implémentation, review ou livraison. Le parcours libre démarre avec une seule étape. Le parcours classique prépare une timeline ; un projet peut imposer son workflow aux nouvelles missions. Configurez de 1 à 16 sous-agents simultanés ; des écrivains aux périmètres déclarés distincts travaillent en parallèle.
3. La création ou l’envoi d’un message démarre le passage courant. Répondez aux questions bloquantes : la reprise est automatique dès que toutes les décisions nécessaires sont renseignées.
4. Consultez la carte du résultat et validez-la humainement. En parcours libre, Djinn propose la plus petite suite utile : ajoutez-la à la timeline, adaptez-la ou terminez ici. L’ajout ne lance aucun processus. Une spécification est une discussion en lecture seule qui peut conclure la mission sans code ni prototype. Les étapes futures sont désactivées ; visiter une étape passée ne lance rien.
5. Discutez des supports Markdown et visualisations interactives isolées. Une étape de réflexion peut demander `grill-me` ; le harnais doit vérifier la disponibilité de la skill avant d’annoncer son activation.
6. Après du code, validez une review avant la livraison. Exportez la session v2, la synthèse et le brouillon de MR. La livraison demande votre validation explicite.

Les conventions et workflows d’un projet s’enregistrent pour les nouvelles missions. Chaque mission conserve une copie datée ; les modifications ultérieures du projet ne remplacent pas implicitement son contexte.

Les **sources de vérité du projet** nomment des documents de référence par chemin relatif et précisent leur rôle. Dans **Supports**, le bouton **Définir comme source de vérité** désigne également un support canonique. Ces supports sont prioritaires dans le budget de contexte, avec leur révision et leurs dernières éditions humaines, et restent transmis aux étapes suivantes. Une actualisation d’agent conserve cette désignation ; une proposition concurrente ne remplace pas une édition humaine.

Codex utilise son app-server local durable : le chef conserve une conversation en lecture seule pendant les passages des workers. Discuter avec lui ne redémarre pas les écrivains. Un message ciblé rejoint directement un tour Codex actif ; Claude utilise une interruption/reprise contrôlée du destinataire, après fermeture effective. Le modèle et les protections configurés sont conservés. Le suivi affiche les agents réellement actifs, leur tâche, le chef qui attend ou répond et la dernière activité horodatée. Le silence est une durée ; un avertissement stderr ne devient pas une erreur fatale.

La timeline conserve les passages réels, avec un accès au chat de l’agent. Les anciens éléments sans étape prouvable se consultent dans **Historique v1**. Les documents s’affichent en Markdown GFM ; une édition humaine garde sa révision et une actualisation concurrente d’un agent devient une proposition distincte.

Les sous-agents déclarent leurs fichiers ou dossiers relatifs avec `writeScope`, leurs éventuels prérequis avec `dependsOn` et leur lecture seule avec `readOnly`. Sans périmètre déclaré, un écrivain réserve le projet entier. Les chemins sont comparés après résolution des liens symboliques. Le scheduler utilise immédiatement chaque place libérée ; les travaux indépendants n’attendent pas une vague entière. Le chef intègre après la fin effective des écrivains. Codex reçoit les racines d’écriture préparées dans sa sandbox ; Claude conserve ses permissions et le contrat de périmètre dans son prompt.

Après rechargement du renderer, Djinn se rattache aux passages réellement présents dans son runtime natif et réaffiche leurs agents actifs et leur dernière activité. Il rejoue un journal borné avec déduplication ; cette reconnexion ne lance aucun fournisseur. Un identifiant sauvegardé ou une conversation Codex extérieure ne constitue pas un passage actif.

À la fin d’une implémentation réussie, Djinn recherche le script de développement du projet et lance son serveur local lorsqu’un seul candidat convient. S’il y a plusieurs applications, les actions proposées permettent de choisir celle à lancer. Les cartes **Actions** donnent accès à l’aperçu, à son arrêt et à sa relance, ainsi qu’aux vérifications proposées par l’agent. Djinn vérifie que le serveur répond avant de le déclarer prêt et gère les processus qu’il a démarrés. Une session restaurée ne déclare aucun ancien serveur actif sans vérification native.

La barre flottante est repliée par défaut. Les indications donnent lieu à un accusé transmis, remis au destinataire ou empêché avec sa raison, et restent dans le contexte des reprises. Les nouvelles questions et actions ouvrent une alerte persistante dans Djinn ; la cloche permet de les retrouver. Les notifications macOS sont envoyées via Electron, avec résultat de dispatch et échecs affichés dans Connexions ; le bouton Tester permet de vérifier les réglages du système. L’aperçu navigateur peut activer les notifications avec le bouton Activer.

Les diagnostics de démarrage Codex connus ne deviennent pas des échecs de passage. La conversation masque les anciens avertissements et messages stdin enregistrés comme erreurs, sans effacer le journal ni masquer les véritables erreurs du fournisseur.

La pause interrompt les processus lancés. Une reprise inclut les décisions, les retours, les supports édités et les derniers comptes rendus. Les changements de code restent soumis aux permissions du fournisseur ; Djinn ne désactive pas ses protections.

## Raccourcis

| Action                    | macOS              | Windows / Linux       |
| ------------------------- | ------------------ | --------------------- |
| Donner une indication     | ⌘ J                | Ctrl J                |
| Faire un retour de review | ⌘ ⇧ J              | Ctrl ⇧ J              |
| Envoyer                   | ⌘ Entrée ou Entrée | Ctrl Entrée ou Entrée |
| Nouvelle ligne            | ⇧ Entrée           | ⇧ Entrée              |
| Replier / fermer          | Échap              | Échap                 |
| Changer de vue            | ⌘ 1–5              | Ctrl 1–5              |
| Commandes                 | ⌘ K                | Ctrl K                |
| Nouvelle mission          | ⌘ N                | Ctrl N                |

## Connexions

- Codex : `npm install -g @openai/codex`, puis `codex login`.
- Claude Code : installez sa CLI et utilisez `claude auth login`.

Djinn ne demande pas de clé API et ne copie pas les jetons d’authentification dans les sessions. La disponibilité d’un modèle dépend de votre fournisseur et de votre abonnement.

## Données et partage

L’espace de travail est sauvegardé atomiquement dans le répertoire de données utilisateur Electron. Les sessions v1 sont migrées en v2 avec une sauvegarde native récupérable `state.json.v1.backup`. Les imports ne réutilisent pas les conversations natives d’une autre machine. Un état corrompu est conservé et suspend la sauvegarde automatique. Le fichier `.djinn.json` contient les projets, les étapes et le contexte, les décisions, les agents, les événements, les supports et les annotations. L’import ne lance aucune commande. Vérifiez le chemin du projet sur la machine de reprise.

Le fichier peut inclure du code et des captures de votre projet : choisissez ses destinataires. Pas d’infrastructure cloud dans cette version.

## Vérification

```sh
npm run build
npm test
npm run test:e2e
node scripts/electron-smoke.cjs
```

Les tests Electron utilisent un espace de données temporaire et un faux fournisseur déterministe pour vérifier les flux de processus sans appeler de modèle payant. Les captures actuelles du README sont dans `docs/screenshots/readme/`. Les parcours Chromium vérifient la review à 1440 et 1024 px, les visualisations, les notifications et le changement de fournisseur. Le détail des contrôles récents figure dans `docs/v0.2.1-completion.md`.

## Périmètre actuel des sources

Fonctionnels : Electron, connexion aux CLI, exécution locale, événements typés, timeline temporelle, décisions avec historique, actions de fin, serveurs locaux gérés, supports éditables, annotations, pause/reprise, import/export et livrables locaux.

La review visuelle se fait dans Djinn via les supports ou l’import de captures. L’extension Chrome, la collaboration cloud, la publication automatique d’une MR et l’enregistrement automatique d’une vidéo de projet ne font pas partie de cette version. Le brouillon de MR peut être exporté, puis publié avec vos outils habituels.

Le moteur est conçu autour d’adaptateurs CLI et d’un pont Electron isolé, étudiés à partir de [T3 Code](https://github.com/pingdotgg/t3code) (MIT). Le sélecteur de modèle et le catalogue Claude sont adaptés de T3 Code ; les notices précisent leur provenance. Les réponses visuelles reprennent le concept de pages HTML dans la conversation, avec une démo originale et le moteur isolé de Djinn. Les illustrations de machine et de signal utilisent des SVG originaux. Les avatars animés utilisent [Thinking Orbs](https://libraries.dev/orbs), sous licence MIT, avec une animation et une couleur propres à chaque agent.

Protocole des agents : [docs/agent-protocol.md](docs/agent-protocol.md).

Les sources et `dist/` ne mettent pas à jour une application Djinn déjà installée.
Lancer la version compilée depuis ce dépôt utilise ses nouvelles sources ; la
production d’un installateur reste une action distincte.
