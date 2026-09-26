import assert from 'node:assert/strict';
import test from 'node:test';

import {isAccountSignOutSuccessful} from './account_feedback';

test('recognizes only a successful DSH Remote sign-out response', async () => {
  assert.equal(await isAccountSignOutSuccessful(new Response(JSON.stringify({
    type: 'server-response', result: {ok: true}
  }))), true);
  assert.equal(await isAccountSignOutSuccessful(new Response(JSON.stringify({
    type: 'server-response', result: {ok: false}
  }))), false);
  assert.equal(await isAccountSignOutSuccessful(new Response(JSON.stringify({
    type: 'unexpected', result: {ok: true}
  }))), false);
  assert.equal(await isAccountSignOutSuccessful(new Response('{}', {status: 500})), false);
  assert.equal(await isAccountSignOutSuccessful(new Response('not json')), false);
});
