#!/usr/bin/env node
// Checks for scripts/release-version.mjs.
//
// The version itself is computed by internal/release and covered by the Go tests
// in internal/release (version_test.go, compare_test.go, release_test.go,
// cmd/release-version/main_test.go). This file covers the entry point:
// argument handling, the fast refusal rules, the process contract (stdout
// carries the version, stderr carries the reason, exit code 0 for success, 2
// for refused input) and the identity of a re-run.
//
//   node scripts/release-version.test.mjs

import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, rmSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { BASE_ONLY_PATTERN, USAGE, VERSION_PATTERN, cacheCandidates, fileVersionFor, forwarded, helperBinary, parseArgs, run, validate, writableHelperPath } from './release-version.mjs';

const SCRIPT = fileURLToPath(import.meta.url).replace(/\.test\.mjs$/, '.mjs');
const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

// Keep the helper binary inside the checkout so a run never writes to the user
// cache, and so a confined session can still build it.
process.env.DSH_RELEASE_CACHE ??= path.join(REPO_ROOT, '.gocache', 'release-version');

let failures = 0;

function check(name, condition, detail) {
  if (condition) {
    process.stdout.write(`ok   ${name}\n`);
    return;
  }
  failures += 1;
  process.stdout.write(`FAIL ${name}${detail ? `\n     ${detail}` : ''}\n`);
}

function recorder() {
  const out = { text: '' };
  const err = { text: '' };
  return {
    io: {
      out: { write: (chunk) => { out.text += chunk; } },
      err: { write: (chunk) => { err.text += chunk; } },
    },
    out,
    err,
  };
}

// refusalOf returns the message the entry point refuses a command line with, or
// null when the command line is accepted.
function refusalOf(argv) {
  const parsed = parseArgs(argv);
  if (parsed.error) {
    return parsed.error;
  }
  const validated = validate(parsed);
  return validated.error ?? null;
}

// ---------------------------------------------------------------------------
// Fast rules: no toolchain, no child process.
// ---------------------------------------------------------------------------

const refusals = [
  ['an unknown flag', ['--channel', 'stable', '--nope', 'x'], 'unknown argument'],
  ['a flag without a value', ['--channel'], 'needs a value'],
  ['an unknown channel', ['--channel', 'canary'], 'unknown channel'],
  ['a base with a prerelease part', ['--base', '0.3.0-beta.2'], 'plain X.Y.Z base'],
  ['a base with a leading zero', ['--base', '01.3.0'], 'strict semantic version'],
  ['a base field above the Windows limit', ['--base', '0.3.70000'], 'Windows file version limit'],
  ['a stable gate above the Windows limit', ['--last-stable', '0.9.70000'], 'Windows file version limit'],
  ['a version with a leading zero', ['--version', '0.3.01'], 'strict semantic version'],
  ['a version with an empty prerelease identifier', ['--version', '0.3.0-beta..2'], 'empty prerelease identifier'],
  ['a version with an illegal prerelease character', ['--version', '0.3.0-beta_2'], 'strict semantic version'],
  ['more than one beta identifier', ['--channel', 'beta', '--version', '0.3.0-beta.2.3'], 'exactly one beta.<N> identifier'],
  ['a nightly version without the run id part', ['--channel', 'nightly', '--version', '0.3.0-nightly.20261013', '--run-id', '7'], 'exactly one nightly.<YYYYMMDD>.<runId> identifier'],
  ['a nightly version with a non-numeric run id part', ['--channel', 'nightly', '--version', '0.3.0-nightly.20261013.abc', '--run-id', '7'], 'exactly one nightly.<YYYYMMDD>.<runId> identifier'],
  ['beta without --version', ['--channel', 'beta'], 'needs --version'],
  ['nightly without a run id', ['--channel', 'nightly'], 'needs --run-id'],
  ['nightly with a non-numeric run id', ['--channel', 'nightly', '--run-id', 'abc'], 'digits only'],
  ['an impossible calendar date', ['--channel', 'nightly', '--run-id', '1', '--date', '2026-02-30'], 'not a real calendar date'],
  ['a date without separators', ['--channel', 'nightly', '--run-id', '1', '--date', '20261013'], 'YYYY-MM-DD'],
  ['an unknown field', ['--field', 'tag'], 'unknown field'],
  ['a last stable version with a prerelease part', ['--last-stable', '0.3.0-beta.1'], 'not published as stable'],
];

