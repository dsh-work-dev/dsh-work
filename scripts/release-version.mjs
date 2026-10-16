#!/usr/bin/env node
// Single preflight for the release version of one build.
//
// This file owns the entry point and the fast feedback rules; the version
// itself is computed by internal/release, invoked as
// internal/release/cmd/release-version, so a workflow, a package script and a
// developer all run the same code over the same SemVer rules. See
// docs/release-channels.md for the identity contract.
//
//   node scripts/release-version.mjs --channel stable
//   node scripts/release-version.mjs --channel beta --version v0.3.0-beta.2
//   node scripts/release-version.mjs --channel nightly --date 2026-10-13 \
//     --run-id 123456789 --last-stable 0.2.0 --source-sha "$GITHUB_SHA"
//
// The command never writes a version into a file: it prints the version on
// stdout and the components with --field or --json, so the caller injects the
// exact same string into the build, the installer name and the release record.
//
// The helper is spawned with inherited stdio, so its stdout is this process's
// stdout and the version string is never re-encoded on the way out.
//
// Checked by: node scripts/release-version.test.mjs

import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
export const REPO_ROOT = path.resolve(SCRIPT_DIR, '..');
export const PACKAGE_DIR = path.join('internal', 'release');
export const PACKAGE_SOURCES = path.join(PACKAGE_DIR, 'cmd', 'release-version');
export const HELPER_PACKAGE = `./${PACKAGE_SOURCES.split(path.sep).join('/')}`;

const CHANNELS = ['stable', 'beta', 'nightly'];
const FIELDS = ['version', 'channel', 'base', 'date', 'runId', 'sourceSha', 'fileVersion', 'lastStableBase'];
const VALUE_FLAGS = [
  '--channel',
  '--version',
  '--base',
  '--config',
  '--date',
  '--run-id',
  '--source-sha',
  '--last-stable',
  '--field',
  '--go',
  '--helper',
];

// Rules mirrored from internal/release so a typo is refused before any Go
// toolchain work happens. The authoritative check still runs in Go.
export const VERSION_PATTERN = /^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/;
export const BASE_ONLY_PATTERN = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const IDENTIFIER_PATTERN = /^[0-9A-Za-z-]+$/;
const YMD_PATTERN = /^(\d{4})-(\d{2})-(\d{2})$/;
const RUN_ID_PATTERN = /^[0-9]+$/;
const BETA_PRERELEASE = /^[0-9]+\.[0-9]+\.[0-9]+-beta\.(0|[1-9][0-9]*)$/;
const NIGHTLY_PRERELEASE = /^[0-9]+\.[0-9]+\.[0-9]+-nightly\.\d{8}\.[0-9]+$/;
const MAX_FILE_FIELD = 65535;

// fileVersionFor maps a semantic version to the four numeric fields Windows
// file metadata uses: X.Y.Z.0. It returns null when the version does not fit.
export function fileVersionFor(version) {
  const match = VERSION_PATTERN.exec(version);
  if (!match) {
    return null;
  }
  const fields = [match[1], match[2], match[3]].map((part) => Number(part));
  if (fields.some((part) => part > MAX_FILE_FIELD)) {
    return null;
  }
  return `${fields.join(".")}.0`;
}

export const USAGE = `usage: node scripts/release-version.mjs [flags]

Computes the single release version for one build. The version is validated by
internal/release; this entry point only refuses obvious mistakes early and
forwards the rest.

  node scripts/release-version.mjs --channel stable
  node scripts/release-version.mjs --channel beta --version v0.3.0-beta.2
  node scripts/release-version.mjs --channel nightly --date 2026-10-13 \\
    --run-id 123456789 --last-stable 0.2.0 --source-sha "$GITHUB_SHA"

Flags:
  --channel stable|beta|nightly   release channel (default stable)
  --version VERSION               full release version for stable and beta
  --base X.Y.Z                    base version; read from build/config.yml when absent
  --config PATH                   build/config.yml path (default build/config.yml)
  --date YYYY-MM-DD               UTC date of the run; nightly defaults to today
  --run-id ID                     GitHub workflow run id, required for nightly
  --source-sha SHA                build commit, recorded next to the version
  --last-stable X.Y.Z             published stable version a nightly base must exceed
  --field NAME                    print one component: ${FIELDS.join(', ')}
  --json                          print the whole identity as JSON
  --dry-run                       validate the arguments here and stop before Go runs
  --go PATH                       Go binary used to build the release helper
  --helper PATH                   run an already built release helper instead of building one
  --help                          print this text
`;

