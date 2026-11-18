import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { createEffect, createRoot, flush } from 'solid-js';
import { createClient } from '../src/client.js';
import { requiredDownloads } from '../src/downloads.js';
import { fixture, response, settle, stream } from './fixture.js';

let app, stop, saved, request;
beforeEach(() => {
  saved = fixture();
  createRoot(dispose => {
    stop = dispose;
    app = createClient();
  });
  app.apply(saved);
  flush();
  request = vi.fn();
  vi.stubGlobal('fetch', request);
});
afterEach(() => {
  stop();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

test('a local language choice lasts until the next valid settings snapshot', () => {
  app.apply({ ...saved, settingsError: 'invalid settings' });
  flush();
  app.selectLanguage('ja');
  app.apply({ ...saved, settingsError: 'still invalid' });
  flush();
  expect(app.languageSetting()).toBe('ja');
  expect(app.t('instance.details')).toBe('詳細');
  expect(app.state.settings.UI.Language).toBe('en');
  expect(request).not.toHaveBeenCalled();
  app.apply(saved);
  flush();
  expect(app.language()).toBe('en');
  expect(app.t('instance.details')).toBe('Details');
});

test.each(['en', 'ja'])('a blocked edit reports the save error in %s without a PUT', async lang => {
  app.apply({ ...saved, settingsError: 'invalid file' });
  app.selectLanguage(lang);
  flush();
  await expect(
    app
      .edit(s => {
        s.Chatbox.Address = 'new';
      })
      .save(),
  ).rejects.toThrow(app.t('error.notSaved'));
  expect(request).not.toHaveBeenCalled();
});

test('reconciles by natural key and keeps the selected screen across pushes', () => {
  let old, dispose;
  createRoot(d => {
    dispose = d;
    createEffect(
      () => app.state.languages.spoken.map(row => row.code),
      () => {},
    );
  });
  flush();
  old = app.state.languages.spoken[0];
  const next = fixture();
  next.languages.spoken.reverse();
  app.setScreen('settings');
  app.apply(next);
  flush();
  expect(app.state.languages.spoken[1]).toBe(old);
  expect(app.screen()).toBe('settings');
  const reload = vi.fn();
  vi.stubGlobal('location', { reload });
  app.apply({ ...next, launch: 'replacement' });
  expect(reload).toHaveBeenCalledOnce();
  dispose();
});

test('overlapping edits derive downloads immediately, keep pushes live, and save without flicker', async () => {
  const seen = [];
  let dispose;
  createRoot(d => {
    dispose = d;
    createEffect(
      () => app.settings().Flows.Self.Spoken,
      value => {
        seen.push(value);
      },
    );
  });
  const language = app.edit(s => {
    s.Flows.Self.Spoken = 'ja';
  });
  const locale = app.edit(s => {
    s.UI.Language = 'ja';
  });
  flush();
  expect(requiredDownloads(app.state.languages, app.settings(), 'self')).toEqual(['runtime', 'model', 'ja-asr']);
  expect(app.state.settings.Flows.Self.Spoken).toBe('en');
  const held = Promise.withResolvers();
  request
    .mockReturnValueOnce(held.promise)
    .mockImplementationOnce(() => response(result))
    .mockImplementationOnce((_path, options) => response({ ...fixture(), settings: JSON.parse(options.body) }));
  const first = language.save(),
    second = locale.save();
  await settle();
  expect(request).toHaveBeenCalledTimes(1);
  const pushed = fixture();
  pushed.settings.Flows.World.Spoken = 'en';
  app.apply(pushed);
  flush();
  expect(app.settings().Flows.World.Spoken).toBe('en');
  expect(app.settings().UI.Language).toBe('ja');
  const result = structuredClone(pushed);
  result.settings.Flows.Self.Spoken = 'ja';
  held.resolve(response(result));
  await Promise.all([first, second]);
  flush();
  expect(JSON.parse(request.mock.calls[2][1].body)).toEqual({ ...result.settings, UI: { Language: 'ja' } });
  expect(new Set(seen.slice(seen.indexOf('ja')))).toEqual(new Set(['ja']));
  expect(app.settings()).toBe(app.state.settings);
  dispose();
});

test('a refusal reverts only the select and retains a dirty text edit until discarded', async () => {
  request.mockImplementation(() => response({ code: 'invalid' }, 400));
  const text = app.edit(
    s => {
      s.text = 'typed';
    },
    { keepOnError: true },
  );
  const select = app.edit(s => {
    s.UI.Language = 'ja';
  });
  await expect(select.save()).rejects.toMatchObject({ status: 400 });
  await expect(text.save()).rejects.toMatchObject({ status: 400 });
  flush();
  expect(app.settings().UI.Language).toBe('en');
  expect(app.settings().text).toBe('typed');
  text.discard();
  flush();
  expect(app.settings().text).toBeUndefined();
});

test('a discarded queued address does not send a PUT before its replacement', async () => {
  const held = Promise.withResolvers();
  request
    .mockReturnValueOnce(held.promise)
    .mockImplementation((_url, options) => response({ ...saved, settings: JSON.parse(options.body) }));
  const first = app
    .edit(s => {
      s.UI.Language = 'ja';
    })
    .save();
  const old = app.edit(
    s => {
      s.Chatbox.Address = 'old';
    },
    { keepOnError: true },
  );
  const queued = old.save();
  await settle();
  old.discard();
  const last = app
    .edit(s => {
      s.Chatbox.Address = 'new';
    })
    .save();
  held.resolve(response(saved));
  await Promise.all([first, queued, last]);
  expect(request.mock.calls.map(([, opts]) => JSON.parse(opts.body).Chatbox.Address)).toEqual([
    saved.settings.Chatbox.Address,
    'new',
  ]);
});

test('a settings conflict retries the edit over the latest saved settings', async () => {
  const pushed = fixture();
  pushed.settings.Flows.World.Spoken = 'en';
  request
    .mockResolvedValueOnce(response({ code: 'settings_changed' }, 409))
    .mockResolvedValueOnce(response(pushed))
    .mockImplementationOnce((_path, options) => response({ ...pushed, settings: JSON.parse(options.body) }));
  await app
    .edit(s => {
      s.UI.Language = 'ja';
    })
    .save();
  flush();
  expect(app.settings()).toEqual({ ...pushed.settings, UI: { Language: 'ja' } });
});

test('a stale GET cannot replace an accepted save', async () => {
  const held = Promise.withResolvers(),
    accepted = fixture();
  accepted.settings.UI.Language = 'ja';
  request
    .mockReturnValueOnce(held.promise)
    .mockResolvedValueOnce(response(accepted))
    .mockResolvedValueOnce(response(accepted));
  const fetching = app.refresh();
  await app
    .edit(s => {
      s.UI.Language = 'ja';
    })
    .save();
  held.resolve(response(fixture()));
  await fetching;
  flush();
  expect(app.settings().UI.Language).toBe('ja');
  expect(request.mock.calls.map(([path]) => path)).toEqual(['/api/state', '/api/settings', '/api/state']);
});

test('stream events arriving during one GET cause just one more GET, with gaps and duplicates handled', async () => {
  const events = stream(),
    held = Promise.withResolvers(),
    logged = vi.fn(),
    gap = vi.fn();
  request
    .mockResolvedValueOnce(events.response)
    .mockResolvedValueOnce(response(saved))
    .mockReturnValueOnce(held.promise)
    .mockImplementation(path => (path === '/' ? Promise.reject(new Error('gone')) : response(saved)));
  const running = app.listen(logged, gap);
  await settle();
  for (const id of [1, 2, 2, 4, 5, 6]) events.send(JSON.stringify({ kind: 'download', id }) + '\n');
  await settle();
  expect(request).toHaveBeenCalledTimes(3);
  held.resolve(response(saved));
  await settle();
  expect(request).toHaveBeenCalledTimes(4);
  expect(gap).toHaveBeenCalledOnce();
  expect(logged.mock.calls.map(([event]) => event.id)).toEqual([1, 2, 4, 5, 6]);
  vi.useFakeTimers();
  events.send('{"kind":"exit"}\n');
  await running;
});

test.each([true, false])('a delayed PUT keeps a later push, confirming GET online %s', async online => {
  const pending = Promise.withResolvers(),
    fresh = fixture();
  fresh.settings.Flows.World.Spoken = 'en';
  fresh.artifacts.runtime = { state: 'installed' };
  request.mockReturnValueOnce(pending.promise);
  if (online)
    request.mockResolvedValueOnce(response({ ...fresh, settings: { ...fresh.settings, UI: { Language: 'ja' } } }));
  else request.mockRejectedValueOnce(new Error('offline'));
  const saving = app
    .edit(s => {
      s.UI.Language = 'ja';
    })
    .save();
  await settle();
  app.apply(fresh);
  pending.resolve(response({ ...saved, settings: { ...saved.settings, UI: { Language: 'ja' } } }));
  await saving;
  flush();
  expect(app.state.artifacts.runtime.state).toBe('installed');
  expect(app.settings().Flows.World.Spoken).toBe('en');
  expect(app.settings().UI.Language).toBe('ja');
});

test('a failed initial snapshot releases the open stream before retrying', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  const cancel = vi.fn();
  request
    .mockResolvedValueOnce(new Response(new ReadableStream({ cancel })))
    .mockResolvedValueOnce(response({}, 500))
    .mockRejectedValue(new Error('offline'));
  const running = app.listen();
  await settle();
  expect(cancel).toHaveBeenCalledOnce();
  await vi.advanceTimersByTimeAsync(4000);
  await running;
});
