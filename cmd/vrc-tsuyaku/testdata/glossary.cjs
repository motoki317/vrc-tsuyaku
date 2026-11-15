const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');

const source = readFileSync('ui/app.js', 'utf8').replace(/\nstart\(\);\s*$/, '');
const entry = (Source = '桔梗', Target = 'Kikyo', Reading = 'ききょう') => ({SourceLang: 'ja', Source, TargetLang: 'en', Target, Reading});
const plain = value => JSON.parse(JSON.stringify(value));
const drain = () => new Promise(resolve => setImmediate(resolve));

function editor(saved = []) {
  const requests = [], announcements = [];
  const events = {}, nodes = new Map();
  const document = {activeElement: null};
  function element(tag = 'div') {
    const attributes = new Map();
    return {
      tagName: tag.toUpperCase(), children: [], dataset: {}, value: '', hidden: false, events: {},
      classList: {add() {}, remove() {}},
      get options() { return this.children; },
      get textContent() { return this.children.map(child => typeof child === 'string' ? child : child.textContent).join(''); },
      set textContent(text) { this.replaceChildren(text); },
      append(...children) {
        for (const child of children) {
          if (typeof child !== 'string') { child.remove(); child.parentNode = this; }
          this.children.push(child);
        }
      },
      replaceChildren(...children) {
        for (const child of this.children) if (typeof child !== 'string') child.parentNode = null;
        this.children = [];
        this.append(...children);
      },
      remove() {
        if (this.parentNode) this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1);
        this.parentNode = null;
      },
      contains(node) { return this === node || this.children.some(child => typeof child !== 'string' && child.contains(node)); },
      add(option) { this.append(option); },
      setAttribute(name, value) { attributes.set(name, String(value)); },
      getAttribute(name) { return attributes.get(name) ?? null; },
      removeAttribute(name) { attributes.delete(name); },
      addEventListener(type, fn) { this.events[type] = fn; },
      focus() { document.activeElement = this; },
    };
  }
  document.createElement = element;
  document.createElementNS = (_namespace, tag) => element(tag);
  document.getElementById = id => {
    if (!nodes.has(id)) {
      const node = element();
      if (id === 'glossary-list') node.events = events;
      if (id === 'glossary-saved') node.hidden = true;
      nodes.set(id, node);
    }
    return nodes.get(id);
  };
  let respond = async settings => ({ok: true, json: async () => ({settings})});
  const context = vm.createContext({
    structuredClone, setTimeout, clearTimeout, queueMicrotask,
    document, Option: function(text, value) { return Object.assign(element('option'), {text, value}); },
    fetch: async (path, options) => {
      const settings = options.body && JSON.parse(options.body);
      requests.push({path, settings});
      return respond(settings, path);
    },
  });
  vm.runInContext(source, context);
  const api = vm.runInContext(`
    render = () => {};
    apply = next => { state = {...state, ...next}; };
    state = {settings: {Glossary: [], Other: 'preserved'}, glossaryLimit: 200,
      languages: {targets: ['ja', 'en'], variants: {}}};
    ({editor: glossaryEditor, read: readGlossaryRow, save: saveGlossary, changed: glossaryChanged,
      state: () => state, RequestError, h, newEntry: newGlossaryEntry,
      row: glossaryRow, redraw: renderGlossary, setLocale: value => { lang = value; },
      setLanguageNames: () => { displayNames = new Intl.DisplayNames(['en'], {type: 'language'}); collator = new Intl.Collator('en'); },
      setAnnounce: fn => { announce = fn; },
      setStrings: catalogs => { strings = catalogs; }, build: buildGlossary,
      setRenderGlossary: fn => { renderGlossary = fn; }});
  `, context);
  api.setAnnounce(text => announcements.push(text));
  api.setStrings(Object.fromEntries(['en', 'ja'].map(lang => [lang, JSON.parse(readFileSync('ui/i18n/' + lang + '.json', 'utf8'))])));
  api.setLanguageNames();
  api.state().settings.Glossary = saved;
  return {...api, requests, announcements, events, document, node: document.getElementById, respond: fn => { respond = fn; },
    async settle() { await drain(); await drain(); },
    add(value) {
      const row = api.row(value);
      api.editor.rows.push(row);
      return row;
    },
  };
}

