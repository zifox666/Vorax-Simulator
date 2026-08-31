import test from 'node:test';
import assert from 'node:assert/strict';
import {buildSteps, sampleStep, interpolateInteger, totalContribution, createPlayer, readSpeed, saveSpeed, SPEEDS, SPEED_KEY} from '../web/assets/animation.mjs';

const monster = (id='m1', activity='10', quantity='20', family='BONE', rarity='NORMAL') => ({id,activity,quantity,family,rarity});
const slots = (...monsters) => Array.from({length:6},(_,index)=>({index,monster:monsters[index] || null}));
const event = (sequence,kind,slotsAfter, targetIds=['m1'],extra={}) => ({sequence:String(sequence),kind,slotsAfter,targetIds,...extra});

test('named monster identity changes with the structural animation frame',()=>{
  const before={...monster(),definitionId:'bone_soldier',name:'士兵'};
  const after={...monster('m1','15','44','BONE','MAGIC'),definitionId:'bone_light_crossbowman',name:'轻弩兵'};
  for(const kind of ['mutated','awakened']){
    const [step]=buildSteps(slots(before),[event(1,kind,slots(after))]);
    assert.equal(sampleStep(step,.49)[0].monster.name,'士兵');
    assert.equal(sampleStep(step,.51)[0].monster.name,'轻弩兵');
    assert.equal(sampleStep(step,1)[0].monster.definitionId,'bone_light_crossbowman');
  }
});

test('gray marrow plays buff, mutation, second mutation and final buff in order',()=>{
  const initial=slots(monster());
  const frames=[slots(monster('m1','51')),slots(monster('m1','56','44','INSECT','MAGIC')),slots(monster('m1','71','56','FIEND','RARE')),slots(monster('m1','101','56','FIEND','RARE'))];
  const events=frames.map((frame,i)=>event(i+1,['stats_changed','mutated','mutated','stats_changed'][i],frame));
  const frozen=JSON.stringify({initial,events});
  const steps=buildSteps(initial,events);
  assert.deepEqual(steps.map(step=>step.kind),['stats_changed','mutated','mutated','stats_changed']);
  assert.equal(steps[0].affected[0].activityDelta,'41');
  assert.equal(steps[3].affected[0].activityDelta,'30');
  assert.equal(sampleStep(steps[1],.49)[0].monster.family,'BONE');
  assert.equal(sampleStep(steps[1],.51)[0].monster.family,'INSECT');
  assert.equal(sampleStep(steps[2],.51)[0].monster.family,'FIEND');
  steps.forEach((step,i)=>assert.deepEqual(sampleStep(step,1).map(({contribution,...slot})=>slot),frames[i]));
  assert.equal(JSON.stringify({initial,events}),frozen);
  assert.equal(buildSteps(initial,events.slice(0,2)).length,2);
});

test('activity and quantity animate sequentially without leaking the final value',()=>{
  const steps=buildSteps(slots(monster()),[event(1,'stats_changed',slots(monster('m1','25','35')))]);
  assert.deepEqual(steps.map(step=>step.field),['activity','quantity']);
  assert.deepEqual(sampleStep(steps[0],1)[0].monster,monster('m1','25','20'));
  const halfway=sampleStep(steps[0],.45)[0].monster;
  assert.ok(BigInt(halfway.activity)>10n && BigInt(halfway.activity)<25n);
  assert.equal(halfway.quantity,'20');
  assert.deepEqual(sampleStep(steps[1],1)[0].monster,monster('m1','25','35'));
});

test('removal retains the outgoing specimen and addition reuses its slot only at its own step',()=>{
  const steps=buildSteps(slots(monster()),[event(1,'removed',slots()),event(2,'added',slots(monster('m2','5','24','FIEND','MAGIC')),['m2'])]);
  assert.equal(sampleStep(steps[0],.3)[0].monster.id,'m1');
  assert.equal(sampleStep(steps[0],1)[0].monster,null);
  assert.equal(sampleStep(steps[1],.4)[0].monster,null);
  assert.equal(sampleStep(steps[1],1)[0].monster.id,'m2');
});

test('fusion animates every source and the destination while preserving empty slots',()=>{
  const initial=slots(monster(),null,null,monster('m2','30','40','INSECT','MAGIC'));
  const after=slots(monster('m3','40','60','AWAKENER','RARE'));
  const [step]=buildSteps(initial,[event(1,'fused',after,['m2','m1','m3'])]);
  assert.deepEqual(step.affected.map(slot=>slot.index),[0,3]);
  assert.deepEqual(step.affected.map(slot=>slot.role),['destination','outgoing']);
  assert.equal(sampleStep(step,.49)[3].monster.id,'m2');
  assert.equal(sampleStep(step,1)[3].monster,null);
  assert.equal(sampleStep(step,1)[0].monster.id,'m3');
  assert.equal(totalContribution(sampleStep(step,1)),'2400');
});

test('awakening and promotion show the new rarity at the transformation midpoint',()=>{
  const [step]=buildSteps(slots(monster()),[event(1,'awakened',slots(monster('m1','15','44','BONE','MAGIC')))]);
  assert.equal(step.label,'觉醒 · 升阶');
  assert.equal(step.affected[0].upgraded,true);
  assert.equal(sampleStep(step,.4)[0].monster.rarity,'NORMAL');
  assert.equal(sampleStep(step,.6)[0].monster.rarity,'MAGIC');
});

