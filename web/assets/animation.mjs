export const SPEEDS = Object.freeze([1, 2, 4, 8]);
export const SPEED_KEY = 'vorax-animation-speed';

export function readSpeed(storage) {
  try {
    const speed = Number((storage ?? window.localStorage).getItem(SPEED_KEY));
    return SPEEDS.includes(speed) ? speed : 1;
  } catch { return 1; }
}

export function saveSpeed(speed, storage) {
  if (!SPEEDS.includes(speed)) return false;
  try {
    (storage ?? window.localStorage).setItem(SPEED_KEY, String(speed));
    return true;
  } catch { return false; }
}

const integer = value => BigInt(value ?? 0);
const cloneSlots = slots => slots.map(slot => ({index: slot.index, monster: slot.monster ? {...slot.monster} : null}));
const rank = {NORMAL: 1, MAGIC: 2, RARE: 3, BOSS: 4};
const effectLabels = {
  stats_changed: '属性变化', mutated: '变异', awakened: '觉醒', upgraded: '升阶',
  fused: '融合', removed: '移除', added: '添加', devoured: '吞噬',
  tool_acquired: '获得手术用具', turn_end: '回合结束', scored: '结算完成',
  overflow: '培养槽已满', refreshed: '候选已刷新', box_opened: '打开药剂箱'
};

export function eventLabel(event) { return effectLabels[event.kind] || '效果生效'; }

export function interpolateInteger(from, to, progress) {
  const p = BigInt(Math.round(Math.max(0, Math.min(1, progress)) * 1000000));
  const a = integer(from), b = integer(to);
  return String(a + (b - a) * p / 1000000n);
}

export function withContributions(slots) {
  return slots.map(slot => ({...slot, contribution: slot.monster ? String(integer(slot.monster.activity) * integer(slot.monster.quantity)) : '0'}));
}

export function totalContribution(slots) {
  return String(slots.reduce((total, slot) => total + integer(slot.contribution), 0n));
}

function makeStep(event, before, after, field, ordinal) {
  const ids = new Set(event.targetIds || []);
  const affected = before.map((slot, index) => {
    const old = slot.monster, next = after[index].monster;
    const changed = JSON.stringify(old) !== JSON.stringify(next);
    if (!changed && !ids.has(old?.id) && !ids.has(next?.id)) return null;
    const same = old?.id && old.id === next?.id;
    const delta = key => next ? integer(next[key]) - (same ? integer(old[key]) : 0n) : 0n;
    return {
      index: slot.index, before: old, after: next,
      activityDelta: String(delta('activity')), quantityDelta: String(delta('quantity')),
      upgraded: same && rank[next.rarity] > rank[old.rarity],
      role: next ? 'destination' : 'outgoing'
    };
  }).filter(Boolean);
  const kind = event.kind;
  const label = kind === 'stats_changed' ? (field === 'activity' ? '活性变化' : '数量变化')
    : kind === 'awakened' && affected.some(slot => slot.upgraded) ? '觉醒 · 升阶' : eventLabel(event);
  const duration = {stats_changed: 900, mutated: 1200, awakened: 1300, upgraded: 1300, fused: 1400, devoured: 1200, removed: 950, added: 1100}[kind] || 450;
  return {key: `${event.sequence}-${ordinal}`, event, kind, field, label, before, after, affected, duration};
}

export function buildSteps(initialSlots, events = []) {
  if (!events.length || events.some(event => !Array.isArray(event.slotsAfter) || event.slotsAfter.length !== initialSlots.length || event.slotsAfter.some((slot, i) => slot.index !== initialSlots[i].index))) return [];
  let before = cloneSlots(initialSlots);
  const steps = [];
  for (const event of events) {
    const after = cloneSlots(event.slotsAfter);
    if (event.kind === 'stats_changed') {
      const fields = ['activity', 'quantity'].filter(field => after.some((slot, i) => slot.monster && slot.monster.id === before[i].monster?.id && integer(slot.monster[field]) !== integer(before[i].monster[field])));
      for (const field of fields) {
        const partial = cloneSlots(before);
        for (let i = 0; i < partial.length; i++) {
          if (partial[i].monster && partial[i].monster.id === after[i].monster?.id) partial[i].monster[field] = after[i].monster[field];
        }
        steps.push(makeStep(event, before, partial, field, steps.length));
        before = partial;
      }
    } else {
      steps.push(makeStep(event, before, after, null, steps.length));
    }
    before = after;
  }
  return steps;
}

const clamp = value => Math.max(0, Math.min(1, value));
const ease = value => 1 - (1 - clamp(value)) ** 3;

export function sampleStep(step, progress) {
  const p = clamp(progress);
  const structural = ['mutated', 'awakened', 'upgraded', 'fused', 'devoured', 'added'].includes(step.kind);
  const switchAt = step.kind === 'removed' ? 0.72 : structural ? 0.5 : 0;
  const slots = cloneSlots(p < switchAt ? step.before : step.after);
  const amount = ease(structural ? (p - 0.5) / 0.38 : (p - 0.12) / 0.7);
  for (const slot of slots) {
    const before = step.before.find(old => old.index === slot.index)?.monster;
    const after = step.after.find(next => next.index === slot.index)?.monster;
    if (!slot.monster || !after || (structural && p < switchAt)) continue;
    const same = before?.id === after.id;
    for (const field of ['activity', 'quantity']) {
      slot.monster[field] = interpolateInteger(same ? before[field] : 0, after[field], amount);
    }
  }
  return withContributions(slots);
}

export function createPlayer({requestFrame = callback => requestAnimationFrame(callback), cancelFrame = id => cancelAnimationFrame(id), now = () => performance.now()} = {}) {
  let stop;
  return {
    cancel() { stop?.(); },
    async play(steps, {getSpeed, onFrame}) {
      stop?.();
      let cancelled = false;
      let finishFrame;
      const cancel = () => { cancelled = true; finishFrame?.(); };
      stop = cancel;
      try {
        for (let index = 0; index < steps.length && !cancelled; index++) {
          const step = steps[index];
          await new Promise((resolve, reject) => {
            let frame, last = now(), elapsed = 0;
            finishFrame = () => { if (frame !== undefined) cancelFrame(frame); resolve(); };
            const tick = time => {
              try {
                const speed = getSpeed();
                elapsed += Math.max(0, Math.min(64, time - last)) * (SPEEDS.includes(speed) ? speed : 1);
                last = time;
                const progress = Math.min(1, elapsed / step.duration);
                onFrame({step, index, total: steps.length, progress, slots: sampleStep(step, progress)});
                if (progress === 1 || cancelled) resolve();
                else frame = requestFrame(tick);
              } catch (error) { reject(error); }
            };
            tick(last);
          });
        }
      } finally { if (stop === cancel) stop = null; finishFrame = null; }
    }
  };
}