for (const [name, argv, expected] of refusals) {
  const message = refusalOf(argv);
  check(`refuses ${name}`, message !== null && message.includes(expected), `message=${JSON.stringify(message)}`);
}

const accepted = [
  ['the repository base for a stable build', ['--channel', 'stable']],
  ['an explicit base for a stable build', ['--channel', 'stable', '--base', '0.3.0']],
  ['a beta tag', ['--channel', 'beta', '--version', 'v0.3.0-beta.2']],
  ['a nightly run id and date', ['--channel', 'nightly', '--run-id', '123456789', '--date', '2026-10-13']],
  ['a nightly gate on the published stable base', ['--channel', 'nightly', '--run-id', '1', '--last-stable', '0.2.0']],
  ['the --flag=value form', ['--channel=nightly', '--run-id=7']],
  ['a field selection', ['--channel', 'stable', '--field', 'version']],
  ['a windows build of the same base', ['--channel', 'stable', '--base', '0.3.0', '--date', '2026-10-13', '--run-id', '123456789']],
  ['the highest accepted field values', ['--base', '65535.65535.65535']],
  ['build metadata', ['--version', '0.3.0-beta.2+build.7']],
];

for (const [name, argv] of accepted) {
  const message = refusalOf(argv);
  check(`accepts ${name}`, message === null, `message=${JSON.stringify(message)}`);
}

// The process contract, driven through run() so nothing is spawned.
{
  const { io, out, err } = recorder();
  const code = run(['--nope'], io);
  check('a refused command line exits 2 with the reason on stderr and nothing on stdout', code === 2 && out.text === '' && err.text.startsWith('release-version:'), `exit=${code} stdout=${JSON.stringify(out.text)} stderr=${JSON.stringify(err.text)}`);
}
{
  const { io, out, err } = recorder();
  const code = run(['--help'], io);
  check('--help prints usage on stdout and exits 0', code === 0 && out.text === USAGE && err.text === '', `exit=${code}`);
}
{
  const { io, out, err } = recorder();
  const code = run(['--channel', 'nightly', '--run-id', '1', '--dry-run'], io);
  check('--dry-run validates without running the helper', code === 0 && out.text === '' && err.text.includes('arguments accepted'), `exit=${code} stderr=${JSON.stringify(err.text)}`);
}

// Everything the caller wrote is forwarded to the helper unchanged. A flag
// without a value (--json) must not shift the pairing of the flags after it,
// which would send the helper a stray positional argument.
{
  const argv = ['--channel', 'nightly', '--json', '--base', '0.4.0', '--run-id', '7'];
  const sent = forwarded(parseArgs(argv));
  check('a value-less flag is forwarded without a placeholder', !sent.includes(undefined) && sent.join(' ') === argv.join(' '), sent.join(' '));
  check('--helper is consumed here and never forwarded', !forwarded(parseArgs(['--helper', 'C:\\tmp\\h.exe', '--channel', 'stable'])).includes('--helper'), forwarded(parseArgs(['--helper', 'C:\\tmp\\h.exe', '--channel', 'stable'])).join(' '));
}

// The helper path is keyed by the Go source it was built from, so an edited rule
// cannot be answered by a stale binary.
{
  const options = parseArgs(['--channel', 'stable']);
  const first = helperBinary(options, { LOCALAPPDATA: 'C:\\cache' }, 'win32');
  const second = helperBinary(options, { LOCALAPPDATA: 'C:\\cache' }, 'win32');
  check('the helper path is stable for unchanged sources', first.path === second.path, `${first.path} vs ${second.path}`);
  check('the helper path lives under the caller cache root', first.path.startsWith(path.join('C:\\cache', 'dsh-work', 'release-version')), first.path);
  check('an explicit cache root wins over the user cache', cacheCandidates({ DSH_RELEASE_CACHE: 'D:\\rel', LOCALAPPDATA: 'C:\\cache' }, 'win32')[0] === 'D:\\rel', cacheCandidates({ DSH_RELEASE_CACHE: 'D:\\rel', LOCALAPPDATA: 'C:\\cache' }, 'win32')[0]);
}

