import * as store from './storage.mjs';
import ChoicePanel from './choice-panel.mjs';
import TermText, { FAMILY_NAMES, FAMILY_SYMBOLS } from './term-text.mjs';
import RewardJars from './reward-jars.mjs';
import { SPEEDS, readSpeed, saveSpeed, buildSteps, createPlayer, withContributions, totalContribution, eventLabel } from './animation.mjs';

const { createApp, ref, computed, onMounted, onUnmounted, nextTick } = window.Vue;

createApp({
  components: { ChoicePanel, TermText, RewardJars },
  setup() {
    const page=ref('lab'), record=ref(null), profile=ref(null), history=ref([]);
    const busy=ref(false), ready=ref(false), connected=ref(false), pending=ref(null);
    const error=ref(''), message=ref(''), setupOpen=ref(false), pet=ref(0), seedInput=ref('');
    const selectedIndex=ref(-1), targets=ref([]);
    const themeStorageKey='vorax-theme-preference';
    const systemTheme=window.matchMedia?.('(prefers-color-scheme: dark)');
    const readThemePreference=()=>{
      try {
        const saved=localStorage.getItem(themeStorageKey);
        return ['system','light','dark'].includes(saved) ? saved : 'system';
      } catch {return 'system';}
    };
    const themePreference=ref(readThemePreference()), themeOpen=ref(false);
    const themeIcon=computed(()=>({system:'◐',light:'☼',dark:'☾'})[themePreference.value]);
    const applyTheme=preference=>{
      const theme=preference==='system' ? (systemTheme?.matches ? 'dark' : 'light') : preference;
      document.documentElement.dataset.theme=theme;
      document.documentElement.style.colorScheme=theme;
      document.querySelector('meta[name="theme-color"]')?.setAttribute('content',theme==='dark' ? '#0d253d' : '#f6f9fc');
    };
    const setTheme=preference=>{
      if(!['system','light','dark'].includes(preference))return;
      themePreference.value=preference;
      themeOpen.value=false;
      try {localStorage.setItem(themeStorageKey,preference);} catch {}
      applyTheme(preference);
    };
    const onSystemThemeChange=()=>{if(themePreference.value==='system')applyTheme('system');};
    const animationSpeed=ref(readSpeed()), playback=ref(null), animationFrame=ref(null);
    const player=createPlayer();
    const animating=computed(()=>!!playback.value);
    const visibleEvents=computed(()=>playback.value ? playback.value.events.filter(event=>BigInt(event.sequence)<=BigInt(animationFrame.value?.step.event.sequence ?? 0)) : record.value?.events || []);
    const view=computed(()=>{
      if(!playback.value)return record.value?.view;
      const base=playback.value.view, slots=animationFrame.value?.slots || withContributions(base.state.slots);
      const acquired=new Set(visibleEvents.value.filter(event=>event.kind==='tool_acquired').map(event=>event.source));
      const tools=[...base.tools,...(record.value?.view.tools || []).filter(tool=>acquired.has(tool.id) && !base.tools.some(old=>old.id===tool.id))];
      return {...base,tools,state:{...base.state,slots,score:totalContribution(slots)}};
    });
    const state=computed(()=>view.value?.state);
    const selectedCard=computed(()=>view.value?.cards[selectedIndex.value] || null);
    const locked=computed(()=>busy.value || !ready.value || !connected.value || !!pending.value);
    const occupied=computed(()=>state.value?.slots.filter(s=>s.monster).length || 0);
    const familyNames=FAMILY_NAMES;
    const familySymbols=FAMILY_SYMBOLS;
    const monsterRarities={NORMAL:'普通',MAGIC:'魔法',RARE:'稀有',BOSS:'首领'};
    const potionRarities={WHITE:'普通',BLUE:'魔法',GOLD:'稀有',RED:'至臻'};
    const kindNames={UNKNOWN:'未知手术器具',POTION:'药剂',TOOL:'手术用具',SCHEME:'手术方案'};
    const prefix=(a,b)=>b.every((v,i)=>a[i]===v);
    const canConfirm=computed(()=>!!selectedCard.value?.legalTargets.some(set=>set.ids.length===targets.value.length && prefix(set.ids,targets.value)));
    const targetHint=computed(()=>{
      const card=selectedCard.value?.definition;if(!card)return '';
      if(card.maxTargets===0)return '无需选择目标，确认后生效。';
      const count=card.minTargets===card.maxTargets ? card.minTargets : `${card.minTargets}–${card.maxTargets}`;
      return `点击上方高亮培养槽，选择 ${count} 组怪物 · 已选 ${targets.value.length} 组`;
    });
    const savedLabel=computed(()=>record.value ? `已保存在浏览器 · ${new Date(record.value.updatedAt).toLocaleTimeString('zh-CN')}` : '');
    const channel=typeof BroadcastChannel!=='undefined' ? new BroadcastChannel('vorax-updates') : null;
    const announce=()=>channel?.postMessage('changed');
    const resetSelection=()=>{selectedIndex.value=-1;targets.value=[];};
    const number=value=>String(value ?? '0').replace(/\B(?=(\d{3})+(?!\d))/g,',');
    const date=value=>new Date(value).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'});
    const cardIcon=card=>({UNKNOWN:'⌁',TOOL:'✦',SCHEME:'≋',POTION:'◈'})[card.kind];
    const activeEffect=computed(()=>animationFrame.value?.step);
    const effectStyle=computed(()=>({'--effect-time':`${-(animationFrame.value?.progress || 0)*1000}ms`}));
    const slotEffect=slot=>activeEffect.value?.affected.find(effect=>effect.index===slot.index);
    const slotEffectClass=slot=>{
      const effect=slotEffect(slot);
      return effect ? [`effect-${activeEffect.value.kind}`,`effect-${effect.role}`,{'effect-upgraded':effect.upgraded}] : [];
    };
    const effectDelta=(slot,field)=>slotEffect(slot)?.[`${field}Delta`] || '0';
    const signed=value=>`${BigInt(value)>0n ? '+' : ''}${number(value)}`;
    const eventSource=event=>event.sourceName || view.value?.tools.find(tool=>tool.id===event.source)?.name || view.value?.cards.find(card=>card.definition.id===event.source)?.definition.name || '手术台';
    function setAnimationSpeed(speed) {
      if(!SPEEDS.includes(speed))return;
      animationSpeed.value=speed;
      if(!saveSpeed(speed))error.value='无法保存动画速度，本次设置仍然有效。';
    }
    const slotLabel=slot=>slot.monster ? `培养槽 ${slot.index+1}，${familyNames[slot.monster.family]}，${monsterRarities[slot.monster.rarity]}，活性 ${slot.monster.activity}，数量 ${slot.monster.quantity}` : `空培养槽 ${slot.index+1}`;
    function canTarget(monster) {
      if(!monster || locked.value || !selectedCard.value)return false;
      if(targets.value.includes(monster.id))return true;
      return selectedCard.value.legalTargets.some(set=>prefix(set.ids,[...targets.value,monster.id]));
    }
    function selectCard(index) {if(locked.value)return;selectedIndex.value=index;targets.value=[];}
    function toggleTarget(id) {
      if(locked.value)return;
      const existing=targets.value.indexOf(id);
      if(existing>=0)targets.value=targets.value.slice(0,existing);
      else targets.value.push(id);
    }
    async function api(path,body) {
      const controller=new AbortController(), timer=setTimeout(()=>controller.abort(),20000);
      try {
        const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body),signal:controller.signal});
        const data=await response.json();
        if(!response.ok){const err=new Error(data.message||'请求未完成');err.status=response.status;throw err;}
        connected.value=true;return data;
      } catch(e) {
        if(!e.status){connected.value=false;throw new Error('无法连接后端或响应超时。进度仍保存在本地，请重新连接或重试待保存操作。');}
        throw e;
      } finally {clearTimeout(timer);}
    }
    const refreshLocal=async()=>{history.value=await store.listRuns();pending.value=await store.getPending();};
    async function applyPending(p) {
      const response=await api(p.path,p.request);
      const previous=record.value;
      let saved;
      try {saved=await store.commitPending(p,response);}
      catch(e){throw new Error(`结果尚未保存：${e.message} 请重试；不要关闭或清除浏览器数据。`);}
      let steps=[];
      try {
        if(p.kind==='command' && previous?.id===p.expected.id && previous?.view.state.revision===p.expected.revision)steps=buildSteps(previous.view.state.slots,response.events);
      } catch {steps=[];}
      if(steps.length)playback.value={view:previous.view,events:response.events};
      record.value=saved;pending.value=null;
      resetSelection();setupOpen.value=false;page.value='lab';message.value='';
      announce();
      try {
        if(steps.length){
          await nextTick();
          document.querySelector('.chamber-panel')?.scrollIntoView({block:'nearest',behavior:'instant'});
          await player.play(steps,{getSpeed:()=>animationSpeed.value,onFrame:frame=>{animationFrame.value=frame;}});
        }
      } catch {message.value='动画已结束，当前显示本次结算结果。';}
      finally {playback.value=null;animationFrame.value=null;}
      await refreshLocal();
    }
    async function execute(p) {
      if(busy.value)return;
      busy.value=true;error.value='';message.value='';
      try {
        await store.stagePending(p);pending.value=p;await applyPending(p);
      } catch(e) {
        if(e.status && e.status>=400 && e.status<500 && e.status!==429){await store.clearRejected(p.request.requestId).catch(()=>{});}
        error.value=e.message;
        try {await refreshLocal();} catch(saveError){error.value+=` 本地存储异常：${saveError.message}`;}
      } finally {busy.value=false;}
    }
    async function startRun(options) {
      if(busy.value || pending.value)return;
      const previous=options?.view?.state;
      const request={userId:profile.value.id,petRefreshes:previous ? previous.initialPetRefreshes : pet.value,seed:previous ? previous.seed : seedInput.value.trim(),requestId:crypto.randomUUID()};
      if(previous)Object.assign(request,{rulesVersion:previous.rulesVersion,contentVersion:previous.contentVersion,rngVersion:previous.rngVersion});
      await execute({kind:'create',expected:store.stamp(record.value),path:'/api/v1/runs',request});
    }
    const repeatRun=run=>startRun(run);
    async function sendCommand(command) {
      if(locked.value)return;
      await execute({kind:'command',expected:store.stamp(record.value),path:`/api/v1/runs/${state.value.runId}/commands`,request:{stateToken:record.value.stateToken,expectedRevision:state.value.revision,requestId:crypto.randomUUID(),command:{...command,offerId:state.value.offer.id}}});
    }
    const refresh=()=>sendCommand({type:'refresh'});
    const skipUnknown=()=>sendCommand({type:'skip_unknown'});
    const confirmChoice=()=>canConfirm.value && sendCommand({type:'choose',cardId:selectedCard.value.definition.id,targetIds:[...targets.value]});
    async function retryPending() {
      if(busy.value)return;
      busy.value=true;error.value='';
      try {
        const p=await store.getPending();pending.value=p;
        if(p){try{await applyPending(p);}catch(e){if(e.status&&e.status>=400&&e.status<500&&e.status!==429){await store.clearRejected(p.request.requestId);await refreshLocal();}throw e;}}
        else {record.value=await store.getCurrent();connected.value=false;message.value='其他标签页已完成保存，请点击重新连接。';}
      }catch(e){error.value=e.message;}finally{busy.value=false;}
    }
    async function restoreCurrent() {
      if(busy.value)return;
      busy.value=true;error.value='';message.value='';connected.value=false;
      try {
        await refreshLocal();record.value=await store.getCurrent();resetSelection();
        if(pending.value){message.value='请先重试待保存操作。';return;}
        if(record.value){const response=await api(`/api/v1/runs/${record.value.id}/restore`,{stateToken:record.value.stateToken});record.value={...record.value,view:response.view};}
      }catch(e){error.value=e.message;}finally{busy.value=false;}
    }
    async function showHistory() {if(busy.value)return;page.value='history';try{await refreshLocal();}catch(e){error.value=`无法读取历史：${e.message}`;}}
    async function resumeRun(run) {
      if(busy.value || pending.value)return;
      try {record.value=await store.switchCurrent(run.id,store.stamp(record.value));page.value='lab';setupOpen.value=false;announce();await restoreCurrent();}
      catch(e){error.value=e.message;}
    }
    async function verifyRun(run) {
      if(busy.value)return;busy.value=true;error.value='';message.value='';
      try {
        const s=run.view.state;
        const result=await api('/api/v1/replays/verify',{seed:s.seed,rulesVersion:s.rulesVersion,contentVersion:s.contentVersion,rngVersion:s.rngVersion,petRefreshes:s.initialPetRefreshes,commands:run.commands,expectedGameplayHash:run.gameplayHash});
        message.value=result.verified ? `复现验证通过：${run.commands.length} 次操作后，完整游戏状态一致，分数 ${number(result.view.state.score)}。` : '已重放记录，但缺少原始摘要，无法确认一致性。';
      }catch(e){error.value=e.message;}finally{busy.value=false;}
    }
    async function copySeed(){try{await navigator.clipboard.writeText(state.value.seed);message.value='种子已复制。';}catch{error.value='无法访问剪贴板，请从页面底部选中种子复制。';}}
    async function onOtherTab(){if(busy.value)return;try{await refreshLocal();const current=await store.getCurrent();if(current?.id!==record.value?.id||current?.view.state.revision!==state.value?.revision){connected.value=false;message.value='其他标签页更新了进度。请点击重新连接加载最新存档。';}}catch(e){error.value=e.message;}}
    if(channel)channel.onmessage=onOtherTab;
    onMounted(async()=>{
      document.getElementById('boot-status').remove();
      applyTheme(themePreference.value);
      systemTheme?.addEventListener?.('change',onSystemThemeChange);
      try {profile.value=await store.identity();ready.value=true;record.value=await store.getCurrent();await refreshLocal();if(pending.value)await retryPending();else if(record.value)await restoreCurrent();}
      catch(e){error.value=`本地存储初始化失败：${e.message}。请使用允许网站存储的浏览器，通过 localhost 或 HTTPS 访问。`;ready.value=false;}
    });
    onUnmounted(()=>{player.cancel();channel?.close();systemTheme?.removeEventListener?.('change',onSystemThemeChange);});
    return {page,record,profile,history,busy,ready,connected,pending,error,message,setupOpen,pet,seedInput,selectedIndex,targets,themePreference,themeOpen,themeIcon,setTheme,state,view,selectedCard,locked,occupied,canConfirm,targetHint,savedLabel,familyNames,familySymbols,monsterRarities,potionRarities,kindNames,number,date,cardIcon,slotLabel,canTarget,selectCard,toggleTarget,startRun,repeatRun,refresh,skipUnknown,confirmChoice,retryPending,restoreCurrent,showHistory,resumeRun,verifyRun,copySeed,SPEEDS,animationSpeed,animating,animationFrame,activeEffect,effectStyle,slotEffect,slotEffectClass,effectDelta,signed,setAnimationSpeed,visibleEvents,eventLabel,eventSource};
  }
}).mount('#app');
