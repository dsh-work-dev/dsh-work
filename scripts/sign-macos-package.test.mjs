import assert from 'node:assert/strict';
import {existsSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, unlinkSync, writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {basename, join} from 'node:path';
import test from 'node:test';

import {signMacApplication, signMacDiskImage, verifyMacDiskImage} from './sign-macos-package.mjs';

test('Developer ID signs nested code before its containers with per-role entitlements', t => {
  const root = mkdtempSync(join(tmpdir(), 'dsh-mac-signing-'));
  t.after(() => rmSync(root, {recursive: true, force: true}));
  const app = join(root, 'DSH Work.app');
  const image = join(root, 'DSH Work.dmg');
  const frameworks = join(app, 'Contents/Frameworks/DSHWorkBrowser.app/Contents/Frameworks');
  const frameworkName = 'Chromium Embedded Framework.framework';
  const libraries = join(frameworks, frameworkName, 'Versions/A/Libraries');
  mkdirSync(libraries, {recursive: true});
  writeFileSync(join(libraries, 'libcef_sandbox.dylib'), '');
  writeFileSync(join(libraries, 'vk_swiftshader_icd.json'), '{}');
  const helpers = ['DSHWorkBrowser Helper.app', 'DSHWorkBrowser Helper (GPU).app', 'DSHWorkBrowser Helper (Renderer).app'];
  for (const helper of helpers) mkdirSync(join(frameworks, helper));

  const commands = [];
  const record = (command, args) => {
    assert.equal(command, '/usr/bin/codesign');
    commands.push(args);
  };
  const profile = {identity: 'AB'.repeat(20), notaryProfile: ''};
  signMacApplication(record, app, profile, 'cef-helper-jit.plist');
  signMacDiskImage(record, app, image, profile);
  const signing = commands.filter(args => args.includes('--sign'));
  const names = signing.map(args => basename(args.at(-1)));
  assert.deepEqual([...names].sort(), [
    'libcef_sandbox.dylib', frameworkName, ...helpers,
    'dsh-cef-owner', 'DSHWorkBrowser.app', 'workeripc-mac.node', 'dsh-preview-sandbox.node',
    'dsh-work-cli', 'DSH Work.app', 'DSH Work.dmg',
  ].sort());
  const before = (item, container) => assert(names.indexOf(item) < names.indexOf(container));
  before('libcef_sandbox.dylib', frameworkName);
  for (const part of [frameworkName, ...helpers, 'dsh-cef-owner']) before(part, 'DSHWorkBrowser.app');
  for (const part of ['DSHWorkBrowser.app', 'workeripc-mac.node', 'dsh-preview-sandbox.node', 'dsh-work-cli']) {
    before(part, 'DSH Work.app');
  }
  before('DSH Work.app', 'DSH Work.dmg');
  for (const args of signing) {
    assert(args.includes(profile.identity) && args.includes('--timestamp'));
    assert(!args.includes('--deep'));
  }
  assert.deepEqual(
    signing.filter(args => args.includes('--entitlements')).map(args => basename(args.at(-1))).sort(),
    helpers.slice(1),
  );
  assert.deepEqual(
    signing.filter(args => args.includes('--options')).map(args => basename(args.at(-1))).sort(),
    [...helpers, 'dsh-cef-owner', 'DSHWorkBrowser.app', 'dsh-work-cli', 'DSH Work.app'].sort(),
  );
  assert.deepEqual(commands.filter(args => args.includes('--verify')), [
    ['--verify', '--deep', '--strict', app],
    ['--verify', '--strict', image],
  ]);
});

test('verifies the mounted app and ejects the image on success or verification failure', () => {
  for (const failure of ['', 'link', 'cli', 'attach']) {
    const commands = [];
    let mount;
    const image = '/package/DSH Work.dmg';
    const appName = 'DSH Work.app';
    const run = (command, args) => {
      commands.push([command, args]);
      if (args[0] === 'attach') {
        mount = args.at(-1);
        if (failure === 'attach') throw Error('attach failed');
        symlinkSync(failure === 'link' ? '/unexpected' : '/Applications', join(mount, 'Applications'));
      }
      if (args[0] === 'detach') unlinkSync(join(mount, 'Applications'));
    };
    const verify = () => verifyMacDiskImage(run, image, appName, app => {
      assert.equal(app, join(mount, appName));
      if (failure === 'cli') throw Error('cli failed');
    });
    if (failure) assert.throws(verify, new RegExp(failure));
    else verify();
    assert.deepEqual(commands[0], [
      '/usr/bin/hdiutil', ['attach', image, '-readonly', '-nobrowse', '-mountpoint', mount],
    ]);
    if (failure !== 'attach') {
      assert.deepEqual(commands.at(-1), ['/usr/bin/hdiutil', ['detach', mount]]);
    }
    if (failure === 'link' || failure === 'attach') {
      assert.equal(commands.some(([command]) => command === '/usr/bin/codesign'), false);
    } else {
      assert.deepEqual(commands[1], ['/usr/bin/codesign', ['--verify', '--deep', '--strict', join(mount, appName)]]);
    }
    assert.equal(existsSync(mount), false);
  }
});

test('notarization requires a complete accepted submission before stapling', () => {
  const id = '2efe2717-52ef-43a5-96dc-0797e4ca1041';
  const profile = {identity: 'AB'.repeat(20), notaryProfile: 'notary-test-profile'};
  const image = '/package/DSH Work.dmg';
  const app = '/package/DSH Work.app';
  const submit = [
    '/usr/bin/xcrun', [
      'notarytool', 'submit', image, '--keychain-profile', profile.notaryProfile,
      '--wait', '--timeout', '30m', '--output-format', 'json',
    ], {capture: true},
  ];
  const staple = (action, path) => ['/usr/bin/xcrun', ['stapler', action, path], undefined];
  const plans = [
    {output: JSON.stringify({status: 'Accepted', id}), expected: [
      staple('staple', image), staple('validate', image), staple('staple', app), staple('validate', app),
    ]},
    {output: JSON.stringify({status: 'Invalid', id}), error: /Invalid.*submission/},
    {output: JSON.stringify({status: 'In Progress', id}), error: /In Progress.*submission/},
    {output: JSON.stringify({status: 'Accepted'}), error: /incomplete notarization result/i},
    {output: JSON.stringify({status: 'Accepted', id: 'invalid-id'}), error: /incomplete notarization result/i},
    {output: 'not JSON', error: /invalid notarization response/i},
    {output: 'null', error: /incomplete notarization result/i},
  ];
  for (const {output, expected = [], error} of plans) {
    const commands = [];
    const run = (command, args, options) => {
      commands.push([command, args, options]);
      if (args[0] === 'notarytool') return output;
    };
    const verify = () => signMacDiskImage(run, app, image, profile);
    if (error) assert.throws(verify, error);
    else verify();
    assert.deepEqual(commands.slice(2), [submit, ...expected]);
  }

  for (const stage of ['submit', 'validate']) {
    const commands = [];
    assert.throws(() => signMacDiskImage((command, args, options) => {
      commands.push([command, args, options]);
      if (args[1] === stage) throw Error(stage + ' failed');
      if (args[0] === 'notarytool') return JSON.stringify({status: 'Accepted', id});
    }, app, image, profile), new RegExp(stage + ' failed'));
    assert.deepEqual(commands.slice(2), stage === 'submit' ? [submit] : [
      submit, staple('staple', image), staple('validate', image),
    ]);
  }
});
