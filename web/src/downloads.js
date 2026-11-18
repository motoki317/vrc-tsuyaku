import { createEffect, createMemo, createSignal, createStore, flush } from 'solid-js';

export const flows = ['self', 'world'];
export const flowKey = { self: 'Self', world: 'World' };
export const flowIcon = { self: 'mic', world: 'users' };
const translatorId = (languages, settings) => languages.translators.find(t => t.id === settings.Translator).artifact;
function recognizerId(languages, settings, flow) {
  const pair = settings.Flows[flowKey[flow]];
  return languages.spoken.find(l => l.code === pair.Spoken).recognizer;
}
export function requiredDownloads(languages, settings, flow) {
  return [...languages.commonArtifacts, translatorId(languages, settings), recognizerId(languages, settings, flow)];
}

export const artifactSize = artifact => artifact.Sources.reduce((sum, source) => sum + source.Size, 0);
export function artifactKind(value) {
  if (value.state === 'installed') return value.error ? 'leftover' : 'installed';
  if (value.state !== 'downloading') return value.error ? 'failed' : 'missing';
  if (!value.progress.Phase) return 'queued';
  return value.progress.Phase === 'download' ? 'downloading' : 'verifying';
}

export function formatBytes(app, bytes, done) {
  const gb = bytes >= 1e9;
  const digits = !gb && bytes >= 1e7 ? 0 : 1;
  const number = new Intl.NumberFormat(app.language(), {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });
  const size = value => number.format(value / (gb ? 1e9 : 1e6));
  return done === undefined
    ? app.t(gb ? 'value.gigabytes' : 'value.megabytes', { n: size(bytes) })
    : app.t(gb ? 'value.progressGigabytes' : 'value.progressMegabytes', { done: size(done), total: size(bytes) });
}
export const percent = (app, fraction) => new Intl.NumberFormat(app.language(), { style: 'percent' }).format(fraction);

