// The official SDK supplies the framework, wrapper and helper suffixes.
// DSH Work supplies the broker, helpers and bundle metadata.
import {execFileSync} from 'node:child_process';
import {
  copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync,
  realpathSync, renameSync, rmSync, symlinkSync, writeFileSync,
} from 'node:fs';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

if (process.platform !== 'darwin' || process.arch !== 'arm64') throw Error('Requires native macOS arm64');
const options = new Map();
for (let i = 2; i < process.argv.length; i += 2) {
  const key = process.argv[i], value = process.argv[i + 1];
  if (!['--cef-root', '--output-dir'].includes(key) || !value || options.has(key)) {
    throw Error('Expected --cef-root SDK --output-dir DIR');
  }
  options.set(key, resolve(value));
}
if (options.size !== 2) throw Error('Expected --cef-root SDK --output-dir DIR');

const sdk = realpathSync(options.get('--cef-root'));
const repo = fileURLToPath(new URL('../', import.meta.url));
const version = '156.0.15+gcf671c0+chromium-156.0.8078.80';
const bundleVersion = version.split('+')[0];
const minimumOS = '15.0';
const browserName = 'DSH Work Browser';
const browserExecutable = 'DSHWorkBrowser';
const bundleID = 'dev.dsh.embeddedbrowser.broker';

if (!readFileSync(join(sdk, 'include/cef_version.h'), 'utf8').includes(version)) throw Error('Approved CEF156 SDK required');
if (readFileSync(join(sdk, 'CMakeLists.txt'), 'utf8').includes('# browser-lab bridge target')) {
  throw Error('Formal build requires an unmodified official SDK');
}
mkdirSync(options.get('--output-dir'), {recursive: true});
const output = realpathSync(options.get('--output-dir'));
const run = (exe, args, extra = {}) => execFileSync(exe, args, {stdio: 'inherit', ...extra});
const official = join(output, 'official');
const build = join(output, 'native');
const cmakeOptions = [
  '-DCMAKE_BUILD_TYPE=Release', '-DCMAKE_OSX_ARCHITECTURES=arm64',
  `-DCMAKE_OSX_DEPLOYMENT_TARGET=${minimumOS}`, '-DUSE_SANDBOX=ON',
];

run('cmake', ['-S', sdk, '-B', official, ...cmakeOptions]);
run('cmake', ['--build', official, '--config', 'Release', '--target', 'libcef_dll_wrapper', '--parallel', '3']);
const wrapper = join(official, 'libcef_dll_wrapper/libcef_dll_wrapper.a');
if (!existsSync(wrapper)) throw Error('Official SDK wrapper missing');
run('cmake', [
  '-S', join(repo, 'native/embeddedbrowser/mac'), '-B', build,
  `-DCEF_ROOT=${sdk}`, `-DCEF_WRAPPER_LIBRARY=${wrapper}`, ...cmakeOptions,
]);
run('cmake', ['--build', build, '--parallel', '3']);

const helperLayout = readFileSync(join(build, 'cef-helper-layout.txt'), 'utf8').trimEnd().split('\n');
if (helperLayout[0] !== '::') throw Error('Official CEF helper layout missing');
function installBundle(app, name, identifier, binary, metadata = {}) {
  const contents = join(app, 'Contents');
  mkdirSync(join(contents, 'MacOS'), {recursive: true});
  const executable = join(contents, 'MacOS', name);
  copyFileSync(binary, executable);
  const plist = {
    CFBundleDevelopmentRegion: 'en',
    CFBundleIdentifier: identifier,
    CFBundleExecutable: name,
    CFBundleDisplayName: name,
    CFBundleName: name,
    CFBundlePackageType: 'APPL',
    CFBundleInfoDictionaryVersion: '6.0',
    CFBundleVersion: bundleVersion,
    CFBundleShortVersionString: bundleVersion,
    LSMinimumSystemVersion: minimumOS,
    NSSupportsAutomaticGraphicsSwitching: true,
    LSEnvironment: {MallocNanoZone: '0'},
    ...metadata,
  };
  run('plutil', ['-convert', 'xml1', '-o', join(contents, 'Info.plist'), '--', '-'], {
    input: JSON.stringify(plist), stdio: ['pipe', 'inherit', 'inherit'],
  });
  return executable;
}

