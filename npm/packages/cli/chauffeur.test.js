"use strict";

const assert = require("node:assert/strict");
const crypto = require("crypto");
const fs = require("fs");
const os = require("os");
const path = require("path");
const test = require("node:test");

const { archiveURL, assertChecksum, cacheFile, launchResult, loadPins, pinFor } = require("./bin/chauffeur.js");

test("CDN fallback is pinned to the wrapper version and verified", () => {
  const url = archiveURL("linux-amd64", "1.2.3");
  assert.equal(url, "https://cdn.blazium.online/tools/chauffeur/linux-amd64/1.2.3/archive/default");
  assert.equal(url.includes("/latest/"), false);
  assert.throws(() => archiveURL("linux-amd64", "latest"), /unpinned/);
  assert.throws(() => archiveURL("linux-amd64", "0.0.0"), /unpinned/);
  assert.throws(() => archiveURL("../etc", "1.2.3"), /unsupported/);
});

test("cached binary is stored by version and rechecked", () => {
  const cached = cacheFile("chauffeur", "1.2.3");
  assert.equal(cached.includes(`${path.sep}latest${path.sep}`), false);
  assert.equal(cached.includes(`${path.sep}1.2.3${path.sep}`), true);
  assert.throws(() => cacheFile("chauffeur", "latest"), /unpinned/);

  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "chauffeur-test-"));
  const file = path.join(dir, "chauffeur");
  fs.writeFileSync(file, "pinned-bytes");
  const good = crypto.createHash("sha256").update("pinned-bytes").digest("hex");
  assert.doesNotThrow(() => assertChecksum(file, good, "chauffeur"));
  assert.throws(() => assertChecksum(file, "ab".repeat(32), "chauffeur"), /checksum mismatch/);
});

test("a non-executable platform binary is chmodded once then falls back", () => {
  const err = Object.assign(new Error("spawn EACCES"), { code: "EACCES" });
  let spawns = 0;
  let mode = 0;
  const once = launchResult(
    "bin/chauffeur",
    () => {
      spawns += 1;
      return spawns === 1 ? { error: err } : { status: 0 };
    },
    (_bin, next) => {
      mode = next;
    }
  );
  assert.equal(mode, 0o755);
  assert.equal(spawns, 2);
  assert.equal(once.status, 0);

  const still = launchResult(
    "bin/chauffeur",
    () => ({ error: err }),
    () => {}
  );
  assert.equal(still.error.code, "EACCES");
});

test("publish marks unix platform binaries executable", () => {
  const workflow = fs.readFileSync(
    path.join(__dirname, "..", "..", "..", ".github", "workflows", "chauffeur-upload.yml"),
    "utf8"
  );
  const stageStart = workflow.indexOf("stage() {");
  const stageEnd = workflow.indexOf("stage linux", stageStart);
  const stage = workflow.slice(stageStart, stageEnd);
  assert.ok(stage.includes('cp "$src"'), stage);
  assert.ok(stage.indexOf("chmod 0755") > stage.indexOf('cp "$src"'), stage);
  for (const pkg of ["cli-linux-x64", "cli-linux-arm64", "cli-darwin-x64", "cli-darwin-arm64"]) {
    const meta = JSON.parse(fs.readFileSync(path.join(__dirname, "..", pkg, "package.json"), "utf8"));
    assert.equal(meta.bin && meta.bin.chauffeur, "bin/chauffeur", pkg);
  }
});

test("missing checksums fail closed", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "chauffeur-pins-"));
  assert.throws(() => loadPins(root), /checksums.json is missing/);
  assert.throws(() => pinFor({}, "linux-amd64"), /no published checksum/);
  assert.throws(() => pinFor({ "linux-amd64": { archive: "nope", binary: "ab".repeat(32) } }, "linux-amd64"), /no published checksum/);
});
