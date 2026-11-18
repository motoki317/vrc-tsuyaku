import { render } from '@solidjs/web';
import { flush } from 'solid-js';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { Frame } from '../src/frame.jsx';
import { createClient } from '../src/client.js';
import { fixture, response, settle, stream } from './fixture.js';

let app, saved, dispose, request, close, reload;
const node = id => document.getElementById(id);
const count = path => request.mock.calls.filter(([url]) => url === path).length;
const rows = () => [...node('instance-list').children];
const info = (slot, changes = {}) => ({
  slot,
  pid: 4200 + slot,
  url: 'http://localhost/' + slot,
  started: new Date(Date.now() - 37 * 60000).toISOString(),
  ...changes,
});
function show(...others) {
  saved = { ...saved, instance: others.length ? { state: 'waiting', others } : { state: '' } };
  app.apply(saved);
  flush();
}
async function tick(ms) {
  await vi.advanceTimersByTimeAsync(ms);
  await settle();
}
beforeEach(() => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
  document.head.innerHTML = '<title></title>';
  saved = fixture();
  close = vi.spyOn(window, 'close').mockImplementation(() => {});
  reload = vi.fn();
  vi.stubGlobal('location', { reload });
  request = vi.fn(() => response(saved));
  vi.stubGlobal('fetch', (url, options) => (url === '/api/microphones' ? response([]) : request(url, options)));
  dispose = render(() => {
    app = createClient();
    return <Frame app={app} />;
  }, document.body);
  show();
});
afterEach(() => {
  dispose();
  document.body.replaceChildren();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

test.each(['en', 'ja'])('instance modal keeps rows, focus, and localized ages in %s', async locale => {
  saved = fixture();
  saved.settings.UI.Language = locale;
  const interval = vi.spyOn(globalThis, 'setInterval'),
    clear = vi.spyOn(globalThis, 'clearInterval');
  const address = '<example address>',
    first = info(0, { url: address }),
    second = info(1);
  show(first, second);
  const dialog = node('instance-dialog'),
    details = node('instance-details'),
    [row] = rows();
  expect(dialog.open).toBe(true);
  expect(details.open).toBe(false);
  expect(node('instance-title').textContent).toContain('2');
  expect(document.activeElement).toBe(node('instance-replace'));
  expect(row.querySelector('strong').textContent).toBe('PID 4200');
  expect(row.querySelector('p.mono').textContent).toBe(address);
  expect(row.querySelector('p.mono').childElementCount).toBe(0);
  const ago = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }).format(-37, 'minute');
  expect(row.querySelector('time').textContent).toBe(locale === 'en' ? 'Started ' + ago : ago + 'に起動');
  expect(row.querySelector('time').dateTime).toBe(first.started);
  expect(row.querySelector('button').getAttribute('aria-label')).toBe(
    locale === 'en' ? 'Stop PID 4200' : 'PID 4200を終了',
  );
  expect(dialog.dispatchEvent(new Event('cancel', { cancelable: true }))).toBe(false);
  for (const key of ['Escape', 'Escape', 'Tab']) {
    expect(dialog.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }))).toBe(
      key === 'Tab',
    );
  }
  details.open = true;
  row.querySelector('button').focus();
  show({ ...first }, { ...second });
  expect(rows()[0]).toBe(row);
  expect(document.activeElement).toBe(row.querySelector('button'));
  await tick(60000);
  expect(row.querySelector('time').textContent).toContain('38');
  show();
  expect(dialog.open).toBe(false);
  expect(interval.mock.calls.map(([, ms]) => ms).filter(ms => ms === 1000)).toEqual([1000]);
  expect(clear).toHaveBeenCalledWith(
    interval.mock.results[interval.mock.calls.findIndex(([, ms]) => ms === 1000)].value,
  );
});

test('replace posts once per available row, keeps server stopping states, and shows only the refusal', async () => {
  const first = info(0),
    second = info(1);
  show(first, second);
  const held = Promise.withResolvers();
  request.mockImplementation((url, options) => (options.method === 'POST' ? held.promise : response(saved)));
  node('instance-replace').click();
  node('instance-replace').click();
  expect(request.mock.calls.map(([url]) => url)).toEqual(['/api/instance/stop/0', '/api/instance/stop/1']);
  expect(node('instance-replace-text').textContent).toBe('Stopping the earlier VRC\u00a0Tsuyaku…');
  expect(rows()[0].querySelector('button').textContent).toBe('Stopping…');
  expect(rows()[0].querySelector('button').getAttribute('aria-label')).toBe('Stop PID 4200');
  show({ ...first, state: 'stopping' }, { ...second, state: 'stopping' });
  held.resolve(response({}, 202));
  await settle();
  expect(node('instance-replace').getAttribute('aria-disabled')).toBe('true');
  show(first, second, info(2));
  expect(node('instance-replace').hasAttribute('aria-disabled')).toBe(false);
  request.mockClear();
  request.mockImplementation(url =>
    url.endsWith('/1') ? response({ code: 'instance_not_stopped' }, 409) : response(saved),
  );
  node('instance-replace').click();
  await settle();
  expect(request.mock.calls.map(([url]) => url)).toEqual([
    '/api/instance/stop/0',
    '/api/instance/stop/1',
    '/api/instance/stop/2',
    '/api/state',
  ]);
  expect(rows()[1].querySelector('[role=alert]').textContent).toBe('It did not stop. Close its console window.');
  expect(node('instance-details').open).toBe(true);
  show({ ...first, state: 'stopping' }, second);
  rows()[0].querySelector('button').click();
  expect(count('/api/instance/stop/0')).toBe(1);
});

