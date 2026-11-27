import {execFileSync} from 'node:child_process';
import {mkdtempSync, readdirSync, readlinkSync, rmdirSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';

export function macPackageSigning() {
  const requested = process.env.DSH_WORK_MAC_SIGN_IDENTITY?.trim() || '';
  const notaryProfile = process.env.DSH_WORK_MAC_NOTARY_PROFILE?.trim() || '';
  if (!requested) {
    if (notaryProfile) throw Error('Mac notarization requires DSH_WORK_MAC_SIGN_IDENTITY');
    return {identity: '-', notaryProfile};
  }
  const identities = execFileSync('/usr/bin/security', ['find-identity', '-v', '-p', 'codesigning'], {encoding: 'utf8'});
  const matches = [...identities.matchAll(/^\s*\d+\) ([\dA-F]{40}) "(Developer ID Application: [^"]+)"\s*$/gim)]
    .filter(([, fingerprint, name]) => fingerprint === requested.toUpperCase() || name === requested);
  if (matches.length !== 1) {
    throw Error('DSH_WORK_MAC_SIGN_IDENTITY must match one valid Developer ID Application name or SHA-1 fingerprint');
  }
  if (notaryProfile) {
    execFileSync('/usr/bin/xcrun', ['--find', 'notarytool'], {stdio: 'pipe'});
    execFileSync('/usr/bin/xcrun', ['--find', 'stapler'], {stdio: 'pipe'});
  }
  return {identity: matches[0][1], notaryProfile};
}

function sign(run, identity, path, {executable = false, entitlements} = {}) {
  const arguments_ = ['--force', '--sign', identity];
  if (identity !== '-') {
    arguments_.push('--timestamp');
    if (executable) arguments_.push('--options', 'runtime');
    if (entitlements) arguments_.push('--entitlements', entitlements);
  }
  run('/usr/bin/codesign', [...arguments_, path]);
}

// Sign the staged component layout from the inside out. The CEF publication's
// relocatable manifest holds paths, so re-signing does not change its contents.
export function signMacApplication(run, app, {identity}, jitEntitlements) {
  const contents = join(app, 'Contents');
  const browser = join(contents, 'Frameworks', 'DSHWorkBrowser.app');
  const cefFrameworks = join(browser, 'Contents', 'Frameworks');
  const framework = join(cefFrameworks, 'Chromium Embedded Framework.framework');
  const libraries = join(framework, 'Versions', 'A', 'Libraries');
  for (const item of readdirSync(libraries, {withFileTypes: true})) {
    if (item.isFile() && item.name.endsWith('.dylib')) sign(run, identity, join(libraries, item.name));
  }
  sign(run, identity, framework);
  for (const item of readdirSync(cefFrameworks, {withFileTypes: true})) {
    if (!item.isDirectory() || !item.name.endsWith('.app')) continue;
    const usesJIT = item.name.endsWith(' (Renderer).app') || item.name.endsWith(' (GPU).app');
    sign(run, identity, join(cefFrameworks, item.name), {
      executable: true, entitlements: usesJIT ? jitEntitlements : undefined,
    });
  }
  sign(run, identity, join(browser, 'Contents', 'MacOS', 'dsh-cef-owner'), {executable: true});
  sign(run, identity, browser, {executable: true});
  for (const addon of ['workeripc-mac.node', 'dsh-preview-sandbox.node']) {
    sign(run, identity, join(contents, 'MacOS', addon));
  }
  sign(run, identity, join(contents, 'MacOS', 'dsh-work-cli'), {executable: true});
  sign(run, identity, app, {executable: true});
  run('/usr/bin/codesign', ['--verify', '--deep', '--strict', app]);
}

export function signMacDiskImage(run, app, image, {identity, notaryProfile}) {
  if (identity === '-') return;
  sign(run, identity, image);
  run('/usr/bin/codesign', ['--verify', '--strict', image]);
  if (!notaryProfile) return;

  const reply = run('/usr/bin/xcrun', [
    'notarytool', 'submit', image, '--keychain-profile', notaryProfile,
    '--wait', '--timeout', '30m', '--output-format', 'json',
  ], {capture: true});
  let result;
  try {
    result = JSON.parse(reply);
  } catch {
    throw Error('Invalid notarization response from notarytool');
  }
  const {id, status} = result ?? {};
  if (typeof id !== 'string' || !/^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(id) ||
      typeof status !== 'string' || !status) {
    throw Error('Incomplete notarization result from notarytool');
  }
  if (status !== 'Accepted') {
    throw Error(`Apple notarization did not accept this package (${status}; submission ${id})`);
  }
  // The disk-image submission covers the nested app. Staple the separately
  // published app too, so it can be validated outside the image.
  for (const path of [image, app]) {
    run('/usr/bin/xcrun', ['stapler', 'staple', path]);
    run('/usr/bin/xcrun', ['stapler', 'validate', path]);
  }
}

export function verifyMacDiskImage(run, image, appName, verifyApplication) {
  const mount = mkdtempSync(join(tmpdir(), 'dsh-work-package-check-'));
  let attached = false;
  try {
    run('/usr/bin/hdiutil', ['attach', image, '-readonly', '-nobrowse', '-mountpoint', mount]);
    attached = true;
    if (readlinkSync(join(mount, 'Applications')) !== '/Applications') {
      throw Error('The disk image Applications link must target /Applications');
    }
    const app = join(mount, appName);
    run('/usr/bin/codesign', ['--verify', '--deep', '--strict', app]);
    verifyApplication(app);
  } finally {
    if (attached) run('/usr/bin/hdiutil', ['detach', mount]);
    rmdirSync(mount);
  }
}
