import { execFileSync } from 'node:child_process';
import {
  copyFileSync, existsSync, mkdirSync, mkdtempSync, renameSync, rmSync, symlinkSync,
} from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { VERSION_PATTERN } from './release-version.mjs';
import { stageMacEmbeddedCEF } from './stage-mac-embedded-cef.mjs';
import { macPackageSigning, signMacApplication, signMacDiskImage, verifyMacDiskImage } from './sign-macos-package.mjs';

const repo = fileURLToPath(new URL('../', import.meta.url));
const minimumOS = '15.0';
const appName = 'DSH Work.app';
const args = new Map();
for (let index = 2; index < process.argv.length; index += 2) {
  const [key, value] = process.argv.slice(index, index + 2);
  if (!['--cef-root', '--output-dir', '--node-include'].includes(key) || !value || args.has(key)) {
    throw Error('Usage: node scripts/package-macos.mjs --cef-root SDK --output-dir DIR [--node-include HEADERS]');
  }
  args.set(key, resolve(value));
}
if (process.platform !== 'darwin' || process.arch !== 'arm64') throw Error('Requires native macOS arm64');
if (!args.has('--cef-root') || !args.has('--output-dir')) throw Error('CEF SDK and output paths are required');
if (!existsSync(join(repo, 'frontend/dist/index.html'))) throw Error('Build the frontend before packaging');
const signing = macPackageSigning();

const run = (command, parameters, { env = process.env, capture = false } = {}) =>
  execFileSync(command, parameters, {
    cwd: repo, env, encoding: 'utf8', stdio: capture ? ['ignore', 'pipe', 'inherit'] : 'inherit',
  });
const output = args.get('--output-dir');
mkdirSync(output, { recursive: true });
const work = mkdtempSync(join(output, '.package-'));
const contents = join(work, appName, 'Contents');
const app = join(work, appName);
const executables = join(contents, 'MacOS');
const resources = join(contents, 'Resources');
const cef = join(work, 'cef-build');
const addons = join(work, 'addons');
const includes = args.has('--node-include') ? ['--node-include', args.get('--node-include')] : [];
const imageRoot = join(work, 'image');
const image = join(work, 'DSH Work.dmg');
const cli = join(executables, 'dsh-work-cli');

let published = false;
try {
  run(process.execPath, ['scripts/sync-version.mjs', '--check']);
  run(process.execPath, ['scripts/build-embedded-cef-mac.mjs', '--cef-root', args.get('--cef-root'), '--output-dir', cef]);
  run(process.execPath, ['scripts/build-workeripc-mac.mjs', '--output-dir', addons, ...includes]);
  run(process.execPath, ['scripts/build-mac-preview-sandbox.mjs', '--output-dir', addons, ...includes]);

  mkdirSync(executables, { recursive: true });
  mkdirSync(resources, { recursive: true });
  const plist = join(contents, 'Info.plist');
  copyFileSync(join(repo, 'build/darwin/Info.plist'), plist);
  copyFileSync(join(repo, 'build/darwin/icons.icns'), join(resources, 'icons.icns'));
  run('/usr/bin/plutil', ['-replace', 'LSMinimumSystemVersion', '-string', minimumOS, plist]);
  run('/usr/bin/plutil', ['-insert', 'NSAppTransportSecurity', '-json', '{"NSAllowsLocalNetworking":true}', plist]);
  run('/usr/bin/plutil', ['-lint', plist]);
  const version = execFileSync('/usr/bin/plutil', ['-extract', 'CFBundleShortVersionString', 'raw', plist], { encoding: 'utf8' }).trim();
  if (!VERSION_PATTERN.test(version)) throw Error('The application plist contains an invalid version');
  const buildEnv = {
    ...process.env,
    GOOS: 'darwin',
    GOARCH: 'arm64',
    CGO_ENABLED: '1',
    MACOSX_DEPLOYMENT_TARGET: minimumOS,
    CGO_CFLAGS: `${process.env.CGO_CFLAGS || ''} -mmacosx-version-min=${minimumOS}`,
    CGO_CXXFLAGS: `${process.env.CGO_CXXFLAGS || ''} -mmacosx-version-min=${minimumOS}`,
    CGO_LDFLAGS: `${process.env.CGO_LDFLAGS || ''} -mmacosx-version-min=${minimumOS}`,
  };
  const buildFlags = ['build', '-trimpath', '-buildvcs=false', '-tags=production',
    `-ldflags=-X github.com/local/dsh-work/internal/version.Value=${version}`];
  run('go', [...buildFlags, '-o', join(executables, 'dsh-work'), '.'], {env: buildEnv});
  run('go', [...buildFlags, '-o', cli, './cmd/dsh-work'], {env: buildEnv});
  for (const name of ['workeripc-mac.node', 'dsh-preview-sandbox.node']) {
    copyFileSync(join(addons, name), join(executables, name));
  }
  stageMacEmbeddedCEF(app, join(cef, 'publication.json'));
  signMacApplication(run, app, signing, join(repo, 'build/darwin/cef-helper-jit.plist'));
  const verifyApplication = candidate => {
    const bin = join(candidate, 'Contents', 'MacOS');
    for (const executable of ['dsh-work', 'dsh-work-cli']) {
      run(join(bin, executable), ['cef-package-verify']);
    }
    if (execFileSync(join(bin, 'dsh-work-cli'), ['version'], { encoding: 'utf8' }).trim() !== version) {
      throw Error('The packaged CLI version differs from the application plist');
    }
  };
  verifyApplication(app);

  mkdirSync(imageRoot);
  run('/usr/bin/ditto', [app, join(imageRoot, appName)]);
  symlinkSync('/Applications', join(imageRoot, 'Applications'));
  run('/usr/bin/hdiutil', ['create', '-srcfolder', imageRoot, '-volname', 'DSH Work', '-format', 'UDZO', image]);
  verifyMacDiskImage(run, image, appName, verifyApplication);
  signMacDiskImage(run, app, image, signing);
  run('/usr/bin/hdiutil', ['verify', image]);
  rmSync(imageRoot, { recursive: true });
  rmSync(addons, { recursive: true });
  rmSync(cef, { recursive: true });
  const destination = join(output, basename(work).replace('.package-', 'build-'));
  renameSync(work, destination);
  published = true;
  console.log(`macOS application: ${join(destination, appName)}`);
  console.log(`macOS disk image: ${join(destination, 'DSH Work.dmg')}`);
  console.log(`Distribution: ${signing.notaryProfile ? 'Developer ID and notarized' : signing.identity === '-' ? 'ad hoc' : 'Developer ID'}`);
} finally {
  if (!published) rmSync(work, { recursive: true, force: true });
}
