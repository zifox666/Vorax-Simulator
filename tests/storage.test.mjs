import test, { beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import 'fake-indexeddb/auto';
import * as store from '../web/assets/storage.mjs';

beforeEach(async()=>{
  const db=await store.openDB();
  await new Promise((resolve,reject)=>{const tx=db.transaction(['meta','runs','pending'],'readwrite');for(const name of ['meta','runs','pending'])tx.objectStore(name).clear();tx.oncomplete=resolve;tx.onabort=()=>reject(tx.error);});
});
const response=(id,revision,score='0')=>({stateToken:'signed-'+id+'-'+revision,gameplayHash:'hash',events:[],view:{state:{runId:id,revision,score,phase:'CHOOSING'}}});
const create=async(id='run-1')=>{
  const p={kind:'create',expected:{id:'',revision:''},request:{requestId:'create-'+id}};
  await store.stagePending(p);return store.commitPending(p,response(id,'1'));
};

test('checkpoint and operation log commit atomically, including integers above JS safe range',async()=>{
  const first=await create();
  const p={kind:'command',expected:store.stamp(first),request:{requestId:'command-1',command:{type:'choose'}}};
  await store.stagePending(p);
  assert.equal((await store.getCurrent()).view.state.revision,'1');
  assert.equal((await store.getPending()).request.requestId,'command-1');
  await store.commitPending(p,response('run-1','2','9007199254740993'));
  const saved=await store.getCurrent();
  assert.equal(saved.view.state.score,'9007199254740993');
  assert.deepEqual(saved.commands,[{type:'choose'}]);
  assert.equal(await store.getPending(),null);
});

test('two tabs cannot stage concurrent commands or overwrite a newer checkpoint',async()=>{
  const first=await create();const expected=store.stamp(first);
  const a={kind:'command',expected,request:{requestId:'tab-a',command:{type:'refresh'}}};
  const b={kind:'command',expected,request:{requestId:'tab-b',command:{type:'refresh'}}};
  await store.stagePending(a);await assert.rejects(store.stagePending(b),/尚未完成/);
  await store.commitPending(a,response('run-1','2'));
  await assert.rejects(store.stagePending(b),/另一标签页/);
  await assert.rejects(store.commitPending(a,response('run-1','2')),/其他标签页/);
  assert.equal((await store.getCurrent()).commands.length,1);
});

test('retry after network loss retains the original request and only one log entry',async()=>{
  const first=await create();const p={kind:'command',expected:store.stamp(first),path:'/command',request:{requestId:'retry-me',command:{type:'refresh'}}};
  await store.stagePending(p);
  const reloaded=await store.getPending();assert.equal(reloaded.request.requestId,'retry-me');
  await store.commitPending(reloaded,response('run-1','2'));
  assert.equal((await store.getCurrent()).commands.length,1);
});

test('a local write failure aborts checkpoint, pointer, history and pending deletion together',async()=>{
  const first=await create();const p={kind:'command',expected:store.stamp(first),request:{requestId:'fail-save',command:{type:'choose'}}};await store.stagePending(p);
  const db=await store.openDB(), original=db.transaction.bind(db);
  db.transaction=(...args)=>{
    const tx=original(...args);
    const originalStore=tx.objectStore.bind(tx);
    tx.objectStore=name=>{
      const objectStore=originalStore(name);
      if(name==='runs'&&args[1]==='readwrite'){
        const put=objectStore.put.bind(objectStore);
        objectStore.put=(...values)=>{const req=put(...values);req.addEventListener('success',()=>tx.abort());return req;};
      }
      return objectStore;
    };
    return tx;
  };
  try {await assert.rejects(store.commitPending(p,response('run-1','2')));}
  finally {db.transaction=original;}
  assert.equal((await store.getCurrent()).view.state.revision,'1');
  assert.equal((await store.getPending()).request.requestId,'fail-save');
});

test('new runs preserve history and unfinished runs can be resumed',async()=>{
  const first=await create();const p={kind:'create',expected:store.stamp(first),request:{requestId:'new-run'}};
  await store.stagePending(p);const second=await store.commitPending(p,response('run-2','1'));
  assert.equal((await store.listRuns()).length,2);
  await store.switchCurrent('run-1',store.stamp(second));assert.equal((await store.getCurrent()).id,'run-1');
});

test('effect snapshots commit with the final checkpoint and survive reload reads',async()=>{
  const first=await create();
  const p={kind:'command',expected:store.stamp(first),request:{requestId:'animated-command',command:{type:'choose'}}};
  const result=response('run-1','2','9007199254740993');
  result.events=[{sequence:'1',kind:'stats_changed',sourceName:'灰质脊髓溶液',slotsAfter:[{index:0,monster:{id:'m1',activity:'9007199254740993',quantity:'1'}}]}];
  await store.stagePending(p);
  await store.commitPending(p,result);
  result.events[0].slotsAfter[0].monster.activity='0';
  const saved=await store.getCurrent();
  assert.equal(saved.events[0].slotsAfter[0].monster.activity,'9007199254740993');
  assert.equal(saved.view.state.revision,'2');
  assert.equal(await store.getPending(),null);
});
