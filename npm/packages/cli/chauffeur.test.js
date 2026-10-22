"use strict";

const assert = require("node:assert/strict");
const crypto = require("crypto");
const fs = require("fs");
const os = require("os");
const path = require("path");
const test = require("node:test");

const { archiveURL, assertChecksum, cacheFile, loadPins, pinFor } = require("./bin/chauffeur.js");

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

test("missing checksums fail closed", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "chauffeur-pins-"));
  assert.throws(() => loadPins(root), /checksums.json is missing/);
  assert.throws(() => pinFor({}, "linux-amd64"), /no published checksum/);
  assert.throws(() => pinFor({ "linux-amd64": { archive: "nope", binary: "ab".repeat(32) } }, "linux-amd64"), /no published checksum/);
});
