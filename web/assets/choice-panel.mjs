import TermText from './term-text.mjs';

export default {
  components: {TermText},
  props: ['view','locked','selectedIndex','selectedHint','canConfirm','busy',
          'aiEnabled','aiLoading','aiError','aiStrategy','aiSuggestion','aiCardIndex','aiText'],
  emits: ['select','refresh','skip','confirm','toggle-ai','ai-strategy','apply-ai'],
  setup() {
    const strategies = [
      {id:'random', label:'随机'},
      {id:'greedy', label:'贪心'},
      {id:'sampler', label:'采样'},
    ];
    return {
      kindNames:{UNKNOWN:'未知手术器具',POTION:'药剂',TOOL:'手术用具',SCHEME:'手术方案'},
      rarityNames:{WHITE:'普通',BLUE:'魔法',GOLD:'稀有',RED:'至臻'},
      icons:{UNKNOWN:'⌁',TOOL:'✦',SCHEME:'≋',POTION:'◈'},
      strategies,
    };
  },
  template: `<section class="choices panel">
    <div class="section-title">
      <div><div class="eyebrow">YOUR NEXT MOVE</div><h2>{{ view.stageLabel }}</h2></div>
      <div class="choice-actions">
        <div v-if="aiEnabled" class="ai-strategy segmented-mini" role="group" aria-label="AI 策略">
          <button v-for="s in strategies" :key="s.id" type="button"
            :class="{selected:aiStrategy===s.id}" :aria-pressed="aiStrategy===s.id"
            :disabled="locked" @click="$emit('ai-strategy',s.id)">{{ s.label }}</button>
        </div>
        <button class="ai-toggle" :class="{on:aiEnabled}" :aria-pressed="aiEnabled" :disabled="locked" @click="$emit('toggle-ai')">
          <span class="ai-toggle-dot"></span>AI 推荐</button>
        <button v-if="['POTION','TOOL'].includes(view.state.offer.kind)" class="button secondary small" :disabled="!view.canRefresh || locked" @click="$emit('refresh')">↻ 刷新 <span>{{ view.state.offer.kind==='TOOL' ? view.state.toolRefreshes : view.state.potionRefreshes }}</span></button>
      </div>
    </div>
    <div v-if="aiEnabled" class="ai-banner" :class="{loading:aiLoading&&!aiError, error:!!aiError}" role="status" aria-live="polite">
      <template v-if="aiLoading && !aiError"><span class="ai-spinner" aria-hidden="true"></span><span>AI 正在推演候选…</span></template>
      <template v-else-if="aiError"><span aria-hidden="true">⚠</span><span>AI 建议暂不可用：{{ aiError }}</span></template>
      <template v-else-if="aiSuggestion && aiSuggestion.action">
        <span class="ai-idea" aria-hidden="true">✦</span>
        <span class="ai-banner-text">{{ aiText }}</span>
        <span class="ai-strategy-tag">{{ strategyLabel(aiSuggestion.strategy) }}</span>
        <button class="button primary small ai-apply" :disabled="locked" @click="$emit('apply-ai')">{{ aiSuggestion.action.type==='choose' ? '采纳并预选' : '采纳建议' }}</button>
      </template>
      <template v-else><span aria-hidden="true">✦</span><span>当前候选已就绪，AI 尚未给出建议</span></template>
    </div>
    <div class="cards" :class="{'single-card':view.cards.length===1}">
      <button v-for="(card,index) in view.cards" :key="view.state.offer.id+'-'+index" :class="['card',card.definition.rarity.toLowerCase(),{selected:selectedIndex===index,'ai-recommended':aiEnabled&&aiCardIndex===index}]" :disabled="locked || !card.playable" @click="$emit('select',index)">
        <span v-if="aiEnabled && aiCardIndex===index" class="ai-card-badge">AI 推荐</span>
        <span class="card-top"><span>{{ kindNames[card.definition.kind] }}</span><span>{{ rarityNames[card.definition.rarity] || '用具' }}</span></span>
        <span class="card-icon">{{ icons[card.definition.kind] }}</span><strong>{{ card.definition.name }}</strong>
        <term-text class="card-description" :text="card.definition.description"></term-text><span class="card-bottom">{{ !card.playable ? '当前没有合法目标' : selectedIndex===index ? '已选择 ✓' : '选择此卡 →' }}</span>
      </button>
    </div>
    <div class="choice-footer"><span>{{ selectedHint || '先选择卡牌，再确认操作。' }}</span><div class="actions">
      <button v-if="view.canSkip" class="button ghost" :disabled="locked" @click="$emit('skip')">跳过，随机三组</button>
      <button class="button primary" :disabled="!canConfirm || locked" @click="$emit('confirm')">{{ busy ? '结算中…' : '确认选择 →' }}</button>
    </div></div>
  </section>`,
  methods: {
    strategyLabel(s) {
      return ({random:'随机', greedy:'贪心', sampler:'采样推演'})[s] || s;
    },
  },
};
