import { createEffect, createMemo, createSignal, flush, For, onCleanup } from 'solid-js';
import { Icon } from './icon.jsx';
import { artifactSize, flowIcon, formatBytes, percent } from './downloads.js';
import { errorMessage, flowLabel } from './i18n.js';

function DownloadRow({ app, downloads: d, id, place, refs }) {
  let row, button;
  const [opening, setOpening] = createSignal(false),
    [refusal, setRefusal] = createSignal(null);
  const value = () => app.state.artifacts[id],
    kind = () => d.kind(id),
    name = () => value().artifact.Name;
  const users = createMemo(() => d.users(id));
  const action = createMemo(() => {
    if (['missing', 'failed'].includes(kind())) return 'download';
    if (kind() === 'leftover') return 'folder';
    return kind() === 'installed' && place === 'settings' && !users().some(d.active) ? 'delete' : '';
  });
  const actionName = createMemo(prev => {
    if (!action()) return prev;
    if (action() === 'folder') return app.t('action.openData');
    if (action() === 'delete') return app.t('action.deleteName', { name: name() });
    return app.t(kind() === 'failed' ? 'action.retryDownload' : 'action.download', { name: name() });
  });
  const actionIcon = () => {
    if (action() === 'folder') return 'folder-open';
    if (action() === 'delete') return 'trash-2';
    return kind() === 'failed' ? 'rotate-ccw' : 'download';
  };
  const progressText = () => {
    if (kind() === 'verifying') return app.t('state.verifying');
    return kind() === 'downloading' ? percent(app, fraction()) : '';
  };
  const error = () => {
    const v = value();
    if (kind() === 'failed') return errorMessage(app.t, { status: 400, code: v.code, message: v.error });
    if (kind() === 'leftover') return refusal() ? errorMessage(app.t, refusal()) : app.t('error.leftover');
    return kind() === 'missing' ? errorMessage(app.t, d.errors[id]) : '';
  };
  const blocked = () => opening() || (action() === 'download' && (d.pending[id] || !!app.state.settingsError));
  const running = () => ['downloading', 'verifying'].includes(kind());
  const fraction = () => (value().progress.Total ? value().progress.Downloaded / value().progress.Total : 0);
  const lead = {
    installed: 'check',
    leftover: 'check',
    missing: 'circle-dashed',
    queued: 'clock',
    failed: 'circle-alert',
  };
  const status = {
    installed: 'state.installed',
    leftover: 'state.leftover',
    missing: 'state.missing',
    queued: 'state.queued',
    failed: 'state.failed',
    downloading: 'state.downloading',
    verifying: 'state.verifying',
  };
  async function press() {
    if (blocked()) return;
    if (action() === 'download') return d.start([id], button);
    if (action() === 'delete')
      return d.setDeletion({
        id,
        restoreFocus() {
          const current = refs.get(id),
            action = current?.querySelector('button');
          (action && !action.hidden ? action : current)?.focus();
        },
      });
    if (action() !== 'folder') return;
    setOpening(true);
    flush();
    try {
      await app.api('open/data', { method: 'POST' });
      setRefusal(null);
    } catch (e) {
      if (e.status) setRefusal(e);
    } finally {
      setOpening(false);
    }
  }
  createEffect(kind, k => {
    if (k !== 'leftover') setRefusal(null);
  });
  createEffect(action, a => {
    if (!button) return;
    if (!a && document.activeElement === button) row.focus();
    button.hidden = !a;
  });
  onCleanup(() => {
    if (!refs) return;
    if (refs.get(id) === row) refs.delete(id);
    const focused = document.activeElement;
    if (row.contains(focused))
      queueMicrotask(() => {
        const moved = refs.get(id),
          action = moved?.querySelector('button');
        (focused === button && action && !action.hidden ? action : moved)?.focus({ preventScroll: true });
      });
  });
  return (
    <li
      class="download"
      tabindex="-1"
      data-kind={kind()}
      ref={el => {
        row = el;
        refs?.set(id, el);
      }}
    >
      <span class="lead">
        <Icon name={lead[kind()] || 'loader-circle'} extra={running() ? 'spin' : ''} />
        <Icon name="circle-alert" extra="warn" />
        <span class="visually-hidden">{app.t(status[kind()])}</span>
      </span>
      <span class="name">{name()}</span>
      <span class="size value">{formatBytes(app, artifactSize(value().artifact))}</span>
      {place !== 'plan' && (
        <span class="flows">
          <For each={users()}>
            {flow => (
              <span
                data-flow={flow}
                role="img"
                class={d.active(flow) ? 'on' : undefined}
                aria-label={app.t(d.active(flow) ? 'state.inUseBy' : 'state.usedBy', { flow: flowLabel(app, flow) })}
              >
                <Icon name={flowIcon[flow]} />
              </span>
            )}
          </For>
        </span>
      )}
      {place !== 'license' && (
        <button
          type="button"
          class="icon-button"
          data-action={action()}
          aria-label={actionName()}
          aria-disabled={blocked() ? 'true' : undefined}
          ref={el => (button = el)}
          onClick={press}
        >
          <Icon name={actionIcon()} />
          <Icon name="arrow-up-right" extra="small exit" />
        </button>
      )}
      <div class="progress" hidden={!running()}>
        <div class="bar">
          <span class="fill" style={kind() === 'downloading' ? { '--p': fraction() } : {}} />
        </div>
        <span class="bar-text value">{progressText()}</span>
      </div>
      <p class={kind() === 'leftover' && !refusal() ? 'error-text warning' : 'error-text'} hidden={!error()}>
        {error()}
      </p>
    </li>
  );
}

