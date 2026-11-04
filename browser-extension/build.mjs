// Setup slice: plain MV3 modules, no dependency download or generated credential.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {HOST_NAME, PROTOCOL} from './protocol.js';
import {compileRoutes} from './compile.mjs';
const source = path.dirname(fileURLToPath(import.meta.url));
if (process.argv.length !== 4 || process.argv[2] !== '--out') throw Error('Usage: node browser-extension/build.mjs --out <empty-output-directory>');
const out = path.resolve(process.argv[3]);
if (out === source || source.startsWith(out + path.sep)) throw Error('Output must not contain or replace source');
const marker = path.join(out, '.dsh-work-extension-build');
if (fs.existsSync(out) && fs.readdirSync(out).length && (!fs.existsSync(marker) || fs.readFileSync(marker, 'utf8') !== 'setup-v1\n')) throw Error('Output directory belongs to another owner');
fs.mkdirSync(out, {recursive: true});
const manifest = JSON.parse(fs.readFileSync(path.join(source, 'manifest.json'), 'utf8'));
// Chromium's public-key identity derivation, not an ordinary content hash check.
const id = createHash('sha256').update(Buffer.from(manifest.key, 'base64')).digest('hex').slice(0, 32).replace(/[0-9a-f]/g, c => String.fromCharCode(97 + parseInt(c, 16)));
if (id !== 'fpemabjfldbpdmjfdofdnfdaccafobod') throw Error('Development extension identity changed');
for (const name of ['manifest.json', 'background.js', 'protocol.js', 'popup.html', 'popup.js', 'LICENSE','NOTICE','PROVENANCE.txt','icon-16.png','icon-32.png','icon-48.png','icon-128.png']) fs.copyFileSync(path.join(source, name), path.join(out, name));
fs.writeFileSync(path.join(out,'routes.js'),await compileRoutes());
fs.writeFileSync(marker, 'setup-v1\n');
fs.writeFileSync(path.join(out, 'identity.json'), JSON.stringify({extensionId: id, version: manifest.version, protocol: PROTOCOL, host: HOST_NAME, slice: 'browser-session-v2'}, null, 2) + '\n');
console.log(`Built ${id} (${manifest.version}, setup protocol ${PROTOCOL}) at ${out}`);
