import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import RewardJars, {rewardJars, JAR_NAMES} from '../web/assets/reward-jars.mjs';

test('reward jars preserve the saved colors and slot order without changing a run',()=>{
  const rewards={jars:['RAINBOW','JAR_RED','PURPLE','JAR_WHITE','EMPTY','EMPTY'],dropBonusPercent:25};
  const before=JSON.stringify(rewards);
  const jars=rewardJars(rewards);
  assert.deepEqual(jars.map(jar=>jar.color),['rainbow','jar_red','purple','jar_white','empty','empty']);
  assert.deepEqual(jars.map(jar=>jar.label),rewards.jars.map(color=>JAR_NAMES[color]));
  assert.deepEqual(jars.map(jar=>jar.symbol),['◆','◆','◆','◆','·','·']);
  assert.equal(JSON.stringify(rewards),before);
});

test('completed and unfinished runs display their own reward snapshots',()=>{
  const runs=[
    {phase:'FINISHED',rewards:{jars:['PURPLE','JAR_RED','EMPTY','EMPTY','EMPTY','EMPTY']}},
    {phase:'CHOOSING',rewards:{jars:['JAR_WHITE','EMPTY','EMPTY','EMPTY','EMPTY','EMPTY']}}
  ];
  assert.equal(rewardJars(runs[0].rewards)[0].color,'purple');
  assert.equal(rewardJars(runs[1].rewards)[0].color,'jar_white');
  assert.equal(rewardJars({jars:Array(6).fill('EMPTY')}).length,6);
});

test('missing historical rewards are distinct from six empty reward slots',()=>{
  for(const rewards of [undefined,null,{}, {jars:null},{jars:'EMPTY'}]) {
    assert.deepEqual(rewardJars(rewards),[]);
  }
  assert.deepEqual(rewardJars({jars:['FUTURE_COLOR']}),[{color:'unknown',label:'未知奖励罐',symbol:'◆'}]);
});

test('reward jar component compiles with the bundled Vue runtime without a browser',()=>{
  const context={};
  runInNewContext(readFileSync(new URL('../web/assets/vue.global.prod.js',import.meta.url),'utf8'),context);
  const render=context.Vue.compile(RewardJars.template);
  assert.equal(typeof render,'function');
  const jars=rewardJars({jars:['JAR_RED','EMPTY']});
  const vnode=render({jars},[]);
  assert.equal(vnode.props.role,'group');
  assert.equal(vnode.props['aria-label'],'奖励罐');
  assert.equal(vnode.children[0].children[0].props.class,'reward-jar jar_red');
  assert.equal(vnode.children[0].children[0].props['aria-label'],'红色奖励罐');
});

test('lab, completion and history use the shared reward jar component',()=>{
  const html=readFileSync(new URL('../web/index.html',import.meta.url),'utf8');
  assert.equal((html.match(/<reward-jars\b/g)||[]).length,3);
  assert.match(html,/<h2>游戏结束<\/h2>\s*<reward-jars :rewards="state.rewards"><\/reward-jars>\s*<strong>/);
  assert.match(html,/<td class="history-rewards">\s*<reward-jars :rewards="run.view.state.rewards">/);
  assert.ok(html.includes(':data-rarity="slot.monster?.rarity"'));
});
