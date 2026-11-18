import { createEffect, createMemo, createSignal, createStore, flush, For, onCleanup, untrack } from 'solid-js';
import { instanceKey } from './client.js';
import { errorMessage } from './i18n.js';

function age(started, now, language) {
  const seconds = (Date.parse(started) - now) / 1000;
  const [unit, scale] = [
    ['day', 86400],
    ['hour', 3600],
    ['minute', 60],
    ['second', 1],
  ].find(([, scale]) => Math.abs(seconds) >= scale || scale === 1);
  return new Intl.RelativeTimeFormat(language, { numeric: 'auto' }).format(Math.round(seconds / scale), unit);
}

export function Instances({ app, exit }) {
  let dialog, details, replace, summary;
  const [requests, setRequests] = createStore({});
  const [starting, setStarting] = createSignal(false);
  const [refusal, setRefusal] = createSignal(null);
  const [now, setNow] = createSignal(Date.now());
  const waiting = createMemo(() => app.state.instance?.state === 'waiting' && !app.phase());
  const others = () => app.state.instance?.others ?? [];
  const busy = row => row.state === 'stopping' || requests[instanceKey(row)]?.pending;
  const stopping = () => others().some(busy);
  createEffect(waiting, open => {
    if (!open) {
      if (dialog.open) dialog.close();
      return;
    }
    dialog.showModal();
    replace.focus();
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  });
  async function stop(rows) {
    if (!waiting() || starting()) return;
    const targets = rows.filter(row => !busy(row)).map(row => ({ key: instanceKey(row), slot: row.slot }));
    if (!targets.length) return;
    setRefusal(null);
    setRequests(draft => {
      for (const { key } of targets) draft[key] = { pending: true };
    });
    flush();
    await Promise.all(
      targets.map(async ({ key, slot }) => {
        try {
          await app.api('instance/stop/' + slot, { method: 'POST' });
        } catch (error) {
          setRequests(draft => {
            if (draft[key]) draft[key].error = error;
          });
        }
      }),
    );
    await app.refresh().catch(() => {});
    setRequests(draft => {
      for (const { key } of targets) {
        if (draft[key]) draft[key].pending = false;
      }
    });
  }
  async function start() {
    if (!waiting() || starting()) return;
    setStarting(true);
    setRefusal(null);
    flush();
    try {
      await app.update('instance/start', { method: 'POST' });
    } catch (e) {
      setRefusal(e);
      details.open = true;
    } finally {
      setStarting(false);
    }
  }
  return (
    <dialog
      id="instance-dialog"
      class="dialog"
      aria-labelledby="instance-title"
      aria-describedby="instance-description"
      ref={el => (dialog = el)}
      onCancel={e => e.preventDefault()}
      onKeyDown={e => {
        if (e.key === 'Escape') e.preventDefault();
      }}
    >
      <h2 id="instance-title">{waiting() && app.t('title.instance', { n: others().length })}</h2>
      <p id="instance-description">{app.t('instance.description')}</p>
      <button
        type="button"
        id="instance-replace"
        class="button"
        aria-labelledby="instance-replace-text"
        aria-disabled={stopping() || starting() ? 'true' : undefined}
        ref={el => (replace = el)}
        onClick={() => {
          if (!stopping()) stop(others());
        }}
      >
        <span id="instance-replace-text" role="status">
          {waiting() && app.t(stopping() ? 'state.instanceStopping' : 'action.instanceReplace')}
        </span>
      </button>
      <details id="instance-details" class="instance-details" ref={el => (details = el)}>
        <summary ref={el => (summary = el)}>{app.t('instance.details')}</summary>
        <ul id="instance-list" class="instance-list">
          <For each={others()}>
            {row => {
              let li;
              const key = untrack(() => instanceKey(row));
              const error = createMemo(() =>
                busy(row)
                  ? ''
                  : errorMessage(
                      app.t,
                      row.error ? { status: 409, code: row.code, message: row.error } : requests[key]?.error,
                    ),
              );
              createEffect(error, value => {
                if (value) details.open = true;
              });
              onCleanup(() => {
                if (li?.contains(document.activeElement))
                  queueMicrotask(() => {
                    if (dialog.open) summary.focus();
                  });
                setRequests(draft => {
                  delete draft[key];
                });
              });
              return (
                <li class="instance-row" ref={el => (li = el)}>
                  <div class="instance-info">
                    <strong>{app.t('instance.pid', { pid: row.pid })}</strong>
                    <p class="mono">{row.url}</p>
                    <p class="value">
                      <time datetime={row.started}>
                        {app.t('instance.started', { ago: age(row.started, now(), app.language()) })}
                      </time>
                    </p>
                  </div>
                  <button
                    type="button"
                    class="button secondary compact"
                    aria-disabled={busy(row) || starting() ? 'true' : undefined}
                    aria-label={app.t('label.instanceStop', { pid: row.pid })}
                    onClick={() => stop([row])}
                  >
                    {app.t(busy(row) ? 'state.instanceRowStopping' : 'action.instanceStop')}
                  </button>
                  <p class="error-text" role="alert" hidden={!error()}>
                    {error()}
                  </p>
                </li>
              );
            }}
          </For>
        </ul>
        <p id="instance-error" role="alert" class="error-text" hidden={!refusal()}>
          {errorMessage(app.t, refusal())}
        </p>
        <div class="dialog-actions">
          <button
            type="button"
            id="instance-start"
            class="button danger"
            aria-disabled={starting() ? 'true' : undefined}
            onClick={start}
          >
            {app.t('action.instanceStart')}
          </button>
          <button type="button" id="instance-exit" class="button secondary" onClick={exit}>
            {app.t('action.instanceExit')}
          </button>
        </div>
      </details>
    </dialog>
  );
}
