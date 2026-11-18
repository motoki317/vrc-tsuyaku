import { createRoot, flush } from 'solid-js';
import { expect, test, vi } from 'vitest';
import { createClient } from '../src/client.js';
import { createDownloads } from '../src/downloads.js';
import { fixture, response, settle } from './fixture.js';

test('the shown languages drive both flow controls and Download all through a refused edit', () => {
  const [app, downloads, dispose] = createRoot(stop => {
    const app = createClient();
    return [app, createDownloads(app), stop];
  });
  const state = fixture();
  for (const [id, v] of Object.entries(state.artifacts)) {
    if (id !== 'en-asr') v.state = 'installed';
  }
  app.apply(state);
  flush();
  expect(downloads.view(downloads.needed(), true)).toMatchObject({ kind: 'idle', ids: ['en-asr'], bytes: 3000 });
  const edit = app.edit(s => {
    s.Flows.Self.Spoken = 'ja';
  });
  flush();
  expect(downloads.needed()).toEqual(['runtime', 'model', 'ja-asr']);
  expect(downloads.anyReady()).toBe(true);
  expect(downloads.view(downloads.needed(), true)).toEqual({ kind: 'none' });
  edit.discard();
  flush();
  expect(downloads.view(downloads.required.self())).toMatchObject({ kind: 'missing', bytes: 3000 });
  dispose();
});

test('accepted POSTs stay queued through refresh and a finished item counts towards batch progress', async () => {
  const [app, d, dispose] = createRoot(stop => {
    const app = createClient();
    return [app, createDownloads(app), stop];
  });
  const state = fixture(),
    held = Promise.withResolvers(),
    requests = [];
  app.apply(state);
  flush();
  vi.stubGlobal('fetch', async url => {
    requests.push(url);
    return url === '/api/state' ? held.promise : response({}, 202);
  });
  try {
    const starting = d.start(['model', 'runtime']);
    await settle();
    expect(d.view(d.needed(), true)).toMatchObject({ kind: 'running', total: 3000 });
    await d.start(['runtime']);
    expect(requests).toEqual(['/api/downloads/runtime', '/api/downloads/model', '/api/state']);
    const pushed = fixture();
    pushed.artifacts.runtime.state = 'installed';
    Object.assign(pushed.artifacts.model, {
      state: 'downloading',
      progress: { Phase: 'download', Downloaded: 200, Total: 2000 },
    });
    held.resolve(response(pushed));
    await starting;
    flush();
    expect(d.view(d.needed(), true)).toMatchObject({ kind: 'running', done: 1200, total: 3000 });
  } finally {
    dispose();
    vi.unstubAllGlobals();
  }
});

test('clearing a finished batch leaves a newer retry pending', async () => {
  const [app, d, dispose] = createRoot(stop => {
    const app = createClient();
    return [app, createDownloads(app), stop];
  });
  app.apply(fixture());
  flush();
  const firstGet = Promise.withResolvers(),
    retryPost = Promise.withResolvers();
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(response({ error: 'refused' }, 409))
    .mockReturnValueOnce(firstGet.promise)
    .mockReturnValueOnce(retryPost.promise)
    .mockResolvedValueOnce(response(fixture()));
  vi.stubGlobal('fetch', fetch);
  try {
    const first = d.start(['runtime']);
    await settle();
    expect(d.errors.runtime.status).toBe(409);
    const retry = d.start(['runtime']);
    await settle();
    firstGet.resolve(response(fixture()));
    await first;
    flush();
    expect(d.pending.runtime).toBe(true);
    await d.start(['runtime']);
    expect(fetch).toHaveBeenCalledTimes(3);
    retryPost.resolve(response({}, 202));
    await retry;
  } finally {
    dispose();
    vi.unstubAllGlobals();
  }
});