test('save trims words and reading and sends only complete rows in screen order', async () => {
  const ui = editor();
  const draft = ui.add(entry(' unfinished ', ' ', ' かな '));
  const first = ui.add(entry(' 桔梗\u3000', '\tKikyo ', ' ききょう\u3000'));
  const second = ui.add(entry(' 森羅 ', ' Shinra ', ' しんら '));
  ui.changed();
  ui.save();
  ui.save();
  await ui.settle();
  assert.equal(ui.requests.length, 1);
  assert.deepEqual(ui.requests[0].settings.Glossary, [entry(), entry('森羅', 'Shinra', 'しんら')]);
  assert.equal(ui.requests[0].settings.Other, 'preserved');
  assert.equal(draft.inputs.Source.value, 'unfinished');
  assert.equal(draft.inputs.Target.value, '');
  assert.equal(ui.editor.rows[0], draft);
  assert.equal(first.inputs.Source.value, '桔梗');
  assert.equal(second.inputs.Target.value, 'Shinra');
  assert.equal(ui.editor.dirty, false);
});

test('a new row uses offered base languages and keeps its languages different', () => {
  const ui = editor();
  ui.state().languages = {targets: ['ja', 'ja-easy', 'en', 'de'], variants: {'ja-easy': 'ja'}};
  ui.state().settings.Flows = {Self: {}};
  ui.setLanguageNames();
  for (const [spoken, target, wantSource, wantTarget] of [
    ['ja', 'ja-easy', 'ja', 'en'],
    ['en', 'ja', 'en', 'ja'],
    ['pt', 'ja', 'en', 'ja'],
    ['sv', 'en', 'en', 'de'],
  ]) {
    Object.assign(ui.state().settings.Flows.Self, {Spoken: spoken, Target: target});
    assert.deepEqual(plain(ui.newEntry()), {SourceLang: wantSource, TargetLang: wantTarget});
  }
});

test('element properties preserve false for spellcheck', () => {
  const ui = editor();
  assert.equal(ui.h('input', {spellcheck: false}).spellcheck, false);
});

test('a reading move can fire change without reentering the render or saving during the move', async () => {
  const ui = editor();
  ui.build();
  let depth = 0, maxDepth = 0, renders = 0;
  ui.setRenderGlossary(() => {
    maxDepth = Math.max(maxDepth, ++depth);
    if (++renders === 1) {
      ui.events.change();
      ui.events.focusout({target: null});
    }
    depth--;
  });
  ui.events.input();
  assert.equal(renders, 0);
  await ui.settle();
  assert.equal(renders, 2);
  assert.equal(maxDepth, 1);
  assert.equal(ui.editor.dirty, false);
});

test('an incomplete row stays local and saves when both words are filled', async () => {
  const ui = editor();
  const row = ui.add(entry('name', '', ''));
  ui.changed();
  ui.save();
  await ui.settle();
  assert.equal(ui.requests.length, 0);
  assert.equal(ui.node('glossary-saved').hidden, true);
  assert.equal(ui.editor.rows[0], row);
  row.inputs.Target.value = 'Name';
  ui.changed();
  ui.save();
  await ui.settle();
  assert.deepEqual(ui.requests[0].settings.Glossary, [entry('name', 'Name', '')]);
  assert.equal(ui.node('glossary-saved').hidden, false);
  assert.deepEqual(ui.announcements, ['Saved']);
});

test('required words turn red only after leaving the entry and clear as soon as they have text', async () => {
  for (const [locale, required, word] of [['en', 'Enter a word.', 'Word'], ['ja', '単語を入力してください。', '単語']]) {
    const ui = editor();
    ui.setLocale(locale);
    ui.build();
    const row = ui.add(entry('', '', ''));
    ui.changed();
    ui.redraw();
    for (const field of ['Source', 'Target']) assert.equal(row.inputs[field].getAttribute('aria-invalid'), null);
    ui.document.activeElement = null;
    ui.events.focusout({target: row.inputs.Source, relatedTarget: row.inputs.Target});
    await ui.settle();
    for (const field of ['Source', 'Target']) assert.equal(row.errors[field].hidden, true);
    row.inputs.Target.focus();
    ui.node('glossary-add').focus();
    ui.events.focusout({target: row.inputs.Target, relatedTarget: ui.node('glossary-add')});
    await ui.settle();
    for (const [field, position] of [['Source', 1], ['Target', 2]]) {
      assert.equal(row.inputs[field].getAttribute('aria-invalid'), 'true');
      assert.equal(row.inputs[field].getAttribute('aria-label'), word + ' ' + position);
      assert.equal(row.inputs[field].getAttribute('aria-describedby'), row.errors[field].id);
      assert.equal(row.errors[field].hidden, false);
      assert.equal(row.errors[field].textContent, required);
    }
    assert.equal(ui.requests.length, 0);
    row.inputs.Source.focus();
    row.inputs.Source.value = 'name';
    ui.events.input();
    await ui.settle();
    assert.equal(row.inputs.Source.getAttribute('aria-invalid'), null);
    assert.equal(row.inputs.Target.getAttribute('aria-invalid'), 'true');
    row.inputs.Source.value = '';
    ui.events.input();
    await ui.settle();
    assert.equal(row.inputs.Source.getAttribute('aria-invalid'), null);
    assert.equal(ui.requests.length, 0);
  }
});