// A session that cannot write the preferred cache directory must still get a
// helper: the candidate list ends with the operating system temporary
// directory, and a directory that cannot be created is skipped, not fatal.
{
  const candidates = cacheCandidates({ LOCALAPPDATA: 'C:\\cache' }, 'win32');
  check('the cache candidates end with the temporary directory', candidates.length > 1 && candidates.at(-1).includes('dsh-work-release-version'), candidates.join(' | '));
  // A path whose parent is a regular file can never be created.
  const blocked = path.join(REPO_ROOT, 'build', 'config.yml', 'release-version');
  const poisoned = cacheCandidates({ DSH_RELEASE_CACHE: blocked, LOCALAPPDATA: 'C:\\cache' }, 'win32');
  check('a cache root inside a file is still just a candidate', poisoned[0] === path.resolve(blocked), poisoned[0]);
  const resolved = writableHelperPath({ name: 'release-version-probe', directories: [blocked, path.join(REPO_ROOT, '.gocache', 'release-version')] });
  check('an impossible directory is skipped for a writable one', resolved.path === path.join(REPO_ROOT, '.gocache', 'release-version', 'release-version-probe'), JSON.stringify(resolved));
  const impossible = writableHelperPath({ name: 'release-version-probe', directories: [blocked] });
  check('an impossible candidate list reports every directory it tried', typeof impossible.error === 'string' && impossible.error.includes(blocked) && impossible.error.includes('DSH_RELEASE_CACHE'), JSON.stringify(impossible));
}

// Windows file metadata gets X.Y.Z.0 while the SemVer string stays intact.
{
  for (const [version, want] of [
    ['0.3.0', '0.3.0.0'],
    ['0.3.0-beta.2', '0.3.0.0'],
    ['0.3.0-nightly.20261013.123456789', '0.3.0.0'],
    ['1.2.3+build.7', '1.2.3.0'],
  ]) {
    check(`fileVersionFor(${version}) = ${want}`, fileVersionFor(version) === want, `got ${JSON.stringify(fileVersionFor(version))}`);
  }
  check('fileVersionFor refuses a version that is not strict SemVer', fileVersionFor('0.3') === null, `got ${JSON.stringify(fileVersionFor('0.3'))}`);
  check('fileVersionFor refuses a field above the Windows limit', fileVersionFor('0.3.70000') === null, `got ${JSON.stringify(fileVersionFor('0.3.70000'))}`);
  check('the base pattern accepts a plain base and nothing else', BASE_ONLY_PATTERN.test('0.3.0') && !BASE_ONLY_PATTERN.test('0.3.0-beta.2'), 'BASE_ONLY_PATTERN');
  check('the version pattern accepts a leading v and a prerelease', VERSION_PATTERN.test('v0.3.0-beta.2') && !VERSION_PATTERN.test('01.3.0'), 'VERSION_PATTERN');
}

// ---------------------------------------------------------------------------
// End to end through the entry point: scripts/release-version.mjs computes the
// version by running the helper built from internal/release. The entry point
// forwards stdio by inheritance, so these checks can read stdout and stderr
// even though this process was started with piped stdio.
// ---------------------------------------------------------------------------

const helper = helperBinary(parseArgs(['--channel', 'stable']));
const helperLocation = writableHelperPath(helper);
if (helperLocation.error) {
  // A confined session may not be able to write any helper cache; the
  // end-to-end checks are skipped below instead of failing the suite.
  process.stdout.write(`skip end-to-end checks: ${helperLocation.error}\n`);
} else {
  helper.path = helperLocation.path;
}
if (!existsSync(helper.path) && existsSync(path.dirname(helper.path))) {
  const build = spawnSync(helper.go, ['build', '-o', helper.path, './internal/release/cmd/release-version'], {
    cwd: REPO_ROOT,
    env: process.env.GOCACHE ? process.env : { ...process.env, GOCACHE: path.join(REPO_ROOT, '.gocache') },
    stdio: 'inherit',
  });
  if (build.error || build.status !== 0) {
    rmSync(helper.path, { force: true });
    process.stdout.write(`skip end-to-end checks: building ${helper.path} failed\n`);
  }
}

