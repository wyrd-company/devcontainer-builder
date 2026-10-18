#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
// Adapted from wyrd-company/intentional; see ../NOTICE.
// ---
// relationships:
//   implements: manifest
// ---

"use strict";

const fs = require("node:fs");
const os = require("node:os");
const { spawn } = require("node:child_process");

const FORWARDED_SIGNALS = ["SIGINT", "SIGTERM", "SIGHUP"];

function executablePath(
  platform = process.platform,
  architecture = process.arch,
) {
  if (
    !["linux", "darwin"].includes(platform) ||
    !["x64", "arm64"].includes(architecture)
  ) {
    throw new Error(
      `unsupported platform ${platform}/${architecture}; build dcb from source`,
    );
  }
  try {
    return require.resolve(
      `@wyrd-company/dcb-${platform}-${architecture}/bin/dcb`,
    );
  } catch {
    throw new Error(
      "native package is missing; reinstall @wyrd-company/dcb with optional dependencies enabled",
    );
  }
}

function run(argv = process.argv.slice(2), options = {}) {
  const processObject = options.processObject || process;
  let binary;
  try {
    binary =
      options.binary || executablePath(options.platform, options.architecture);
  } catch (error) {
    (options.error || console.error)(`dcb: ${error.message}`);
    processObject.exitCode = 1;
    return null;
  }
  const exists = options.exists || fs.existsSync;
  const spawnProcess = options.spawn || spawn;

  if (!exists(binary)) {
    (options.error || console.error)(
      "dcb: native executable is missing; reinstall @wyrd-company/dcb with optional dependencies enabled",
    );
    processObject.exitCode = 1;
    return null;
  }

  const child = spawnProcess(binary, argv, { argv0: "dcb", stdio: "inherit" });
  const handlers = new Map();
  const cleanup = () => {
    for (const [signal, handler] of handlers) {
      processObject.removeListener(signal, handler);
    }
  };

  if ((options.platform || process.platform) !== "win32") {
    for (const signal of FORWARDED_SIGNALS) {
      const handler = () => child.kill(signal);
      handlers.set(signal, handler);
      processObject.on(signal, handler);
    }
  }

  child.once("error", (error) => {
    cleanup();
    (options.error || console.error)(`dcb: ${error.message}`);
    processObject.exitCode = 1;
  });
  child.once("exit", (code, signal) => {
    cleanup();
    if (signal) {
      const number = os.constants.signals[signal];
      processObject.exitCode = number ? 128 + number : 1;
    } else {
      processObject.exitCode = code ?? 1;
    }
  });
  return child;
}

if (require.main === module) {
  run();
}

module.exports = { FORWARDED_SIGNALS, executablePath, run };
