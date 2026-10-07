"use strict";
const fs = require("node:fs");
const path = require("node:path");
const real = (directory) => {
  if (typeof directory !== "string" || !path.isAbsolute(directory)) return null;
  try {
    return fs.realpathSync(directory);
  } catch {
    return null;
  }
};
const text = (value, max) =>
  typeof value === "string" ? value.slice(0, max) : "";
// Only the user's local Djinn missions for this exact repository are included.
// Native provider transcripts and environment/credentials are never consulted.
function projectHistory(state, directory) {
  const root = real(directory);
  if (!root || !state || !Array.isArray(state.tasks)) return [];
  const candidates = state.tasks
    .slice(-1000)
    .filter((task) => task && real(task.project) === root)
    .slice(-12)
    .map((task) => ({
      title: text(task.title, 300),
      brief: text(task.brief, 1200),
      steps: (Array.isArray(task.steps) ? task.steps : [])
        .slice(0, 12)
        .map((step) => ({
          type: text(step.type, 40),
          title: text(step.title, 200),
          status: text(step.status, 40),
        })),
      instructions: (Array.isArray(task.instructions) ? task.instructions : [])
        .slice(-6)
        .map((instruction) => ({ text: text(instruction.text, 600) })),
      decisions: (Array.isArray(task.questions) ? task.questions : [])
        .filter((question) => question.answer)
        .slice(-6)
        .map((question) => ({
          title: text(question.title, 200),
          answer: text(question.answer, 600),
        })),
      supports: (Array.isArray(task.artifacts) ? task.artifacts : [])
        .slice(-8)
        .map((artifact) => ({
          title: text(artifact.title, 200),
          type: text(artifact.type, 40),
          sourceOfTruth: artifact.sourceOfTruth === true,
        })),
    }));
  let budget = 16000;
  return candidates
    .reverse()
    .filter((entry) => {
      const size = JSON.stringify(entry).length;
      if (size > budget) return false;
      budget -= size;
      return true;
    })
    .reverse();
}
module.exports = { projectHistory };
