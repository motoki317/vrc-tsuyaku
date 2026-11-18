const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');

const source = readFileSync('public/app.js', 'utf8').replace(/\nstart\(\);\s*$/, '');
const catalogs = Object.fromEntries(['en', 'ja'].map(lang => [lang, JSON.parse(readFileSync('public/i18n/' + lang + '.json'))]));
const drain = () => new Promise(resolve => setImmediate(resolve));

function page() {
  const nodes = new Map(), timers = new Map(), requests = [];
  let nextTimer = 0, now = 0;
  function element() {
    const listeners = {};
    return {
      dataset: {}, style: {}, children: [], value: '', hidden: false,
      get lastElementChild() { return this.children.at(-1); },
      get valueAsNumber() { return Number(this.value); },
      get textContent() { return this.children.map(child => child.textContent ?? child).join(''); },
      set textContent(value) { this.replaceChildren(value); },
      append(...children) { this.children.push(...children); },
      removeChild(child) { this.children.splice(this.children.indexOf(child), 1); },
      replaceChildren(...children) { this.children = children; },
      setAttribute(key, value) { this[key] = value; },
      removeAttribute(key) { delete this[key]; },
      addEventListener(type, fn) { (listeners[type] ||= []).push(fn); },
      fire(type) { for (const fn of listeners[type] || []) fn({target: this}); },
    };
  }
  const node = id => {
    if (!nodes.has(id)) nodes.set(id, element());
    return nodes.get(id);
  };
  const context = vm.createContext({
    catalogs, structuredClone, document: {createElement: element, getElementById: node},
    setTimeout(fn, ms) { timers.set(++nextTimer, {fn, at: now + ms}); return nextTimer; },
    clearTimeout(id) { timers.delete(id); },
    fetch(path, options) {
      return new Promise((resolve, reject) => requests.push({path, body: JSON.parse(options.body), reject,
        reply: (body, status = 200) => resolve({ok: status === 200, status, json: async () => body})}));
    },
  });
  vm.runInContext(source, context);
  const ui = vm.runInContext(`
    strings = catalogs;
    state = {reading: {base: {min: 1, max: 10}, factor: {min: 1, max: 8}}, settings: {
      Chatbox: {ReadingBase: 2, ReadingFactor: 2, Template: '{{.Translation}}', Address: 'localhost'},
      Flows: {Self: {Spoken: 'en', Target: 'ko'}}
    }};
    screen = 'settings'; sub = 'chatbox';
    apply = next => { state.settings = next.settings; render(); };
    render = renderChatbox;
    showConnection = () => {};
    flashSaved = id => { $(id + '-saved').hidden = false; };
    buildChatbox();
    ({state, render, runPreview, phrases, locale(value) {
      lang = value; displayNames = new Intl.DisplayNames([lang], {type: 'language'}); renderPreview();
    }})
  `, context);
  ui.locale('en');
  ui.render();
  return {...ui, node, requests, async tick(ms) {
    now += ms;
    for (const [id, timer] of timers) if (timer.at <= now) { timers.delete(id); timer.fn(); }
    await drain();
  }};
}

