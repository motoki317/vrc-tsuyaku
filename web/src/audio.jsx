import { selectValue } from './select.js';
import { createEffect, createMemo, createSignal, For, onCleanup, onSettled } from 'solid-js';
import { Icon } from './icon.jsx';
import { flows, flowKey } from './downloads.js';
import { errorMessage, flowLabel } from './i18n.js';
import { Saved, savedMark, settledSetting } from './setting.jsx';

function Microphone({ app }) {
  const [devices, setDevices] = createSignal(null),
    [listError, setListError] = createSignal(false);
  const saved = savedMark(app);
  const field = settledSetting(
    app,
    (s, id) => {
      s.Flows.Self.Microphone = id;
    },
    saved,
  );
  const selected = () => app.settings().Flows.Self.Microphone;
  const absent = id => id && !devices()?.some(device => device.ID === id);
  const missing = () => devices() && absent(selected());
  const choices = createMemo(() => {
    // Keep the saved device selectable while a different choice is waiting to save.
    const ids = new Set([selected(), app.state.settings.Flows.Self.Microphone]);
    return [
      { ID: '', Name: app.t('value.systemDefault') },
      ...(devices() || []),
      ...[...ids].filter(absent).map(ID => ({ ID })),
    ];
  });
  const error = () => listError() || !!field.error();
  let sequence = 0;
  async function list() {
    const revision = ++sequence;
    try {
      const result = await (await app.api('microphones')).json();
      if (revision === sequence) {
        setDevices(result || []);
        setListError(false);
      }
    } catch (e) {
      if (e.status && revision === sequence) setListError(true);
    }
  }
  onSettled(() => {
    list();
  });
  const hint = () => app.t('error.microphones').split('{refresh}');
  return (
    <div class="field-group">
      <div class="label-row">
        <label class="field-label" for="microphone">
          {app.t('label.microphone')}
        </label>
        <Saved app={app} id="microphone" field={saved} />
      </div>
      <div class="field-row">
        <select
          id="microphone"
          class="select"
          ref={selectValue(selected, choices)}
          {...field.events(input => input.value)}
          aria-describedby={[missing() && 'microphone-state', error() && 'microphone-error'].filter(Boolean).join(' ')}
        >
          <For each={choices()} keyed={device => device.ID}>
            {device => <option value={device().ID}>{device().Name || device().ID}</option>}
          </For>
        </select>
        <button type="button" id="refresh-mics" class="icon-button" aria-label={app.t('action.refresh')} onClick={list}>
          <Icon name="refresh-cw" />
        </button>
        <span id="microphone-state" class="field-state warning" hidden={!missing()}>
          <Icon name="circle-alert" />
          <span>{app.t('state.notConnected')}</span>
        </span>
      </div>
      <p id="microphone-error" class="error-text" hidden={!error()}>
        {listError() ? (
          <>
            {hint()[0]}
            <span class="inline-icon" role="img" aria-label={app.t('action.refresh')}>
              <Icon name="refresh-cw" />
            </span>
            {hint()[1]}
          </>
        ) : (
          errorMessage(app.t, field.error(), 'error.notSaved')
        )}
      </p>
    </div>
  );
}