export function parseArgs(argv) {
  const options = { passthrough: [], json: false, dryRun: false, help: false };
  for (let index = 0; index < argv.length; index += 1) {
    const token = argv[index];
    if (token === '--help' || token === '-h') {
      options.help = true;
      continue;
    }
    if (token === '--json') {
      options.json = true;
      options.passthrough.push([token]);
      continue;
    }
    if (token === '--dry-run') {
      options.dryRun = true;
      continue;
    }
    const equals = token.indexOf('=');
    const name = equals === -1 ? token : token.slice(0, equals);
    if (!VALUE_FLAGS.includes(name)) {
      return { error: `unknown argument ${token}\n${USAGE}` };
    }
    if (equals !== -1) {
      options[name] = token.slice(equals + 1);
      options.passthrough.push([name, options[name]]);
      continue;
    }
    const value = argv[index + 1];
    if (value === undefined || value.startsWith('--')) {
      return { error: `${name} needs a value` };
    }
    index += 1;
    options[name] = value;
    options.passthrough.push([name, value]);
  }
  return options;
}

export function validate(options) {
  const channel = options['--channel'] ?? 'stable';
  if (!CHANNELS.includes(channel)) {
    return { error: `unknown channel ${JSON.stringify(channel)}; expected one of ${CHANNELS.join(', ')}` };
  }

  const explicitVersion = options['--version'];
  if (explicitVersion !== undefined) {
    const failure = strictVersion(explicitVersion, 'release version');
    if (failure) {
      return failure;
    }
  }
  const base = options['--base'];
  if (base !== undefined) {
    const failure = strictVersion(base, 'base version');
    if (failure) {
      return failure;
    }
    if (base.includes('-')) {
      return { error: `base version ${JSON.stringify(base)} must be a plain X.Y.Z base: the prerelease part belongs to the release version, not to build/config.yml` };
    }
  }
  if (channel === 'beta' && explicitVersion === undefined) {
    return { error: 'the beta channel needs --version, for example --version v0.3.0-beta.2, so the published tag and the build agree' };
  }
  if (channel === 'beta' && explicitVersion !== undefined && !BETA_PRERELEASE.test(stripV(explicitVersion))) {
    return { error: `release version ${JSON.stringify(explicitVersion)} must carry exactly one beta.<N> identifier, for example 0.3.0-beta.2` };
  }
  if (channel === 'nightly' && options['--run-id'] === undefined) {
    return { error: 'the nightly channel needs --run-id: the version carries the workflow run id so a re-run rebuilds the same identity' };
  }
  if (channel === 'nightly' && explicitVersion !== undefined && !NIGHTLY_PRERELEASE.test(stripV(explicitVersion))) {
    return { error: `release version ${JSON.stringify(explicitVersion)} must carry exactly one nightly.<YYYYMMDD>.<runId> identifier, for example 0.3.0-nightly.20261013.123456789` };
  }
  if (options['--run-id'] !== undefined && !RUN_ID_PATTERN.test(options['--run-id'])) {
    return { error: `run id ${JSON.stringify(options['--run-id'])} must be a GitHub run id: digits only` };
  }
  if (options['--date'] !== undefined) {
    const failure = validateDate(options['--date']);
    if (failure) {
      return failure;
    }
  }
  if (options['--last-stable'] !== undefined) {
    if (options['--last-stable'].includes('-')) {
      return { error: `last published stable version ${JSON.stringify(options['--last-stable'])} must be a plain X.Y.Z base: a prerelease is not published as stable` };
    }
    const failure = strictVersion(options['--last-stable'], 'last published stable version');
    if (failure) {
      return failure;
    }
  }
  if (options['--field'] !== undefined && !FIELDS.includes(options['--field'])) {
    return { error: `unknown field ${JSON.stringify(options['--field'])}; expected one of ${FIELDS.join(', ')}` };
  }
  if (options['--helper'] !== undefined && !existsSync(options['--helper'])) {
    return { error: `--helper ${JSON.stringify(options['--helper'])} does not exist` };
  }
  return options;
}