test('both language fields show a server refusal and keep their distinct accessible names', async () => {
  const ui = editor();
  const row = ui.add({...entry(), TargetLang: 'ja'});
  ui.changed();
  ui.redraw();
  for (const field of ['SourceLang', 'TargetLang']) assert.equal(row.inputs[field].getAttribute('aria-invalid'), null);
  ui.respond(async () => ({ok: false, status: 400, json: async () => ({
    code: 'glossary_same_language', location: {index: 0, field: 'TargetLang'},
  })}));
  ui.save();
  await ui.settle();
  ui.redraw();
  for (const [field, position] of [['SourceLang', 1], ['TargetLang', 2]]) {
    assert.equal(row.inputs[field].getAttribute('aria-invalid'), 'true');
    assert.equal(row.inputs[field].getAttribute('aria-label'), 'Language ' + position);
    assert.equal(row.inputs[field].getAttribute('aria-describedby'), row.errors[field].id);
    assert.equal(row.errors[field].textContent, 'Choose two different languages.');
  }
  assert.deepEqual(ui.announcements, ['Word 1, Language 2: Choose two different languages.']);
  assert.equal(ui.node('glossary-saved').hidden, true);
});

test('the reading follows Japanese and its accessible name identifies that side', () => {
  const ui = editor();
  const row = ui.add(entry());
  ui.changed();
  ui.redraw();
  assert.equal(row.fields.Reading.parentNode, row.source);
  assert.equal(row.inputs.Reading.getAttribute('aria-label'), 'Kana reading 1');
  row.inputs.SourceLang.value = 'en';
  row.inputs.TargetLang.value = 'ja';
  ui.redraw();
  assert.equal(row.fields.Reading.parentNode, row.target);
  assert.equal(row.inputs.Reading.getAttribute('aria-label'), 'Kana reading 2');
  assert.equal(row.fields.Reading.hidden, false);
  row.inputs.TargetLang.value = 'de';
  ui.redraw();
  assert.equal(row.fields.Reading.hidden, true);
  assert.equal(row.inputs.Reading.value, '');
});

test('the saved mark waits for server acceptance and an unchanged list does not flash it', async () => {
  const ui = editor();
  ui.add(entry());
  let release;
  ui.respond(settings => new Promise(resolve => { release = () => resolve({ok: true, json: async () => ({settings})}); }));
  ui.changed();
  ui.save();
  await drain();
  assert.equal(ui.node('glossary-saved').hidden, true);
  assert.deepEqual(ui.announcements, []);
  release();
  await ui.settle();
  assert.equal(ui.node('glossary-saved').hidden, false);
  assert.deepEqual(ui.announcements, ['Saved']);
  const unchanged = editor([entry()]);
  unchanged.add(entry());
  unchanged.changed();
  unchanged.save();
  await unchanged.settle();
  assert.equal(unchanged.requests.length, 0);
  assert.equal(unchanged.node('glossary-saved').hidden, true);
  assert.deepEqual(unchanged.announcements, []);
});

