import {readFile, writeFile} from "node:fs/promises";
import {resolve} from "node:path";
import {fileURLToPath} from "node:url";

const root = resolve(fileURLToPath(new URL("..", import.meta.url)));
const sourcePath = resolve(root, "build/config.yml");
const checkOnly = process.argv.includes("--check");

const read = path => readFile(path, "utf8");
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
const version = match[1].trim();
if (!/^\d+\.\d+\.\d+$/.test(version)) {
  throw new Error(`Windows packaging requires a numeric release version, got ${version}`);
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
infoJSON.fixed.file_version = version;
infoJSON.info["0000"].ProductVersion = version;
await writeIfChanged(infoPath, `${JSON.stringify(infoJSON, null, "\t")}\n`);

const manifestPath = resolve(root, "build/windows/wails.exe.manifest");
const manifest = await read(manifestPath);
const updatedManifest = manifest.replace(/(assemblyIdentity[^>]*\sversion=")[^"]+(")/, `$1${version}$2`);
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

if (!checkOnly) console.log(`version synchronized: ${version}`);
