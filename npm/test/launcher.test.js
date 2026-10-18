// SPDX-License-Identifier: Apache-2.0
// Adapted from wyrd-company/intentional; see ../NOTICE.
// ---
// relationships:
//   validates: manifest
// ---

"use strict";

const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const test = require("node:test");

const launcher = require("../bin/dcb.js");

function fakeProcess() {
  const processObject = new EventEmitter();
  processObject.exitCode = undefined;
  return processObject;
}

test("launcher fails with actionable output when native executable is missing", () => {
  const processObject = fakeProcess();
  const errors = [];
  const child = launcher.run([], {
    binary: "/missing/dcb-native",
    error: (message) => errors.push(message),
    exists: () => false,
    processObject,
  });
  assert.equal(child, null);
  assert.equal(processObject.exitCode, 1);
  assert.match(errors[0], /reinstall @wyrd-company\/dcb/);
});

test("launcher forwards arguments and inherited standard streams", () => {
  const processObject = fakeProcess();
  const child = new EventEmitter();
  child.kill = () => true;
  let invocation;
  launcher.run(["status", "--json"], {
    binary: "/package/bin/dcb-native",
    exists: () => true,
    platform: "linux",
    processObject,
    spawn: (...args) => {
      invocation = args;
      return child;
    },
  });
  assert.deepEqual(invocation, [
    "/package/bin/dcb-native",
    ["status", "--json"],
    { argv0: "dcb", stdio: "inherit" },
  ]);
  child.emit("exit", 23, null);
  assert.equal(processObject.exitCode, 23);
});

test("launcher forwards supported signals and preserves signal exit status", () => {
  const processObject = fakeProcess();
  const child = new EventEmitter();
  const signals = [];
  child.kill = (signal) => signals.push(signal);
  launcher.run([], {
    binary: "/package/bin/dcb-native",
    exists: () => true,
    platform: "linux",
    processObject,
    spawn: () => child,
  });
  processObject.emit("SIGTERM");
  assert.deepEqual(signals, ["SIGTERM"]);
  child.emit("exit", null, "SIGTERM");
  assert.equal(processObject.exitCode, 143);
  assert.equal(processObject.listenerCount("SIGTERM"), 0);
});

test("launcher reports native spawn errors", () => {
  const processObject = fakeProcess();
  const errors = [];
  const child = new EventEmitter();
  child.kill = () => true;
  launcher.run([], {
    binary: "/package/bin/dcb-native",
    error: (message) => errors.push(message),
    exists: () => true,
    platform: "win32",
    processObject,
    spawn: () => child,
  });
  child.emit("error", new Error("cannot execute"));
  assert.equal(processObject.exitCode, 1);
  assert.match(errors[0], /cannot execute/);
});

test("launcher reports unsupported targets and missing optional native packages", () => {
  for (const [platform, architecture, message] of [
    ["win32", "x64", /unsupported platform/],
    ["linux", "riscv64", /unsupported platform/],
    ["linux", "x64", /optional dependencies enabled/],
  ]) {
    const processObject = fakeProcess();
    const errors = [];
    assert.equal(
      launcher.run([], {
        platform,
        architecture,
        processObject,
        error: (value) => errors.push(value),
      }),
      null,
    );
    assert.equal(processObject.exitCode, 1);
    assert.match(errors[0], message);
  }
});
