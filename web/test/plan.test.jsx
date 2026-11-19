import { render } from '@solidjs/web';
import { flush } from 'solid-js';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { createClient } from '../src/client.js';
import { Frame } from '../src/frame.jsx';
import { fixture, response, settle } from './fixture.js';

const find = selector => document.querySelector(selector);
async function clickAt(button, time) {
  const event = new MouseEvent('click', { bubbles: true });
  Object.defineProperty(event, 'timeStamp', { value: time });
  button.dispatchEvent(event);
  await settle();
}
let app, saved, dispose, request, spoken, target, swap;
function choose(select, value, change = true) {
  select.value = value;
  select.dispatchEvent(new Event('input', { bubbles: true }));
  if (change) select.dispatchEvent(new Event('change', { bubbles: true }));
  flush();
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  document.head.innerHTML = '<title></title>';
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function (value) {
    if (value !== undefined) this.returnValue = value;
    this.open = false;
    this.dispatchEvent(new Event('close'));
  };
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  saved = fixture();
  request = vi.fn((_url, options) => response({ ...saved, settings: JSON.parse(options.body) }));
  vi.stubGlobal('fetch', (url, options) => (url === '/api/microphones' ? response([]) : request(url, options)));
  dispose = render(() => {
    app = createClient();
    return <Frame app={app} />;
  }, document.body);
  app.apply(saved);
  flush();
  [spoken, target] = document.querySelectorAll('.plan-row[data-flow=self] select');
  swap = find('.plan-row[data-flow=self] .swap');
});
afterEach(() => {
  dispose();
  document.body.replaceChildren();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

test.each([
  ['setup', '#screen-setup'],
  ['settings', '#sub-models'],
])('%s language rows have only swap buttons and no download controls or errors', (screen, container) => {
  app.setScreen(screen);
  flush();
  const rows = find(container + ' .plan-rows');
  expect(rows.querySelector('.fc, .fc-error')).toBeNull();
  const buttons = [...rows.querySelectorAll('button')];
  expect(buttons).toHaveLength(2);
  expect(buttons.every(button => button.matches('.swap'))).toBe(true);
});

test('a manual swap absorbs the pending input and an older answer leaves the next edit showing', async () => {
  const held = Promise.withResolvers();
  request.mockReturnValueOnce(held.promise);
  choose(spoken, 'ja');
  choose(target, 'en');
  spoken.dispatchEvent(new FocusEvent('focusout', { bubbles: true, relatedTarget: swap }));
  expect(request).not.toHaveBeenCalled();
  swap.click();
  await settle();
  expect(request).toHaveBeenCalledTimes(1);
  const sent = JSON.parse(request.mock.calls[0][1].body);
  expect(sent.Flows.Self).toMatchObject({ Spoken: 'en', Target: 'ja' });
  choose(target, 'ja-easy', false);
  held.resolve(response({ ...saved, settings: sent }));
  await settle();
  expect(target.value).toBe('ja-easy');
  target.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
  await settle();
  await vi.advanceTimersByTimeAsync(500);
  expect(request).toHaveBeenCalledTimes(2);
  expect(JSON.parse(request.mock.calls[1][1].body).Flows.Self.Target).toBe('ja-easy');
});

test('input updates every dependency immediately and repeated choices settle once after 500 ms', async () => {
  const memory = () => find('#memory-total').textContent,
    initial = memory();
  choose(spoken, 'ja');
  expect(memory()).not.toBe(initial);
  expect(find('.plan-row .option-name').textContent).toBe('ja-asr');
  expect(find('.plan-downloads').textContent).not.toContain('en-asr');
  expect(find('#downloads-needed').textContent).not.toContain('en-asr');
  choose(spoken, 'en');
  choose(spoken, 'ja');
  const push = fixture();
  push.settings.Flows.Self.CutLevel = -40;
  app.apply(push);
  flush();
  expect(app.settings().Flows.Self.CutLevel).toBe(-40);
  await vi.advanceTimersByTimeAsync(499);
  expect(request).not.toHaveBeenCalled();
  await vi.advanceTimersByTimeAsync(1);
  expect(request).toHaveBeenCalledTimes(1);
  expect(JSON.parse(request.mock.calls[0][1].body).Flows.Self).toEqual({ Spoken: 'ja', Target: 'ja', CutLevel: -40 });
});

test('bare language changes update the shown pair and save it once', async () => {
  spoken.value = 'ja';
  spoken.dispatchEvent(new Event('change', { bubbles: true }));
  flush();
  expect(find('.plan-row .option-name').textContent).toBe('ja-asr');
  target.value = 'en';
  target.dispatchEvent(new Event('change', { bubbles: true }));
  flush();
  expect(app.settings().Flows.Self).toMatchObject({ Spoken: 'ja', Target: 'en' });
  await vi.advanceTimersByTimeAsync(500);
  expect(request).toHaveBeenCalledTimes(1);
  expect(JSON.parse(request.mock.calls[0][1].body).Flows.Self).toMatchObject({ Spoken: 'ja', Target: 'en' });
});

test('an external display language change keeps all four shown language selections', () => {
  const next = fixture();
  next.languages.targets.push('ko');
  next.settings.Flows.Self.Spoken = 'ja';
  app.apply(structuredClone(next));
  flush();
  Object.assign(next.settings.Flows.Self, { Spoken: 'en', Target: 'en' });
  next.settings.Flows.World.Target = 'ko';
  app.apply(structuredClone(next));
  flush();
  const values = () => [...document.querySelectorAll('.plan-row select')].map(el => el.value);
  const expected = ['en', 'en', 'ja', 'ko'];
  expect(values()).toEqual(expected);
  next.settings.UI.Language = 'ja';
  app.apply(next);
  flush();
  expect(values()).toEqual(expected);
  expect(request).not.toHaveBeenCalled();
});

test('Enter commits immediately and a newer target edit retains the shown pair through a spoken refusal', async () => {
  const held = Promise.withResolvers();
  request.mockReturnValueOnce(held.promise);
  choose(spoken, 'ja');
  spoken.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  await settle();
  expect(request).toHaveBeenCalledTimes(1);
  choose(target, 'en');
  held.resolve(response({ error: 'refused' }, 400));
  await settle();
  const error = find('#language-error-self');
  expect(error.hidden).toBe(false);
  expect(error.textContent).toBe('refused');
  expect(spoken.value).toBe('ja');
  expect(target.value).toBe('en');
  target.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
  await settle();
  expect(JSON.parse(request.mock.calls[1][1].body).Flows.Self).toEqual({ Spoken: 'ja', Target: 'en' });
  expect(error.hidden).toBe(true);
});

test('downloads ask for each new license and return focus after cancellation or acceptance', async () => {
  const state = fixture();
  for (const id of ['model', 'en-asr', 'ja-asr']) {
    Object.assign(state.artifacts[id].artifact, {
      AcceptanceKey: id + '-terms',
      Licenses: [{ Name: id, Text: id + ' terms' }],
    });
  }
  state.settings.LicensesAccepted = { 'model-terms': '2026-11-19T12:00:00Z' };
  request.mockImplementation(url => (url === '/api/state' ? response(state) : response({}, 202)));
  app.apply(state);
  flush();
  const all = find('#screen-setup .download-all');
  const dialog = find('#license-dialog'),
    accept = find('#license-accept');
  all.focus();
  all.click();
  await settle();
  expect(dialog.open).toBe(true);
  expect(find('#license-title').textContent).toBe('en-asr');
  accept.click();
  await settle();
  expect(dialog.open).toBe(true);
  expect(find('#license-title').textContent).toBe('ja-asr');
  find('#license-dialog .dialog-actions .secondary').click();
  await settle();
  expect(dialog.open).toBe(false);
  expect(document.activeElement).toBe(all);
  expect(request).not.toHaveBeenCalled();

  all.click();
  await settle();
  expect(dialog.open).toBe(true);
  expect(find('#license-title').textContent).toBe('en-asr');
  accept.click();
  await settle();
  expect(dialog.open).toBe(true);
  expect(find('#license-title').textContent).toBe('ja-asr');
  accept.click();
  await settle();
  expect(dialog.open).toBe(false);
  expect(document.activeElement).toBe(all);
  expect(
    request.mock.calls
      .filter(([, options]) => options.method === 'POST')
      .map(([url, options]) => [url, JSON.parse(options.body)]),
  ).toEqual([
    ['/api/downloads/runtime', { acceptLicense: false }],
    ['/api/downloads/model', { acceptLicense: false }],
    ['/api/downloads/en-asr', { acceptLicense: true }],
    ['/api/downloads/ja-asr', { acceptLicense: true }],
  ]);
});

test.each([
  ['setup', '#next', '#screen-setup'],
  ['settings', '#model-downloads-label', '#sub-models'],
])('Download all on %s hands focus to %s when the downloads finish', (screen, destination, container) => {
  app.setScreen(screen);
  flush();
  const all = find(container + ' .download-all');
  all.focus();
  const ready = fixture();
  for (const item of Object.values(ready.artifacts)) item.state = 'installed';
  app.apply(ready);
  flush();
  expect(all.hidden).toBe(true);
  expect(document.activeElement).toBe(find(destination));
});

test.each(['setup', 'settings'])(
  'a focused %s download action hands focus to its row before it hides',
  async screen => {
    app.setScreen(screen);
    flush();
    find('.plan-downloads').open = true;
    const row = find(
      screen === 'setup' ? '.plan-downloads .download' : '#downloads-needed .download:not(.download-head)',
    );
    const button = row.querySelector('button');
    const held = Promise.withResolvers();
    request.mockReturnValueOnce(held.promise).mockResolvedValueOnce(response(saved));
    let visibleAtTransfer;
    row.addEventListener('focus', () => {
      visibleAtTransfer = !button.hidden;
    });
    button.focus();
    button.click();
    await settle();
    const next = fixture();
    const id = request.mock.calls[0][0].split('/').at(-1);
    Object.assign(next.artifacts[id], { state: 'downloading', progress: { Phase: '' } });
    app.apply(next);
    flush();
    expect(visibleAtTransfer).toBe(true);
    expect(document.activeElement).toBe(row);
    held.resolve(response({ error: 'refused' }, 500));
    await settle();
    expect(document.activeElement).toBe(row);
  },
);

test('a stopped flow moves its icon to the new recognizer on input', () => {
  app.setScreen('settings');
  flush();
  const assigned = name => {
    const label = [...document.querySelectorAll('.download-groups .download .name')].find(
      el => el.textContent === name,
    );
    return label.closest('.download').querySelector('.flows [data-flow=self]');
  };
  expect(assigned('en-asr')).not.toBeNull();
  expect(assigned('ja-asr')).toBeNull();

  choose(spoken, 'ja', false);
  expect(assigned('en-asr')).toBeNull();
  expect(assigned('ja-asr')).not.toBeNull();
  expect(request).not.toHaveBeenCalled();
});

test('a running flow protects its current recognizer until the server confirms the new one', async () => {
  const running = fixture();
  running.flows.self.state = 'running';
  running.settings.Flows.Self.Enabled = true;
  for (const artifact of Object.values(running.artifacts)) artifact.state = 'installed';
  app.apply(running);
  app.setScreen('settings');
  flush();

  const held = Promise.withResolvers();
  request.mockReturnValueOnce(held.promise);
  choose(spoken, 'ja');
  const old = find('#downloads-unneeded .download');
  expect(old.querySelector('.name').textContent).toBe('en-asr');
  expect(old.querySelector('button').hidden).toBe(true);
  expect(old.querySelector('.flows [data-flow=self]').classList.contains('on')).toBe(true);
  expect(request).not.toHaveBeenCalled();

  await vi.advanceTimersByTimeAsync(500);
  expect(request).toHaveBeenCalledTimes(1);
  expect(old.querySelector('button').hidden).toBe(true);
  held.resolve(response({ ...running, settings: JSON.parse(request.mock.calls[0][1].body) }));
  await settle();
  expect(old.querySelector('button').dataset.action).toBe('delete');
  expect(old.querySelector('button').hidden).toBe(false);
  expect(old.querySelector('.flows [data-flow=self]')).toBeNull();
});

test('a keyboard-focused action follows its artifact between the download groups', async () => {
  app.setScreen('settings');
  flush();
  const buttonFor = group =>
    [...find('#downloads-' + group).querySelectorAll('.download')]
      .find(row => row.querySelector('.name')?.textContent === 'en-asr')
      ?.querySelector('button');
  buttonFor('needed').focus();
  const next = fixture();
  next.settings.Flows.Self.Spoken = 'ja';
  app.apply(next);
  await settle();
  expect(document.activeElement).toBe(buttonFor('missing'));
  const complete = structuredClone(next);
  complete.artifacts['en-asr'].state = 'installed';
  app.apply(complete);
  await settle();
  const installed = buttonFor('unneeded');
  installed.click();
  await settle();
  request.mockImplementationOnce(() => response(next));
  find('#delete-confirm').click();
  find('#delete-confirm').click();
  await settle();
  expect(request.mock.calls.filter(([, opt]) => opt.method === 'DELETE')).toHaveLength(1);
  expect(document.activeElement).toBe(buttonFor('missing'));
});

test('a save refusal stays with the same plan row after Next opens Translation', async () => {
  const ready = fixture();
  for (const item of Object.values(ready.artifacts)) item.state = 'installed';
  app.apply(ready);
  flush();
  const held = Promise.withResolvers();
  request.mockReturnValueOnce(held.promise);
  choose(target, 'ja-easy');
  target.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  find('#next').click();
  flush();
  expect(find('#sub-models .plan-row select')).toBe(spoken);
  held.resolve(response({ error: 'refused' }, 400));
  await settle();
  expect(document.getElementById(spoken.getAttribute('aria-describedby')).textContent).toBe('refused');
});

test.each([false, true])(
  'a held flow request flips the bar switch and a refusal restores enabled=%s',
  async enabled => {
    saved = fixture();
    for (const item of Object.values(saved.artifacts)) item.state = 'installed';
    saved.settings.Flows.Self.Enabled = enabled;
    saved.flows.self.state = enabled ? 'running' : 'stopped';
    app.apply(saved);
    app.setScreen('translation');
    flush();
    const bar = find('#switchbar .fc[data-flow=self]');
    const other = find('#switchbar .fc[data-flow=world]');
    const post = Promise.withResolvers(),
      refresh = Promise.withResolvers();
    request.mockReturnValueOnce(post.promise).mockReturnValueOnce(refresh.promise);
    bar.click();
    bar.click();
    expect(request.mock.calls.map(([url]) => url)).toEqual(['/api/flows/self/' + (enabled ? 'stop' : 'start')]);
    expect(bar.getAttribute('aria-checked')).toBe(String(!enabled));
    expect(bar.getAttribute('aria-disabled')).toBe('true');
    expect(other.getAttribute('aria-disabled')).toBeNull();
    const confirmed = structuredClone(saved);
    confirmed.settings.Flows.Self.Enabled = !enabled;
    app.apply(confirmed);
    flush();
    expect(bar.getAttribute('aria-checked')).toBe(String(!enabled));
    app.apply(saved);
    flush();
    expect(bar.getAttribute('aria-checked')).toBe(String(!enabled));
    post.resolve(response({ error: 'refused' }, 409));
    await settle();
    expect(bar.getAttribute('aria-checked')).toBe(String(enabled));
    expect(find('#fc-error-self').textContent).toBe('refused');
    refresh.resolve(response(saved));
    await settle();
  },
);

test.each(['retry', 'push'])('%s returns focus to the flow switch before hiding Try again', async reason => {
  saved = fixture();
  for (const item of Object.values(saved.artifacts)) item.state = 'installed';
  saved.flows.self = { state: 'error', error: 'Refused', code: 'missing_downloads' };
  app.apply(saved);
  app.setScreen('translation');
  flush();
  const card = find('#switchbar [data-flow=self].card-wrap');
  const button = card.querySelector('.fc'),
    box = card.querySelector('.fc-error'),
    retry = box.querySelector('button');
  retry.focus();
  expect(document.activeElement).toBe(retry);
  let visibleAtTransfer;
  button.addEventListener('focus', () => {
    visibleAtTransfer = !box.hidden && !retry.hidden;
  });
  const held = Promise.withResolvers();
  if (reason === 'retry') {
    request.mockReturnValueOnce(held.promise).mockResolvedValueOnce(response(saved));
    retry.click();
  } else {
    const next = structuredClone(saved);
    next.flows.self = { state: 'stopped' };
    app.apply(next);
    flush();
  }
  expect(visibleAtTransfer).toBe(true);
  expect(document.activeElement).toBe(button);
  if (reason === 'retry') {
    held.resolve(response({ code: 'missing_downloads' }, 409));
    await settle();
    expect(document.activeElement).toBe(button);
    expect(retry.hidden).toBe(false);
  }
});

test('a second switch click within 500 ms stays ignored after the first POST succeeds', async () => {
  const ready = fixture();
  for (const item of Object.values(ready.artifacts)) item.state = 'installed';
  const running = structuredClone(ready);
  running.flows.self.state = 'running';
  running.settings.Flows.Self.Enabled = true;
  request.mockResolvedValueOnce(response(running)).mockResolvedValueOnce(response(ready));
  app.apply(ready);
  app.setScreen('translation');
  flush();
  const button = find('#switchbar .fc[data-flow=self]');
  await clickAt(button, 1000);
  await clickAt(button, 1499);
  expect(request).toHaveBeenCalledTimes(1);
  expect(button.getAttribute('aria-checked')).toBe('true');
  await clickAt(button, 1500);
  expect(request.mock.calls.map(([url]) => url)).toEqual(['/api/flows/self/start', '/api/flows/self/stop']);
});

test('a nonempty log rings only the flow whose download this page started, until its next start', async () => {
  const local = fixture();
  local.artifacts.model.state = local.artifacts.runtime.state = 'installed';
  const ready = structuredClone(local);
  ready.artifacts['en-asr'].state = ready.artifacts['ja-asr'].state = 'installed';
  app.apply(local);
  app.setScreen('settings');
  app.log.event({ kind: 'recognized', phraseId: 1, flow: 'world', text: 'hello', time: new Date().toISOString() });
  flush();
  request.mockResolvedValueOnce(response({}, 202)).mockResolvedValueOnce(response(ready));
  const mine = find('#switchbar .fc[data-flow=self]'),
    theirs = find('#switchbar .fc[data-flow=world]');
  const download = find('#downloads-needed .download:has([data-flow=self]) button[data-action=download]');
  download.click();
  await settle();
  expect(request.mock.calls.map(([url]) => url)).toEqual(['/api/downloads/en-asr', '/api/state']);
  app.setScreen('translation');
  flush();
  expect(mine.classList.contains('next')).toBe(true);
  expect(theirs.classList.contains('next')).toBe(false);
  request.mockResolvedValueOnce(response({ error: 'refused' }, 409)).mockResolvedValueOnce(response(ready));
  mine.click();
  await settle();
  expect(request.mock.calls[2][0]).toBe('/api/flows/self/start');
  expect(mine.dataset.kind).toBe('off');
  expect(mine.classList.contains('next')).toBe(false);
});
