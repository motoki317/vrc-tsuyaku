import { createEffect, createSignal, flush } from 'solid-js';
import { Icon } from './icon.jsx';
import { errorMessage } from './i18n.js';

function OpenButton({ app, target, report, rail }) {
  const [busy, setBusy] = createSignal(false);
  const data = target === 'data',
    label = data ? 'action.openData' : 'action.openSettingsFile';
  const railLabel = data ? 'label.dataFolder' : 'label.settingsFile';
  async function open() {
    if (busy()) return;
    setBusy(true);
    flush();
    try {
      await app.api('open/' + target, { method: 'POST' });
      report(null);
    } catch (e) {
      if (e.status) report(e);
    } finally {
      setBusy(false);
    }
  }
  return (
    <button
      type="button"
      class={rail ? 'rail-item' : 'button secondary'}
      data-open={target}
      aria-label={rail ? app.t(label) : undefined}
      aria-disabled={busy() ? 'true' : undefined}
      onClick={open}
    >
      <Icon name={data ? 'folder-open' : 'file-cog'} />
      <span>{app.t(rail ? railLabel : label)}</span>
      <Icon name="arrow-up-right" extra={rail ? 'small exit' : 'small'} />
    </button>
  );
}

function Refusal({ app, id, error }) {
  return (
    <p id={id} class="error-text" hidden={!error()}>
      {errorMessage(app.t, error())}
    </p>
  );
}

export function FolderButtons({ app }) {
  return (
    <div class="rail-foot">
      {['data', 'settings'].map(target => {
        const [error, report] = createSignal(),
          id = 'open-' + target + '-error';
        return (
          <>
            <OpenButton app={app} target={target} report={report} rail />
            <Refusal app={app} id={id} error={error} />
          </>
        );
      })}
    </div>
  );
}

export function SettingsError({ app, error, report }) {
  function confirm() {
    const dialog = document.getElementById('replace-dialog');
    dialog.showModal();
    dialog.querySelector('[data-close]').focus();
  }
  return (
    <section id="screen-settings-error" class="screen settings-error" hidden={!app.state.settingsError}>
      <div class="settings-error-column">
        <h2 class="settings-error-title">
          <Icon name="file-warning" extra="large" />
          <span>{app.t('title.settingsError')}</span>
        </h2>
        <pre id="settings-error-text" class="settings-error-text">
          {app.state.settingsError || ''}
        </pre>
        <div class="settings-error-actions">
          <OpenButton app={app} target="settings" report={report} />
          <button type="button" id="replace-settings" class="button secondary" onClick={confirm}>
            <span>{app.t('action.replaceSettings')}</span>
          </button>
        </div>
        <Refusal app={app} id="replace-error" error={error} />
      </div>
    </section>
  );
}

export function ReplaceDialog({ app, report }) {
  let dialog;
  const [busy, setBusy] = createSignal(false);
  createEffect(
    () => app.state.settingsError,
    error => {
      if (!error && dialog.open) dialog.close();
    },
  );
  async function replace() {
    if (busy()) return;
    setBusy(true);
    flush();
    try {
      await app.replaceSettings();
      report(null);
    } catch (e) {
      report(e);
    } finally {
      setBusy(false);
      flush();
      dialog.close();
    }
  }
  function restoreFocus() {
    if (app.phase()) return;
    let next;
    if (app.state.settingsError) next = '#replace-settings';
    else next = app.screen() === 'setup' ? '#screen-setup select' : '.tab[aria-current=page]';
    document.querySelector(next)?.focus();
  }
  return (
    <dialog
      id="replace-dialog"
      class="dialog"
      aria-labelledby="replace-title"
      ref={el => (dialog = el)}
      onClose={restoreFocus}
    >
      <h2 id="replace-title">{app.t('title.replaceSettings')}</h2>
      <div class="dialog-actions">
        <button type="button" class="button secondary" data-close="" onClick={() => dialog.close()}>
          {app.t('action.cancel')}
        </button>
        <button
          type="button"
          id="replace-confirm"
          class="button danger"
          aria-disabled={busy() ? 'true' : undefined}
          onClick={replace}
        >
          {app.t('action.replace')}
        </button>
      </div>
    </dialog>
  );
}
