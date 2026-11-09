// Build-time only. Reuse for CI tests and the application bundle; startup never
// invokes a compiler or installs Node headers/dependencies.
import {execFileSync} from 'node:child_process';
import {existsSync, mkdirSync, mkdtempSync, renameSync, rmdirSync, statSync, unlinkSync, writeFileSync} from 'node:fs';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const options = new Map();
for (let i = 2; i < process.argv.length; i += 2) {
  const key = process.argv[i], value = process.argv[i + 1];
  if (!['--output-dir', '--node-include', '--sign'].includes(key) || !value || options.has(key)) {
    throw Error('Usage: node build-workeripc-mac.mjs --output-dir DIR [--node-include DIR] [--sign IDENTITY]');
  }
  options.set(key, value);
}
if (!options.has('--output-dir')) throw Error('--output-dir is required');
if (process.platform !== 'darwin' || process.arch !== 'arm64') throw Error('Build requires a native macOS arm64 SDK environment');
const root = fileURLToPath(new URL('../', import.meta.url));
const output = resolve(options.get('--output-dir'));
// setup-node's official distribution keeps headers alongside bin/node. An
// existing explicit SDK input is also accepted; nothing is downloaded here.
const include = resolve(options.get('--node-include') ?? join(dirname(process.execPath), '../include/node'));
for (const header of ['node_api.h', 'node_api_types.h']) {
  if (!existsSync(join(include, header)) || !statSync(join(include, header)).isFile()) {
    throw Error(`Missing build input ${join(include, header)}; provide installed official Node headers with --node-include`);
  }
}
mkdirSync(output, {recursive: true});
const stage = mkdtempSync(join(output, '.workeripc-build-'));
const stagedAddon = join(stage, 'workeripc-mac.node');
const addon = join(output, 'workeripc-mac.node');
const run = (program, args) => execFileSync(program, args, {stdio: 'inherit'});
try {
  run('clang', [
    '-std=c11', '-Wall', '-Wextra', '-Werror', '-arch', 'arm64', '-mmacosx-version-min=15.0',
    '-DNODE_GYP_MODULE_NAME=dsh_workeripc', `-I${include}`, '-bundle', '-undefined', 'dynamic_lookup',
    join(root, 'internal/workeripc/native/bridge_darwin.c'),
    join(root, 'internal/localipc/mach_peer_darwin.c'), '-o', stagedAddon,
  ]);
  run('codesign', ['--force', '--sign', options.get('--sign') ?? '-', stagedAddon]);
  run('codesign', ['--verify', '--strict', stagedAddon]);
  renameSync(stagedAddon, addon);
  const build = {
    schemaVersion: 1, platform: 'darwin', architecture: 'arm64', minimumMacOS: '15.0',
    nodeAPI: 8, buildNode: process.version, nodeHeaders: include, addon,
  };
  writeFileSync(join(output, 'workeripc-mac-build.json'), `${JSON.stringify(build, null, 2)}\n`);
  console.log(JSON.stringify(build));
} finally {
  if (existsSync(stagedAddon)) unlinkSync(stagedAddon);
  rmdirSync(stage);
}
