export const JAR_NAMES = Object.freeze({EMPTY:'空奖励位', JAR_WHITE:'白色奖励罐', PURPLE:'紫色奖励罐', JAR_RED:'红色奖励罐', RAINBOW:'彩色奖励罐'});

export function rewardJars(rewards) {
  if (!Array.isArray(rewards?.jars)) return [];
  return rewards.jars.map(color=>({
    color: Object.hasOwn(JAR_NAMES, color) ? color.toLowerCase() : 'unknown',
    label: JAR_NAMES[color] ?? '未知奖励罐',
    symbol: color === 'EMPTY' ? '·' : '◆'
  }));
}

export default {
  name: 'RewardJars',
  props: {rewards: {type:Object, default:null}},
  computed: {jars() { return rewardJars(this.rewards); }},
  template: `<div class="reward-jars" role="group" aria-label="奖励罐"><span v-for="(jar,index) in jars" :key="index" :class="['reward-jar',jar.color]" :title="jar.label" :aria-label="jar.label" role="img">{{ jar.symbol }}</span><span v-if="!jars.length" class="reward-jars-missing">暂无奖励记录</span></div>`
};
