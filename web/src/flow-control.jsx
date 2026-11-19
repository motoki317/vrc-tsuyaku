import { createEffect, createMemo } from 'solid-js';
import { Icon } from './icon.jsx';
import { flowIcon, flowKey, formatBytes, percent } from './downloads.js';
import { errorMessage, flowLabel } from './i18n.js';

function FlowControl({ app, downloads: d, flow }) {
  let button, errorBox, retryButton;
  const request = app.switches[flow];
  const stateId = 'fc-state-' + flow,
    errorId = 'fc-error-' + flow;
  const label = () => flowLabel(app, flow);
  const pair = () => app.settings().Flows[flowKey[flow]];
  const source = () => app.languageName(pair().Spoken),
    target = () => app.languageName(pair().Target);
  function choose() {
    if (app.state.settingsError) return;
    app.setScreen('settings');
    app.setSub('models');
    queueMicrotask(() => document.querySelector('.plan-row[data-flow=' + flow + '] select')?.focus());
  }
  const serverView = () => {
    const v = d.view(d.required[flow]());
    if (v.kind !== 'ready') return v;
    const run = app.state.flows[flow],
      enabled = pair().Enabled;
    if (run.state === 'starting' || run.state === 'running')
      return { kind: run.state === 'running' ? 'on' : 'starting', checked: true };
    if (run.state === 'error') return { kind: 'error', checked: enabled, error: run };
    return { kind: enabled ? 'on' : 'off', checked: enabled };
  };
  const view = createMemo(() => {
    const v = serverView(),
      checked = request.requested();
    if (checked === null || checked === v.checked) return v;
    return { kind: checked ? 'on' : 'off', checked };
  });
  const kind = () => view().kind,
    isSwitch = () => 'checked' in view();
  const action = () => {
    if (request.requested() !== null) return;
    if (['failed', 'missing'].includes(kind()) && !app.state.settingsError && !view().ids.some(id => d.pending[id]))
      return 'download';
    if (isSwitch() && kind() !== 'starting') {
      if (view().checked) return 'stop';
      if (!app.state.settingsError) return 'start';
    }
  };
  const size = () => (kind() === 'missing' ? formatBytes(app, view().bytes) : '');
  const progress = () => (kind() === 'downloading' ? percent(app, view().fraction) : '');
  const name = () => {
    if (kind() === 'missing') return app.t('action.downloadFor', { size: size(), flow: label() });
    if (kind() === 'failed') return app.t('action.retryFor', { flow: label() });
    if (isSwitch()) return label();
    const state =
      kind() === 'queued' ? app.t('state.queued') : app.t('state.downloadingPercent', { percent: progress() });
    return app.t('state.flowState', { flow: label(), state });
  };
  const error = () => {
    if (view().error) return errorMessage(app.t, { status: 400, code: view().error.code, message: view().error.error });
    return isSwitch() ? errorMessage(app.t, request.error()) : '';
  };
  const retry = () => kind() === 'error' && !app.state.settingsError;
  createEffect(
    () => [!!error(), retry()],
    ([show, canRetry]) => {
      if ((!show || !canRetry) && document.activeElement === retryButton) button.focus();
      errorBox.hidden = !show;
      retryButton.hidden = !canRetry;
    },
  );
  function press(event) {
    const command = action();
    if (!command || event.timeStamp - request.lastClick < 500) return;
    request.lastClick = event.timeStamp;
    if (command === 'download') d.start(view().ids, button);
    else request.change(command);
  }
  const ring = () => kind() === 'off' && action() === 'start' && (app.log.empty() || d.ringing[flow]);
  return (
    <div class="card-wrap" data-flow={flow}>
      <div class="card">
        <Icon name={flowIcon[flow]} extra="flow-icon" />
        <span class="card-text">
          <span class="card-label">{label()}</span>
          <button
            type="button"
            class="chip"
            aria-disabled={app.state.settingsError ? 'true' : undefined}
            aria-label={app.t('action.changeLanguages', {
              flow: label(),
              pair: app.t('value.pair', { source: source(), target: target() }),
            })}
            onClick={choose}
          >
            <span class="chip-part">
              <span>{source()}</span>
              <Icon name="arrow-right" extra="small" />
            </span>
            <span class="chip-part">
              <span>{target()}</span>
            </span>
          </button>
        </span>
        <button
          type="button"
          class={'fc' + (view().checked ? ' checked' : '') + (ring() ? ' next' : '')}
          data-flow={flow}
          data-kind={kind()}
          role={isSwitch() ? 'switch' : undefined}
          aria-checked={isSwitch() ? String(!!view().checked) : undefined}
          aria-label={name()}
          aria-disabled={action() ? undefined : 'true'}
          aria-describedby={stateId + (error() ? ' ' + errorId : '')}
          ref={el => (button = el)}
          onClick={press}
        >
          <span class="track">
            <span class="thumb">
              <Icon name={{ starting: 'loader-circle', error: 'circle-alert' }[kind()] || 'check'} />
            </span>
          </span>
          <Icon name={{ queued: 'clock', failed: 'circle-alert' }[kind()] || 'download'} extra="glyph" />
          <span class="fc-text">{size()}</span>
          <span class="ring" style={kind() === 'downloading' ? { '--p': view().fraction } : {}}>
            <span class="ring-text">{progress()}</span>
          </span>
          <Icon name="rotate-ccw" extra="retry-glyph" />
          <span class="visually-hidden" id={stateId}>
            {kind() === 'starting' ? app.t('state.starting') : ''}
          </span>
        </button>
      </div>
      <div class="fc-error" ref={el => (errorBox = el)}>
        <Icon name="circle-alert" />
        <span id={errorId}>{error()}</span>
        <button
          type="button"
          class="button secondary compact"
          ref={el => (retryButton = el)}
          onClick={() => request.change('start')}
        >
          <Icon name="rotate-ccw" />
          <span>{app.t('action.tryAgain')}</span>
        </button>
      </div>
    </div>
  );
}

export function SwitchBar({ app, downloads }) {
  return (
    <div id="switchbar" class="switchbar" hidden={app.screen() !== 'translation' && !app.state.settingsError}>
      <div class="switchbar-inner">
        {['world', 'self'].map(flow => (
          <FlowControl app={app} downloads={downloads} flow={flow} />
        ))}
      </div>
    </div>
  );
}
