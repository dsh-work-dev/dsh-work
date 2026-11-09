// go test -exec adapter. The build artifact is selected by this build/test
// command, not by product configuration. Linux/Windows keep their normal entry.
import {spawn} from 'node:child_process';
import {constants, copyFileSync, lstatSync, realpathSync, statSync, unlinkSync} from 'node:fs';
import {dirname, join, resolve} from 'node:path';

const [flag, assetArgument, executableArgument, ...args] = process.argv.slice(2);
if (flag !== '--asset' || !assetArgument || !executableArgument) {
  throw Error('Usage: node with-workeripc-mac.mjs --asset BUILT_ADDON TEST_EXECUTABLE [ARGS...]');
}
if (process.platform !== 'darwin') throw Error('This test adapter requires macOS');
const asset = realpathSync(assetArgument), executable = resolve(executableArgument);
if (!statSync(asset).isFile() || !statSync(executable).isFile()) throw Error('Built addon and test executable must be regular files');
const target = join(dirname(executable), 'workeripc-mac.node');
let installed;
// The explicitly compiled test binary may already be beside the build output.
// Otherwise copy exclusively into Go's isolated temporary executable directory.
if (resolve(asset) !== resolve(target)) {
  copyFileSync(asset, target, constants.COPYFILE_EXCL);
  installed = lstatSync(target, {bigint: true});
}
const child = spawn(executable, args, {stdio: 'inherit'});
const forward = signal => child.kill(signal);
const interrupt = () => forward('SIGINT'), terminate = () => forward('SIGTERM');
process.on('SIGINT', interrupt); process.on('SIGTERM', terminate);
let result;
try {
  result = await new Promise((resolveResult, reject) => {
    child.once('error', reject);
    child.once('exit', (code, signal) => resolveResult({code, signal}));
  });
} finally {
  process.off('SIGINT', interrupt); process.off('SIGTERM', terminate);
  if (installed) {
    try {
      const current = lstatSync(target, {bigint: true});
      if (current.dev === installed.dev && current.ino === installed.ino) unlinkSync(target);
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
  }
}
if (result.signal) process.kill(process.pid, result.signal);
else process.exitCode = result.code ?? 1;
