"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { createRequire, Module } = require("node:module");

function harness({ profile, lockResult = true, exposeLock = true, window } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-single-instance-"));
  const userData = profile || path.join(root, "default-profile");
  const events = new Map();
  const order = [];
  let whenReadyCalls = 0;
  let quitCalls = 0;
  const app = {
    commandLine: { getSwitchValue: () => "" },
    on(name, callback) {
      events.set(name, callback);
    },
    whenReady() {
      whenReadyCalls += 1;
      order.push(["whenReady"]);
      return new Promise(() => {});
    },
    getPath(name) {
      assert.equal(name, "userData");
      return userData;
    },
    setPath(name, value) {
      assert.equal(name, "userData");
      order.push(["setPath", value]);
      this._userData = value;
    },
    quit() {
      quitCalls += 1;
    },
  };
  Object.defineProperty(app, "userData", {
    get() {
      return this._userData || userData;
    },
  });
  if (exposeLock) {
    app.requestSingleInstanceLock = () => {
      order.push(["requestSingleInstanceLock", app.userData]);
      return lockResult;
    };
  }

  const realRequire = createRequire(
    path.resolve(__dirname, "../electron/main.cjs"),
  );
  const fakeProcess = Object.create(process);
  fakeProcess.env = {
    ...process.env,
    DJINN_USER_DATA: userData,
  };
  const compiled = new Module(path.resolve(__dirname, "../electron/main.cjs"));
  compiled.testBindings = { testProcess: fakeProcess, mainWindow: window };
  compiled.require = (name) =>
    name === "electron"
      ? {
          app,
          BrowserWindow: class BrowserWindow {},
          dialog: {},
          ipcMain: {},
          Notification: {},
          shell: {},
          protocol: {
            registerSchemesAsPrivileged() {},
          },
        }
      : name === "node:child_process"
        ? {
            spawn() {
              throw new Error("provider launch forbidden");
            },
            spawnSync: () => ({ status: 1 }),
          }
        : realRequire(name);
  const source = fs.readFileSync(
    path.resolve(__dirname, "../electron/main.cjs"),
    "utf8",
  );
  compiled._compile(
    "const { testProcess: process } = module.testBindings;\n" +
      source +
      "\nif (module.testBindings.mainWindow) mainWindow = module.testBindings.mainWindow;\n",
    path.resolve(__dirname, "../electron/main.cjs"),
  );
  return {
    app,
    api: compiled.exports,
    events,
    order,
    root,
    get whenReadyCalls() {
      return whenReadyCalls;
    },
    get quitCalls() {
      return quitCalls;
    },
    dispose() {
      fs.rmSync(root, { recursive: true, force: true });
    },
  };
}

test("locks the configured profile before Electron readiness and routes a second instance to the window", () => {
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-profile-"));
  const calls = [];
  const primaryWindow = {
    isDestroyed: () => false,
    isMinimized: () => true,
    restore: () => calls.push("restore"),
    show: () => calls.push("show"),
    focus: () => calls.push("focus"),
  };
  const h = harness({ profile, window: primaryWindow });
  try {
    assert.deepEqual(h.order, [
      ["setPath", profile],
      ["requestSingleInstanceLock", profile],
      ["whenReady"],
    ]);
    assert.equal(h.whenReadyCalls, 1);
    const secondInstance = h.events.get("second-instance");
    assert.equal(typeof secondInstance, "function");
    secondInstance();
    assert.deepEqual(calls, ["restore", "show", "focus"]);
  } finally {
    h.dispose();
    fs.rmSync(profile, { recursive: true, force: true });
  }
});

test("quits a secondary instance without reaching readiness or registering another handler", () => {
  const h = harness({ lockResult: false });
  try {
    assert.equal(h.quitCalls, 1);
    assert.equal(h.whenReadyCalls, 0);
    assert.equal(h.events.has("second-instance"), false);
  } finally {
    h.dispose();
  }
});

test("keeps distinct configured profiles independent and tolerates test apps without the lock API", () => {
  const profileA = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-profile-a-"));
  const profileB = fs.mkdtempSync(path.join(os.tmpdir(), "djinn-profile-b-"));
  const first = harness({ profile: profileA });
  const second = harness({ profile: profileB });
  const legacy = harness({ exposeLock: false });
  try {
    assert.deepEqual(first.order.find(([step]) => step === "requestSingleInstanceLock"), [
      "requestSingleInstanceLock",
      profileA,
    ]);
    assert.deepEqual(second.order.find(([step]) => step === "requestSingleInstanceLock"), [
      "requestSingleInstanceLock",
      profileB,
    ]);
    assert.notEqual(
      first.order.find(([step]) => step === "requestSingleInstanceLock")[1],
      second.order.find(([step]) => step === "requestSingleInstanceLock")[1],
    );
    assert.equal(legacy.api.acquireSingleInstanceLock(), true);
    assert.equal(legacy.quitCalls, 0);
  } finally {
    first.dispose();
    second.dispose();
    legacy.dispose();
    fs.rmSync(profileA, { recursive: true, force: true });
    fs.rmSync(profileB, { recursive: true, force: true });
  }
});