export function createDownloads(app) {
  const artifacts = () => app.state.artifacts;
  const byName = createMemo(() =>
    Object.keys(artifacts() ?? {}).sort((a, b) =>
      artifacts()[a].artifact.Name.localeCompare(artifacts()[b].artifact.Name, app.language()),
    ),
  );
  const required = Object.fromEntries(
    flows.map(flow => [
      flow,
      createMemo(() => (app.settings() ? requiredDownloads(app.state.languages, app.settings(), flow) : [])),
    ]),
  );
  const translator = () => translatorId(app.state.languages, app.settings());
  const recognizer = flow => recognizerId(app.state.languages, app.settings(), flow);
  const needed = createMemo(() => [...new Set(flows.flatMap(flow => required[flow]()))]);
  const ready = flow =>
    required[flow]().length > 0 && required[flow]().every(id => artifacts()[id]?.state === 'installed');
  const kind = id => artifactKind(artifacts()[id]);
  const bytes = ids => ids.reduce((sum, id) => sum + artifactSize(artifacts()[id].artifact), 0);
  const active = flow => ['running', 'starting'].includes(app.state.flows[flow]?.state);
  // Active flows keep using their models until the server applies the edit.
  const users = id =>
    flows.filter(flow =>
      (active(flow) ? requiredDownloads(app.state.languages, app.state.settings, flow) : required[flow]()).includes(id),
    );
  const startable = id => ['missing', 'failed'].includes(kind(id));
  const [pending, setPending] = createStore({});
  const [errors, setErrors] = createStore({});
  const [batch, setBatch] = createStore({});
  const [license, setLicense] = createSignal(null);
  const [deletion, setDeletion] = createSignal(null);
  const awaited = { self: new Set(), world: new Set() };
  const [ringing, setRinging] = createStore({});
  for (const flow of flows) {
    createEffect(
      () => ({
        ids: required[flow](),
        ready: ready(flow),
        off:
          app.state.flows?.[flow]?.state === 'stopped' &&
          !app.settings()?.Flows[flowKey[flow]].Enabled &&
          app.switches[flow].requested() === null,
      }),
      v => {
        if (!v.ready || !v.off)
          setRinging(r => {
            r[flow] = false;
          });
        // A completed download invites a start only if this page requested it for this flow.
        else if (v.ids.some(id => awaited[flow].has(id)))
          setRinging(r => {
            r[flow] = true;
          });
        if (v.ready) awaited[flow].clear();
      },
    );
  }
  createEffect(
    () => Object.entries(artifacts() ?? {}).map(([id, v]) => [id, v.state, v.error, pending[id]]),
    rows => {
      const running = rows.some(([, state, , waiting]) => state === 'downloading' || waiting);
      setBatch(draft => {
        for (const [id, state] of rows) {
          if (!running) delete draft[id];
          else if (state === 'downloading') draft[id] = true;
        }
      });
      setErrors(draft => {
        for (const [id, state, error] of rows) {
          if (state !== 'missing' || error) delete draft[id];
        }
      });
    },
  );
  function progress(ids) {
    let done = 0,
      total = 0;
    for (const id of ids) {
      const v = artifacts()[id];
      if (!batch[id] && v.state !== 'downloading' && !pending[id]) continue;
      const size = artifactSize(v.artifact);
      total += size;
      if (v.state === 'installed') done += size;
      if (v.state === 'downloading') done += Math.min(v.progress.Downloaded, size);
    }
    return { done, total, fraction: total ? done / total : 0 };
  }
  function view(ids, all = false) {
    const missing = ids.filter(startable),
      failed = ids.find(id => kind(id) === 'failed');
    const downloading = ids.some(id => ['downloading', 'verifying'].includes(kind(id)));
    const queued = ids.some(id => pending[id] || kind(id) === 'queued');
    if (all && (downloading || queued)) return { kind: 'running', ...progress(ids) };
    if (failed)
      return { kind: all ? 'retry' : 'failed', ids: missing, bytes: bytes(missing), error: artifacts()[failed] };
    if (downloading) return { kind: 'downloading', ...progress(ids) };
    if (queued) return { kind: 'queued' };
    if (missing.length) return { kind: all ? 'idle' : 'missing', ids: missing, bytes: bytes(missing) };
    return { kind: all ? 'none' : 'ready' };
  }
  async function start(ids, opener) {
    if (app.state.settingsError || license() || app.phase()) return;
    const shared = [...app.state.languages.commonArtifacts, translator()];
    const rank = id => {
      if (shared.includes(id)) return 0;
      return required.self().includes(id) ? 1 : 2;
    };
    ids = ids.filter(id => startable(id) && !pending[id]).sort((a, b) => rank(a) - rank(b) || bytes([a]) - bytes([b]));
    if (!ids.length) return;
    const key = id => artifacts()[id].artifact.AcceptanceKey;
    const unaccepted = ids.filter(id => key(id) && !app.settings().LicensesAccepted?.[key(id)]);
    for (const k of new Set(unaccepted.map(key))) {
      const accepted = await new Promise(resolve => {
        setLicense({ ids: unaccepted.filter(id => key(id) === k), opener, resolve });
        flush();
      });
      if (!accepted || app.phase()) return;
    }
    setPending(s => {
      for (const id of ids) s[id] = true;
    });
    setBatch(s => {
      for (const id of ids) s[id] = true;
    });
    setErrors(s => {
      for (const id of ids) delete s[id];
    });
    for (const flow of flows) {
      for (const id of ids) {
        if (required[flow]().includes(id)) awaited[flow].add(id);
      }
    }
    flush();
    const accepted = [];
    for (const id of ids) {
      try {
        await app.api('downloads/' + id, {
          method: 'POST',
          body: JSON.stringify({ acceptLicense: unaccepted.includes(id) }),
        });
        accepted.push(id);
      } catch (e) {
        if (e.status)
          setErrors(s => {
            s[id] = e;
          });
        setPending(s => {
          delete s[id];
        });
      }
    }
    await app.refresh().catch(() => {});
    setPending(s => {
      for (const id of accepted) delete s[id];
    });
  }
  return {
    byName,
    required,
    translator,
    recognizer,
    needed,
    anyReady: () => flows.some(ready),
    users,
    active,
    kind,
    bytes,
    view,
    start,
    pending,
    errors,
    ringing,
    license,
    setLicense,
    deletion,
    setDeletion,
  };
}
