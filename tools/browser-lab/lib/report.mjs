import {mkdirSync, writeFileSync} from 'node:fs';
import {join, resolve, relative, isAbsolute} from 'node:path';
import {catalogVersion} from '../catalog.mjs';

export function evidencePath(directory, name) {
 const root = resolve(directory), path = resolve(root, name), rel = relative(root, path);
 if (!rel || rel.startsWith('..') || isAbsolute(rel)) throw Error('Evidence path escapes output root');
 return path;
}

export function assessResults(results, profile) {
 const unexpected = results.some(r => r.status === 'fail');
 const blocked = results.some(r => r.status === 'blocked') || !results.some(r => ['pass','known-gap'].includes(r.status));
 const unresolved = results.some(r => ['known-gap', 'unimplemented', 'manual-required','not-selected'].includes(r.status));
 const ready = results.length > 0 && !unexpected && !blocked && !unresolved;
 return {qualification: ready ? 'ready' : 'not-ready',
  exitCode: unexpected ? 1 : profile === 'qualification' && (blocked || unresolved) ? 2 : blocked ? 2 : 0};
}

export function createReport(directory, environment, profile) {
 mkdirSync(directory, {recursive: true});
 const report = {schemaVersion: 1, catalogVersion, profile, runId: environment.runId,
  started: new Date().toISOString(), environment, results: []};
 return {
  data: report,
  record(item) {
   if (report.results.some(r => r.id === item.id)) throw Error('Duplicate result: ' + item.id);
   report.results.push(item); this.flush();
  },
  flush() {writeFileSync(join(directory, 'report.json'), JSON.stringify(report, null, 2));},
  finish() {
   Object.assign(report, assessResults(report.results, profile), {finished: new Date().toISOString()});
   this.flush(); return report;
  }
 };
}

export function requireManualResult(input, runId, caseId, candidate) {
 if (!input || input.runId !== runId) throw Error('Manual evidence belongs to another run');
 if(candidate)for(const key of ['created','composition','sourceCommit','wails','cefOverlay'])if(input.candidate?.[key]!==candidate[key])throw Error('Manual evidence belongs to another candidate build');
 const result = input.results?.find(r => r.id === caseId);
 if (!result || !['pass', 'fail','blocked'].includes(result.status) || !result.operator || !result.observation || !result.at) {
  throw Error('Manual evidence requires operator, time and concrete observation');
 }
 return result;
}
