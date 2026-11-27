import test from 'node:test';
import assert from 'node:assert/strict';
import {normalizeCandidateFlags} from './e67-go-flags.mjs';
const path='C:/Users/livei/.codex/worktrees/812a/dsh-work/bin/e67.mod';
test('removes only this worktree historical flags and preserves other flags',()=>{
 const result=normalizeCandidateFlags(`-modfile=${path} -tags=e67presenter -trimpath -buildvcs=false`,path);
 assert.equal(result.flags,'-trimpath -buildvcs=false');assert.equal(result.removed,2);
 assert.equal(normalizeCandidateFlags(`"-modfile=${path}" -trimpath`,path).flags,'-trimpath');
});
test('no surviving flags uses nonempty whitespace, suppressing GOENV fallback without editing it',()=>{
 assert.equal(normalizeCandidateFlags(`-modfile=${path} -tags=e67presenter`,path).flags,' ');
});
test('foreign modfile is rejected rather than silently changing user build flags',()=>{
 assert.throws(()=>normalizeCandidateFlags('-modfile=C:/other/go.mod -trimpath',path),/non-candidate/);
 assert.equal(normalizeCandidateFlags('-tags=userTag,e67presenter -trimpath',path).flags,'-tags=userTag,e67presenter -trimpath');
});
