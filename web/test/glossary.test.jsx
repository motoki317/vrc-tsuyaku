import { render } from '@solidjs/web';
import { flush, Show } from 'solid-js';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { createClient } from '../src/client.js';
import { Glossary } from '../src/glossary.jsx';
import { fixture, response, settle } from './fixture.js';

let app, state, stop, requests;
const row = i => document.querySelectorAll('.glossary-entry')[i];
const control = (i, key) => row(i).querySelector('[id$="-' + key + '"]');
const add = () => {
  document.getElementById('glossary-add').click();
  flush();
};
const change = (i, key, value, type = 'input') => {
  const node = control(i, key);
  node.value = value;
  node.dispatchEvent(new Event(type, { bubbles: true }));
  flush();
};
const save = async (i, key = 'Source') => {
  control(i, key).dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
  await settle();
};
const write = async n => {
  const req = requests[n];
  state.settings = req.body;
  req.resolve(response(state));
  await settle();
};
const flash = () => !document.getElementById('glossary-saved').hidden;
const push = () => {
  app.apply(structuredClone(state));
  flush();
};
const entry = (Source, Target, Reading = '') => ({ SourceLang: 'ja', Source, TargetLang: 'en', Target, Reading });
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  state = fixture();
  state.settings.Flows.Self = { Spoken: 'ja', Target: 'en' };
  requests = [];
  vi.stubGlobal('fetch', (path, opts) => {
    const req = { path, body: opts.body && JSON.parse(opts.body), ...Promise.withResolvers() };
    requests.push(req);
    return req.promise;
  });
  stop = render(() => {
    app = createClient();
    return (
      <Show when={app.state.launch}>
        <Glossary app={app} />
      </Show>
    );
  }, document.body);
  push();
});
afterEach(() => {
  stop();
  document.body.replaceChildren();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

test('glossary saves trim complete rows in order, keep drafts across pushes and flash on changes', async () => {
  add();
  change(0, 'Source', ' draft ');
  await save(0);
  expect(requests).toHaveLength(0);
  expect(flash()).toBe(false);
  expect(control(0, 'Source').value).toBe('draft');
  for (const [i, source, target] of [
    [1, ' 桔梗　', ' Kikyo '],
    [2, ' 森羅 ', ' Shinra '],
  ]) {
    add();
    change(i, 'Source', source);
    change(i, 'Target', target);
    change(i, 'Reading', ' かな ');
  }
  await save(2);
  await save(2);
  expect(requests).toHaveLength(1);
  expect(flash()).toBe(false);
  expect(requests[0].body.Glossary).toEqual([entry('桔梗', 'Kikyo', 'かな'), entry('森羅', 'Shinra', 'かな')]);
  expect(requests[0].body.Chatbox).toEqual(state.settings.Chatbox);
  await write(0);
  expect(flash()).toBe(true);
  expect(control(2, 'Target').value).toBe('Shinra');
  state.settings.Glossary = [entry('外', 'outside')];
  push();
  expect(control(0, 'Source').value).toBe('draft');
  expect(control(1, 'Target').value).toBe('outside');
  expect(row(2)).toBeUndefined();
  change(0, 'Target', 'Draft');
  await save(0);
  expect(requests[1].body.Glossary[0]).toEqual(entry('draft', 'Draft'));
});

test('reordering the language options preserves the glossary pair', () => {
  state.languages.targets.push('de', 'ko', 'fr');
  state.settings.Glossary = [{ ...entry('one', 'two'), SourceLang: 'ko', TargetLang: 'de' }];
  push();
  const pair = () => ['SourceLang', 'TargetLang'].map(key => control(0, key).value);
  expect(pair()).toEqual(['ko', 'de']);
  state.settings.UI.Language = 'ja';
  push();
  expect(pair()).toEqual(['ko', 'de']);
});

test('new rows choose distinct offered base languages, keep unknown saved codes, and respect the limit', async () => {
  state.languages.targets = ['ja', 'ja-easy', 'en', 'de'];
  state.glossaryLimit = 4;
  for (const [i, spoken, target, source, want] of [
    [0, 'ja', 'ja-easy', 'ja', 'en'],
    [1, 'en', 'ja', 'en', 'ja'],
    [2, 'pt', 'ja', 'en', 'ja'],
    [3, 'sv', 'en', 'en', 'de'],
  ]) {
    Object.assign(state.settings.Flows.Self, { Spoken: spoken, Target: target });
    push();
    add();
    expect(control(i, 'SourceLang').value).toBe(source);
    expect(control(i, 'TargetLang').value).toBe(want);
    expect(control(i, 'Source').getAttribute('spellcheck')).toBe('false');
  }
  expect(document.getElementById('glossary-add').disabled).toBe(true);
  state.settings.Glossary = [{ ...entry('x', 'y'), SourceLang: 'fr' }];
  await save(3);
  push();
  expect(control(4, 'SourceLang').value).toBe('fr');
});

test.each(['en', 'ja'])('required words are marked only when focus leaves the entry in %s', async lang => {
  state.settings.UI.Language = lang;
  push();
  add();
  const leave = async target => {
    control(0, 'Source').dispatchEvent(new FocusEvent('focusout', { bubbles: true, relatedTarget: target }));
    await settle();
  };
  await leave(control(0, 'Target'));
  expect(control(0, 'Source').hasAttribute('aria-invalid')).toBe(false);
  await leave(document.getElementById('glossary-add'));
  for (const key of ['Source', 'Target']) {
    const input = control(0, key),
      error = document.getElementById(input.getAttribute('aria-describedby'));
    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect(error.textContent).toBe(app.t('error.code.glossary_required'));
  }
  change(0, 'Source', 'a');
  change(0, 'Source', '');
  expect(control(0, 'Source').hasAttribute('aria-invalid')).toBe(false);
  expect(control(0, 'Target').getAttribute('aria-invalid')).toBe('true');
  expect(requests).toHaveLength(0);
});

test('reading follows the Japanese side and clears once when neither language is Japanese', async () => {
  state.languages.targets.push('de');
  state.settings.Glossary = [entry('語', 'word', 'ご')];
  push();
  const reading = control(0, 'Reading');
  expect(reading.getAttribute('aria-label')).toBe('Kana reading 1');
  expect(reading.lang).toBe('ja');
  change(0, 'TargetLang', 'ja');
  change(0, 'SourceLang', 'en', 'change');
  expect(control(0, 'Reading')).toBe(reading);
  expect(reading.closest('.glossary-side').contains(control(0, 'Target'))).toBe(true);
  expect(reading.getAttribute('aria-label')).toBe('Kana reading 2');
  change(0, 'TargetLang', 'de', 'change');
  expect(reading.value).toBe('');
  expect(reading.parentNode.hidden).toBe(true);
  expect(requests).toHaveLength(0);
  await vi.advanceTimersByTimeAsync(500);
  expect(requests).toHaveLength(1);
});

test.each([
  ['glossary_reading', 'Reading'],
  ['glossary_same_language', 'TargetLang'],
])('map %s past a draft, announce it and wait for a different edit', async (code, key) => {
  add();
  add();
  change(1, 'Source', '語');
  change(1, 'Target', 'Word');
  change(1, 'Reading', 'invalid');
  if (key === 'TargetLang') change(1, key, 'ja');
  await save(1);
  requests[0].resolve(response({ code, location: { index: 0, field: key } }, 400));
  await settle();
  expect(control(1, key).getAttribute('aria-invalid')).toBe('true');
  expect(control(0, key).hasAttribute('aria-invalid')).toBe(false);
  if (key === 'TargetLang') {
    expect(control(1, 'SourceLang').getAttribute('aria-invalid')).toBe('true');
    expect(control(1, key).getAttribute('aria-label')).toBe('Language 2');
  }
  await vi.advanceTimersByTimeAsync(50);
  flush();
  expect(app.announcement()).toContain('Word 2');
  expect(app.state.settings.Glossary).toEqual([]);
  await save(1);
  change(1, key, control(1, key).value, 'change');
  await vi.advanceTimersByTimeAsync(500);
  expect(requests).toHaveLength(1);
  change(1, key, key === 'Reading' ? 'ご' : 'en');
  await save(1);
  await write(1);
  expect(flash()).toBe(true);
});

test.each(['disk_write', 'glossary_limit'])(
  'a list refusal preserves whitespace and retry rules for %s',
  async code => {
    add();
    change(0, 'Source', ' 桔梗 ');
    change(0, 'Target', 'Kikyo');
    await save(0);
    requests[0].resolve(response({ code }, 500));
    await settle();
    expect(document.getElementById('glossary-error').hidden).toBe(false);
    expect(control(0, 'Source').value).toBe(' 桔梗 ');
    if (code === 'glossary_limit') {
      change(0, 'Target', 'Kikyo ');
      await save(0);
      expect(control(0, 'Target').value).toBe('Kikyo ');
      expect(document.getElementById('glossary-error').hidden).toBe(false);
      app.setSub('about');
      flush();
      await settle();
      expect(requests).toHaveLength(1);
      change(0, 'Reading', 'ききょう');
      await save(0);
    } else {
      app.setSub('about');
      flush();
      await settle();
    }
    await write(1);
    expect(control(0, 'Source').value).toBe('桔梗');
  },
);

test.each([false, true])('a later edit survives an older response (refused %s)', async refuse => {
  add();
  change(0, 'Source', '語');
  change(0, 'Target', 'old');
  await save(0);
  change(0, 'Target', 'new');
  await save(0);
  if (refuse) {
    requests[0].resolve(response({ code: 'glossary_reading', location: { index: 0, field: 'Reading' } }, 400));
    await settle();
  } else await write(0);
  expect(requests[1].body.Glossary[0].Target).toBe('new');
  expect(control(0, 'Target').value).toBe('new');
  expect(control(0, 'Reading').hasAttribute('aria-invalid')).toBe(false);
  expect(flash()).toBe(false);
  await write(1);
  expect(flash()).toBe(true);
});

test('retry on a settings conflict preserves external changes and deletion returns focus to Add', async () => {
  state.settings.Glossary = [entry('語', 'old')];
  push();
  change(0, 'Target', 'new');
  await save(0);
  requests[0].resolve(response({ code: 'settings_changed' }, 409));
  await settle();
  state.settings.Chatbox.Address = 'external';
  requests[1].resolve(response(state));
  await settle();
  expect(requests[2].body.Chatbox.Address).toBe('external');
  expect(requests[2].body.Glossary).toEqual([entry('語', 'new')]);
  await write(2);
  row(0).querySelector('button').click();
  await settle();
  expect(document.activeElement.id).toBe('glossary-add');
  expect(requests[3].body.Glossary).toEqual([]);
});
