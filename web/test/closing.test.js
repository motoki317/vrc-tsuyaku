import { expect, test, vi } from 'vitest';
import { fixture, response, settle, stream } from './fixture.js';

test('the page accepts a local language and sends closing beacons before startup completes', async () => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  document.head.innerHTML = '<title></title>';
  const beacon = vi.fn(),
    head = Promise.withResolvers(),
    startup = Promise.withResolvers(),
    events = stream();
  Object.defineProperty(navigator, 'sendBeacon', { configurable: true, value: beacon });
  vi.stubGlobal('fetch', url => {
    if (url === '/api/microphones') return response([]);
    if (url === '/api/events') return startup.promise;
    if (url === '/') return head.promise;
    return response(fixture());
  });
  await import('../src/main.jsx');
  const hide = () => {
    for (const persisted of [false, true]) window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted }));
  };
  hide();
  expect(beacon.mock.calls).toEqual([['/api/closing'], ['/api/closing']]);
  document.getElementById('ui-language').click();
  document.querySelector('[data-value=ja]').click();
  await settle();
  expect(document.documentElement.lang).toBe('ja');
  startup.resolve(events.response);
  await settle();
  expect(document.documentElement.lang).toBe('en');
  document.getElementById('exit').click();
  await settle();
  hide();
  expect(beacon).toHaveBeenCalledTimes(4);
  head.reject(new Error('gone'));
  await settle();
  hide();
  expect(beacon).toHaveBeenCalledTimes(4);
  events.close();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