function Level({ app, flow, levels }) {
  const field = settledSetting(app, (s, value) => {
    s.Flows[flowKey[flow]].CutLevel = value;
  });
  const cut = () => app.settings().Flows[flowKey[flow]].CutLevel;
  let manual = -50;
  createEffect(cut, value => {
    if (value) manual = value;
  });
  const value = () => cut() || manual,
    automatic = () => cut() === 0;
  const label = () => flowLabel(app, flow),
    db = () => app.t('value.decibels', { n: value() });
  const hasLevel = () => Number.isFinite(levels()?.[flow]);
  const shown = () => (hasLevel() ? Math.max(-80, Math.min(0, levels()[flow])) : -80);
  const speaking = () => app.log.speech[flow] === 'open';
  const inactive = () => levels() !== null && !hasLevel() && app.state.flows[flow].state === 'stopped';
  const described = hint => hint + '-' + flow + (field.error() ? ' level-error-' + flow : '');
  return (
    <div
      class="level-control"
      id={'level-' + flow}
      {...field.events(input => {
        if (input.type === 'range') return Number(input.value);
        return input.checked ? 0 : manual;
      })}
    >
      <div class="level-heading">
        <label class="automatic">
          <input
            type="checkbox"
            id={'automatic-' + flow}
            checked={automatic()}
            disabled={!!app.state.settingsError}
            aria-labelledby={'audio-' + flow + '-label automatic-label-' + flow}
            aria-describedby={described('automatic-hint')}
          />
          <span id={'automatic-label-' + flow}>{app.t('label.automatic')}</span>
        </label>
        <span class="value" hidden={automatic()}>
          {db()}
        </span>
      </div>
      <p class="hint" id={'automatic-hint-' + flow}>
        {app.t('note.automaticLevel')}
      </p>
      <div class="level-scale">
        <div
          class={'level-meter' + (speaking() ? ' speaking' : '')}
          role="meter"
          aria-valuemin="-80"
          aria-valuemax="0"
          aria-label={app.t('label.level', { flow: label() })}
          aria-valuenow={shown()}
          aria-describedby={inactive() ? 'level-inactive-' + flow : undefined}
          aria-valuetext={app.t('value.levelState', {
            level: hasLevel() ? app.t('value.decibelsFS', { n: Math.round(shown()) }) : app.t('state.noLevel'),
            state: app.t(speaking() ? 'state.speechActive' : 'state.speechInactive'),
          })}
        >
          <div class="level-fill" style={{ width: ((shown() + 80) / 80) * 100 + '%' }} />
        </div>
        <input
          type="range"
          class="level-slider"
          id={'cut-level-' + flow}
          min="-80"
          max="-10"
          step="1"
          value={value()}
          hidden={automatic()}
          aria-label={app.t('label.cutLevel', { flow: label() })}
          aria-valuetext={db()}
          aria-describedby={described('cut-hint')}
          disabled={!!app.state.settingsError}
        />
      </div>
      <div class="level-ticks" aria-hidden="true">
        <span>{app.t('value.decibelsFS', { n: -80 })}</span>
        <span>{app.t('value.decibelsFS', { n: 0 })}</span>
      </div>
      <p class="hint" id={'cut-hint-' + flow} hidden={automatic()}>
        {app.t('note.cutLevel')}
      </p>
      <p class="hint" id={'level-inactive-' + flow} hidden={!inactive()}>
        {app.t('note.levelInactive', { flow: label() })}
      </p>
      <p class="error-text" id={'level-error-' + flow} hidden={!field.error()}>
        {errorMessage(app.t, field.error(), 'error.notSaved')}
      </p>
    </div>
  );
}

export function Audio({ app }) {
  const [levels, setLevels] = createSignal(null),
    [visible, setVisible] = createSignal(!document.hidden);
  const visibility = () => setVisible(!document.hidden);
  document.addEventListener('visibilitychange', visibility);
  onCleanup(() => document.removeEventListener('visibilitychange', visibility));
  createEffect(
    () =>
      visible() &&
      app.screen() === 'settings' &&
      app.sub() === 'audio' &&
      !app.state.settingsError &&
      !app.reconnecting() &&
      app.phase() !== 'exited',
    active => {
      if (!active) {
        setLevels(null);
        return;
      }
      const request = new AbortController();
      let timer;
      async function poll() {
        let next = null;
        try {
          const res = await fetch('/api/levels', { cache: 'no-store', signal: request.signal });
          if (res.ok) next = await res.json();
        } catch {
          /* A missed reading leaves the meter empty until the next poll. */
        }
        if (request.signal.aborted) return;
        setLevels(next);
        timer = setTimeout(poll, 100);
      }
      poll();
      return () => {
        clearTimeout(timer);
        request.abort();
      };
    },
  );
  return (
    <>
      <h2 class="sub-title" tabindex="-1">
        {app.t('label.audio')}
      </h2>
      {flows.map(flow => (
        <section class="settings-group" aria-labelledby={'audio-' + flow + '-label'}>
          <h3 id={'audio-' + flow + '-label'}>{flowLabel(app, flow)}</h3>
          {flow === 'self' && <Microphone app={app} />}
          <Level app={app} flow={flow} levels={levels} />
        </section>
      ))}
    </>
  );
}