// strictVersion returns null when value is a strict semantic version and the
// refusal otherwise.
function strictVersion(value, label) {
  const match = VERSION_PATTERN.exec(value);
  if (!match) {
    return { error: `${label} ${JSON.stringify(value)} is not a strict semantic version: expected X.Y.Z or X.Y.Z-<prerelease>, with numeric identifiers that have no leading zeros` };
  }
  if (match[4] !== undefined) {
    for (const identifier of match[4].split('.')) {
      if (identifier === '') {
        return { error: `${label} ${JSON.stringify(value)} has an empty prerelease identifier` };
      }
      if (!IDENTIFIER_PATTERN.test(identifier)) {
        return { error: `${label} ${JSON.stringify(value)} has an invalid prerelease identifier ${JSON.stringify(identifier)}` };
      }
    }
  }
  const numeric = [match[1], match[2], match[3]].map((part) => Number(part));
  if (numeric.some((part) => part > MAX_FILE_FIELD)) {
    return { error: `${label} ${JSON.stringify(value)} exceeds the Windows file version limit: every X.Y.Z field must be 0..${MAX_FILE_FIELD}` };
  }
  return null;
}

// stripV removes the optional leading v a release tag carries.
function stripV(value) {
  return value.startsWith('v') ? value.slice(1) : value;
}

function validateDate(value) {
  const match = YMD_PATTERN.exec(value);
  if (!match) {
    return { error: `date ${JSON.stringify(value)} must be a UTC date as YYYY-MM-DD` };
  }
  const parsed = new Date(`${value}T00:00:00Z`);
  if (Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== value) {
    return { error: `date ${JSON.stringify(value)} is not a real calendar date` };
  }
  return null;
}

// cacheCandidates lists the directories the compiled helper may live in, most
// preferred first. A confined session can be denied the user cache directory,
// so the operating system temporary directory is always the last resort.
export function cacheCandidates(env = process.env, platform = process.platform) {
  const candidates = [];
  if (env.DSH_RELEASE_CACHE) {
    candidates.push(env.DSH_RELEASE_CACHE);
  }
  if (platform === 'win32') {
    candidates.push(path.join(env.LOCALAPPDATA ?? path.join(homedir(), 'AppData', 'Local'), 'dsh-work', 'release-version'));
  } else {
    candidates.push(path.join(env.XDG_CACHE_HOME ?? path.join(homedir(), '.cache'), 'dsh-work', 'release-version'));
  }
  candidates.push(path.join(tmpdir(), 'dsh-work-release-version'));
  return [...new Set(candidates.map((directory) => path.resolve(directory)))];
}

export function cacheRoot(env = process.env, platform = process.platform) {
  return cacheCandidates(env, platform)[0];
}

// sourceDigest keys the compiled helper to the Go source it was built from, so
// a stale binary can never answer for edited rules.
export function sourceDigest(root = REPO_ROOT) {
  const hash = createHash('sha256');
  for (const file of sourceFiles(root)) {
    hash.update(path.relative(root, file));
    hash.update(readFileSync(file));
  }
  return hash.digest('hex').slice(0, 16);
}

function sourceFiles(root) {
  const files = [];
  for (const dir of [PACKAGE_DIR, PACKAGE_SOURCES]) {
    const absolute = path.join(root, dir);
    if (!existsSync(absolute)) {
      continue;
    }
    for (const entry of readdirSync(absolute)) {
      if (entry.endsWith('.go')) {
        files.push(path.join(absolute, entry));
      }
    }
  }
  return files.sort();
}

export function helperBinary(options, env = process.env, platform = process.platform) {
  if (options['--helper']) {
    return { go: options['--go'] ?? env.GO ?? 'go', path: path.resolve(options['--helper']) };
  }
  const digest = sourceDigest();
  const name = platform === 'win32' ? `release-version-${digest}.exe` : `release-version-${digest}`;
  const directories = cacheCandidates(env, platform);
  return {
    go: options['--go'] ?? env.GO ?? 'go',
    name,
    directories,
    path: path.join(directories[0], name),
  };
}

