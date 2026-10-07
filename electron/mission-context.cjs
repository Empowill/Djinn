"use strict";
const fs = require("node:fs");
const fsp = fs.promises;
const path = require("node:path");
const crypto = require("node:crypto");
const key = (value) => crypto.createHash("sha256").update(value).digest("hex");

/** Full text stays available on demand; only excerpts travel with each turn. */
async function writeMissionContext(task, userData) {
  const base = await fsp.realpath(userData);
  const supportRoot = path.join(base, "mission-supports");
  await fsp.mkdir(supportRoot, { recursive: true, mode: 0o700 });
  const relativeRoot = path.relative(base, await fsp.realpath(supportRoot));
  if (relativeRoot.startsWith(`..${path.sep}`) || path.isAbsolute(relativeRoot))
    throw new Error("Le dossier des supports sort du stockage Djinn.");
  const directory = path.join(supportRoot, key(task.id));
  await fsp.mkdir(directory, { recursive: true, mode: 0o700 });
  const real = await fsp.realpath(directory);
  const relative = path.relative(base, real);
  if (relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative))
    throw new Error("Le dossier des supports sort du stockage Djinn.");
  const write = (filename, text) =>
    fsp.writeFile(filename, text, {
      encoding: "utf8",
      mode: 0o600,
      flag:
        fs.constants.O_WRONLY |
        fs.constants.O_CREAT |
        fs.constants.O_TRUNC |
        fs.constants.O_NOFOLLOW,
    });
  const supports = [];
  const missionPath = path.join(real, "mission.txt");
  await write(
    missionPath,
    [
      `# ${task.title || task.id}\n\n${task.brief || ""}`,
      "## Dernières instructions humaines\n" +
        (task.instructions || [])
          .slice()
          .reverse()
          .map((instruction) => `[${instruction.id}] ${instruction.text}`)
          .join("\n\n"),
      "## Décisions acquises\n" +
        (task.questions || [])
          .filter((question) => question.answer)
          .map((question) => `${question.title}\n${question.answer}`)
          .join("\n\n"),
      "## Étapes et critères de sortie\n" +
        JSON.stringify(task.steps || [], null, 2),
    ].join("\n\n"),
  );
  for (const artifact of task.artifacts || []) {
    if (artifact.type === "screenshot") continue;
    const filename = path.join(real, `${key(artifact.id)}.txt`);
    await write(filename, artifact.content);
    supports.push({
      id: artifact.id,
      title: artifact.title,
      type: artifact.type,
      stepId: artifact.stepId,
      sourceOfTruth: !!artifact.sourceOfTruth,
      revision: artifact.revision,
      updatedAt: artifact.updatedAt,
      path: filename,
    });
  }
  const filename = path.join(real, "index.json");
  await write(
    filename,
    JSON.stringify({ taskId: task.id, missionPath, supports }, null, 2),
  );
  return filename;
}

module.exports = { writeMissionContext };