test('row errors reveal Details on change, and a replacement PID loses the old error and focus', async () => {
  const first = info(0),
    failed = { ...first, code: 'instance_not_stopped', error: 'diagnostic' };
  show(failed);
  const details = node('instance-details'),
    [old] = rows();
  expect(details.open).toBe(true);
  details.open = false;
  show({ ...failed });
  expect(details.open).toBe(false);
  details.open = true;
  show(first);
  expect(details.open).toBe(true);
  expect(old.querySelector('[role=alert]').hidden).toBe(true);
  old.querySelector('button').focus();
  show({ ...first, pid: 9999, code: 'unknown', error: 'other failure' });
  await settle();
  expect(rows()[0]).not.toBe(old);
  expect(rows()[0].querySelector('[role=alert]').textContent).toBe('other failure');
  expect(document.activeElement).toBe(details.querySelector('summary'));
});

test('row Stop targets only its slot and Start beside can be pressed only once', async () => {
  show(info(0), info(1));
  rows()[1].querySelector('button').click();
  await settle();
  expect(request.mock.calls.map(([url]) => url)).toEqual(['/api/instance/stop/1', '/api/state']);
  request.mockImplementation(() => response({ ...saved, instance: { state: '' } }));
  node('instance-start').click();
  node('instance-start').click();
  await settle();
  expect(count('/api/instance/start')).toBe(1);
  expect(node('instance-dialog').open).toBe(false);
});

test('Start beside refreshes when a push overtakes its reply', async () => {
  show(info(0));
  const held = Promise.withResolvers(),
    old = { ...saved, instance: { state: '' } };
  request.mockReturnValueOnce(held.promise);
  node('instance-start').click();
  show(info(1));
  held.resolve(response(old));
  await settle();
  expect(count('/api/state')).toBe(1);
  expect(node('instance-dialog').open).toBe(true);
  expect(rows()[0].textContent).toContain('4201');
});

test.each(['notice', 'press'])('%s closes every open dialog and waits for server exit', async trigger => {
  const events = stream(),
    first = Promise.withResolvers(),
    second = Promise.withResolvers(),
    post = Promise.withResolvers(),
    logged = vi.fn();
  request.mockImplementation(url => {
    if (url === '/api/events') return events.response;
    if (url === '/api/exit') return post.promise;
    if (url === '/') return count('/') === 1 ? first.promise : second.promise;
    return response(saved);
  });
  show(info(0));
  for (let i = 0; i < 3; i++) document.body.appendChild(document.createElement('dialog')).showModal();
  let running;
  if (trigger === 'notice') {
    running = app.listen(logged);
    events.send('{"id":42,"kind":"speech"}\n\n{"ki');
    events.send('nd":"exit"}\n{"id":999,"kind":"state"}\n');
  } else {
    node('instance-exit').click();
    expect(node('instance-dialog').open).toBe(false);
    expect(app.phase()).toBe('waiting');
    post.resolve(response({}, 202));
  }
  await settle();
  expect(app.phase()).toBe('waiting');
  expect(node('exit').querySelector('use').getAttribute('href')).toBe('#i-loader-circle');
  expect(node('exit').querySelector('svg').classList.contains('spin')).toBe(true);
  expect(node('main').hasAttribute('inert')).toBe(true);
  expect(document.querySelectorAll('dialog[open]')).toHaveLength(0);
  expect(node('screen-exited').hidden).toBe(true);
  expect(count('/')).toBe(1);
  first.resolve(response());
  await settle();
  await tick(250);
  expect(count('/')).toBe(2);
  second.reject(new Error('gone'));
  await running;
  await settle();
  expect(count('/api/exit')).toBe(trigger === 'press' ? 1 : 0);
  expect(logged.mock.calls.map(([e]) => e.id)).toEqual(trigger === 'notice' ? [42] : []);
  expect(app.phase()).toBe('exited');
  expect(node('screen-exited').hidden).toBe(false);
  expect(document.activeElement).toBe(node('exited-title'));
  if (trigger === 'notice') {
    await tick(3000);
    expect(close).toHaveBeenCalledOnce();
    await tick(500);
    expect(node('exited-close').textContent).toBe('Close this tab.');
    expect(count('/')).toBe(2);
  }
});

