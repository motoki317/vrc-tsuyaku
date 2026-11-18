import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { render } from '@solidjs/web';
import { TranslationLog } from '../src/translation-log.jsx';
import { createRoot, flush } from 'solid-js';
import { createClient } from '../src/client.js';
import { fixture, response, settle, stream } from './fixture.js';

let app, log, dispose;
const now = '2026-10-10T12:01:02Z';
function emit(kind, phraseId, extra = {}) {
  log.event({ flow: 'self', time: now, kind, phraseId, ...extra }, Date.parse(now));
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] });
  vi.setSystemTime(new Date(now));
  createRoot(d => {
    dispose = d;
    app = createClient();
    log = app.log;
  });
  app.apply(fixture());
  flush();
});
afterEach(() => {
  dispose();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

test('recognition, translation, merge and delivery keep each message at its first bubble', () => {
  emit('recognized', 1, { text: 'a' });
  flush();
  expect(log.rows[0]).toMatchObject({ original: 'a', message: 1 });
  emit('translated', 1, { text: 'A' });
  emit('sent', 1);
  emit('recognized', 2, { flow: 'world', text: 'intervening' });
  emit('translated', 3, { original: 'b', text: 'B' });
  flush();
  expect(log.rows[0].status).toBe('sent');
  expect(log.rows[2].status).toBe('sending');
  emit('merged', 3, { into: 1, original: 'ab', text: 'AB' });
  emit('sent', 3);
  flush();
  expect(log.rows.map(r => r.phraseId)).toEqual([3, 2]);
  expect(log.rows[0]).toMatchObject({ message: 1, original: 'ab', translation: 'AB', status: 'sent' });
  emit('merged', 4, { into: 1, original: 'abc', text: 'ABC' });
  emit('error', 4, { code: 'template', text: 'invalid' });
  emit('error', 1, { text: 'expired' });
  flush();
  expect(log.rows.map(r => r.phraseId)).toEqual([4, 2]);
  expect(log.rows[0]).toMatchObject({ status: 'failed', code: 'template', error: 'invalid' });
});

test('eviction bounds the log and a merged replay can start without its original bubble', () => {
  for (let i = 1; i <= 302; i++) emit('recognized', i);
  emit('merged', 304, { into: 1, original: 'replayed', text: 'translation' });
  flush();
  expect(log.rows).toHaveLength(300);
  expect(log.rows[0].phraseId).toBe(4);
  expect(log.rows.at(-1)).toMatchObject({ phraseId: 304, message: 1, original: 'replayed' });
});

test('speech ends on recognition or timeout and an older phrase cannot hide a new start', async () => {
  const speak = text => emit('speech', undefined, { text });
  speak('start');
  speak('end');
  flush();
  expect(log.speech.self).toBe('ending');
  speak('start');
  emit('recognized', 1);
  flush();
  await vi.advanceTimersByTimeAsync(2100);
  flush();
  expect(log.speech.self).toBe('open');
  vi.setSystemTime(new Date(now));
  speak('end');
  emit('recognized', 2);
  flush();
  expect(log.speech.self).toBe('');
  speak('start');
  speak('end');
  await vi.advanceTimersByTimeAsync(2000);
  flush();
  expect(log.speech.self).toBe('');
});

test('only live pills and translations announce, and running resolves errors for its own flow', async () => {
  log.event({ kind: 'state', flow: 'world', text: 'stopped', time: now }, Date.parse(now) + 1);
  await vi.advanceTimersByTimeAsync(50);
  flush();
  expect(app.announcement()).toBe('');
  emit('error', undefined, { code: 'unexpected', text: 'problem' });
  emit('error', undefined, { flow: 'world', text: 'other' });
  await vi.advanceTimersByTimeAsync(50);
  flush();
  expect(app.announcement()).toBe("Others' voices: Stopped by an error other");
  emit('state', undefined, { text: 'running' });
  emit('translated', 9, { text: 'Hi' });
  await vi.advanceTimersByTimeAsync(50);
  flush();
  expect(app.announcement()).toBe('My voice: Hi');
  expect(log.rows[1].resolved).toBe(true);
  expect(log.rows[2].resolved).toBeUndefined();
});

test('stopping one flow closes only its pending phrases', () => {
  emit('recognized', 1);
  emit('recognized', 2, { flow: 'world' });
  emit('state', undefined, { text: 'stopped' });
  flush();
  expect(log.rows[0].unfinished).toBe(true);
  expect(log.rows[1].unfinished).toBeUndefined();
});

test('recognition counts unread phrases until following resumes', () => {
  log.follow(false);
  emit('recognized', 1);
  emit('recognized', 2);
  flush();
  expect(log.unread()).toBe(2);
  log.follow(true);
  flush();
  expect(log.unread()).toBe(0);
});

test('the log follows new bubbles until the player scrolls up, then resumes at the bottom', () => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  const unmount = render(() => TranslationLog({ app }), document.body);
  try {
    const viewport = document.getElementById('log');
    let height = 700,
      top = 0;
    Object.defineProperties(viewport, {
      clientHeight: { value: 100 },
      scrollHeight: { get: () => height },
      scrollTop: {
        get: () => top,
        set: y => {
          top = Math.min(y, height - 100);
        },
      },
    });
    app.setScreen('translation');
    flush();
    expect(top).toBe(600);
    height = 800;
    emit('recognized', 1);
    flush();
    expect(top).toBe(700);
    viewport.scrollTop = 400;
    viewport.dispatchEvent(new Event('scroll'));
    height = 900;
    emit('recognized', 2);
    flush();
    expect(top).toBe(400);
    viewport.scrollTop = 800;
    viewport.dispatchEvent(new Event('scroll'));
    height = 1000;
    emit('recognized', 3);
    flush();
    expect(top).toBe(900);
  } finally {
    unmount();
  }
});

test('a stream gap closes pending phrases in both flows and phrase events issue no state GET', async () => {
  const events = stream();
  const get = vi.fn(url => {
    if (url === '/api/events') return events.response;
    if (url === '/') return Promise.reject(new Error('exited'));
    return response(fixture());
  });
  vi.stubGlobal('fetch', get);
  const listening = app.listen(log.event, log.closePending);
  await settle();
  function send(id, kind, flow, phraseId, extra) {
    events.send(JSON.stringify({ id, kind, flow, phraseId, time: now, text: 'x', ...extra }) + '\n');
  }
  send(1, 'recognized', 'world', 7);
  send(2, 'translated', 'self', 8);
  send(3, 'merged', 'self', 9, { into: 6, original: 'merged' });
  send(4, 'sent', 'self', 9);
  send(5, 'error', 'self', 9);
  send(7, 'speech', 'world');
  await settle();
  expect(log.rows.map(r => r.phraseId)).toEqual([7, 8, 9]);
  expect(log.rows[0].unfinished).toBe(true);
  expect(log.rows[1].status).toBe('unfinished');
  expect(log.rows[2].status).toBe('failed');
  expect(get.mock.calls.map(([url]) => url)).toEqual(['/api/events', '/api/state']);
  send(8, 'state', 'world');
  await settle();
  expect(get.mock.calls.filter(([url]) => url === '/api/state')).toHaveLength(2);
  events.send('{"kind":"exit"}\n');
  await listening;
});

test('a second live event leaves the preceding announcement audible', async () => {
  emit('error', undefined, { text: 'problem' });
  await vi.advanceTimersByTimeAsync(25);
  emit('translated', 1, { text: 'Hello' });
  await vi.advanceTimersByTimeAsync(25);
  flush();
  expect(app.announcement()).toBe('My voice: Stopped by an error problem');
  await vi.advanceTimersByTimeAsync(25);
  flush();
  expect(app.announcement()).toBe('My voice: Hello');
});