test('devouring shows the prey leaving and the eater inheriting its attributes',()=>{
  const initial=slots(monster(),monster('m2','30','40'));
  const [step]=buildSteps(initial,[event(1,'devoured',slots(monster('m1','40','60')),['m1','m2'])]);
  assert.equal(step.affected[0].activityDelta,'30');
  assert.equal(step.affected[0].quantityDelta,'40');
  assert.equal(sampleStep(step,1)[1].monster,null);
});

test('old responses without snapshots do not invent random intermediate monsters',()=>{
  assert.deepEqual(buildSteps(slots(monster()),[{kind:'mutated',sequence:'1'}]),[]);
  assert.deepEqual(buildSteps(slots(monster()),[event(1,'mutated',[])]),[]);
  assert.deepEqual(buildSteps(slots(monster()),[]),[]);
});

test('large integer animation remains exact and monotonic, including decreases',()=>{
  assert.equal(interpolateInteger('9007199254740993','9007199254741093',.5),'9007199254741043');
  assert.equal(interpolateInteger('9223372036854775790','9223372036854775807',1),'9223372036854775807');
  assert.equal(interpolateInteger('100','0',.5),'50');
  assert.equal(interpolateInteger('10','20',-1),'10');
  assert.equal(interpolateInteger('10','20',2),'20');
});

test('speed preferences validate values and tolerate inaccessible browser storage',()=>{
  const data=new Map();
  const storage={getItem:key=>data.get(key),setItem:(key,value)=>data.set(key,value)};
  assert.equal(readSpeed(storage),1);
  for(const speed of SPEEDS){assert.equal(saveSpeed(speed,storage),true);assert.equal(readSpeed(storage),speed);}
  assert.equal(saveSpeed(3,storage),false);
  for(const value of ['0','3','NaN','',null]){data.set(SPEED_KEY,value);assert.equal(readSpeed(storage),1);}
  const blocked={getItem(){throw Error('denied');},setItem(){throw Error('denied');}};
  assert.equal(readSpeed(blocked),1);
  assert.equal(saveSpeed(4,blocked),false);
});

function clock() {
  let time=0, id=0;
  const queued=new Map();
  return {
    now:()=>time,
    requestFrame:callback=>{queued.set(++id,callback);return id;},
    cancelFrame:id=>queued.delete(id),
    async tick(delta=16){time+=delta;const callbacks=[...queued.values()];queued.clear();callbacks.forEach(callback=>callback(time));await Promise.resolve();},
    pending:()=>queued.size
  };
}

test('all four speeds preserve event order and finish every numeric tween',async()=>{
  const steps=buildSteps(slots(monster()),[event(1,'stats_changed',slots(monster('m1','51'))),event(2,'mutated',slots(monster('m1','56','44','FIEND','MAGIC')))]);
  const frameCounts=[];
  for(const speed of SPEEDS){
    const scheduler=clock(), player=createPlayer(scheduler), completed=[];
    const promise=player.play(steps,{getSpeed:()=>speed,onFrame:frame=>{if(frame.progress===1)completed.push(frame.step.key);}});
    let ticks=0;
    while(scheduler.pending() && ticks<500){await scheduler.tick();ticks++;}
    await promise;
    assert.deepEqual(completed,steps.map(step=>step.key));
    frameCounts.push(ticks);
  }
  assert.ok(frameCounts.every((count,i)=>i===0 || count<frameCounts[i-1]));
});

test('changing speed affects the current tween and cancellation resolves without another event',async()=>{
  const steps=buildSteps(slots(monster()),[event(1,'stats_changed',slots(monster('m1','51'))),event(2,'removed',slots())]);
  const scheduler=clock(), player=createPlayer(scheduler);
  let speed=1, latest;
  const promise=player.play(steps,{getSpeed:()=>speed,onFrame:frame=>{latest=frame;}});
  await scheduler.tick(20);const first=latest.progress;
  speed=8;await scheduler.tick(20);
  assert.ok(Math.abs((latest.progress-first)/first-8)<.0001);
  player.cancel();await promise;
  assert.equal(scheduler.pending(),0);
  assert.equal(latest.index,0);
});

test('rendering errors reject and the player can be reused',async()=>{
  const steps=buildSteps(slots(monster()),[event(1,'removed',slots())]);
  const scheduler=clock(), player=createPlayer(scheduler);
  await assert.rejects(player.play(steps,{getSpeed:()=>1,onFrame(){throw Error('render');}}),/render/);
  let completed=false;
  const promise=player.play(steps,{getSpeed:()=>8,onFrame:frame=>{completed=frame.progress===1;}});
  while(scheduler.pending())await scheduler.tick();
  await promise;
  assert.equal(completed,true);
});

test('replacing a running sequence keeps the new sequence cancellable',async()=>{
  const steps=buildSteps(slots(monster()),[event(1,'removed',slots())]);
  const scheduler=clock(), player=createPlayer(scheduler);
  const options={getSpeed:()=>1,onFrame(){}};
  const first=player.play(steps,options);
  const second=player.play(steps,options);
  await first;
  await scheduler.tick();
  player.cancel();
  await second;
  assert.equal(scheduler.pending(),0);
});
