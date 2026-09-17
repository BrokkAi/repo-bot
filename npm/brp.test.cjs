"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const launcher = require("./brp.cjs");

test("launcher selects the native package", () => {
  assert.equal(launcher.packageName("darwin", "arm64"), "@brokkai/repo-bot-darwin-arm64");
  assert.throws(() => launcher.packageName("win32", "x64"), /does not support/);
});
