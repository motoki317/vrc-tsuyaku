import { render } from '@solidjs/web';
import { flush } from 'solid-js';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { createClient } from '../src/client.js';
import { Frame } from '../src/frame.jsx';
import { fixture, response, settle } from './fixture.js';

let app, state, dispose, request;
const find = selector => document.querySelector(selector);
function input(node, type) {
  node.dispatchEvent(new Event(type, { bubbles: true }));
  flush();
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  document.head.innerHTML = '<title></title>';
  state = fixture();
  state.languages.translators.push({ id: 'large', artifact: 'large-model', speed: 2 });
  state.artifacts['large-model'] = {
    state: 'missing',
    artifact: { Name: 'Large', MemoryBytes: 2e9, Sources: [{ Size: 5e8 }] },
  };
  for (const [id, item] of Object.entries(state.artifacts)) {
    if (id !== 'large-model') item.state = 'installed';
  }
  request = vi.fn(url =>
    url === '/api/microphones' ? response([{ ID: 'mic', Name: 'Microphone' }]) : response(state),
  );
  vi.stubGlobal('fetch', request);
  dispose = render(() => {
    app = createClient();
    return <Frame app={app} />;
  }, document.body);
  app.apply(structuredClone(state));
  app.setScreen('settings');
  flush();
});
afterEach(() => {
  dispose();
  document.body.replaceChildren();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

test('Enter commits the selected translator immediately', async () => {
  const radio = find('[value=large]');
  request.mockImplementation((_url, { body }) => response({ ...state, settings: JSON.parse(body) }));
  radio.checked = true;
  input(radio, 'input');
  input(radio, 'change');
  radio.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
  await settle();
  expect(app.state.settings.Translator).toBe('large');
  expect(find('#translator-saved').hidden).toBe(false);
});

test('radio edits update memory, downloads and switches before saving and roll back on refusal', async () => {
  const radio = find('[value=large]'),
    memory = find('#memory-total').textContent;
  const post = Promise.withResolvers();
  request.mockImplementation(url => (url === '/api/settings' ? post.promise : response(state)));
  radio.checked = true;
  input(radio, 'input');
  expect(app.settings().Translator).toBe('large');
  expect(find('#memory-total').textContent).not.toBe(memory);
  expect(find('#downloads-needed').textContent).toContain('Large');
  expect(find('#switchbar .fc').dataset.kind).toBe('missing');
  input(radio, 'change');
  await vi.advanceTimersByTimeAsync(499);
  expect(request.mock.calls.filter(([url]) => url === '/api/settings')).toHaveLength(0);
  await vi.advanceTimersByTimeAsync(1);
  expect(JSON.parse(request.mock.calls.find(([url]) => url === '/api/settings')[1].body).Translator).toBe('large');
  post.resolve(response({ code: 'disk_write' }, 500));
  await settle();
  expect(find('input[value=small]').checked).toBe(true);
  expect(radio.checked).toBe(false);
  expect(find('#memory-total').textContent).toBe(memory);
  expect(find('#translator-error').hidden).toBe(false);
});

test('refreshing the device list keeps a saved microphone that moves within it', async () => {
  const devices = ['a', 'b', 'c', 'd'].map(ID => ({ ID, Name: ID }));
  request.mockImplementation(() => response(devices));
  find('#refresh-mics').click();
  await settle();
  state.settings.Flows.Self.Microphone = 'c';
  app.apply(structuredClone(state));
  flush();
  const mic = find('#microphone');
  expect(mic.value).toBe('c');
  devices.unshift(...devices.splice(2, 1));
  find('#refresh-mics').click();
  await settle();
  expect([...mic.options].map(o => o.value)).toEqual(['', 'c', 'a', 'b', 'd']);
  expect(mic.value).toBe('c');
});

test('microphone edits clear the warning and a refusal restores the device and description', async () => {
  state.settings.Flows.Self.Microphone = 'unplugged';
  app.apply(structuredClone(state));
  await settle();
  const mic = find('#microphone'),
    warning = find('#microphone-state');
  expect(mic.value).toBe('unplugged');
  expect(warning.hidden).toBe(false);
  mic.value = 'mic';
  input(mic, 'input');
  expect(app.settings().Flows.Self.Microphone).toBe('mic');
  expect(warning.hidden).toBe(true);
  expect(mic.querySelector('option[value=unplugged]')).toBeTruthy();
  expect(mic.getAttribute('aria-describedby')).toBe('');
  request.mockResolvedValue(response({ code: 'disk_write' }, 500));
  input(mic, 'focusout');
  await settle();
  expect(mic.value).toBe('unplugged');
  expect(warning.hidden).toBe(false);
  expect(mic.getAttribute('aria-describedby')).toBe('microphone-state microphone-error');
});

test('a failed list refresh retains devices and a successful edit announces Saved before its mark fades', async () => {
  await settle();
  const mic = find('#microphone'),
    mark = find('#microphone-saved');
  request.mockResolvedValueOnce(response({}, 503));
  find('#refresh-mics').click();
  await settle();
  expect(find('#microphone-error [role=img]').getAttribute('aria-label')).toBe('Refresh');
  expect(mic.querySelector('[value=mic]')).toBeTruthy();
  request.mockResolvedValueOnce(response([{ ID: 'mic', Name: 'Microphone' }]));
  find('#refresh-mics').click();
  await settle();
  expect(find('#microphone-error').hidden).toBe(true);
  request.mockImplementation((_url, { body }) => response({ ...state, settings: JSON.parse(body) }));
  mic.value = 'mic';
  input(mic, 'input');
  input(mic, 'change');
  await vi.advanceTimersByTimeAsync(500);
  await settle();
  expect(mic.value).toBe('mic');
  expect(mark.hidden).toBe(false);
  await vi.advanceTimersByTimeAsync(50);
  flush();
  expect(find('#announce').textContent).toBe('Saved');
  await vi.advanceTimersByTimeAsync(1450);
  flush();
  expect(mark.classList.contains('fade')).toBe(true);
  await vi.advanceTimersByTimeAsync(400);
  flush();
  expect(mark.hidden).toBe(true);
});

test.each(['self', 'world'])(
  'a manual gain is remembered across Automatic and rolls back on refusal for %s',
  async flow => {
    const key = flow === 'self' ? 'Self' : 'World';
    state.settings.Flows[key].CutLevel = -72;
    app.apply(structuredClone(state));
    flush();
    const auto = find('#automatic-' + flow),
      range = find('#cut-level-' + flow);
    auto.checked = true;
    input(auto, 'input');
    expect(range.hidden).toBe(true);
    auto.checked = false;
    input(auto, 'input');
    expect(range.value).toBe('-72');
    range.value = '-10';
    input(range, 'input');
    expect(app.settings().Flows[key].CutLevel).toBe(-10);
    expect(range.getAttribute('aria-valuetext')).toContain('-10');
    request.mockResolvedValue(response({ code: 'disk_write' }, 500));
    input(range, 'focusout');
    await settle();
    expect(range.value).toBe('-72');
    expect(find('#level-error-' + flow).hidden).toBe(false);
  },
);

test('a hidden Audio sub aborts its reading and a late reply cannot overwrite the next poll', async () => {
  const stale = Promise.withResolvers(),
    live = Promise.withResolvers();
  const meter = find('#level-self [role=meter]');
  request.mockReturnValueOnce(stale.promise).mockReturnValueOnce(live.promise);
  app.setSub('audio');
  flush();
  const old = request.mock.lastCall[1].signal;
  await vi.advanceTimersByTimeAsync(500);
  app.setSub('models');
  flush();
  expect(old.aborted).toBe(true);
  app.setSub('audio');
  flush();
  live.resolve(response({ self: 5 }));
  await settle();
  expect(meter.getAttribute('aria-valuenow')).toBe('0');
  expect(find('#level-inactive-world').hidden).toBe(false);
  stale.resolve(response({ self: -22 }));
  await settle();
  expect(meter.getAttribute('aria-valuenow')).toBe('0');
  app.setScreen('translation');
  flush();
  await vi.advanceTimersByTimeAsync(500);
  expect(request.mock.calls.filter(([url]) => url === '/api/levels')).toHaveLength(2);
});

test('About follows build and update changes and keeps the catalog license credit', () => {
  expect(find('#app-version').textContent).toBe('Development build');
  state.app = { ...state.app, version: 'v1', revision: 'abcdef012345', time: '2026-01-01T00:00:00Z', modified: true };
  state.artifacts.runtime.artifact.Licenses = [
    { Name: 'MIT', Credit: 'Attribution', URL: 'https://example.test/license' },
  ];
  for (const [status, icon] of [
    ['failed', 'circle-alert'],
    ['checking', 'loader-circle'],
    ['current', 'check'],
    ['available', 'circle-arrow-up'],
  ]) {
    state.update = { state: status, version: 'v2', url: 'https://example.test/releases/v2' };
    app.apply(structuredClone(state));
    flush();
    expect(find('#app-update use').getAttribute('href')).toBe('#i-' + icon);
  }
  expect(find('#app-version .mono').textContent).toBe('abcdef0');
  expect(find('#app-version time').dateTime).toBe(state.app.time);
  expect(find('#app-update a').href).toBe(state.update.url);
  expect(find('#licenses .credit').textContent).toBe('Attribution');
});

test.each(['settings', 'data'])(
  'opening %s once while pending reports a refusal and clears it on retry',
  async target => {
    const button = find('.rail-foot [data-open=' + target + ']'),
      error = find('#open-' + target + '-error');
    const first = Promise.withResolvers();
    request.mockReturnValueOnce(first.promise);
    button.click();
    button.click();
    flush();
    expect(button.getAttribute('aria-disabled')).toBe('true');
    expect(request.mock.calls.filter(([url]) => url === '/api/open/' + target)).toHaveLength(1);
    first.resolve(response({ error: 'Cannot open' }, 503));
    await settle();
    expect(error.textContent).toBe('Cannot open');
    button.click();
    await settle();
    expect(error.hidden).toBe(true);
  },
);

test('replace requires confirmation, keeps the local language and never double-submits', async () => {
  const dialog = find('#replace-dialog'),
    opener = find('#replace-settings'),
    cancel = dialog.querySelector('[data-close]');
  dialog.showModal = () => {
    dialog.open = true;
  };
  dialog.close = () => {
    dialog.open = false;
    dialog.dispatchEvent(new Event('close'));
  };
  state.settingsError = 'invalid settings';
  app.apply(structuredClone(state));
  flush();
  app.selectLanguage('ja');
  flush();
  expect(find('#settings-error-text').textContent).toBe('invalid settings');
  opener.click();
  expect(document.activeElement).toBe(cancel);
  cancel.click();
  expect(document.activeElement).toBe(opener);
  expect(request.mock.calls.filter(([url]) => url === '/api/settings')).toHaveLength(0);
  opener.click();
  request.mockResolvedValueOnce(response({ error: 'Cannot write' }, 500));
  find('#replace-confirm').click();
  await settle();
  expect(find('#replace-error').textContent).toBe('Cannot write');
  expect(document.activeElement).toBe(opener);
  opener.click();
  const put = Promise.withResolvers();
  request.mockReturnValueOnce(put.promise);
  find('#replace-confirm').click();
  find('#replace-confirm').click();
  await settle();
  const writes = request.mock.calls.filter(([url]) => url === '/api/settings');
  expect(writes).toHaveLength(2);
  const saved = JSON.parse(writes.at(-1)[1].body);
  expect(saved).toEqual({ ...state.settings, UI: { Language: 'ja' } });
  put.resolve(response({ ...state, settings: saved, settingsError: '' }));
  await settle();
  expect(find('#screen-settings-error').hidden).toBe(true);
  expect(document.activeElement).toBe(find('.tab[aria-current=page]'));
});

test('a Replace conflict preserves the language in an externally repaired file', async () => {
  state.settingsError = 'invalid settings';
  app.apply(structuredClone(state));
  flush();
  app.selectLanguage('ja');
  flush();
  const repaired = { ...state, settingsError: '' };
  request.mockResolvedValueOnce(response({ code: 'settings_changed' }, 409)).mockResolvedValueOnce(response(repaired));
  await app.replaceSettings();
  const languages = request.mock.calls
    .filter(([url]) => url === '/api/settings')
    .map(([, opts]) => JSON.parse(opts.body).UI.Language);
  expect(languages).toEqual(['ja']);
  flush();
  expect(app.settings().UI.Language).toBe('en');
});

test('dragging a gain again cancels the previous settle until the new drag ends', async () => {
  const slider = find('#cut-level-self');
  slider.value = '-40';
  input(slider, 'input');
  input(slider, 'change');
  await vi.advanceTimersByTimeAsync(450);
  slider.value = '-32';
  input(slider, 'input');
  await vi.advanceTimersByTimeAsync(100);
  expect(request.mock.calls.filter(([url]) => url === '/api/settings')).toHaveLength(0);
  request.mockImplementation((_url, { body }) => response({ ...state, settings: JSON.parse(body) }));
  input(slider, 'change');
  await vi.advanceTimersByTimeAsync(500);
  expect(app.state.settings.Flows.Self.CutLevel).toBe(-32);
});