test.each([true, false])('an exit notice shares a pending POST wait, lost answer %s', async lost => {
  const events = stream(),
    post = Promise.withResolvers(),
    head = Promise.withResolvers();
  request.mockImplementation(url => {
    if (url === '/api/events') return events.response;
    if (url === '/api/exit') return post.promise;
    if (url === '/') return head.promise;
    return response(saved);
  });
  const running = app.listen();
  await settle();
  const exit = app.exit();
  app.exit();
  events.send('{"kind":"exit"}\n');
  await settle();
  if (lost) post.reject(new Error('lost'));
  else post.resolve(response());
  await settle();
  expect(count('/')).toBe(1);
  expect(count('/api/exit')).toBe(1);
  head.reject(new Error('gone'));
  await Promise.all([running, exit]);
  expect(app.phase()).toBe('exited');
  await tick(3500);
  expect(close).toHaveBeenCalledOnce();
});

test.each(['post accepted', 'post lost', 'HEAD in flight'])('three failures end polling despite %s', async race => {
  const pending = Promise.withResolvers(),
    held = Promise.withResolvers();
  let level;
  request.mockImplementation((url, options) => {
    if (url === '/api/levels') {
      level = options.signal;
      level.addEventListener('abort', () => held.reject(new Error('aborted')));
      return held.promise;
    }
    if (url === '/api/exit') return race === 'HEAD in flight' ? response() : pending.promise;
    if (url === '/' && race === 'HEAD in flight') return pending.promise;
    throw new Error('gone');
  });
  const levels = app.api('levels').catch(() => {});
  const running = app.listen();
  await settle();
  expect(node('connection').hidden).toBe(false);
  const exit = app.exit();
  await tick(2000);
  expect(app.phase()).toBe('waiting');
  await tick(2000);
  await Promise.all([running, levels]);
  expect(level.aborted).toBe(true);
  expect(count('/api/events')).toBe(3);
  if (race === 'post lost') pending.reject(new Error('gone'));
  else pending.resolve(response());
  await exit;
  await app.refresh();
  app.apply({ launch: 'replacement' });
  expect(reload).not.toHaveBeenCalled();
  expect(count('/api/state')).toBe(0);
  expect(count('/')).toBe(race === 'HEAD in flight' ? 1 : 0);
  expect(app.phase()).toBe('exited');
  request
    .mockImplementationOnce(() => {
      throw new Error('gone');
    })
    .mockResolvedValue(response());
  await tick(2000);
  expect(reload).not.toHaveBeenCalled();
  await tick(2000);
  expect(reload).toHaveBeenCalledOnce();
  expect(count('/api/events')).toBe(3);
});

test('two failures recover, then a dropped stream and failed Exit keep reconnecting', async () => {
  const first = stream(),
    second = stream(),
    post = Promise.withResolvers();
  request.mockImplementation(url => {
    if (url === '/api/events') {
      if (count(url) === 3) return first.response;
      if (count(url) === 5) return second.response;
      throw new Error('temporary');
    }
    if (url === '/api/exit') return post.promise;
    if (url === '/') throw new Error('gone');
    return response(saved);
  });
  const running = app.listen();
  await settle();
  await tick(4000);
  expect(count('/api/events')).toBe(3);
  expect(node('connection').hidden).toBe(true);
  const exit = app.exit();
  first.fail();
  post.reject(new Error('lost'));
  await exit;
  await settle();
  expect(app.phase()).toBe('');
  expect(node('connection').hidden).toBe(false);
  await tick(4000);
  expect(count('/api/events')).toBe(5);
  expect(node('connection').hidden).toBe(true);
  second.send('{"kind":"exit"}\n');
  await running;
});

test('a reconnect delay outliving exit sends no later stream request', async () => {
  request.mockImplementation(url => {
    if (url === '/api/exit') return response();
    throw new Error('gone');
  });
  const running = app.listen();
  await settle();
  await app.exit();
  await tick(2000);
  await running;
  expect(count('/api/events')).toBe(1);
});

test('a server exit notice clears an earlier Exit refusal', async () => {
  const events = stream(),
    head = Promise.withResolvers();
  request.mockImplementation(url => {
    if (url === '/api/events') return events.response;
    if (url === '/') return head.promise;
    if (url === '/api/exit') return response({ error: 'refused' }, 409);
    return response(saved);
  });
  const running = app.listen();
  await settle();
  node('exit').click();
  await settle();
  expect(node('exit-error').textContent).toBe('refused');
  events.send('{"kind":"exit"}\n');
  await settle();
  expect(node('exit-error').hidden).toBe(true);
  head.reject(new Error('gone'));
  await running;
});
