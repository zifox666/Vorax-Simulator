import test from 'node:test';
import assert from 'node:assert/strict';
import TermText, {tokenizeTerms, FAMILY_NAMES, FAMILY_SYMBOLS} from '../web/assets/term-text.mjs';

test('all four family terms use the same symbols as the board',()=>{
  for(const [family,name] of Object.entries(FAMILY_NAMES)){
    assert.deepEqual(tokenizeTerms(name),[{text:name,kind:family.toLowerCase(),symbol:FAMILY_SYMBOLS[family]}]);
  }
  assert.deepEqual(FAMILY_SYMBOLS,{BONE:'⟐',FIEND:'⟁',AWAKENER:'✧',INSECT:'❋'});
});

test('stat and family icons preserve every word, number and punctuation in descriptions',()=>{
  const text='移除非骨卫兵的怪物时，随机1组异魔+150数量；觉醒者、蛊虫+35活性。';
  const tokens=tokenizeTerms(text);
  assert.equal(tokens.map(token=>token.text).join(''),text);
  assert.deepEqual(tokens.filter(token=>token.kind).map(token=>token.kind),['bone','fiend','quantity','awakener','insect','activity']);
});

test('adjacent and repeated stat labels each receive an icon',()=>{
  assert.deepEqual(tokenizeTerms('活性数量活性').map(token=>token.kind),['activity','quantity','activity']);
  assert.equal(tokenizeTerms('活性 +41')[0].kind,'activity');
  assert.equal(tokenizeTerms('数量 +30')[0].kind,'quantity');
  assert.deepEqual(tokenizeTerms(null),[]);
  assert.deepEqual(tokenizeTerms('手术用具'),[{text:'手术用具'}]);
});

test('external descriptions stay plain text and decorative icons are hidden from assistive readers',()=>{
  const text='<img src=x onerror=alert(1)>活性';
  assert.equal(tokenizeTerms(text).map(token=>token.text).join(''),text);
  assert.ok(!TermText.template.includes('v-html'));
  assert.equal((TermText.template.match(/aria-hidden="true"/g)||[]).length,3);
  assert.ok(TermText.template.includes('{{ token.text }}'));
});
