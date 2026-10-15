// Synchronizes every versioned build artifact with one release version.
//
// The base version lives in build/config.yml and stays a plain X.Y.Z. The
// release version for a specific build is passed in:
//
//   node scripts/sync-version.mjs                                  base only
//   node scripts/sync-version.mjs --release-version 0.3.0-beta.2   beta build
//   node scripts/sync-version.mjs --release-version 0.3.0-nightly.20261013.123456789
//
// Windows file metadata needs four numeric fields, so fixed.file_version
// receives X.Y.Z.0 while info["0000"].ProductVersion keeps the full semantic
// version. nfpm and the macOS plists carry the SemVer identifier and its build
// metadata only, which is the part a package manager is allowed to see.
//
//   node scripts/sync-version.mjs --check    verify without writing

import { readFile, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { BASE_ONLY_PATTERN, VERSION_PATTERN, fileVersionFor } from "./release-version.mjs";

const root = resolve(fileURLToPath(new URL("..", import.meta.url)));
const sourcePath = resolve(root, "build/config.yml");
const checkOnly = process.argv.includes("--check");

const releaseVersionFlag = process.argv.indexOf("--release-version");
const releaseVersion = releaseVersionFlag === -1 ? "" : (process.argv[releaseVersionFlag + 1] ?? "").trim();
if (releaseVersionFlag !== -1 && !releaseVersion) {
  throw new Error("--release-version needs a value, for example --release-version 0.3.0-beta.2");
}

const read = (path) => readFile(path, "utf8");
const writeIfChanged = async (path, value) => {
  const current = await read(path);
  if (current === value) return false;
  if (checkOnly) throw new Error(`${path} is out of sync with build/config.yml`);
  await writeFile(path, value, "utf8");
  return true;
};

const config = await read(sourcePath);
const match = config.match(/^\s+version:\s*["']([^"']+)["']\s*$/m);
if (!match) throw new Error("build/config.yml must contain info.version");
const base = match[1].trim();
if (!BASE_ONLY_PATTERN.test(base)) {
  throw new Error(`build/config.yml info.version must be a plain X.Y.Z base, got ${base}`);
}

// A release version must agree with the configured base, so a package can never
// carry a version that no configuration describes.
const version = releaseVersion || base;
if (!VERSION_PATTERN.test(stripV(version))) {
  throw new Error(`--release-version must be a strict semantic version, got ${version}`);
}
if (!stripV(version).startsWith(`${base}-`) && stripV(version) !== base) {
  throw new Error(`release version ${version} does not belong to base ${base} from build/config.yml`);
}

// Windows file metadata: four numeric fields, so pre-release builds share the
// numeric version of their base.
const fileVersion = fileVersionFor(stripV(version));
if (!fileVersion) {
  throw new Error(`Windows packaging needs a numeric release version, got ${version}`);
}

function stripV(value) {
  return value.startsWith("v") ? value.slice(1) : value;
}

const packagePath = resolve(root, "frontend/package.json");
const packageJSON = JSON.parse(await read(packagePath));
packageJSON.version = version;
await writeIfChanged(packagePath, `${JSON.stringify(packageJSON, null, 2)}\n`);

const lockPath = resolve(root, "frontend/package-lock.json");
const lockJSON = JSON.parse(await read(lockPath));
lockJSON.version = version;
if (lockJSON.packages?.[""]) lockJSON.packages[""].version = version;
await writeIfChanged(lockPath, `${JSON.stringify(lockJSON, null, 2)}\n`);

const infoPath = resolve(root, "build/windows/info.json");
const infoJSON = JSON.parse(await read(infoPath));
if (!infoJSON.fixed || !infoJSON.info?.["0000"]) throw new Error("build/windows/info.json has an unexpected shape");
infoJSON.fixed.file_version = fileVersion;
infoJSON.info["0000"].ProductVersion = version;
await writeIfChanged(infoPath, `${JSON.stringify(infoJSON, null, "\t")}\n`);

const manifestPath = resolve(root, "build/windows/wails.exe.manifest");
const manifest = await read(manifestPath);
const updatedManifest = manifest.replace(/(assemblyIdentity[^>]*\sversion=")[^"]+(")/, `$1${fileVersion}$2`);
if (!/(assemblyIdentity[^>]*\sversion=")[^"]+(")/.test(manifest)) throw new Error("build/windows/wails.exe.manifest has no assembly version");
await writeIfChanged(manifestPath, updatedManifest);

const plistVersionPattern = /(<key>CFBundle(?:Short)?Version(?:String)?<\/key>\s*<string>)[^<]+(<\/string>)/g;
for (const name of ["Info.plist", "Info.dev.plist"]) {
  const plistPath = resolve(root, "build/darwin", name);
  const plist = await read(plistPath);
  if ((plist.match(plistVersionPattern) ?? []).length !== 2) throw new Error(`${plistPath} has an unexpected shape`);
  await writeIfChanged(plistPath, plist.replace(plistVersionPattern, `$1${version}$2`));
}

const nfpmPath = resolve(root, "build/linux/nfpm/nfpm.yaml");
const nfpm = await read(nfpmPath);
const nfpmVersionPattern = /^(version:\s*")[^"]+(")$/m;
if (!nfpmVersionPattern.test(nfpm)) throw new Error("build/linux/nfpm/nfpm.yaml has no package version");
await writeIfChanged(nfpmPath, nfpm.replace(nfpmVersionPattern, `$1${version}$2`));

if (!checkOnly) console.log(`version synchronized: ${version} (file version ${fileVersion})`);
