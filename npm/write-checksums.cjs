"use strict";

const fs = require("fs");
const path = require("path");

const names = ["linux-amd64", "linux-arm64", "windows-amd64", "darwin-amd64", "darwin-arm64"];

function readSum(file) {
  const text = fs.readFileSync(file, "utf8").trim().split(/\s+/)[0].toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(text)) {
    throw new Error(`bad checksum in ${file}`);
  }
  return text;
}

const bins = process.argv[2] || path.join("dist", "bins");
const out = {};
for (const name of names) {
  const dir = path.join(bins, `chauffeur-${name}`);
  out[name] = {
    archive: readSum(path.join(dir, "archive.sha256")),
    binary: readSum(path.join(dir, "binary.sha256")),
  };
}
const dest = path.join(__dirname, "packages", "cli", "checksums.json");
fs.writeFileSync(dest, JSON.stringify(out, null, 2) + "\n");
