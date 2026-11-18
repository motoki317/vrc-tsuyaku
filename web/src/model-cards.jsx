import { For } from 'solid-js';
import { Icon } from './icon.jsx';
import { artifactSize, formatBytes } from './downloads.js';
import { errorMessage, flowLabel } from './i18n.js';
import { Saved, savedMark, settledSetting } from './setting.jsx';

function ModelCard({ app, flow, translator, recognizer }) {
  const name = flow ? 'recognizer-' + flow : 'translator',
    value = () => translator?.id || '';
  const id = () => 'model-' + name + (translator ? '-' + value() : '');
  const artifact = () => app.state.artifacts[flow ? recognizer(flow) : translator.artifact];
  const installed = () => artifact().state === 'installed';
  return (
    <label class="option">
      <input
        type="radio"
        name={name}
        value={value()}
        checked={flow ? true : app.settings().Translator === value()}
        aria-labelledby={id() + '-name'}
        aria-describedby={id() + '-detail'}
      />
      <span class="option-body">
        <span class="option-head" id={id() + '-name'}>
          <span class="option-name">{artifact().artifact.Name}</span>
          <span class="tag" hidden={!translator?.default}>
            {translator && app.t('state.default')}
          </span>
        </span>
        <span class="option-detail" id={id() + '-detail'}>
          <span class={'detail' + (installed() ? ' installed' : '')}>
            <svg
              class="icon"
              role="img"
              aria-hidden={installed() ? 'true' : undefined}
              aria-label={app.t('state.missing')}
            >
              <use href={installed() ? '#i-check' : '#i-download'} />
            </svg>
            <span>{installed() ? app.t('state.installed') : formatBytes(app, artifactSize(artifact().artifact))}</span>
          </span>
          <span class="detail">
            <Icon name="memory-stick" />
            <span class="value">
              {app.t('value.memory', { size: formatBytes(app, artifact().artifact.MemoryBytes) })}
            </span>
          </span>
          {translator && (
            <span class="detail">
              <Icon name={translator.speed > 1 ? 'gauge' : 'target'} />
              <span>
                {translator.speed > 1
                  ? app.t('value.faster', { n: Math.floor(translator.speed) })
                  : app.t('value.moreAccurate')}
              </span>
            </span>
          )}
        </span>
      </span>
    </label>
  );
}

export function Recognizer({ app, flow, recognizer }) {
  return (
    <div class="model" hidden={app.screen() === 'setup'}>
      <span class="model-label">{app.t('label.recognizer')}</span>
      <div class="options" role="radiogroup" aria-label={app.t('label.recognizerFor', { flow: flowLabel(app, flow) })}>
        <ModelCard app={app} flow={flow} recognizer={recognizer} />
      </div>
    </div>
  );
}

export function Translators({ app }) {
  const saved = savedMark(app);
  const field = settledSetting(
    app,
    (s, value) => {
      s.Translator = value;
    },
    saved,
  );
  return (
    <section class="settings-group" aria-labelledby="translator-label">
      <h3 class="label-row" id="translator-label">
        <span>{app.t('label.translator')}</span>
        <span class="shared" aria-hidden="true">
          <svg class="icon">
            <use href="#i-mic" />
          </svg>
          <svg class="icon">
            <use href="#i-users" />
          </svg>
        </span>
        <Saved app={app} id="translator" field={saved} />
      </h3>
      <div
        id="translators"
        class="options"
        role="radiogroup"
        aria-labelledby="translator-label"
        aria-describedby={'translator-hint' + (field.error() ? ' translator-error' : '')}
        {...field.events(input => (input.checked ? input.value : undefined))}
      >
        <For each={app.state.languages.translators}>
          {translator => <ModelCard app={app} translator={translator} />}
        </For>
      </div>
      <p id="translator-error" class="error-text" hidden={!field.error()}>
        {errorMessage(app.t, field.error(), 'error.notSaved')}
      </p>
      <p id="translator-hint" class="hint">
        {app.t('note.translatorHint')}
      </p>
    </section>
  );
}
