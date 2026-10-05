"use strict";

const fs = require("fs");
const path = require("path");

const version = process.env.BLAZIUM_GAMES_CLI_VERSION || "";
if (!/^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/.test(version)) {
  throw new Error("BLAZIUM_GAMES_CLI_VERSION must be a semver");
}

const root = path.join(__dirname, "packages");
for (const name of fs.readdirSync(root)) {
  const pkgPath = path.join(root, name, "package.json");
  if (!fs.existsSync(pkgPath)) {
    continue;
  }
  const pkg = JSON.parse(fs.readFileSync(pkgPath, "utf8"));
  pkg.version = version;
  if (pkg.optionalDependencies) {
    for (const key of Object.keys(pkg.optionalDependencies)) {
      pkg.optionalDependencies[key] = version;
    }
  }
  fs.writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + "\n");
}
