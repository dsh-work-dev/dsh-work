// Reuses an explicitly built browser-lab SDK/wrapper/bundle. No SDK download,
// cache relocation, lab patch, or product/native source edit.
import {execFileSync} from 'node:child_process';
import {copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, writeFileSync} from 'node:fs';
import {join, relative, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

if (process.platform !== 'darwin' || process.arch !== 'arm64') throw Error('Requires native macOS arm64');
const options = new Map();
for (let i = 2; i < process.argv.length; i += 2) {
  if (!['--lab-work', '--output-dir'].includes(process.argv[i]) || !process.argv[i + 1]) throw Error('Expected --lab-work DIR --output-dir DIR');
  options.set(process.argv[i], resolve(process.argv[i + 1]));
}
if (!options.has('--lab-work') || !options.has('--output-dir')) throw Error('Expected --lab-work DIR --output-dir DIR');
const version = '156.0.15+gcf671c0+chromium-156.0.8078.80';
const work = options.get('--lab-work'), output = options.get('--output-dir');
const sdk = join(work, `cef_binary_${version}_macosarm64`);
const wrapper = join(work, 'build/libcef_dll_wrapper/libcef_dll_wrapper.a');
const sourceApp = join(work, 'bundle/broker.app');
for (const path of [join(sdk, 'include/cef_version.h'), wrapper, join(sourceApp, 'Contents/Info.plist')]) if (!existsSync(path)) throw Error(`Missing already-built lab input: ${path}`);
if (!readFileSync(join(sdk, 'include/cef_version.h'), 'utf8').includes(version)) throw Error('CEF version differs from the investigated candidate');
mkdirSync(output, {recursive: true});
const stage = mkdtempSync(join(output, 'cef-owner-')), build = join(stage, 'build'), app = join(stage, 'broker.app');
const source = fileURLToPath(new URL('../tools/posix-ipc-probe/cef-owner/', import.meta.url));
const run = (program, args, extra = {}) => execFileSync(program, args, {stdio: 'inherit', ...extra});
run('cmake', ['-S', source, '-B', build, '-DCMAKE_BUILD_TYPE=Release', '-DCMAKE_OSX_ARCHITECTURES=arm64', '-DCMAKE_OSX_DEPLOYMENT_TARGET=15.0', '-DUSE_SANDBOX=ON', `-DCEF_ROOT=${sdk}`, `-DCEF_WRAPPER_LIBRARY=${wrapper}`]);
run('cmake', ['--build', build, '--parallel', '3']);
run('ditto', [sourceApp, app]);
const executable = join(app, 'Contents/MacOS/cefsimple');
copyFileSync(join(build, 'out/cef_owner_broker'), executable);
const helpers = [];
for (const entry of readdirSync(join(app, 'Contents/Frameworks'), {withFileTypes: true})) {
  if (!entry.isDirectory() || !/^cefsimple Helper.*\.app$/.test(entry.name)) continue;
  const helper = join(app, 'Contents/Frameworks', entry.name);
  const binary = execFileSync('plutil', ['-extract', 'CFBundleExecutable', 'raw', '-o', '-', join(helper, 'Contents/Info.plist')], {encoding: 'utf8'}).trim();
  const target = join(helper, 'Contents/MacOS', binary);
  if (!existsSync(target)) throw Error(`Missing SDK helper executable: ${target}`);
  copyFileSync(join(build, 'out/cef_owner_helper'), target);
  run('codesign', ['--force', '--sign', '-', helper]);
  helpers.push(relative(app, target));
}
if (!helpers.length) throw Error('No official helper app bundles found');
const crashConfigs = [];
function findConfigs(directory) {
  for (const entry of readdirSync(directory, {withFileTypes: true})) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) findConfigs(path);
    else if (entry.name === 'crash_reporter.cfg') crashConfigs.push(relative(app, path));
  }
}
findConfigs(app);
if (crashConfigs.length) throw Error(`Candidate unexpectedly enables crash configuration: ${crashConfigs.join(', ')}`);
run('codesign', ['--force', '--deep', '--sign', '-', app]);
run('codesign', ['--verify', '--deep', '--strict', app]);
const manifest = {schemaVersion: 1, kind: 'cef-specific-owner-diagnostic', version, sdk, wrapper, app, executable, inspector: join(build, 'out/cef_owner_inspect'), helpers, crashConfigs, sandbox: true, control: 'private diagnostic files/FIFO; no formal transport claim'};
writeFileSync(join(output, 'cef-owner-bundle.json'), `${JSON.stringify(manifest, null, 2)}\n`);
console.log(JSON.stringify(manifest));
