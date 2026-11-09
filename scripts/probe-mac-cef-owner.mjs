// Actual fixed CEF bundle diagnostic, not a general Supervisor Adapter.
import {execFileSync, spawn, spawnSync} from 'node:child_process';
import {appendFileSync, closeSync, existsSync, fstatSync, mkdirSync, mkdtempSync, openSync, readFileSync, statSync, writeFileSync} from 'node:fs';
import {createServer} from 'node:http';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomBytes} from 'node:crypto';

if (process.platform !== 'darwin' || process.arch !== 'arm64') throw Error('Requires native macOS arm64');
const script = fileURLToPath(import.meta.url);
if (process.argv[2] === '--lease-holder' && process.argv.length === 4) {
  const fd = openSync(process.argv[3], 'w'); writeFileSync(fd, 'R');
  const stat = fstatSync(fd);
  writeFileSync(`${process.argv[3]}-writer.json`, JSON.stringify({pid: process.pid, ppid: process.ppid, fd, device: stat.dev, inode: stat.ino, fifo: stat.isFIFO(), childrenSpawned: false}));
  const keepalive = setInterval(() => {}, 1000);
  setTimeout(() => { clearInterval(keepalive); closeSync(fd); }, 90000);
} else {
  const options = new Map();
  for (let i = 2; i < process.argv.length; i += 2) {
    if (!['--manifest', '--output-dir'].includes(process.argv[i]) || !process.argv[i + 1]) throw Error('Expected --manifest FILE --output-dir DIR');
    options.set(process.argv[i], resolve(process.argv[i + 1]));
  }
  if (!options.has('--manifest') || !options.has('--output-dir')) throw Error('Expected --manifest FILE --output-dir DIR');
  const manifest = JSON.parse(readFileSync(options.get('--manifest'), 'utf8'));
  if (manifest.kind !== 'cef-specific-owner-diagnostic' || manifest.version !== '156.0.15+gcf671c0+chromium-156.0.8078.80' || manifest.sandbox !== true || manifest.crashConfigs.length) throw Error('Unexpected diagnostic bundle identity');
  const output = options.get('--output-dir'); mkdirSync(output, {recursive: true});
  const domain = `gui/${process.getuid()}`;
  execFileSync('launchctl', ['print', domain], {stdio: 'ignore'});
  const pause = ms => new Promise(done => setTimeout(done, ms));
  const xml = value => value.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
  const page = `<!doctype html><meta charset="utf-8"><title>Owned CEF diagnostic</title><canvas id="surface"></canvas><script>
    const canvas=document.getElementById('surface'); const gl=canvas.getContext('webgl'); if(gl){gl.clearColor(.1,.4,.7,1);gl.clear(gl.COLOR_BUFFER_BIT);}
    fetch('/ping').then(()=>document.title='network-ready');
    const worker=new Worker(URL.createObjectURL(new Blob(['onmessage=()=>postMessage("ready")'],{type:'text/javascript'})));worker.postMessage('start');
  </script>`;
  const server = createServer((req, res) => { res.writeHead(200, {'content-type': req.url.startsWith('/page') ? 'text/html' : 'text/plain'}); res.end(req.url.startsWith('/page') ? page : 'ok'); });
  await new Promise((done, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', done); });
  const pageURL = `http://127.0.0.1:${server.address().port}/page`;
  const summary = [];
  const run = (args, file) => {
    const result = spawnSync('launchctl', args, {encoding: 'utf8', timeout: 10000});
    writeFileSync(file, JSON.stringify({args, status: result.status, error: result.error?.message, stdout: result.stdout, stderr: result.stderr}, null, 2));
    return result;
  };
  function events(dir) {
    const file = join(dir, 'stdout.log'); if (!existsSync(file)) return [];
    if (statSync(file).size > 8 * 1024 * 1024) throw Error('Diagnostic stdout exceeded bound');
    return readFileSync(file, 'utf8').split('\n').filter(line => line.startsWith('{')).flatMap(line => { try { return [JSON.parse(line)]; } catch { return []; } });
  }
  const group = pgid => JSON.parse(execFileSync(manifest.inspector, ['--group', String(pgid)], {encoding: 'utf8'}));
  const inspect = identity => JSON.parse(execFileSync(manifest.inspector, ['--identity', String(identity.pid), String(identity.started)], {encoding: 'utf8'}));
  const entries = dir => events(dir).filter(event => ['broker-entry', 'helper-entry'].includes(event.event));
  const instanceStates = dir => entries(dir).map(entry => ({original: entry.identity, ...inspect(entry.identity)}));
  function censusMembers(census) {
    if (census.error || census.truncated || !Array.isArray(census.members) || census.members.some(member => !['live', 'zombie', 'gone'].includes(member.state))) throw Error('Kernel group census is unavailable/unknown');
    // Preserve the raw snapshot. ESRCH after the PID enumeration confirms that
    // the sampled slot no longer names a member; it is not a live renderer.
    return census.members.filter(member => member.state !== 'gone');
  }
  function measureBarrier(dir, rootIdentity, helpersOnly) {
    const instances = instanceStates(dir);
    if (instances.some(instance => instance.state === 'unknown')) {
      writeFileSync(join(dir, 'measurement-error.json'), JSON.stringify({error: 'process identity observation unavailable', instances}, null, 2));
      throw Error('Process identity observation failed; unknown is not gone');
    }
    const census = group(rootIdentity.pgid);
    let members;
    try { members = censusMembers(census); }
    catch (error) {
      writeFileSync(join(dir, 'measurement-error.json'), JSON.stringify({observedAt: new Date().toISOString(), error: error.message, instances, group: census}, null, 2));
      throw error;
    }
    const originalRoot = instances.find(instance => instance.original.pid === rootIdentity.pid && instance.original.started === rootIdentity.started);
    if (helpersOnly && originalRoot?.state !== 'live') throw Error('Broker exited before the helper/reap barrier; launchd fallback must not complete normal shutdown');
    const targets = helpersOnly ? instances.filter(instance => instance !== originalRoot) : instances;
    const groupReady = helpersOnly
      ? members.length === 1 && members[0].pid === rootIdentity.pid && members[0].started === rootIdentity.started && members[0].state === 'live'
      : members.length === 0;
    const sample = {observedAt: new Date().toISOString(), controllerMonotonicNs: process.hrtime.bigint().toString(), stage: helpersOnly ? 'helpers-before-root-release' : 'all-instances-after-root-exit', instances, group: census, complete: targets.every(instance => ['gone', 'reused'].includes(instance.state)) && groupReady};
    appendFileSync(join(dir, 'settlement-samples.log'), `${JSON.stringify(sample)}\n`);
    return sample;
  }
  async function waitFor(test, duration, message) {
    const end = Date.now() + duration;
    do { if (test()) return; await pause(100); } while (Date.now() < end);
    throw Error(message);
  }
  try {
    for (const mode of ['normal-close', 'broker-crash', 'owner-death']) {
      const dir = mkdtempSync(join(output, `${mode}-`)), lease = join(dir, 'lease');
      const phase = (stage, detail = {}) => appendFileSync(join(dir, 'phases.log'), `${JSON.stringify({stage, observedAt: new Date().toISOString(), controllerMonotonicNs: process.hrtime.bigint().toString(), ...detail})}\n`);
      const leaseHolders = stage => {
        // Evidence only: never authorize cleanup from a numeric PID in lsof.
        const result = spawnSync('/usr/sbin/lsof', ['-nP', '-Fpcfatn', lease], {encoding: 'utf8', timeout: 5000});
        writeFileSync(join(dir, `lease-holders-${stage}.json`), JSON.stringify({observedAt: new Date().toISOString(), status: result.status, error: result.error?.message, stdout: result.stdout, stderr: result.stderr}, null, 2));
      };
      execFileSync('mkfifo', ['-m', '600', lease]);
      const label = `dev.dsh.cefowner.${process.pid}.${randomBytes(6).toString('hex')}`, service = `${domain}/${label}`;
      const argv = [manifest.executable, `--probe-directory=${dir}`, `--probe-url=${pageURL}`];
      const plist = join(dir, 'probe.plist');
      writeFileSync(plist, `<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>${label}</string><key>ProgramArguments</key><array>${argv.map(value => `<string>${xml(value)}</string>`).join('')}</array><key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>AbandonProcessGroup</key><false/><key>ExitTimeOut</key><integer>3</integer><key>StandardOutPath</key><string>${xml(join(dir, 'stdout.log'))}</string><key>StandardErrorPath</key><string>${xml(join(dir, 'stderr.log'))}</string></dict></plist>`);
      const owner = spawn(process.execPath, [script, '--lease-holder', lease], {stdio: 'ignore'});
      const ownerExit = new Promise((done, reject) => { owner.once('error', reject); owner.once('exit', (code, signal) => done({code, signal})); });
      let loaded = true, killedOwner = false;
      try {
        const boot = run(['bootstrap', domain, plist], join(dir, 'bootstrap.json'));
        if (boot.status !== 0) throw Error(`LaunchAgent bootstrap failed: ${boot.stderr}`);
        await waitFor(() => {
          const log = events(dir);
          if (log.some(event => ['helper-sandbox-failed', 'helper-framework-failed', 'lease-open-failed', 'create-failed', 'self-audit-failed', 'load-error'].includes(event.event))) throw Error('Actual component startup failed; see stdout.log');
          return log.some(event => event.event === 'initialized') && log.some(event => event.event === 'lease-armed') && new Set(log.filter(event => event.event === 'loaded' && event.status === 200).map(event => event.index)).size === 2;
        }, 30000, 'Actual sandbox CEF pages/lease did not become ready');
        await pause(1000);
        const initialEvents = events(dir), initialized = initialEvents.find(event => event.event === 'initialized');
        if (initialized.crash_reporting !== false || initialized.version !== manifest.version) throw Error('Runtime crash reporting/version differs from fixed candidate');
        const rootEntry = initialEvents.find(event => event.event === 'broker-entry');
        if (!rootEntry?.identity.started || rootEntry.identity.pgid !== rootEntry.identity.pid) throw Error('Job broker must be its actual process-group leader');
        const helpers = initialEvents.filter(event => event.event === 'helper-entry');
        if (!helpers.some(event => event.type === 'renderer') || !helpers.some(event => event.type === 'gpu-process') || !helpers.some(event => event.type === 'utility' && event.utility.includes('NetworkService'))) throw Error('Workload did not exercise renderer/GPU/network helper inventory');
        if (helpers.some(event => !event.identity.started || event.identity.pgid !== rootEntry.identity.pgid)) throw Error('A real helper is outside the proposed owned PGID');
        const before = group(rootEntry.identity.pgid);
        if (before.error || before.truncated || before.members.some(member => !member.started)) throw Error('Kernel group inventory unavailable/incomplete');
        const seen = new Set(entries(dir).map(event => `${event.identity.pid}:${event.identity.started}`));
        if (before.members.some(member => !seen.has(`${member.pid}:${member.started}`))) throw Error('Kernel group contains an instance without a helper-entry diagnostic');
        writeFileSync(join(dir, 'inventory-before.json'), JSON.stringify({helpers, before, launchIntents: initialEvents.filter(event => event.event === 'launch-intent')}, null, 2));
        phase('inventory-complete', {root: rootEntry.identity, group: before});
        run(['print', service], join(dir, 'job-before.json'));
        for (const entry of entries(dir)) run(['procinfo', String(entry.identity.pid)], join(dir, `procinfo-${entry.identity.pid}.json`));
        if (mode === 'normal-close') writeFileSync(join(dir, 'close'), 'normal close\n');
        else if (mode === 'broker-crash') writeFileSync(join(dir, 'crash'), 'SIGKILL inside owned broker\n');
        else { leaseHolders('before-kill'); killedOwner = true; if (!owner.kill('SIGKILL')) throw Error('Could not kill the actual independent lease owner'); }
        phase('transition-requested', {mode});
        if (killedOwner) {
          const exited = await ownerExit; phase('lease-owner-exited', exited);
          if (exited.signal !== 'SIGKILL') throw Error('Lease owner did not really die by SIGKILL');
          leaseHolders('after-kill');
        }
        let helperBarrier, shutdown;
        if (mode !== 'broker-crash') {
          await waitFor(() => {
            const log = events(dir);
            if (log.some(event => ['reap-error', 'root-release-timeout', 'lease-read-failed', 'lease-poll-failed'].includes(event.event))) throw Error('Broker lease/reap/hold failed');
            shutdown = log.find(event => event.event === 'shutdown-return');
            return Boolean(shutdown);
          }, 15000, 'CefShutdown did not return');
          phase('cef-shutdown-observed', {shutdown});
          censusMembers(shutdown.group); // Unknown is still fatal; no instant-empty claim.
          await waitFor(() => {
            helperBarrier = measureBarrier(dir, rootEntry.identity, true);
            return helperBarrier.complete;
          }, 15000, 'Original helpers/reap did not settle while broker remained alive');
          writeFileSync(join(dir, 'helper-barrier.json'), JSON.stringify(helperBarrier, null, 2));
          phase('helper-reap-group-barrier-complete', {barrier: helperBarrier});
          writeFileSync(join(dir, 'release'), 'original helpers gone and fresh group contains only held broker\n');
          phase('broker-release-requested');
        }
        let finalBarrier;
        await waitFor(() => {
          finalBarrier = measureBarrier(dir, rootEntry.identity, false);
          return finalBarrier.complete;
        }, 15000, 'Original process instances/fresh group did not become empty');
        const after = finalBarrier.group;
        writeFileSync(join(dir, 'final-barrier.json'), JSON.stringify(finalBarrier, null, 2));
        phase('all-instances-group-empty-before-cleanup', {barrier: finalBarrier});
        const finalEvents = events(dir); shutdown = finalEvents.find(event => event.event === 'shutdown-return');
        if (mode !== 'broker-crash') {
          if (new Set(finalEvents.filter(event => event.event === 'before-close').map(event => event.index)).size !== 2 || !shutdown) throw Error('Missing normal OnBeforeClose/CefShutdown barrier');
          if (mode === 'owner-death' && !finalEvents.some(event => event.event === 'lease-eof')) throw Error('Owner-death case did not exercise actual FIFO EOF');
          if (!finalEvents.some(event => event.event === 'root-release')) throw Error('Held broker exited without the release barrier');
        } else if (shutdown) throw Error('SIGKILL case unexpectedly ran graceful shutdown');
        run(['print', service], join(dir, 'job-after.json'));
        const observation = {mode, dir, service, fixedVersion: initialized.version, crashReporting: initialized.crash_reporting, before, after, registeredInstances: entries(dir), shutdown, helperBarrier, finalBarrier, outcome: 'OBSERVED_FIXED_WORKLOAD_CONVERGED', limits: ['entry markers are not pre-exec ownership', 'audio/notification action/download and unexercised helper paths pending', 'group census does not prove a generic LaunchPlan tree owner', 'profile lock release/reopen and product daemon integration pending']};
        summary.push(observation); writeFileSync(join(dir, 'observation.json'), JSON.stringify(observation, null, 2));
        console.log(JSON.stringify({mode, outcome: observation.outcome, helpers: helpers.map(event => ({type: event.type, utility: event.utility, identity: event.identity}))}));
      } finally {
        phase('cleanup-start');
        writeFileSync(join(dir, 'close'), 'probe cleanup\n');
        writeFileSync(join(dir, 'release'), 'probe cleanup only; not a measured barrier\n');
        if (!killedOwner) owner.kill('SIGTERM');
        await ownerExit;
        if (loaded) { const result = run(['bootout', service], join(dir, 'cleanup-bootout.json')); phase('cleanup-bootout-complete', {status: result.status}); loaded = false; }
        await pause(200);
        const cleanup = [], observationErrors = [];
        for (const entry of entries(dir)) {
          const current = inspect(entry.identity);
          if (current.state === 'unknown') observationErrors.push({original: entry.identity, ...current});
          // Unknown liveness does not authorize an empty result or skipping
          // cleanup. The original kernel audit token still safely targets only
          // the probe-owned instance, even if numeric PID lookup is unavailable.
          if (['live', 'zombie', 'unknown'].includes(current.state)) {
            if (!Array.isArray(entry.audit) || entry.audit.length !== 8) throw Error('Cannot safely clean an instance without its original audit token');
            const result = spawnSync(manifest.inspector, ['--cleanup-audit', ...entry.audit.map(String)], {encoding: 'utf8', timeout: 5000});
            cleanup.push({identity: entry.identity, status: result.status, stdout: result.stdout, stderr: result.stderr});
            if (result.status !== 0) throw Error('Audit-token cleanup failed');
          }
        }
        let settlementError;
        try {
          await waitFor(() => {
            const instances = instanceStates(dir);
            for (const instance of instances.filter(value => value.state === 'unknown')) {
              if (!observationErrors.some(value => value.original.pid === instance.original.pid && value.error === instance.error)) observationErrors.push(instance);
            }
            return instances.every(instance => ['gone', 'reused'].includes(instance.state));
          }, 5000, 'Probe instance exit is unresolved (live/zombie/unknown)');
        } catch (error) { settlementError = error.message; }
        const rootEntry = entries(dir).find(entry => entry.event === 'broker-entry');
        const finalGroup = rootEntry ? group(rootEntry.identity.pgid) : null;
        const instances = instanceStates(dir);
        writeFileSync(join(dir, 'cleanup.json'), JSON.stringify({signals: cleanup, observationErrors, settlementError, group: finalGroup, instances}, null, 2));
        phase('cleanup-observed', {signals: cleanup.length, observationErrors, settlementError, group: finalGroup, instances});
        if (observationErrors.length || settlementError || instances.some(instance => !['gone', 'reused'].includes(instance.state))) throw Error('Probe cleanup could not verify every original instance exited; see cleanup.json');
        if (finalGroup && (finalGroup.error || finalGroup.truncated || finalGroup.members.length)) throw Error('Probe cleanup has an unresolved group member');
      }
    }
  } finally {
    server.closeAllConnections(); await new Promise(done => server.close(done));
    writeFileSync(join(output, 'summary.json'), JSON.stringify({kind: 'fixed-cef-workload-observation', cases: summary, formalOwnerQualification: false}, null, 2));
  }
}
