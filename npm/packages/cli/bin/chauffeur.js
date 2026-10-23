#!/usr/bin/env node
"use strict";

const { execFileSync, spawnSync } = require("child_process");
const crypto = require("crypto");
const fs = require("fs");
const https = require("https");
const os = require("os");
const path = require("path");

const CDN_PLATFORMS = new Set([
  "linux-amd64",
  "linux-arm64",
  "windows-amd64",
  "darwin-amd64",
  "darwin-arm64",
]);

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

function wrapperRoot() {
  return path.join(__dirname, "..");
}

function readVersion(root) {
  const pkg = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));
  return String(pkg.version || "");
}

function pinnedVersion(version) {
  if (!/^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/.test(version) || version === "0.0.0") {
    throw new Error("refusing to download an unpinned chauffeur build");
  }
  return version;
}

function cacheFile(exe, version) {
  const pinned = pinnedVersion(version);
  const base =
    process.platform === "win32"
      ? process.env.LOCALAPPDATA || path.join(os.homedir(), "AppData", "Local")
      : path.join(os.homedir(), ".cache");
  return path.join(base, "blazium-games", "npm-cli", pinned, exe);
}

function archiveURL(cdn, version) {
  if (!CDN_PLATFORMS.has(cdn)) {
    throw new Error("unsupported chauffeur platform");
  }
  const pinned = pinnedVersion(version);
  return `https://cdn.blazium.online/tools/chauffeur/${encodeURIComponent(cdn)}/${encodeURIComponent(pinned)}/archive/default`;
}

function isSHA256(value) {
  return typeof value === "string" && /^[0-9a-fA-F]{64}$/.test(value);
}

function loadPins(root) {
  const file = path.join(root, "checksums.json");
  let raw;
  try {
    raw = fs.readFileSync(file, "utf8");
  } catch (err) {
    throw new Error("checksums.json is missing; refusing to download an unverified binary");
  }
  return JSON.parse(raw);
}

function pinFor(pins, cdn) {
  const pin = pins && pins[cdn];
  if (!pin || !isSHA256(pin.archive) || !isSHA256(pin.binary)) {
    throw new Error(`no published checksum for ${cdn}; refusing to download`);
  }
  return { archive: pin.archive.toLowerCase(), binary: pin.binary.toLowerCase() };
}

function sha256File(file) {
  return crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
}

function assertChecksum(file, expected, label) {
  const want = String(expected || "").toLowerCase();
  const got = sha256File(file);
  if (!isSHA256(want) || got !== want) {
    throw new Error(`checksum mismatch for ${label}`);
  }
}

function fetchBuffer(url, redirectsLeft) {
  return new Promise((resolve, reject) => {
    if (!url.startsWith("https:")) {
      reject(new Error("Refusing non-https URL"));
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

async function downloadBinary(spec, dest, version, pin) {
  const url = archiveURL(spec.cdn, version);
  const body = await fetchBuffer(url, 5);
  const got = crypto.createHash("sha256").update(body).digest("hex");
  if (got !== pin.archive) {
    throw new Error("CDN archive checksum mismatch; refusing to unpack");
  }
  const zipDir = fs.mkdtempSync(path.join(os.tmpdir(), "chauffeur-"));
  const zip = path.join(zipDir, "archive.zip");
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.writeFileSync(zip, body);
  try {
    const bin = unpack(zip, path.dirname(dest), spec.exe);
    try {
      assertChecksum(bin, pin.binary, spec.exe);
    } catch (err) {
      fs.rmSync(bin, { force: true });
      throw err;
    }
    return bin;
  } finally {
    fs.rmSync(zipDir, { recursive: true, force: true });
  }
}

function launchResult(bin, spawnFn, chmodFn) {
  const spawn = spawnFn || ((target) => spawnSync(target, process.argv.slice(2), { stdio: "inherit" }));
  const chmod = chmodFn || ((target, mode) => fs.chmodSync(target, mode));
  let result = spawn(bin);
  if (result && result.error && result.error.code === "EACCES") {
    try {
      chmod(bin, 0o755);
    } catch (err) {
      return result;
    }
    result = spawn(bin);
  }
  return result;
}

function run(bin) {
  const result = launchResult(bin);
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
    const result = launchResult(installed);
    if (!(result.error && result.error.code === "EACCES")) {
      if (result.error) {
        fail(result.error.message);
      }
      process.exit(result.status === null ? 1 : result.status);
      return;
    }
    console.error(`Could not execute ${installed}. Falling back to the CDN download.`);
  }

  const root = wrapperRoot();
  const version = readVersion(root);
  const pin = pinFor(loadPins(root), spec.cdn);
  const cached = cacheFile(spec.exe, version);
  if (fs.existsSync(cached)) {
    try {
      assertChecksum(cached, pin.binary, spec.exe);
      run(cached);
      return;
    } catch (err) {
      fs.rmSync(cached, { force: true });
      console.error(`${err.message}. Refusing the cached binary.`);
    }
  }

  console.error(`Optional package ${pkgName} is not installed. Downloading chauffeur ${version} from the CDN.`);
  run(await downloadBinary(spec, cached, version, pin));
}

if (require.main === module) {
  main().catch((err) => fail(err && err.message ? err.message : String(err)));
}

module.exports = {
  platformTarget,
  cacheFile,
  archiveURL,
  readVersion,
  loadPins,
  pinFor,
  assertChecksum,
  launchResult,
};