function rejectCrashConfig(dir) {
  for (const entry of readdirSync(dir, {withFileTypes: true})) {
    if (entry.name === 'crash_reporter.cfg') throw Error('Unqualified Crashpad configuration');
    if (entry.isDirectory()) rejectCrashConfig(join(dir, entry.name));
  }
}

// Publish a new bundle at a distinct path. Existing owners can keep using the
// signed files they opened while publication.json switches to the new build.
const publication = mkdtempSync(join(output, 'publication-'));
let published = false;
try {
  const app = join(publication, `${browserExecutable}.app`);
  const executable = installBundle(app, browserExecutable, bundleID, join(build, 'out/dsh_cef_broker'), {
    CFBundleDisplayName: browserName,
    CFBundleName: browserName,
    CFBundleIconFile: 'icons.icns',
    NSPrincipalClass: 'NSApplication',
  });
  const contents = join(app, 'Contents');
  const resources = join(contents, 'Resources');
  mkdirSync(resources);
  copyFileSync(join(repo, 'build/darwin/icons.icns'), join(resources, 'icons.icns'));

  const frameworks = join(contents, 'Frameworks');
  const frameworkName = 'Chromium Embedded Framework';
  const framework = join(frameworks, `${frameworkName}.framework`);
  mkdirSync(join(framework, 'Versions'), {recursive: true});
  run('ditto', [join(sdk, 'Release', `${frameworkName}.framework`), join(framework, 'Versions/A')]);
  symlinkSync('A', join(framework, 'Versions/Current'));
  for (const name of [frameworkName, 'Libraries', 'Resources']) {
    symlinkSync(`Versions/A/${name}`, join(framework, name));
  }

  const helpers = helperLayout.map(spec => {
    const [suffix, , idSuffix] = spec.split(':');
    if (!/^( \([A-Za-z]+\))?$/.test(suffix) || !/^(\.[a-z]+)?$/.test(idSuffix)) {
      throw Error(`Unexpected CEF helper layout: ${spec}`);
    }
    const name = `${browserExecutable} Helper${suffix}`;
    const displayName = `${browserName} Helper${suffix}`;
    return installBundle(
      join(frameworks, `${name}.app`), name, `${bundleID}.helper${idSuffix}`,
      join(build, 'out/dsh_cef_helper'), {
        CFBundleDisplayName: displayName, CFBundleName: displayName,
        LSFileQuarantineEnabled: true, LSUIElement: '1',
      },
    );
  });

  const owner = join(contents, 'MacOS/dsh-cef-owner');
  run('go', ['build', '-o', owner, './cmd/dsh-cef-owner-mac'], {
    cwd: repo,
    env: {
      ...process.env, MACOSX_DEPLOYMENT_TARGET: minimumOS,
      CGO_CFLAGS: `${process.env.CGO_CFLAGS || ''} -mmacosx-version-min=${minimumOS}`,
      CGO_LDFLAGS: `${process.env.CGO_LDFLAGS || ''} -mmacosx-version-min=${minimumOS}`,
    },
  });
  rejectCrashConfig(app);
  run('codesign', ['--force', '--deep', '--sign', '-', app]);
  run('codesign', ['--verify', '--deep', '--strict', app]);
  const manifest = {
    kind: 'dsh-owned-cef-mac', version, app, executable, owner, helpers, sandbox: true,
    minimumOS, architecture: 'arm64',
  };
  const stagedManifest = join(publication, 'publication.json');
  const manifestPath = join(output, 'publication.json');
  writeFileSync(stagedManifest, `${JSON.stringify(manifest, null, 2)}\n`);
  renameSync(stagedManifest, manifestPath);
  published = true;
  console.log(`Formal Mac CEF publication: ${manifestPath}`);
} finally {
  if (!published) rmSync(publication, {recursive: true, force: true});
}