export function DownloadRows(props) {
  return (
    <For each={props.ids}>
      {id => <DownloadRow app={props.app} downloads={props.downloads} place={props.place} refs={props.refs} id={id} />}
    </For>
  );
}

export function DownloadGroups({ app, downloads: d }) {
  const refs = new Map();
  const groups = createMemo(() => {
    const groups = { needed: [], unneeded: [], missing: [] },
      artifacts = app.state.artifacts;
    for (const id of d.byName()) {
      if (d.needed().includes(id)) groups.needed.push(id);
      else if (artifacts[id].state === 'installed') groups.unneeded.push(id);
      else groups.missing.push(id);
    }
    return groups;
  });
  const installed = () => Object.keys(app.state.artifacts).filter(id => app.state.artifacts[id].state === 'installed');
  return (
    <>
      <p class="installed-total">
        <span>{app.t('label.installed')}</span>{' '}
        <span id="installed-size" class="value">
          {formatBytes(app, d.bytes(installed()))}
        </span>
      </p>
      <div class="download-groups">
        {[
          ['needed', 'label.neededDownloads'],
          ['unneeded', 'label.unneededDownloads'],
          ['missing', 'label.missingDownloads'],
        ].map(([key, label]) => {
          const id = 'downloads-' + key;
          return (
            <div id={id} class="download-group" hidden={!groups()[key].length}>
              <h4 class="field-label" id={id + '-label'}>
                {app.t(label)}
              </h4>
              <ul class="download-list" aria-labelledby={id + '-label'}>
                {key === 'needed' && (
                  <li class="download download-head" aria-hidden="true">
                    <span class="used-by">{app.t('label.usedBy')}</span>
                  </li>
                )}
                <DownloadRows app={app} downloads={d} ids={groups()[key]} place="settings" refs={refs} />
              </ul>
            </div>
          );
        })}
      </div>
    </>
  );
}

