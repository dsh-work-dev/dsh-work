import {execFileSync} from 'node:child_process';
import {mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync} from 'node:fs';
import {basename, isAbsolute, join, relative} from 'node:path';

// Stage the signed CEF publication before the outer app is signed.
export function stageMacEmbeddedCEF(app, publicationPath) {
  const source = JSON.parse(readFileSync(publicationPath, 'utf8'));
  if (source.kind !== 'dsh-owned-cef-mac' || source.architecture !== 'arm64' ||
      !source.sandbox || basename(source.app) !== 'DSHWorkBrowser.app' || realpathSync(source.app) !== source.app) {
    throw Error('Expected a native DSH Work Browser publication');
  }
  execFileSync('/usr/bin/codesign', ['--verify', '--deep', '--strict', source.app], {stdio: 'inherit'});
  const contents = join(app, 'Contents');
  const bundle = 'Frameworks/DSHWorkBrowser.app';
  const relocate = path => {
    const entry = relative(source.app, path);
    if (!entry || entry === '..' || entry.startsWith('../') || isAbsolute(entry) || realpathSync(path) !== path) {
      throw Error('CEF publication contains an invalid component path');
    }
    return join(bundle, entry);
  };
  const published = {
    kind: source.kind,
    version: source.version,
    app: bundle,
    executable: relocate(source.executable),
    owner: relocate(source.owner),
    helpers: source.helpers.map(relocate),
    sandbox: source.sandbox,
    minimumOS: source.minimumOS,
    architecture: source.architecture,
  };
  const destination = join(contents, bundle);
  mkdirSync(join(contents, 'Frameworks'), {recursive: true});
  mkdirSync(join(contents, 'Resources'), {recursive: true});
  mkdirSync(destination);
  try {
    execFileSync('/usr/bin/ditto', [source.app, destination], {stdio: 'inherit'});
    writeFileSync(join(contents, 'Resources', 'dsh-cef-publication.json'), JSON.stringify(published, null, 2) + '\n', {flag: 'wx'});
  } catch (error) {
    rmSync(destination, {recursive: true, force: true});
    throw error;
  }
}