test('sliders preview a drag before saving, preserve other settings, and follow external edits', async () => {
  const ui = page(), base = ui.node('reading-base'), factor = ui.node('reading-factor');
  assert.deepEqual([base.min, base.max, factor.min, factor.max], [1, 10, 1, 8]);
  assert.equal(base['aria-valuetext'], '2 s');
  assert.equal(factor['aria-valuetext'], '×2.0');
  for (const [slider, field, value] of [[base, 'ReadingBase', 3.5], [factor, 'ReadingFactor', 2.7]]) {
    slider.value = 3;
    slider.fire('input');
    await ui.tick(200);
    slider.value = value;
    slider.fire('input');
    ui.render();
    assert.equal(slider.value, value);
    const count = ui.requests.length;
    await ui.tick(100);
    assert.deepEqual(ui.requests.slice(count).map(r => r.path), ['/api/chatbox/preview']);
    assert.equal(ui.requests.at(-1).body.sourceLang, 'ja');
    assert.equal(ui.requests.at(-1).body.targetLang, 'en');
    assert.equal(ui.requests.at(-1).body[field === 'ReadingBase' ? 'readingBase' : 'readingFactor'], value);
    slider.fire('change');
    await ui.tick(500);
    const save = ui.requests.at(-1);
    assert.equal(save.path, '/api/settings');
    assert.deepEqual(save.body.Chatbox, {...ui.state.settings.Chatbox, [field]: value});
    save.reply({settings: save.body});
    await drain();
    assert.equal(slider.dataset.dirty, undefined);
  }
  ui.state.settings.Chatbox.ReadingBase = 4.5;
  ui.state.reading.base.max = 9;
  ui.state.settingsError = 'invalid file';
  ui.render();
  assert.equal(base.value, 4.5);
  assert.equal(base.max, 9);
  assert.ok(base.disabled && factor.disabled);
  await ui.tick(300);
  assert.equal(ui.requests.at(-1).body.readingBase, 4.5);
});

test('only a successful current preview replaces the localized example', async () => {
  const ui = page(), p = ui.node('preview'), line = ui.node('reading-example');
  ui.phrases.set(1, {flow: 'self', original: 'こんにちは', translation: 'Hello', source: 'ja', target: 'en'});
  const first = ui.runPreview(), old = ui.requests.at(-1);
  assert.equal(old.body.original, 'こんにちは');
  const current = ui.runPreview(), latest = ui.requests.at(-1);
  latest.reply({pages: ['Hello'], seconds: [3.706], reading: {characters: 5, secondsPerCharacter: 0.17}});
  await current;
  old.reply({pages: ['stale'], seconds: [10], reading: {characters: 80, secondsPerCharacter: 0.05}});
  await first;
  assert.equal(p.textContent, 'Hello');
  const bar = p.children[0].lastElementChild;
  assert.equal(bar['aria-hidden'], 'true');
  assert.equal(bar.style.animationDuration, '3.706s');
  assert.equal(line.textContent, '3.71 s = 2 s + 5 characters × 0.17 s/character × 2');
  const failed = ui.runPreview();
  ui.requests.at(-1).reply({error: 'bad template'}, 400);
  await failed;
  assert.equal(p.textContent, 'Hello');
  assert.match(line.textContent, /^3.71 s/);
  const offline = ui.runPreview();
  ui.requests.at(-1).reject(new Error('disconnected'));
  await offline;
  assert.equal(p.textContent, 'Hello');
  const count = ui.requests.length;
  ui.render();
  await ui.tick(300);
  assert.equal(ui.requests.length, count + 1);
  ui.requests.at(-1).reply({pages: ['Hello'], seconds: [3.706], reading: {characters: 5, secondsPerCharacter: 0.17}});
  await drain();
  ui.locale('ja');
  assert.equal(p.children[0].lastElementChild, bar);
  assert.equal(line.textContent, '3.71秒 = 2秒 + 5文字 × 0.17秒/文字 × 2');
  ui.state.settings.Chatbox.ReadingFactor = 2.5;
  const multiple = ui.runPreview();
  ui.requests.at(-1).reply({pages: ['a', 'b'], seconds: [16.4, 3.6], reading: {characters: 160, secondsPerCharacter: 0.05}});
  await multiple;
  assert.equal(p.children[0].textContent, '1/2 · 16.4秒a');
  assert.equal(p.children[1].textContent, '2/2 · 3.6秒b');
  assert.deepEqual(p.children.map(page => page.lastElementChild.style.animationDuration), ['16.4s', '3.6s']);
  assert.equal(line.textContent, '2秒 + 160文字 × 0.05秒/文字 × 2.5');
  const empty = ui.runPreview();
  ui.requests.at(-1).reply({pages: [], seconds: [], reading: {characters: 0, secondsPerCharacter: 0.17}});
  await empty;
  assert.ok(line.hidden);
  assert.equal(p.children.length, 0);
});
