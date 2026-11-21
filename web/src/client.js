import { createMemo, createSignal, createStore, deep, flush, reconcile, snapshot } from 'solid-js';
import { browserLanguage, translate } from './i18n.js';
import { flows, requiredDownloads } from './downloads.js';
import { createLog } from './log.js';

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
export const instanceKey = row => row.slot + ':' + row.pid;
const rowKey = row =>
  row.slot != null
    ? instanceKey(row)
    : (row.id ??
      row.code ??
      row.URL ??
      row.Path ??
      (row.Source != null ? JSON.stringify([row.SourceLang, row.TargetLang, row.Source]) : undefined));

export function createClient() {
  const [state, setState] = createStore({});
  const [unsaved, setUnsaved] = createSignal([]);
  const settings = createMemo(() => {
    if (!unsaved().length) return state.settings;
    // Track nested settings changes so pending edits stay based on the latest server values.
    const shown = structuredClone(deep(state.settings));
    for (const change of unsaved()) change(shown);
    return shown;
  });
  const [localLanguage, setLocalLanguage] = createSignal(null);
  const languageSetting = () => localLanguage() ?? settings()?.UI.Language ?? '';
  const language = createMemo(() => languageSetting() || browserLanguage());
  const t = (key, vars) => translate(language(), key, vars);
  const [announcement, setAnnouncement] = createSignal('');
  const pendingAnnouncements = new Set();
  function announce(text) {
    const alreadyPending = pendingAnnouncements.size > 0;
    pendingAnnouncements.add(text);
    if (alreadyPending) return;
    setAnnouncement('');
    setTimeout(() => {
      const combined = [...pendingAnnouncements].join('\n');
      pendingAnnouncements.clear();
      setAnnouncement(combined);
    }, 50);
  }
  const log = createLog({ t, announce });
  const names = createMemo(() => new Intl.DisplayNames(language(), { type: 'language' }));
  const languageName = code => {
    if (code === 'ja-easy') return t('value.jaEasy');
    if (code === 'auto') return t('value.auto');
    return names().of(code);
  };
  const [screen, setScreen] = createSignal('');
  const [sub, setSub] = createSignal('models');
  const [reconnecting, setReconnecting] = createSignal(false);
  const [phase, setPhase] = createSignal('');
  const [closeHint, setCloseHint] = createSignal(false);
  const lifetime = new AbortController();
  let saves = Promise.resolve(),
    fetching,
    queued,
    revision = 0,
    exitWait,
    exitNotice = false;

  function apply(next) {
    if (lifetime.signal.aborted) return;
    if (state.launch && next.launch !== state.launch) return location.reload();
    if (!state.launch) {
      const ready = flows.some(flow =>
        requiredDownloads(next.languages, next.settings, flow).every(id => next.artifacts[id]?.state === 'installed'),
      );
      setScreen(ready ? 'translation' : 'setup');
    }
    revision++;
    setState(reconcile(next, rowKey));
    if (next.settings && !next.settingsError) setLocalLanguage(null);
  }

  async function api(path, options) {
    let response;
    try {
      response = await fetch('/api/' + path, {
        signal: lifetime.signal,
        ...options,
        headers: { 'Content-Type': 'application/json' },
      });
    } catch (cause) {
      if (!lifetime.signal.aborted) setReconnecting(true);
      throw Object.assign(new Error('Request failed', { cause }), { status: 0 });
    }
    setReconnecting(false);
    if (!response.ok) {
      const { error, code, location } = await response.json().catch(() => ({}));
      throw Object.assign(new Error(error || 'HTTP ' + response.status), { status: response.status, code, location });
    }
    return response;
  }

  function refresh() {
    if (lifetime.signal.aborted) return Promise.resolve();
    if (fetching) {
      queued = true;
      return fetching;
    }
    fetching = (async () => {
      try {
        do {
          queued = false;
          const before = revision;
          let next;
          try {
            next = await (await api('state')).json();
          } catch (e) {
            if (!queued || lifetime.signal.aborted) throw e;
            continue;
          }
          // A delayed GET must not roll back a snapshot the page applied while it waited.
          if (revision === before) apply(next);
          else queued = true;
        } while (queued && !lifetime.signal.aborted);
      } finally {
        fetching = null;
      }
    })();
    return fetching;
  }

  async function update(path, options, offline) {
    const before = revision;
    const accepted = await (await api(path, options)).json();
    if (before === revision) apply(accepted);
    else {
      // Refresh because another snapshot may already include this write and unrelated changes.
      try {
        await refresh();
      } catch {
        offline?.(accepted);
      }
    }
  }

  function flowSwitch(flow) {
    const [requested, setRequested] = createSignal(null),
      [error, setError] = createSignal(null);
    async function change(action) {
      if (requested() !== null) return;
      setRequested(action === 'start');
      setError(null);
      flush();
      try {
        await update('flows/' + flow + '/' + action, { method: 'POST' });
      } catch (e) {
        if (e.status && e.code !== 'missing_downloads') setError(e);
        refresh().catch(() => {});
      } finally {
        setRequested(null);
      }
    }
    return { requested, error, change, lastClick: -Infinity };
  }

  function edit(change, { keepOnError = false, overwrite = false } = {}) {
    setUnsaved(list => [...list, change]);
    const discard = () => setUnsaved(list => list.filter(item => item !== change));
    async function put() {
      if (!unsaved().includes(change)) return;
      // A queued Replace must preserve settings.yaml if an external edit repairs it before the save.
      if (overwrite && !state.settingsError) return discard();
      if (state.settingsError && !overwrite) throw Object.assign(new Error(t('error.notSaved')), { status: 409 });
      const next = structuredClone(snapshot(state.settings));
      if (change(next) === false) {
        discard();
        return false;
      }
      await update('settings', { method: 'PUT', body: JSON.stringify(next) }, () => {
        const latest = structuredClone(snapshot(state.settings));
        change(latest);
        apply({ ...snapshot(state), settings: latest });
      });
      discard();
      return true;
    }
    function save() {
      const run = saves
        .then(async () => {
          try {
            return await put();
          } catch (error) {
            if (error.code !== 'settings_changed') throw error;
            await refresh();
            if (state.settingsError) throw error;
            return put();
          }
        })
        .catch(error => {
          if (!keepOnError) discard();
          throw error;
        });
      saves = run.catch(() => {});
      return run;
    }
    return { discard, save };
  }

  function selectLanguage(value) {
    if (!state.settings || state.settingsError) {
      setLocalLanguage(value);
    } else if (value !== languageSetting()) {
      return edit(s => {
        s.UI.Language = value;
      }).save();
    }
  }

  function replaceSettings() {
    return edit(
      s => {
        if (localLanguage() !== null) s.UI.Language = localLanguage();
      },
      { overwrite: true },
    ).save();
  }

  async function answers() {
    try {
      await fetch('/', { method: 'HEAD', cache: 'no-store', signal: AbortSignal.timeout(1000) });
      return true;
    } catch {
      return false;
    }
  }
  function closeDialogs() {
    for (const dialog of document.querySelectorAll('dialog[open]')) dialog.close();
  }
  function exited() {
    if (lifetime.signal.aborted) return;
    lifetime.abort();
    closeDialogs();
    setPhase('exited');
    setTimeout(() => {
      window.close();
      setTimeout(() => setCloseHint(true), 500);
    }, 3000);
    // A lost stream can leave the app running, so keep checking unless it sent an exit notice.
    if (!exitNotice)
      (async () => {
        do {
          await sleep(2000);
        } while (!(await answers()));
        location.reload();
      })();
  }
  function beginExit() {
    closeDialogs();
    setPhase('waiting');
    flush();
  }
  function waitForExit() {
    return (exitWait ??= (async () => {
      while (!lifetime.signal.aborted && (await answers())) {
        if (!lifetime.signal.aborted) await sleep(250);
      }
      exited();
    })());
  }
  async function exit() {
    if (phase()) return;
    beginExit();
    try {
      await api('exit', { method: 'POST' });
    } catch (e) {
      if (exitWait || lifetime.signal.aborted) return exitWait;
      setPhase('');
      if (e.status) throw e;
      return;
    }
    if (!lifetime.signal.aborted) await waitForExit();
  }

  async function listen(onEvent, onGap) {
    let failures = 0,
      lastID = 0;
    while (!lifetime.signal.aborted) {
      try {
        const response = await api('events');
        if (lifetime.signal.aborted) {
          await response.body.cancel();
          return;
        }
        failures = 0;
        const liveSince = Date.now() - 500;
        try {
          await refresh();
        } catch (e) {
          await response.body.cancel();
          throw e;
        }
        let buffered = '';
        for await (const chunk of response.body.pipeThrough(new TextDecoderStream())) {
          buffered += chunk;
          const lines = buffered.split('\n');
          buffered = lines.pop();
          for (const line of lines) {
            if (!line) continue;
            if (lifetime.signal.aborted) return;
            const event = JSON.parse(line);
            if (event.kind === 'exit') {
              exitNotice = true;
              beginExit();
              await waitForExit();
              return;
            }
            if (event.id <= lastID) continue;
            if (lastID && event.id !== lastID + 1) onGap?.();
            lastID = event.id;
            if (
              ['state', 'settings', 'update', 'download'].includes(event.kind) ||
              (event.kind === 'error' && !event.phraseId)
            )
              refresh().catch(() => {});
            onEvent?.(event, liveSince);
          }
        }
      } catch {
        /* A closed stream and a failed request follow the same reconnect path. */
      }
      if (lifetime.signal.aborted) return;
      if (++failures === 3) return exited();
      setReconnecting(true);
      await sleep(2000);
    }
  }
  function closing() {
    if (!lifetime.signal.aborted) navigator.sendBeacon('/api/closing');
  }

  return {
    log,
    announce,
    announcement,
    switches: { self: flowSwitch('self'), world: flowSwitch('world') },
    state,
    settings,
    apply,
    edit,
    api,
    update,
    refresh,
    languageSetting,
    language,
    languageName,
    selectLanguage,
    replaceSettings,
    t,
    screen,
    setScreen,
    sub,
    setSub,
    phase,
    closeHint,
    reconnecting,
    exit,
    listen,
    closing,
  };
}
