export const FAMILY_NAMES = Object.freeze({BONE:'骨卫兵', FIEND:'异魔', AWAKENER:'觉醒者', INSECT:'蛊虫'});
export const FAMILY_SYMBOLS = Object.freeze({BONE:'⟐', FIEND:'⟁', AWAKENER:'✧', INSECT:'❋'});

const terms = Object.freeze({
  活性: {kind:'activity'}, 数量: {kind:'quantity'},
  ...Object.fromEntries(Object.entries(FAMILY_NAMES).map(([family, name])=>[name, {kind:family.toLowerCase(), symbol:FAMILY_SYMBOLS[family]}]))
});
const pattern = new RegExp(`(${Object.keys(terms).join('|')})`, 'g');

export function tokenizeTerms(text) {
  return String(text ?? '').split(pattern).filter(Boolean).map(text=>({text, ...(terms[text] || {})}));
}

export default {
  name: 'TermText',
  props: {text: {type:String, default:''}},
  computed: {tokens() { return tokenizeTerms(this.text); }},
  template: `<span class="term-text"><template v-for="(token,index) in tokens" :key="index"><span v-if="token.kind" class="term-token"><svg v-if="token.kind==='activity'" class="term-icon term-icon-activity" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path fill="currentColor" d="M12 21S2 15.1 2 8.3C2 3.2 8.3 1.7 12 6c3.7-4.3 10-2.8 10 2.3C22 15.1 12 21 12 21Z"/><path d="M5.2 8.5c0-1.5 1-2.4 2.2-2.4" fill="none" stroke="#fff" stroke-opacity=".65" stroke-width="1.7" stroke-linecap="round"/></svg><svg v-else-if="token.kind==='quantity'" class="term-icon term-icon-quantity" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path fill="currentColor" d="M5.5 3a2 2 0 0 0-2 2 2 2 0 1 0 2 3.3l6.2 6.2a2 2 0 1 0 3.3 2 2 2 0 1 0-2-3.3L6.8 7a2 2 0 0 0-1.3-4Z"/><path d="m15 4 3 1-1 4-3-1ZM4 13l3 2-2 3-3-1ZM17 16l4-2 1 4-3 2Z" fill="currentColor" opacity=".7"/><circle cx="11" cy="4" r="1" fill="currentColor"/><circle cx="10" cy="20" r="1.2" fill="currentColor"/><circle cx="21" cy="10" r="1" fill="currentColor"/></svg><span v-else :class="['term-icon','term-icon-'+token.kind]" aria-hidden="true">{{ token.symbol }}</span>{{ token.text }}</span><template v-else>{{ token.text }}</template></template></span>`
};
