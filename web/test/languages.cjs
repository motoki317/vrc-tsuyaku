const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');

const source = readFileSync('public/app.js', 'utf8').replace(/\nstart\(\);\s*$/, '');
const context = vm.createContext({
  document: {createElement: () => ({append() {}, setAttribute() {}})},
  setTimeout, clearTimeout,
});
vm.runInContext(source, context);
const {swap, commitOnSettle} = vm.runInContext('({swap: swappedLanguages, commitOnSettle})', context);
const plain = value => JSON.parse(JSON.stringify(value));
const pair = (Spoken, Target) => ({Spoken, Target});
const languages = Object.freeze({
  spoken: Object.freeze(['ja', 'en', 'de', 'pt', 'sv', 'auto'].map(code => Object.freeze({code, auto: code === 'auto'}))),
  targets: Object.freeze(['ja', 'ja-easy', 'en', 'de', 'ko']),
  variants: Object.freeze({'ja-easy': 'ja'}),
});

test('both flows swap the screen pair and use a variant target base', () => {
  for (const flow of ['self', 'world']) {
    for (const [input, want] of [
      [pair('ja', 'en'), pair('en', 'ja')],
      [pair('en', 'ja'), pair('ja', 'en')],
      [pair('de', 'en'), pair('en', 'de')],
      [pair('en', 'ja-easy'), pair('ja', 'en')],
      [pair('ja', 'ja-easy'), pair('ja', 'ja')],
      [pair('en', 'en'), pair('en', 'en')],
    ]) {
      assert.deepEqual(plain(swap(languages, flow, Object.freeze(input), null)), want);
    }
  }
});

test('Detect, spoken languages absent from targets, and targets without a spoken choice cannot swap', () => {
  for (const flow of ['self', 'world']) {
    for (const input of [pair('auto', 'en'), pair('pt', 'ja'), pair('sv', 'ja'), pair('en', 'ko')]) {
      assert.equal(swap(languages, flow, input, null), null);
    }
  }
  assert.equal(swap({...languages, targets: [...languages.targets, 'auto']}, 'world', pair('auto', 'en'), null), null);
});

test('a settings error disables an otherwise selectable swap', () => {
  for (const flow of ['self', 'world']) {
    assert.equal(swap(languages, flow, pair('en', 'ja'), 'invalid settings'), null);
  }
});

test('a manual commit absorbs a pending change and keeps later edits through an older answer', async () => {
  const listeners = {}, timers = new Map(), requests = [];
  let nextTimer = 0, renders = 0, current = pair('en', 'ja');
  context.setTimeout = fn => { timers.set(++nextTimer, fn); return nextTimer; };
  context.clearTimeout = id => timers.delete(id);
  vm.runInContext('render = () => onRender()', context);
  context.onRender = () => { renders++; };
  const field = {dataset: {}, addEventListener: (type, fn) => { listeners[type] = fn; }, contains: node => node === field};
  const releases = [];
  const commit = commitOnSettle(field, value => new Promise(resolve => {
    requests.push(value);
    releases.push(resolve);
  }), () => current);
  current = pair('de', 'ja-easy');
  listeners.change();
  listeners.focusout({relatedTarget: field});
  assert.equal(requests.length, 0);
  current = swap(languages, 'self', current, null);
  commit();
  assert.deepEqual(plain(requests), [pair('ja', 'de')]);
  assert.equal(timers.size, 0);
  field.settle();
  assert.equal(requests.length, 1);
  current = pair('en', 'de');
  listeners.input();
  releases.shift()();
  await Promise.resolve();
  assert.equal(field.dataset.dirty, 'changed');
  listeners.focusout({relatedTarget: null});
  assert.deepEqual(plain(requests), [pair('ja', 'de'), pair('en', 'de')]);
  releases.shift()();
  await Promise.resolve();
  assert.equal(field.dataset.dirty, undefined);
  assert.equal(renders, 2);
});
