// DSH owns the profile lock and package operations. This journal covers the
// gap between the package process publishing its files and the DSH CLI exiting.
import fs from 'node:fs';
import {createHash, randomUUID} from 'node:crypto';
import {createRequire} from 'node:module';
import {basename, dirname, join} from 'node:path';
import {isDeepStrictEqual} from 'node:util';
import {pathToFileURL} from 'node:url';

const journalName = 'mac-package-operation.json';
const tokenKey = 'DSH_WORK_MAC_PACKAGE_OPERATION';
const journalLimit = 24 << 20;
const dependencyFields = ['dependencies', 'devDependencies', 'optionalDependencies'];
const packageFields = [
  ...dependencyFields, 'peerDependencies', 'peerDependenciesMeta', 'dependenciesMeta',
  'packageManager', 'engines', 'devEngines', 'pnpm', 'overrides', 'resolutions',
  'workspaces', 'os', 'cpu', 'libc', 'name', 'version', 'private',
];
const packageCommands = new Set(['add', 'update', 'remove', 'install']);
const conflict = () => Error('MAC_PROFILE_RECOVERY_CONFLICT');
const pending = () => Error('MAC_PROFILE_RECOVERY_PENDING');
const own = (value, key) => Object.hasOwn(value, key);

function readFile(path, limit) {
  let fd;
  try {
    fd = fs.openSync(path, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
    const stat = fs.fstatSync(fd);
    if (!stat.isFile() || stat.size > limit) throw conflict();
    const contents = Buffer.alloc(stat.size + 1);
    let offset = 0;
    while (offset < contents.length) {
      const count = fs.readSync(fd, contents, offset, contents.length - offset);
      if (count === 0) break;
      offset += count;
    }
    if (offset !== stat.size) throw conflict();
    return contents.subarray(0, offset).toString('utf8');
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  } finally {
    if (fd !== undefined) fs.closeSync(fd);
  }
}

const hash = value => value === null ? null : createHash('sha256').update(value).digest('hex');
function snapshot(dir) {
  const manifest = JSON.parse(readFile(join(dir, 'package.json'), 1 << 20));
  if (!manifest || typeof manifest !== 'object' || Array.isArray(manifest)) throw conflict();
  const selection = Object.fromEntries(packageFields.filter(key => own(manifest, key)).map(key => [key, manifest[key]]));
  if (own(manifest.dsh?.profile ?? {}, 'bundles')) selection.bundles = manifest.dsh.profile.bundles;
  return {
    manifest,
    selection,
    lock: readFile(join(dir, 'pnpm-lock.yaml'), 8 << 20),
    workspace: hash(readFile(join(dir, 'pnpm-workspace.yaml'), 1 << 20)),
    npmrc: hash(readFile(join(dir, '.npmrc'), 1 << 20)),
  };
}

function fingerprint({selection, lock, workspace, npmrc}) {
  return {selection, lock: hash(lock), workspace, npmrc};
}

function journalPath(dir, create = false) {
  const data = join(dir, '.dsh-work');
  if (create) {
    try { fs.mkdirSync(data, {mode: 0o700}); }
    catch (error) { if (error.code !== 'EEXIST') throw error; }
  }
  try {
    const stat = fs.lstatSync(data);
    if (!stat.isDirectory() || stat.uid !== process.getuid()) throw conflict();
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  return join(data, journalName);
}

function readJournal(path) {
  const contents = readFile(path, journalLimit);
  if (contents === null) return null;
  let state;
  try { state = JSON.parse(contents); }
  catch { throw conflict(); }
  if (state?.version !== 1 || typeof state.token !== 'string' ||
      !Number.isInteger(state.group) || state.group <= 1 || state.group > 2147483647 ||
      typeof state.tool !== 'string' || !state.before?.selection ||
      !(state.before.lock === null || typeof state.before.lock === 'string') ||
      !['running', 'published', 'restoring'].includes(state.phase)) throw conflict();
  return state;
}

function syncDirectory(path) {
  const fd = fs.openSync(dirname(path), fs.constants.O_RDONLY | fs.constants.O_DIRECTORY);
  try { fs.fsyncSync(fd); }
  finally { fs.closeSync(fd); }
}

function saveJournal(path, state) {
  const contents = JSON.stringify(state);
  if (Buffer.byteLength(contents) > journalLimit) throw pending();
  const temp = join(dirname(path), '.' + randomUUID());
  const fd = fs.openSync(temp, 'wx', 0o600);
  try {
    try {
      fs.writeFileSync(fd, contents);
      fs.fsyncSync(fd);
    } finally {
      fs.closeSync(fd);
    }
    fs.renameSync(temp, path);
    syncDirectory(path);
  } finally {
    fs.rmSync(temp, {force: true});
  }
}

function clearJournal(path) {
  fs.unlinkSync(path);
  syncDirectory(path);
}

function exited(pid) {
  try { process.kill(pid, 0); }
  catch (error) { return error.code === 'ESRCH'; }
  return false;
}

function checkEarlierDSHRun(dir) {
  // DSH's lock can outlive its holder, and its recorded pnpm child can
  // outlive that lock. Check before replacing any dependency files.
  const previous = readFile(join(dir, '.plugin-manager', 'run.json'), 4096);
  if (previous === null) return;
  let run;
  try { run = JSON.parse(previous); }
  catch { throw pending(); }
  if (!Number.isInteger(run?.pid) || run.pid <= 1 || run.pid > 2147483647 ||
      typeof run.grouped !== 'boolean' || !exited(run.grouped ? -run.pid : run.pid)) throw pending();
}

function packageTool(config) {
  const path = join(dirname(config.packageManager), '..', 'package.json');
  const tool = JSON.parse(readFile(path, 1 << 20));
  if (tool?.name !== 'pnpm' || typeof tool.version !== 'string') throw pending();
  return tool.version;
}

function checkWorkspace(require, dir, selection) {
  // Recovery must never make an enclosing pnpm workspace converge to the
  // files captured from a single DSH profile.
  if (selection.workspaces !== undefined) throw conflict();
  const file = 'pnpm-workspace.yaml';
  const workspace = readFile(join(dir, file), 1 << 20);
  if (workspace !== null) {
    const {load, JSON_SCHEMA} = require('js-yaml');
    const config = load(workspace, {schema: JSON_SCHEMA});
    if (!isDeepStrictEqual(config?.packages, ['.']) && !isDeepStrictEqual(config?.packages, [])) throw conflict();
    return;
  }
  for (let parent = dirname(dir); ; parent = dirname(parent)) {
    if (readFile(join(parent, file), 1 << 20) !== null) throw conflict();
    if (parent === dirname(parent)) return;
  }
}

function changedManifest(manifest, selection) {
  for (const key of dependencyFields) {
    if (own(selection, key)) manifest[key] = selection[key];
    else delete manifest[key];
  }
  return manifest;
}

// A receipt is written inside the main pnpm process. Its parent may already
// be suspended or dead by the time pnpm finishes publishing the package.
export function recordPackageExit(config) {
  const token = process.env[tokenKey];
  if (!token) return;
  process.once('exit', code => {
    if (code !== 0) return;
    const path = journalPath(process.cwd());
    const state = readJournal(path);
    if (state?.token !== token || state.group !== config.ownerPID || state.phase !== 'running') return;
    const {manifest, ...after} = snapshot(process.cwd());
    saveJournal(path, {...state, phase: 'published', after: fingerprint(after)});
  });
}

export function packageRecovery(config, profile, packageArgs, spawn) {
  const require = createRequire(config.dshEntry);
  const load = name => import(pathToFileURL(require.resolve(name)).href);
  const dir = join(process.env.DSH_HOME, 'profiles', profile);
  let tracked;
  let child;
  let recovering = false;

  return {
    async recover() {
      const path = journalPath(dir);
      if (readJournal(path) === null) return;
      const {withFileLock, writeFileAtomic} = await load('@deepseek-ai/dsh-atomic-write');
      const {runProfilePnpm} = await load('@deepseek-ai/dsh-plugin-manager/operations');
      const manifestPath = join(dir, 'package.json');
      await withFileLock(manifestPath, async () => {
        const state = readJournal(path);
        if (!state) return;
        if (!exited(-state.group) || state.tool !== packageTool(config)) throw pending();
        checkEarlierDSHRun(dir);
        const live = snapshot(dir);
        const current = fingerprint(live);
        const before = fingerprint(state.before);
        const published = state.after && isDeepStrictEqual(current, state.after);
        const restoring = state.phase === 'restoring' && isDeepStrictEqual(current, {...before, lock: state.after?.lock});
        if (!(isDeepStrictEqual(current, before) || published || restoring)) throw conflict();
        // pnpm may write the dependency maps, but changes to config or bundle
        // activation belong to another writer or to a later DSH phase.
        const beforeConfig = Object.fromEntries(Object.entries(before.selection).filter(([key]) => !dependencyFields.includes(key)));
        const currentConfig = Object.fromEntries(Object.entries(current.selection).filter(([key]) => !dependencyFields.includes(key)));
        if (!isDeepStrictEqual(beforeConfig, currentConfig) || current.workspace !== before.workspace ||
            current.npmrc !== before.npmrc) throw conflict();
        checkWorkspace(require, fs.realpathSync(dir), before.selection);
        if (state.before.lock === null &&
            [...dependencyFields, 'peerDependencies'].some(key => Object.keys(before.selection[key] ?? {}).length !== 0)) throw conflict();

        // A crash after either atomic file write replays only these expected
        // intermediate states. Transfer the journal to this owned group
        // before it spawns the repair package process.
        saveJournal(path, {...state, phase: 'restoring', group: config.ownerPID});
        if (!isDeepStrictEqual(current.selection, before.selection)) {
          await writeFileAtomic(manifestPath, JSON.stringify(changedManifest(live.manifest, before.selection), null, 2) + '\n', {mode: 0o600});
        }
        const lockPath = join(dir, 'pnpm-lock.yaml');
        if (live.lock !== state.before.lock) {
          if (state.before.lock === null) fs.unlinkSync(lockPath);
          else await writeFileAtomic(lockPath, state.before.lock, {mode: 0o600});
        }
        recovering = true;
        try {
          const lockOption = state.before.lock === null ? '--config.lockfile=false' : '--frozen-lockfile';
          const result = await runProfilePnpm(
            {profile, dir, cwd: dir, installAnchor: require.resolve('@deepseek-ai/dsh/package.json')},
            ['install', '--force', '--offline', '--reporter=silent', '--config.optimistic-repeat-install=false', lockOption],
            {command: config.node, args: packageArgs, execution: 'cli', activateNewBundles: false, outputBytes: 16384},
          );
          if (result.exitCode !== 0 || !isDeepStrictEqual(fingerprint(snapshot(dir)), before)) throw pending();
          clearJournal(path);
        } finally {
          recovering = false;
        }
      }, {waitMs: 5000});
    },

    spawn(file, args, options) {
      if (recovering || !packageCommands.has(args[packageArgs.length])) return spawn(file, args, options);
      // The pinned DSH operation holds this same lock when it calls spawn.
      if (!options?.cwd || fs.realpathSync(options.cwd) !== fs.realpathSync(dir) ||
          readFile(join(dir, 'package.json.lock'), 64) !== process.pid + '\n') throw pending();
      const path = journalPath(dir, true);
      if (!tracked) {
        if (readJournal(path)) throw pending();
        const {manifest, ...before} = snapshot(dir);
        tracked = {version: 1, token: randomUUID(), tool: packageTool(config), group: config.ownerPID, before};
      }
      saveJournal(path, {...tracked, phase: 'running'});
      child = spawn(file, args, {...options, env: {...options.env, [tokenKey]: tracked.token}});
      return child;
    },

    settle(code) {
      if (!tracked || child?.exitCode === null) return;
      const path = journalPath(dir);
      const state = readJournal(path);
      if (state?.token !== tracked.token) throw pending();
      if (code === 0 || state.phase === 'published' ||
          isDeepStrictEqual(fingerprint(snapshot(dir)), fingerprint(state.before))) clearJournal(path);
    },
  };
}