// writableHelperPath picks the first candidate directory this process can
// actually create. It returns the failure when none can be created, so the
// caller reports which directories were tried instead of an error code.
export function writableHelperPath(binary) {
  const directories = binary.directories ?? [path.dirname(binary.path)];
  const name = binary.name ?? path.basename(binary.path);
  const tried = [];
  let lastError;
  for (const directory of directories) {
    const candidate = path.join(directory, name);
    if (existsSync(candidate)) {
      return { path: candidate };
    }
    tried.push(directory);
    try {
      mkdirSync(directory, { recursive: true });
      return { path: candidate };
    } catch (error) {
      lastError = error;
    }
  }
  return {
    error:
      `no writable directory for the release helper (tried ${tried.join(', ')}): ` +
      `${lastError?.code ?? lastError?.message ?? 'unknown error'}; set DSH_RELEASE_CACHE to a writable directory`,
  };
}

// Stdio is inherited in both steps: the child writes straight to this process's
// stdout and stderr, so the version string is never re-encoded on the way out.
function ensureHelper(binary, env = process.env) {
  const selected = writableHelperPath(binary);
  if (selected.error) {
    return selected;
  }
  binary.path = selected.path;
  if (existsSync(binary.path)) {
    return null;
  }
  const buildEnv = { ...env };
  if (!buildEnv.GOCACHE) {
    // A confined session may not be able to write the default cache; a caller
    // that wants another location sets GOCACHE itself.
    buildEnv.GOCACHE = path.join(REPO_ROOT, '.gocache');
  }
  const build = spawnSync(binary.go, ['build', '-o', binary.path, HELPER_PACKAGE], {
    cwd: REPO_ROOT,
    env: buildEnv,
    stdio: 'inherit',
  });
  if (build.error) {
    return { error: `cannot run ${binary.go} to build the release helper (${build.error.message}); pass --go with the Go binary path` };
  }
  if (build.status !== 0) {
    rmSync(binary.path, { force: true });
    return { error: `building the release helper from ${HELPER_PACKAGE} failed with exit code ${build.status}` };
  }
  return null;
}

// run executes the preflight and returns the exit code. It never exits the
// process itself, so the checks can call it directly.
export function run(argv, io = { out: process.stdout, err: process.stderr }) {
  const parsed = parseArgs(argv);
  if (parsed.error) {
    io.err.write(`release-version: ${parsed.error}\n`);
    return 2;
  }
  if (parsed.help) {
    io.out.write(USAGE);
    return 0;
  }
  const validated = validate(parsed);
  if (validated.error) {
    io.err.write(`release-version: ${validated.error}\n`);
    return 2;
  }
  if (parsed.dryRun) {
    io.err.write('release-version: arguments accepted (--dry-run)\n');
    return 0;
  }

  const binary = helperBinary(parsed);
  const buildFailure = ensureHelper(binary);
  if (buildFailure) {
    io.err.write(`release-version: ${buildFailure.error}\n`);
    return 1;
  }
  const invocation = spawnSync(binary.path, forwarded(parsed), {
    cwd: REPO_ROOT,
    stdio: 'inherit',
  });
  if (invocation.error) {
    io.err.write(`release-version: cannot run the release helper ${binary.path}: ${invocation.error.message}\n`);
    return 1;
  }
  return invocation.status ?? 1;
}

// forwarded drops the arguments this entry point consumes itself, so the helper
// only sees the flags it defines. Each entry is one whole argument group, so a
// flag without a value (--json) cannot shift the pairing of the ones after it.
export function forwarded(options) {  const forwarded = [];
  for (const group of options.passthrough) {
    if (group[0] === '--helper') {
      continue;
    }
    forwarded.push(...group);
  }
  return forwarded;
}

const invokedDirectly = process.argv[1] !== undefined && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (invokedDirectly) {
  process.exit(run(process.argv.slice(2)));
}
