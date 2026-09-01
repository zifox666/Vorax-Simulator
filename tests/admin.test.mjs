import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';

const html=readFileSync(new URL('../web/admin.html',import.meta.url),'utf8');

test('training admin page exposes key lifecycle controls without persisting admin token',()=>{
  assert.match(html,/id="admin-token"/);
  assert.match(html,/桶容量/);
  assert.match(html,/每秒补充/);
  assert.match(html,/过期时间/);
  assert.match(html,/\/api\/v1\/admin\/training-keys/);
  assert.match(html,/method:'PATCH'/);
  assert.match(html,/method:'DELETE'/);
  assert.doesNotMatch(html,/localStorage|sessionStorage|indexedDB/);
});
