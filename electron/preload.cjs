"use strict";

const { contextBridge, ipcRenderer } = require("electron");

function invoke(channel, ...args) {
  return ipcRenderer.invoke(channel, ...args).then((result) => {
    if (result && result.ok === false && result.error) {
      const error = new Error(result.error.message || "Djinn IPC call failed");
      error.code = result.error.code || "ipc_error";
      throw error;
    }
    return result;
  });
}

const api = {
  discoverProject: (input) => invoke("djinn:discover-project", input),
  cancelProjectDiscovery: (scanId) =>
    invoke("djinn:cancel-project-discovery", scanId),
  renderVisualization: (source) => invoke("djinn:render-visualization", source),
  validateProject: (project) => invoke("djinn:validate-project", project),
  getEnvironment: () => invoke("djinn:get-environment"),
  getProviderModels: (provider, refresh) =>
    invoke("djinn:get-provider-models", provider, refresh),
  getRuntimeSnapshot: () => invoke("djinn:get-runtime-snapshot"),
  getPendingPermissions: () => invoke("djinn:get-pending-permissions"),
  getMissionJournalPage: (taskId, cursor, limit) => invoke("djinn:get-mission-journal-page", taskId, cursor, limit),
  getMissionInteractions: (taskId) => invoke("djinn:get-mission-interactions", taskId),
  selectDirectory: () => invoke("djinn:select-directory"),
  loadState: () => invoke("djinn:load-state"),
  saveState: (state) => invoke("djinn:save-state", state),
  exportSession: (session) => invoke("djinn:export-session", session),
  importSession: () => invoke("djinn:import-session"),
  startRun: (input) => invoke("djinn:start-run", input),
  cancelRun: (runId) => invoke("djinn:cancel-run", runId),
  steerRun: (input) => invoke("djinn:steer-run", input),
  getActions: (taskId) => invoke("djinn:get-actions", taskId),
  performAction: (input) => invoke("djinn:perform-action", input),
  loginProvider: (provider) => invoke("djinn:login-provider", provider),
  openExternal: (url) => invoke("djinn:open-external", url),
  saveArtifact: (artifact) => invoke("djinn:save-artifact", artifact),
  notifyQuestion: (input) => invoke("djinn:notify-question", input),
  respondPermission: (input) => invoke("djinn:respond-permission", input),
  onEvent: (callback) => {
    if (typeof callback !== "function") return () => undefined;
    const listener = (_event, value) => {
      try {
        callback(value);
      } catch {
        // A renderer listener must not break the bridge event fan-out.
      }
    };
    ipcRenderer.on("djinn:event", listener);
    return () => ipcRenderer.removeListener("djinn:event", listener);
  },
};

contextBridge.exposeInMainWorld("djinn", Object.freeze(api));
