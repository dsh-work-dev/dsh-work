// Current-user, randomly named LaunchAgent experiment. No global installation,
// private coalition syscall, sudo, or numeric-PID signal is used for cleanup.
import {execFileSync, spawnSync} from 'node:child_process';
import {existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync} from 'node:fs';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomBytes} from 'node:crypto';

if (process.platform !== 'darwin' || process.arch !== 'arm64') throw Error('Requires native macOS arm64');
if (process.argv.length !== 4 || process.argv[2] !== '--output-dir') throw Error('Expected --output-dir DIR');
const output = resolve(process.argv[3]); mkdirSync(output, {recursive: true});
const root = fileURLToPath(new URL('../', import.meta.url)), executable = join(output, 'launchd-tree');
execFileSync('clang', ['-std=c11', '-Wall', '-Wextra', '-Werror', join(root, 'tools/posix-ipc-probe/launchd_tree_darwin.c'), '-o', executable], {stdio: 'inherit'});
const domain = `gui/${process.getuid()}`;
execFileSync('launchctl', ['print', domain], {stdio: 'ignore'}); // Unavailable domain is a real failed prerequisite.
const pause = ms => new Promise(resolvePause => setTimeout(resolvePause, ms));
const escape = text => text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
const roles = ['root', 'same-group', 'escaped'];
function run(args, file) {
  const result = spawnSync('launchctl', args, {encoding: 'utf8', timeout: 5000});
  writeFileSync(file, JSON.stringify({args, status: result.status, error: result.error?.message, stdout: result.stdout, stderr: result.stderr}, null, 2));
  return result;
}
function inspect(dir) {
  return roles.filter(role => existsSync(join(dir, `${role}.identity`))).map(role => ({role, ...JSON.parse(execFileSync(executable, ['--inspect', join(dir, `${role}.identity`)], {encoding: 'utf8'})), heartbeats: existsSync(join(dir, `${role}.heartbeats`)) ? readFileSync(join(dir, `${role}.heartbeats`), 'utf8').trim().split('\n').filter(Boolean).length : 0}));
}
const summary = [];
for (const mode of ['root-exit', 'root-crash', 'bootout']) {
  const dir = mkdtempSync(join(output, `${mode}-`));
  const label = `dev.dsh.treeprobe.${process.pid}.${randomBytes(6).toString('hex')}`;
  const service = `${domain}/${label}`, plist = join(dir, 'probe.plist');
  writeFileSync(plist, `<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>${label}</string><key>ProgramArguments</key><array>${[executable, dir, mode].map(value => `<string>${escape(value)}</string>`).join('')}</array><key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>AbandonProcessGroup</key><false/><key>ExitTimeOut</key><integer>2</integer><key>StandardOutPath</key><string>${escape(join(dir, 'stdout.log'))}</string><key>StandardErrorPath</key><string>${escape(join(dir, 'stderr.log'))}</string></dict></plist>`);
  let loaded = false;
  try {
    loaded = true; // Also boot out a partially accepted bootstrap on failure.
    const bootstrap = run(['bootstrap', domain, plist], join(dir, 'bootstrap.json'));
    if (bootstrap.status !== 0) throw Error(`bootstrap failed: ${bootstrap.stderr}`);
    for (let i = 0; i < 50 && !existsSync(join(dir, 'ready')); i++) await pause(100);
    if (!existsSync(join(dir, 'ready'))) throw Error(`job never reached readiness: ${dir}`);
    const before = inspect(dir); if (before.length !== 3 || before.some(item => item.state !== 'live')) throw Error('All three original process instances must initially be live');
    const rootInstance = before.find(item => item.role === 'root');
    if (before.find(item => item.role === 'same-group').group_at_start !== rootInstance.group_at_start || before.find(item => item.role === 'escaped').group_at_start === rootInstance.group_at_start) throw Error('Process-group control and setsid escape must be real');
    run(['print', service], join(dir, 'job-before.json'));
    for (const item of before) run(['procinfo', String(item.pid)], join(dir, `${item.role}-procinfo.json`));
    writeFileSync(join(dir, 'trigger'), 'go\n');
    if (mode === 'bootout') {
      const stopped = run(['bootout', service], join(dir, 'bootout.json'));
      if (stopped.status !== 0) throw Error(`bootout failed: ${stopped.stderr}`);
      loaded = false;
    }
    await pause(2000);
    const after = inspect(dir);
    if (after.find(item => item.role === 'root').state === 'live') throw Error('Root is still live; cannot interpret descendant cleanup yet');
    run(['print', service], join(dir, 'job-after.json'));
    const escapedStillLive = after.some(item => item.role === 'escaped' && item.state === 'live');
    const finding = {mode, service, dir, before, after, escapedStillLive, wholeTreeClaim: escapedStillLive ? 'REFUTED' : 'NOT_REFUTED_BY_THIS_PROBE'};
    summary.push(finding); writeFileSync(join(dir, 'observation.json'), JSON.stringify(finding, null, 2));
    console.log(JSON.stringify(finding));
  } finally {
    writeFileSync(join(dir, 'cleanup'), 'probe-owned cooperative cleanup\n');
    if (loaded) run(['bootout', service], join(dir, 'cleanup-bootout.json'));
    let remaining = inspect(dir);
    for (let i = 0; i < 60 && remaining.some(item => !['gone', 'reused'].includes(item.state)); i++) { await pause(100); remaining = inspect(dir); }
    writeFileSync(join(dir, 'cleanup-instances.json'), JSON.stringify(remaining, null, 2));
    if (remaining.some(item => !['gone', 'reused'].includes(item.state))) throw Error(`Probe instances not reaped: ${dir}`);
  }
}
writeFileSync(join(output, 'summary.json'), JSON.stringify(summary, null, 2));
console.log('Experiment completed; observations, not Adapter acceptance. Escaped helpers were cleaned cooperatively after measurement.');