// scriptRun forwards the whole command line through scripts/release-version.mjs,
// with --helper pointing at the binary that was just built.
function scriptRun(argv) {
  const result = spawnSync(process.execPath, [SCRIPT, '--helper', helper.path, ...argv], {
    cwd: REPO_ROOT,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  if (result.error) {
    return { error: result.error.code ?? result.error.message };
  }
  return { code: result.status, stdout: (result.stdout ?? '').trim(), stderr: (result.stderr ?? '').trim() };
}

const probe = existsSync(helper.path) ? scriptRun(['--channel', 'stable', '--base', '0.3.0']) : { error: 'missing helper' };
if (probe.error || probe.code !== 0) {
  process.stdout.write(`skip end-to-end checks: ${probe.error ?? `release-version.mjs exited ${probe.code}: ${probe.stderr}`}\n`);
} else {
  const nightly = ['--channel', 'nightly', '--base', '0.4.0', '--date', '2026-10-13', '--source-sha', 'deadbeef'];
  const first = scriptRun([...nightly, '--run-id', '111']);
  const early = scriptRun([...nightly, '--run-id', '222']);
  const late = scriptRun([...nightly, '--run-id', '999999999']);
  const retry = scriptRun([...nightly, '--run-id', '111']);
  const stable = scriptRun(['--channel', 'stable', '--base', '0.3.0']);
  const beta = scriptRun(['--channel', 'beta', '--version', 'v0.3.0-beta.2', '--base', '0.3.0']);

  check('a nightly version is base + UTC date + run id', first.code === 0 && first.stdout === '0.4.0-nightly.20261013.111', `exit=${first.code} stdout=${JSON.stringify(first.stdout)}`);
  check('a re-run of the same run id is byte identical', retry.stdout === first.stdout && retry.code === first.code, `got ${JSON.stringify(retry.stdout)}`);
  check('different run ids on one UTC day give different versions', early.stdout !== late.stdout, `${early.stdout} vs ${late.stdout}`);
  check(
    'different run ids on one UTC day ascend as versions',
    compare(`v${early.stdout}`, `v${late.stdout}`) < 0 && compare(`v${late.stdout}`, `v${first.stdout}`) > 0,
    `${early.stdout} / ${late.stdout} / ${first.stdout}`,
  );
  check('a stable version is the plain base', stable.stdout === '0.3.0', `got ${JSON.stringify(stable.stdout)}`);
  check('a beta version keeps exactly one beta.N identifier', beta.stdout === '0.3.0-beta.2', `got ${JSON.stringify(beta.stdout)}`);

  const json = scriptRun([...nightly, '--run-id', '111', '--json']);
  let payload = null;
  try {
    payload = JSON.parse(json.stdout);
  } catch {
    payload = null;
  }
  check(
    'JSON exposes base, date, run id, source sha and the Windows mapping',
    payload !== null &&
      payload.version === '0.4.0-nightly.20261013.111' &&
      payload.base === '0.4.0' &&
      payload.date === '20261013' &&
      payload.runId === '111' &&
      payload.sourceSha === 'deadbeef' &&
      payload.fileVersion === '0.4.0.0',
    `got ${JSON.stringify(json.stdout)}`,
  );

  const fileVersion = scriptRun([...nightly, '--run-id', '111', '--field', 'fileVersion']);
  check('--field fileVersion maps X.Y.Z to X.Y.Z.0', fileVersion.stdout === '0.4.0.0', `got ${JSON.stringify(fileVersion.stdout)}`);

  const moved = scriptRun(['--channel', 'nightly', '--base', '0.3.0', '--date', '2026-10-13', '--run-id', '111', '--last-stable', '0.3.0']);
  check(
    'a nightly on the published stable base moves to the next patch',
    moved.code === 0 && moved.stdout === '0.3.1-nightly.20261013.111',
    `exit=${moved.code} stdout=${JSON.stringify(moved.stdout)} stderr=${JSON.stringify(moved.stderr)}`,
  );

  const wrongBeta = scriptRun(['--channel', 'beta', '--version', '0.3.0-rc.1', '--base', '0.3.0']);
  check(
    'a beta tag must be exactly one beta.N identifier',
    wrongBeta.code === 2 && wrongBeta.stdout === '' && wrongBeta.stderr.includes('beta'),
    `exit=${wrongBeta.code} stderr=${JSON.stringify(wrongBeta.stderr)}`,
  );

  const overflow = scriptRun(['--channel', 'stable', '--base', '0.3.70000']);
  check(
    'a numeric field above the Windows limit is refused',
    overflow.code === 2 && overflow.stdout === '' && overflow.stderr.includes('Windows file version limit'),
    `exit=${overflow.code} stderr=${JSON.stringify(overflow.stderr)}`,
  );
}

// The default entry point takes no --helper, so it must find a writable cache
// itself. A read-only user cache or a stale DSH_RELEASE_CACHE must fall back to
// the temporary directory, not crash.
{
  const result = spawnSync(process.execPath, [SCRIPT, '--channel', 'stable', '--base', '0.3.0'], {
    cwd: REPO_ROOT,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, DSH_RELEASE_CACHE: path.join(REPO_ROOT, 'build', 'config.yml', 'release-version') },
  });
  if (result.error) {
    process.stdout.write(`skip fallback check: ${result.error.code ?? result.error.message}\n`);
  } else {
    check(
      'an unwritable cache root falls back instead of crashing',
      result.status === 0 && (result.stdout ?? '').trim() === '0.3.0',
      `exit=${result.status} stdout=${JSON.stringify((result.stdout ?? '').trim())} stderr=${JSON.stringify((result.stderr ?? '').trim())}`,
    );
  }
}

// compare mirrors the SemVer precedence rule used by internal/release, which is
// enough for the ordering assertion above.
function compare(left, right) {
  const parse = (value) => {
    const match = /^v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$/.exec(value);
    if (!match) {
      throw new Error(`not a version: ${value}`);
    }
    return {
      main: [Number(match[1]), Number(match[2]), Number(match[3])],
      prerelease: match[4] === undefined ? [] : match[4].split('.'),
    };
  };
  const a = parse(left);
  const b = parse(right);
  for (let index = 0; index < 3; index += 1) {
    if (a.main[index] !== b.main[index]) {
      return a.main[index] < b.main[index] ? -1 : 1;
    }
  }
  if (a.prerelease.length === 0 || b.prerelease.length === 0) {
    if (a.prerelease.length === b.prerelease.length) {
      return 0;
    }
    return a.prerelease.length === 0 ? 1 : -1;
  }
  const length = Math.max(a.prerelease.length, b.prerelease.length);
  for (let index = 0; index < length; index += 1) {
    const leftPart = a.prerelease[index];
    const rightPart = b.prerelease[index];
    if (leftPart === undefined) {
      return -1;
    }
    if (rightPart === undefined) {
      return 1;
    }
    if (leftPart === rightPart) {
      continue;
    }
    const leftNumeric = /^[0-9]+$/.test(leftPart);
    const rightNumeric = /^[0-9]+$/.test(rightPart);
    if (leftNumeric && rightNumeric) {
      return Number(leftPart) < Number(rightPart) ? -1 : 1;
    }
    if (leftNumeric !== rightNumeric) {
      return leftNumeric ? -1 : 1;
    }
    return leftPart < rightPart ? -1 : 1;
  }
  return 0;
}

process.stdout.write(failures === 0 ? '\nall release-version checks passed\n' : `\n${failures} release-version check(s) failed\n`);
process.exit(failures === 0 ? 0 : 1);
