export const DB_NAME = 'vorax-lab-v1';
let connection;
export function openDB() {
  if (connection) return connection;
  connection = new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1);
    request.onupgradeneeded = () => {
      const db = request.result;
      db.createObjectStore('meta', { keyPath: 'key' });
      db.createObjectStore('runs', { keyPath: 'id' });
      db.createObjectStore('pending', { keyPath: 'key' });
    };
    request.onsuccess = () => { const db = request.result; db.onversionchange = () => { db.close(); connection = null; }; resolve(db); };
    request.onerror = () => { connection = null; reject(request.error); };
    request.onblocked = () => reject(new Error('请关闭其他旧版本实验室标签页后重试。'));
  });
  return connection;
}
async function transact(names, mode, operation) {
  const db = await openDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(names, mode); let result, failure;
    const set = value => { result = value; };
    const abort = message => { failure = new Error(message); tx.abort(); };
    tx.oncomplete = () => resolve(result);
    tx.onabort = () => reject(failure || tx.error || new Error('浏览器保存事务失败。'));
    tx.onerror = () => { failure ||= tx.error; };
    try { operation(tx, set, abort); } catch (e) { failure = e; tx.abort(); }
  });
}
export const getPending = () => transact(['pending'], 'readonly', (tx, set) => { tx.objectStore('pending').get('active').onsuccess = e => set(e.target.result || null); });
export const listRuns = async () => {
  const rows = await transact(['runs'], 'readonly', (tx, set) => { tx.objectStore('runs').getAll().onsuccess = e => set(e.target.result); });
  return rows.sort((a,b) => b.updatedAt.localeCompare(a.updatedAt));
};
export const getCurrent = () => transact(['meta','runs'], 'readonly', (tx, set) => {
  tx.objectStore('meta').get('current').onsuccess = e => {
    const id = e.target.result?.value;
    if (!id) { set(null); return; }
    tx.objectStore('runs').get(id).onsuccess = result => set(result.target.result || null);
  };
});
export async function identity() {
  const existing = await transact(['meta'], 'readonly', (tx,set) => { tx.objectStore('meta').get('identity').onsuccess = e => set(e.target.result?.value); });
  if (existing) return existing;
  const salt = crypto.getRandomValues(new Uint8Array(32));
  const features = JSON.stringify([navigator.userAgent, navigator.language, navigator.hardwareConcurrency, screen.colorDepth, Intl.DateTimeFormat().resolvedOptions().timeZone, Array.from(salt)]);
  const hash = new Uint8Array(await crypto.subtle.digest('SHA-256',new TextEncoder().encode(features)));
  const profile = { id: Array.from(hash,x => x.toString(16).padStart(2,'0')).join(''), createdAt:new Date().toISOString() };
  return transact(['meta'],'readwrite',(tx,set) => {
    const store=tx.objectStore('meta');store.get('identity').onsuccess=e => {
      if (e.target.result) set(e.target.result.value);
      else {store.put({key:'identity',value:profile});set(profile);}
    };
  });
}
export const stamp = record => record ? {id:record.id,revision:record.view.state.revision} : {id:'',revision:''};
function checkCurrent(tx, expected, done, abort) {
  tx.objectStore('meta').get('current').onsuccess=e => {
    const id=e.target.result?.value || '';
    if (id!==expected.id) {abort('另一标签页已经切换了对局，请重新连接后继续。');return;}
    if (!id) {done();return;}
    tx.objectStore('runs').get(id).onsuccess=r => {
      if (r.target.result?.view.state.revision!==expected.revision) {abort('另一标签页已经更新了此对局，请重新连接后继续。');return;}
      done();
    };
  };
}
export async function stagePending(pending) {
  return transact(['meta','runs','pending'],'readwrite',(tx,set,abort) => {
    checkCurrent(tx,pending.expected,() => {
      const store=tx.objectStore('pending');store.get('active').onsuccess=e => {
        if(e.target.result){abort('已有尚未完成的操作，请先重试保存。');return;}
        store.put({...pending,key:'active'});set(pending);
      };
    },abort);
  });
}
export async function commitPending(pending,response) {
  return transact(['meta','runs','pending'],'readwrite',(tx,set,abort) => {
    tx.objectStore('pending').get('active').onsuccess=e => {
      if(e.target.result?.request.requestId!==pending.request.requestId){abort('此操作已由其他标签页处理，请重新连接。');return;}
      checkCurrent(tx,pending.expected,() => {
        const finish=old => {
          const now=new Date().toISOString();
          const commands=pending.kind==='command' ? [...(old?.commands||[]),pending.request.command] : [];
          const run={id:response.view.state.runId,createdAt:old?.createdAt||now,updatedAt:now,stateToken:response.stateToken,view:response.view,events:response.events,gameplayHash:response.gameplayHash,commands};
          tx.objectStore('runs').put(run);tx.objectStore('meta').put({key:'current',value:run.id});tx.objectStore('pending').delete('active');set(run);
        };
        if(pending.kind==='command') tx.objectStore('runs').get(pending.expected.id).onsuccess=r=>finish(r.target.result);
        else finish(null);
      },abort);
    };
  });
}
export const clearRejected = requestId => transact(['pending'],'readwrite',(tx) => {
  const store=tx.objectStore('pending');store.get('active').onsuccess=e=>{if(e.target.result?.request.requestId===requestId)store.delete('active');};
});
export const switchCurrent = (id,expected) => transact(['meta','runs','pending'],'readwrite',(tx,set,abort) => {
  checkCurrent(tx,expected,()=>{tx.objectStore('pending').get('active').onsuccess=e=>{
    if(e.target.result){abort('请先完成待保存操作。');return;}
    tx.objectStore('runs').get(id).onsuccess=r=>{if(!r.target.result){abort('找不到本地记录。');return;}tx.objectStore('meta').put({key:'current',value:id});set(r.target.result);};
  };},abort);
});