export function DownloadDialogs({ app, downloads: d }) {
  let license, deletion, text, cancel;
  const [busy, setBusy] = createSignal(false),
    [error, setError] = createSignal(null);
  const licenseInfo = () => {
    const licenses = app.state.artifacts[d.license().ids[0]].artifact.Licenses;
    return licenses.find(l => l.Text) || licenses[0];
  };
  createEffect(d.license, request => {
    if (request) {
      license.returnValue = '';
      license.showModal();
      text.scrollTop = 0;
      text.focus();
    }
  });
  createEffect(d.deletion, request => {
    if (request) {
      setError(null);
      deletion.showModal();
      cancel.focus();
    }
  });
  function finishLicense() {
    const request = d.license();
    d.setLicense(null);
    if (!app.phase()) request.opener.focus();
    request.resolve(license.returnValue === 'accept');
  }
  function finishDelete() {
    const request = d.deletion();
    d.setDeletion(null);
    if (!app.phase()) request.restoreFocus();
  }
  async function remove() {
    if (busy()) return;
    setBusy(true);
    flush();
    const request = d.deletion();
    try {
      await app.update('downloads/' + request.id, { method: 'DELETE' });
      if (d.deletion() === request) deletion.close();
    } catch (e) {
      if (d.deletion() === request) setError(e);
    } finally {
      setBusy(false);
    }
  }
  const target = () => d.deletion() && app.state.artifacts[d.deletion().id].artifact;
  return (
    <>
      <dialog
        id="license-dialog"
        class="dialog license"
        aria-labelledby="license-title"
        ref={el => (license = el)}
        onClose={finishLicense}
      >
        <div class="dialog-head">
          <h2 id="license-title">{d.license() && licenseInfo().Name}</h2>
          <button type="button" class="icon-button" aria-label={app.t('action.close')} onClick={() => license.close()}>
            <Icon name="x" />
          </button>
        </div>
        <ul id="license-downloads" class="download-list license-downloads">
          <DownloadRows app={app} downloads={d} ids={d.license()?.ids || []} place="license" />
        </ul>
        <pre
          id="license-text"
          class="license-text"
          tabindex="0"
          role="region"
          aria-labelledby="license-title"
          ref={el => (text = el)}
        >
          {d.license() && (licenseInfo().Text || licenseInfo().URL)}
        </pre>
        <div class="dialog-actions">
          <button type="button" class="button secondary" onClick={() => license.close()}>
            {app.t('action.cancel')}
          </button>
          <button type="button" id="license-accept" class="button" onClick={() => license.close('accept')}>
            <Icon name="download" />
            <span>{app.t('action.acceptDownload')}</span>
          </button>
        </div>
      </dialog>
      <dialog
        id="delete-dialog"
        class="dialog"
        aria-labelledby="delete-title"
        ref={el => (deletion = el)}
        onClose={finishDelete}
      >
        <h2 id="delete-title">{target() && app.t('title.delete', { name: target().Name })}</h2>
        <p id="delete-size" class="value">
          {target() && formatBytes(app, artifactSize(target()))}
        </p>
        <p id="delete-flows" class="delete-flows" hidden={d.deletion() && !d.users(d.deletion().id).length}>
          <For each={d.deletion() && d.users(d.deletion().id)}>
            {flow => (
              <span
                class="blocked-flow"
                role="img"
                aria-label={app.t('state.blockedBy', { flow: flowLabel(app, flow) })}
              >
                <Icon name={flowIcon[flow]} />
                <Icon name="circle-slash" extra="slash" />
              </span>
            )}
          </For>
        </p>
        <p id="delete-error" class="error-text" hidden={!error()}>
          {error()?.status === 409 ? app.t('error.inUse') : errorMessage(app.t, error())}
        </p>
        <div class="dialog-actions">
          <button type="button" class="button secondary" ref={el => (cancel = el)} onClick={() => deletion.close()}>
            {app.t('action.cancel')}
          </button>
          <button
            type="button"
            id="delete-confirm"
            class="button danger"
            aria-disabled={busy() ? 'true' : undefined}
            onClick={remove}
          >
            <Icon name="trash-2" />
            <span>{app.t('action.delete')}</span>
          </button>
        </div>
      </dialog>
    </>
  );
}

export function DownloadAll({ app, downloads: d }) {
  let button,
    samples = [],
    since,
    total;
  const current = createMemo(() => d.view(d.needed(), true));
  const shown = createMemo(prev => (current().kind === 'none' ? prev : current()));
  const left = createMemo(() => {
    const v = current(),
      now = Date.now();
    if (v.kind !== 'running' || v.total !== total) {
      samples = [];
      since = now;
      total = v.total;
    }
    if (v.kind !== 'running') return '';
    samples = samples.filter(s => s.time >= now - 10000);
    samples.push({ time: now, done: v.done });
    const delta = v.done - samples[0].done,
      elapsed = now - samples[0].time;
    if (now - since < 3000 || delta <= 0 || !elapsed) return '';
    const seconds = ((v.total - v.done) * elapsed) / delta / 1000;
    return seconds < 60
      ? app.t('value.underMinute')
      : app.t('value.minutesLeft', { n: Math.round(seconds / 60).toLocaleString(app.language()) });
  });
  const running = () => shown()?.kind === 'running';
  const progress = () => formatBytes(app, shown().total, shown().done);
  const label = () => {
    if (!shown()) return '';
    if (running()) return progress();
    return app.t(shown().kind === 'retry' ? 'action.retry' : 'action.downloadAll');
  };
  const size = () => {
    if (!shown()) return '';
    return running() ? percent(app, shown().fraction) : formatBytes(app, shown().bytes);
  };
  const blocked = () => running() || (shown() && !!app.state.settingsError);
  createEffect(
    () => current().kind,
    k => {
      if (k === 'none' && document.activeElement === button)
        document.getElementById(app.screen() === 'setup' ? 'next' : 'model-downloads-label')?.focus();
    },
  );
  return (
    <button
      type="button"
      class={'button download-all' + (app.screen() === 'setup' && d.anyReady() ? ' secondary' : '')}
      ref={el => (button = el)}
      hidden={current().kind === 'none'}
      data-kind={shown()?.kind}
      aria-disabled={blocked() ? 'true' : undefined}
      aria-label={
        running()
          ? app.t(left() ? 'state.downloadAllLeft' : 'state.downloadAll', {
              progress: progress(),
              percent: size(),
              left: left(),
            })
          : undefined
      }
      onClick={() => {
        if (!blocked() && current().ids) d.start(current().ids, button);
      }}
    >
      <span class="fill" style={running() ? { '--p': shown().fraction } : {}} />
      <Icon name={shown()?.kind === 'retry' ? 'rotate-ccw' : 'download'} />
      <span class="label">{label()}</span>
      <span class="left">{left()}</span>
      <span class="size">{size()}</span>
    </button>
  );
}
