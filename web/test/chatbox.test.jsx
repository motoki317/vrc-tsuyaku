import { render } from '@solidjs/web';
import { flush, Show } from 'solid-js';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { Chatbox } from '../src/chatbox.jsx';
import { createClient } from '../src/client.js';
import { fixture, response, settle } from './fixture.js';

let app, state, stop, previews, writes;
const q = id => document.getElementById(id);
const input = (id, value, type = 'input') => {
  q(id).value = value;
  q(id).dispatchEvent(new Event(type, { bubbles: true }));
  flush();
};
const sample = (pages = ['Hello'], seconds = [3.706]) => ({
  pages,
  seconds,
  reading: { characters: 5, secondsPerCharacter: 0.17 },
});
beforeEach(async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  state = fixture();
  previews = [];
  writes = [];
  vi.stubGlobal('fetch', (url, options) => {
    const gate = Promise.withResolvers(),
      body = JSON.parse(options.body);
    (url.endsWith('preview') ? previews : writes).push({ body, ...gate });
    return gate.promise;
  });
  stop = render(() => {
    app = createClient();
    return (
      <Show when={app.state.launch}>
        <Chatbox app={app} />
      </Show>
    );
  }, document.body);
  app.apply(structuredClone(state));
  app.setScreen('settings');
  app.setSub('chatbox');
  flush();
  await settle();
});
afterEach(() => {
  stop();
  document.body.replaceChildren();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

test('returning focus to a preset saves the inserted template and shows Saved without leaving Chatbox', async () => {
  previews[0].resolve(response(sample()));
  await settle();
  const button = q('presets').firstElementChild;
  button.click();
  await settle();
  expect(document.activeElement).toBe(button);
  expect(previews.at(-1).body.template).toBe(state.defaultTemplate);
  expect(writes).toHaveLength(0);
  previews.at(-1).resolve(response(sample()));
  await settle();
  expect(writes).toHaveLength(1);
  expect(writes[0].body.Chatbox.Template).toBe(state.defaultTemplate);
  state.settings = writes[0].body;
  writes[0].resolve(response(state));
  await settle();
  expect(q('template-saved').hidden).toBe(false);
  expect(button.getAttribute('aria-pressed')).toBe('true');
});

test('reading sliders coalesce drags and preserve ranges and between-step values', async () => {
  expect([q('reading-base').min, q('reading-base').max, q('reading-factor').min, q('reading-factor').max]).toEqual([
    '1',
    '10',
    '1',
    '8',
  ]);
  expect(q('reading-factor').getAttribute('aria-valuetext')).toBe('×2.0');
  for (const [id, value] of [
    ['reading-base', '3'],
    ['reading-base', '3.5'],
    ['reading-factor', '2.7'],
  ])
    input(id, value);
  expect(previews).toHaveLength(1);
  expect(writes).toHaveLength(0);
  previews[0].resolve(response(sample(['first'])));
  await settle();
  expect(previews).toHaveLength(2);
  expect(q('preview').textContent).toBe('first');
  expect(previews[1].body).toMatchObject({ readingBase: 3.5, readingFactor: 2.7, sourceLang: 'ja', targetLang: 'en' });
  previews[1].resolve(response(sample()));
  await settle();
  for (const [id, value, field] of [
    ['reading-base', 3.5, 'ReadingBase'],
    ['reading-factor', 2.7, 'ReadingFactor'],
  ]) {
    input(id, value, 'change');
    await vi.advanceTimersByTimeAsync(499);
    expect(writes).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1);
    const write = writes.shift();
    expect(write.body.Chatbox).toEqual({ ...state.settings.Chatbox, [field]: value });
    state.settings = write.body;
    write.resolve(response(state));
    await settle();
  }
  state.settings.Chatbox.ReadingBase = 4.25;
  state.settingsError = 'invalid';
  state.reading.base.max = 9;
  app.apply(structuredClone(state));
  flush();
  expect(q('reading-base-value').textContent).toBe('4.25 s');
  expect(previews.at(-1).body.readingBase).toBe(4.25);
  expect(q('reading-base').max).toBe('9');
  expect(q('reading-factor').disabled).toBe(true);
});

test('successful previews preserve the bars and localize the formula', async () => {
  app.log.event(
    {
      kind: 'translated',
      flow: 'self',
      phraseId: 1,
      time: new Date().toISOString(),
      original: 'こんにちは',
      text: 'Hello',
      sourceLang: 'ja',
      targetLang: 'en',
    },
    0,
  );
  flush();
  previews[0].resolve(response(sample(['old'])));
  await settle();
  expect(previews[1].body.original).toBe('こんにちは');
  previews[1].resolve(response(sample()));
  await settle();
  const bar = q('preview').querySelector('.page-timer');
  expect(bar.style.animationDuration).toBe('3.706s');
  expect(bar.getAttribute('aria-hidden')).toBe('true');
  expect(q('reading-example').textContent).toBe('3.71 s = 2 s + 5 characters × 0.17 s/character × 2');
  input('template', 'half typed');
  input('template', '{{');
  previews.at(-1).resolve(response({ error: 'obsolete' }, 400));
  await settle();
  expect(q('template-error').hidden).toBe(true);
  previews.at(-1).resolve(response({ error: 'bad template' }, 400));
  await settle();
  expect(q('template-error').textContent).toContain('bad template');
  expect(q('preview').textContent).toBe('Hello');
  q('template').dispatchEvent(new Event('blur'));
  await settle();
  expect(writes).toHaveLength(0);
  input('template', 'new');
  expect(q('template-error').hidden).toBe(true);
  previews.at(-1).reject(Error('offline'));
  await settle();
  expect(q('preview').textContent).toBe('Hello');
  const before = previews.length;
  app.setSub('models');
  flush();
  const prior = globalThis.fetch;
  vi.stubGlobal('fetch', () => response(state));
  await app.refresh();
  vi.stubGlobal('fetch', prior);
  state.settings.UI.Language = 'ja';
  app.apply(structuredClone(state));
  app.setSub('chatbox');
  flush();
  expect(previews).toHaveLength(before + 1);
  expect(q('reading-example').textContent).toBe('3.71秒 = 2秒 + 5文字 × 0.17秒/文字 × 2');
  previews.at(-1).resolve(response(sample()));
  await settle();
  input('reading-factor', 2.5);
  expect(previews.at(-1).body).toMatchObject({ readingBase: 2, readingFactor: 2.5 });
  input('reading-base', 4);
  input('reading-factor', 3);
  previews
    .at(-1)
    .resolve(
      response({ pages: ['a', 'b'], seconds: [16.4, 3.6], reading: { characters: 160, secondsPerCharacter: 0.05 } }),
    );
  await settle();
  expect(q('preview').firstElementChild.textContent).toBe('1/2 · 16.4秒a');
  expect(q('preview').querySelector('.page-timer')).toBe(bar);
  expect(q('preview').lastElementChild.textContent).toBe('2/2 · 3.6秒b');
  expect([...q('preview').querySelectorAll('.page-timer')].map(el => el.style.animationDuration)).toEqual([
    '16.4s',
    '3.6s',
  ]);
  expect(q('reading-example').textContent).toBe('2秒 + 160文字 × 0.05秒/文字 × 2.5');
  previews.at(-1).resolve(response(sample([], [])));
  await settle();
  expect(q('reading-example').hidden).toBe(true);
  expect(q('preview').childElementCount).toBe(0);
});

test('a template error stays visible through a slider edit and undo during a preview', async () => {
  previews[0].resolve(response(sample()));
  await settle();
  input('template', '{{');
  const refusal = () => response({ error: 'bad template' }, 400);
  previews.at(-1).resolve(refusal());
  await settle();
  function expectInvalid() {
    expect(q('template-error').hidden).toBe(false);
    expect(q('template-error').textContent).toContain('bad template');
    expect(q('template').getAttribute('aria-invalid')).toBe('true');
  }
  input('reading-factor', 2.1);
  expect(previews.at(-1).body).toMatchObject({ template: '{{', readingFactor: 2.1 });
  expectInvalid();
  previews.at(-1).resolve(refusal());
  await settle();

  input('template', 'valid');
  expect(q('template-error').hidden).toBe(true);
  const valid = previews.at(-1);
  input('template', '{{');
  expectInvalid();
  valid.resolve(response(sample(['valid preview'])));
  await settle();
  expect(q('preview').textContent).toBe('valid preview');
  expectInvalid();
});

test('refused address and template retain typed text, retry on blur, and trim address only on acceptance', async () => {
  previews[0].resolve(response(sample()));
  await settle();
  for (const [id, raw, code] of [
    ['address', '  localhost:9000 ', 'disk_write'],
    ['template', 'hello', 'template'],
  ]) {
    input(id, raw);
    if (id === 'template') {
      previews.at(-1).resolve(response(sample()));
      await settle();
    }
    q(id).dispatchEvent(new Event('blur'));
    await settle();
    writes.at(-1).resolve(response({ error: 'write refused', code }, 400));
    await settle();
    expect(q(id).value).toBe(raw);
    expect(q(id).getAttribute('aria-invalid')).toBe('true');
    if (id === 'address') {
      input(id, raw + ' ');
      expect(q('address-error').hidden).toBe(false);
      expect(q('address-error').textContent).toBe(app.t('error.address'));
    }
    q(id).dispatchEvent(new Event('blur'));
    await settle();
    const write = writes.at(-1);
    expect(write.body.Chatbox[id === 'address' ? 'Address' : 'Template']).toBe(raw.trim());
    state.settings = write.body;
    write.resolve(response(state));
    await settle();
    expect(q(id).value).toBe(raw.trim());
    expect(q(id).getAttribute('aria-invalid')).toBeNull();
  }
});

test('an accepted address clears the refusal while the next edit stays unsaved', async () => {
  const enter = async () => {
    q('address').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
    await settle();
  };
  input('address', 'invalid');
  await enter();
  writes[0].resolve(response({ code: 'disk_write' }, 500));
  await settle();
  input('address', 'localhost:9001');
  await enter();
  input('address', 'localhost:9002');
  expect(q('address-error').hidden).toBe(false);
  state.settings = writes[1].body;
  writes[1].resolve(response(state));
  await settle();
  expect(q('address-error').hidden).toBe(true);
  expect(q('address').value).toBe('localhost:9002');
  await enter();
  expect(writes[2].body.Chatbox.Address).toBe('localhost:9002');
});

test('offline preview retries on new input and a merged self phrase wins over an intervening bubble', async () => {
  previews[0].reject(Error('offline'));
  await settle();
  input('template', 'next');
  expect(previews[1].body.template).toBe('next');
  previews[1].resolve(response(sample()));
  await settle();
  for (const [phraseId, text] of [
    [1, 'first'],
    [2, 'second'],
    [3, 'third'],
  ]) {
    app.log.event({ kind: 'translated', flow: 'self', phraseId, text, original: text }, 0);
  }
  app.log.event({ kind: 'merged', flow: 'self', phraseId: 3, into: 1, original: 'first third', text: 'merged' }, 0);
  flush();
  expect(previews.at(-1).body).toMatchObject({ translation: 'merged', original: 'first third' });
});

test('focused text keeps its selection through a push and takes the external value on blur', async () => {
  for (const [id, key] of [
    ['address', 'Address'],
    ['template', 'Template'],
  ]) {
    if (id === 'template') {
      q('presets').lastElementChild.click();
      flush();
    }
    const field = q(id),
      old = field.value;
    field.focus();
    field.setSelectionRange(1, 3);
    state.settings.Chatbox[key] = 'external';
    app.apply(structuredClone(state));
    flush();
    expect([field.value, field.selectionStart, field.selectionEnd]).toEqual([old, 1, 3]);
    field.blur();
    await settle();
    expect(field.value).toBe('external');
  }
});

test.each(['address', 'template'])('navigation retries a refused %s without refocusing the field', async id => {
  previews[0].resolve(response(sample()));
  await settle();
  if (id === 'template') {
    q('presets').lastElementChild.click();
    flush();
  }
  q(id).focus();
  expect(document.activeElement).toBe(q(id));
  input(id, 'edited');
  if (id === 'template') {
    previews.at(-1).resolve(response(sample()));
    await settle();
  }
  q(id).blur();
  await settle();
  writes[0].resolve(response({ code: 'disk_write' }, 500));
  await settle();
  expect(document.activeElement).not.toBe(q(id));
  app.setSub('about');
  flush();
  await settle();
  expect(writes).toHaveLength(2);
  expect(writes[1].body).toEqual(writes[0].body);
});
