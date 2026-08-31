import TermText from './term-text.mjs';

export default {
  components: {TermText},
  props: ['view','locked','selectedIndex','selectedHint','canConfirm','busy'],
  emits: ['select','refresh','skip','confirm'],
  setup() {
    return {
      kindNames:{UNKNOWN:'未知手术器具',POTION:'药剂',TOOL:'手术用具',SCHEME:'手术方案'},
      rarityNames:{WHITE:'普通',BLUE:'魔法',GOLD:'稀有',RED:'至臻'},
      icons:{UNKNOWN:'⌁',TOOL:'✦',SCHEME:'≋',POTION:'◈'}
    };
  },
  template: `<section class="choices panel" aria-label="当前选牌">
    <div class="section-title"><div><div class="eyebrow">YOUR NEXT MOVE</div><h2>{{ view.stageLabel }}</h2></div>
      <button v-if="['POTION','TOOL'].includes(view.state.offer.kind)" class="button secondary small" :disabled="!view.canRefresh || locked" @click="$emit('refresh')">↻ 刷新 <span>{{ view.state.offer.kind==='TOOL' ? view.state.toolRefreshes : view.state.potionRefreshes }}</span></button>
    </div>
    <div class="cards" :class="{'single-card':view.cards.length===1}">
      <button v-for="(card,index) in view.cards" :key="view.state.offer.id+'-'+index" :class="['card',card.definition.rarity.toLowerCase(),{selected:selectedIndex===index}]" :disabled="locked || !card.playable" @click="$emit('select',index)">
        <span class="card-top"><span>{{ kindNames[card.definition.kind] }}</span><span>{{ rarityNames[card.definition.rarity] || '用具' }}</span></span>
        <span class="card-icon">{{ icons[card.definition.kind] }}</span><strong>{{ card.definition.name }}</strong>
        <term-text class="card-description" :text="card.definition.description"></term-text><span class="card-bottom">{{ !card.playable ? '当前没有合法目标' : selectedIndex===index ? '已选择 ✓' : '选择此卡 →' }}</span>
      </button>
    </div>
    <div class="choice-footer"><span>{{ selectedHint || '先选择卡牌，再确认操作。' }}</span><div class="actions">
      <button v-if="view.canSkip" class="button ghost" :disabled="locked" @click="$emit('skip')">跳过，随机三组</button>
      <button class="button primary" :disabled="!canConfirm || locked" @click="$emit('confirm')">{{ busy ? '结算中…' : '确认选择 →' }}</button>
    </div></div>
  </section>`
};