test('a located server error maps the sent index back past incomplete screen rows', async () => {
  const ui = editor();
  ui.build();
  ui.setRenderGlossary(() => {});
  ui.add(entry('draft', ''));
  ui.add(entry());
  const invalid = ui.add(entry('森羅', 'Shinra', 'not kana'));
  ui.respond(async () => ({ok: false, status: 400, json: async () => ({
    error: 'invalid reading', code: 'glossary_reading', location: {index: 1, field: 'Reading'},
  })}));
  ui.changed();
  ui.save();
  await ui.settle();
  assert.equal(ui.editor.refusal.row, invalid);
  assert.equal(ui.editor.refusal.field, 'Reading');
  assert.equal(ui.editor.refusal.error.code, 'glossary_reading');
  assert.deepEqual(ui.announcements, ['Word 3, Kana reading 1: Use only hiragana, katakana and ー for a word in Japanese.']);
  assert.equal(invalid.inputs.Reading.value, 'not kana');
  assert.equal(ui.editor.dirty, true);
  assert.deepEqual(plain(ui.state().settings.Glossary), []);
  for (let i = 0; i < 4; i++) {
    ui.save();
    await ui.settle();
  }
  ui.events.change();
  ui.events.focusout({target: invalid.inputs.Reading});
  await ui.settle();
  assert.equal(ui.requests.length, 1);
  assert.equal(ui.announcements.length, 1);
  invalid.inputs.Reading.value = 'しんら';
  ui.changed();
  ui.respond(async settings => ({ok: true, json: async () => ({settings})}));
  ui.save();
  await ui.settle();
  assert.equal(ui.requests.length, 2);
  assert.equal(ui.editor.refusal, null);
});

test('a content refusal waits for an edit while a failed write can retry unchanged rows', async () => {
  for (const code of ['glossary_limit', 'disk_write']) {
    const ui = editor();
    const row = ui.add(entry(' 桔梗 '));
    ui.respond(async () => ({ok: false, status: 400, json: async () => ({error: 'refused', code})}));
    ui.changed();
    ui.save();
    await ui.settle();
    assert.equal(ui.editor.refusal.row, null);
    assert.equal(ui.editor.refusal.error.code, code);
    assert.equal(row.inputs.Source.value, ' 桔梗 ');
    if (code === 'glossary_limit') {
      ui.save();
      await ui.settle();
      assert.equal(ui.requests.length, 1);
      row.inputs.Source.value = '菊響';
      ui.changed();
    }
    ui.respond(async settings => ({ok: true, json: async () => ({settings})}));
    ui.save();
    await ui.settle();
    assert.equal(ui.requests.length, 2);
    assert.equal(ui.editor.refusal, null);
    assert.equal(row.inputs.Source.value, code === 'glossary_limit' ? '菊響' : '桔梗');
    assert.equal(ui.editor.dirty, false);
  }
});

test('a slow answer does not overwrite later edits and queued saves keep the last edit', async () => {
  const ui = editor();
  const row = ui.add(entry());
  let release;
  ui.respond(settings => new Promise(resolve => { release = () => resolve({ok: true, json: async () => ({settings})}); }));
  ui.changed();
  ui.save();
  await drain();
  row.inputs.Target.value = 'New name';
  ui.changed();
  ui.save();
  release();
  await ui.settle();
  assert.equal(row.inputs.Target.value, 'New name');
  assert.equal(ui.editor.dirty, true);
  assert.equal(ui.requests.length, 2);
  assert.equal(ui.node('glossary-saved').hidden, true);
  release();
  await ui.settle();
  assert.equal(ui.state().settings.Glossary[0].Target, 'New name');
  assert.equal(ui.editor.dirty, false);
  assert.equal(ui.node('glossary-saved').hidden, false);
});

test('an obsolete validation answer does not mark a subsequently edited row', async () => {
  const ui = editor();
  const row = ui.add(entry('桔梗', 'Kikyo', 'invalid'));
  let release;
  ui.respond(() => new Promise(resolve => { release = () => resolve({ok: false, status: 400, json: async () => ({
    code: 'glossary_reading', location: {index: 0, field: 'Reading'},
  })}); }));
  ui.changed();
  ui.save();
  await drain();
  row.inputs.Reading.value = 'ききょう';
  ui.changed();
  release();
  await ui.settle();
  assert.equal(ui.editor.refusal, null);
  assert.equal(ui.editor.dirty, true);
});

test('the settings save path refreshes and retries an external settings change', async () => {
  const ui = editor();
  ui.add(entry());
  ui.respond(async (settings, path) => {
    if (ui.requests.length === 1) return {ok: false, status: 409, json: async () => ({code: 'settings_changed'})};
    if (path === '/api/state') return {ok: true, json: async () => ({settings: {Glossary: [], Other: 'external'}})};
    return {ok: true, json: async () => ({settings})};
  });
  ui.changed();
  ui.save();
  await ui.settle();
  assert.deepEqual(ui.requests.map(request => request.path), ['/api/settings', '/api/state', '/api/settings']);
  assert.equal(ui.requests[2].settings.Other, 'external');
  assert.deepEqual(ui.requests[2].settings.Glossary, [entry()]);
  assert.equal(ui.editor.dirty, false);
});
