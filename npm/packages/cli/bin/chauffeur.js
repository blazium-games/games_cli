#!/usr/bin/env node
"use strict";

const { execFileSync, spawnSync } = require("child_process");
const fs = require("fs");
const https = require("https");
const os = require("os");
const path = require("path");

function fail(message) {
  console.error(message);
  process.exit(1);
}

function platformTarget(platform, arch) {
  const table = {
    "linux:x64": { cdn: "linux-amd64", exe: "chauffeur" },
    "linux:arm64": { cdn: "linux-arm64", exe: "chauffeur" },
    "win32:x64": { cdn: "windows-amd64", exe: "chauffeur.exe" },
    "darwin:x64": { cdn: "darwin-amd64", exe: "chauffeur" },
    "darwin:arm64": { cdn: "darwin-arm64", exe: "chauffeur" },
  };
  return table[`${platform}:${arch}`] || null;
}

function optionalBin(pkgName, exe) {
  let pkgJson;
  try {
    pkgJson = require.resolve(`${pkgName}/package.json`);
  } catch (err) {
    if (err && (err.code === "MODULE_NOT_FOUND" || /Cannot find module/.test(String(err.message)))) {
      return null;
    }
    throw err;
  }
  const bin = path.join(path.dirname(pkgJson), "bin", exe);
  if (!fs.existsSync(bin)) {
    fail(`Optional package ${pkgName} is installed but ${bin} is missing.`);
  }
  return bin;
}

function cacheFile(exe) {
  const base =
    process.platform === "win32"
      ? process.env.LOCALAPPDATA || path.join(os.homedir(), "AppData", "Local")
      : path.join(os.homedir(), ".cache");
  return path.join(base, "blazium-games", "npm-cli", "latest", exe);
}

function fetchBuffer(url, redirectsLeft) {
  return new Promise((resolve, reject) => {
    if (!url.startsWith("https:")) {
      reject(new Error(`Refusing non-https URL`));
      return;
    }
    const req = https.get(url, (res) => {
      const code = res.statusCode || 0;
      if (code >= 300 && code < 400 && res.headers.location) {
        res.resume();
        if (redirectsLeft <= 0) {
          reject(new Error("Too many redirects"));
          return;
        }
        const next = new URL(res.headers.location, url).href;
        resolve(fetchBuffer(next, redirectsLeft - 1));
        return;
      }
      if (code !== 200) {
        res.resume();
        reject(new Error(`HTTP ${code}`));
        return;
      }
      const chunks = [];
      res.on("data", (chunk) => chunks.push(chunk));
      res.on("end", () => resolve(Buffer.concat(chunks)));
    });
    req.on("error", reject);
  });
}

function unpack(zipPath, dest, exe) {
  fs.mkdirSync(dest, { recursive: true });
  if (process.platform === "win32") {
    const named = `${zipPath}.zip`;
    fs.copyFileSync(zipPath, named);
    execFileSync("powershell.exe", [
      "-NoProfile",
      "-NonInteractive",
      "-Command",
      `Expand-Archive -Force -LiteralPath '${named.replace(/'/g, "''")}' -DestinationPath '${dest.replace(/'/g, "''")}'`,
    ]);
  } else {
    execFileSync("unzip", ["-o", zipPath, "-d", dest]);
  }
  const bin = path.join(dest, exe);
  if (!fs.existsSync(bin)) {
    fail(`CDN archive did not contain ${exe}`);
  }
  if (process.platform !== "win32") {
    fs.chmodSync(bin, 0o755);
  }
  return bin;
}

async function downloadBinary(spec, dest) {
  const url = `https://cdn.blazium.online/tools/chauffeur/${spec.cdn}/latest/archive/default`;
  const body = await fetchBuffer(url, 5);
  const zip = `${dest}.zip`;
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.writeFileSync(zip, body);
  return unpack(zip, path.dirname(dest), spec.exe);
}

function run(bin) {
  const result = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
  if (result.error) {
    fail(result.error.message);
  }
  process.exit(result.status === null ? 1 : result.status);
}

async function main() {
  const spec = platformTarget(process.platform, process.arch);
  if (!spec) {
    fail(
      `No @blazium-games/cli binary for ${process.platform}/${process.arch}. ` +
        "Supported: linux x64/arm64, darwin x64/arm64, win32 x64."
    );
  }

  const pkgName = `@blazium-games/cli-${process.platform}-${process.arch}`;
  const installed = optionalBin(pkgName, spec.exe);
  if (installed) {
    run(installed);
    return;
  }

  const cached = cacheFile(spec.exe);
  if (fs.existsSync(cached)) {
    run(cached);
    return;
  }

  console.error(`Optional package ${pkgName} is not installed. Downloading chauffeur from the CDN.`);
  run(await downloadBinary(spec, cached));
}

if (require.main === module) {
  main().catch((err) => fail(err && err.message ? err.message : String(err)));
}

module.exports = { platformTarget, cacheFile };
