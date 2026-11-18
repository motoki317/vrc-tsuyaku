import { selectValue } from './select.js';
import { Recognizer, Translators } from './model-cards.jsx';
import { settledSetting } from './setting.jsx';
import { createEffect, createMemo, For } from 'solid-js';
import { Icon } from './icon.jsx';
import { DownloadGroups, DownloadRows } from './download-list.jsx';
import { FlowControl } from './flow-control.jsx';
import { flows, flowIcon, flowKey, formatBytes } from './downloads.js';
import { swappedLanguages } from './languages.js';
import { errorMessage, flowLabel } from './i18n.js';

const firstTargets = ['ja', 'ja-easy', 'en', 'ko', 'zh-hans', 'zh-hant'];

function PlanRow({ app, downloads: d, flow }) {
  const field = flowKey[flow],
    errorId = 'language-error-' + flow;
  const langs = () => app.state.languages,
    pair = () => app.settings().Flows[field];
  const swap = () => swappedLanguages(langs(), flow, pair(), app.state.settingsError);
  const edit = settledSetting(app, (s, values) => {
    Object.assign(s.Flows[field], values);
  });
  let spoken, target;
  const choices = createMemo(() => {
    const name = code => app.languageName(code),
      collator = new Intl.Collator(app.language());
    const sort = (a, b) => collator.compare(name(a), name(b));
    const spoken = langs().spoken.filter(l => flow === 'world' || !l.auto);
    return {
      ja: spoken.filter(l => l.code === 'ja').map(l => l.code),
      rest: spoken
        .filter(l => l.code !== 'ja')
        .sort((a, b) => b.auto - a.auto || sort(a.code, b.code))
        .map(l => l.code),
      top: firstTargets.filter(code => langs().targets.includes(code)),
      others: langs()
        .targets.filter(code => !firstTargets.includes(code))
        .sort(sort),
    };
  });
  const options = codes => <For each={codes}>{code => <option value={code}>{app.languageName(code)}</option>}</For>;
  const label = () => flowLabel(app, flow);
  return (
    <FlowControl app={app} downloads={d} flow={flow}>
      {(button, failure) => (
        <div class="plan-row" data-flow={flow}>
          <div class="plan-head">
            <Icon name={flowIcon[flow]} extra="flow-icon" />
            <span class="row-label">{label()}</span>
            {button}
          </div>
          <div class="chain" {...edit.events(() => ({ Spoken: spoken.value, Target: target.value }))}>
            <select
              class="select"
              aria-describedby={errorId}
              aria-label={app.t('label.spoken', { flow: label() })}
              ref={[el => (spoken = el), selectValue(() => pair().Spoken, choices)]}
            >
              {options(choices().ja)}
              <optgroup label={app.t('label.european')}>{options(choices().rest)}</optgroup>
            </select>
            <button
              type="button"
              class="icon-button swap"
              aria-describedby={errorId}
              aria-label={app.t('action.swapLanguages')}
              disabled={!swap()}
              onClick={() => {
                const values = swap();
                if (values) {
                  edit.input(values);
                  edit.settle();
                }
              }}
            >
              <Icon name="arrow-left-right" />
            </button>
            <select
              class="select"
              aria-describedby={errorId}
              aria-label={app.t('label.target', { flow: label() })}
              ref={[el => (target = el), selectValue(() => pair().Target, choices)]}
            >
              {options(choices().top)}
              <hr />
              {options(choices().others)}
            </select>
          </div>
          {failure}
          <p id={errorId} class="error-text" hidden={!edit.error()}>
            {errorMessage(app.t, edit.error())}
          </p>
          <Recognizer app={app} flow={flow} recognizer={d.recognizer} />
        </div>
      )}
    </FlowControl>
  );
}
export function PlanRows({ app, downloads }) {
  return (
    <div class="plan-rows">
      {flows.map(flow => (
        <PlanRow app={app} downloads={downloads} flow={flow} />
      ))}
    </div>
  );
}
export function Setup({ app, downloads: d, rows, all }) {
  let details;
  const ids = createMemo(() => d.byName().filter(id => d.needed().includes(id)));
  createEffect(
    () => ids().filter(id => d.kind(id) === 'failed' || d.errors[id]),
    (failed, last = []) => {
      if (failed.some(id => !last.includes(id))) details.open = true;
    },
  );
  return (
    <div class="setup-column">
      <div class="plan">
        <div class="plan-slot">{app.screen() === 'setup' && rows}</div>
        <div class="plan-foot">
          {app.screen() === 'setup' && all}
          <details class="plan-downloads" ref={el => (details = el)}>
            <summary>
              <Icon name="chevron-down" extra="chevron" />
              <span>{app.t('label.downloads')}</span>
            </summary>
            <ul class="download-list">
              <DownloadRows app={app} downloads={d} ids={ids()} place="plan" />
            </ul>
          </details>
        </div>
      </div>
      <div class="setup-next">
        <button
          type="button"
          id="next"
          class={d.anyReady() ? 'button' : 'button secondary'}
          aria-disabled={d.anyReady() ? undefined : 'true'}
          onClick={() => {
            if (d.anyReady()) app.setScreen('translation');
          }}
        >
          <span>{app.t('action.next')}</span>
          <Icon name="arrow-right" />
        </button>
      </div>
    </div>
  );
}
export function Models({ app, downloads: d, rows, all }) {
  const memory = () =>
    [...new Set([d.translator(), ...flows.map(d.recognizer)])].reduce(
      (sum, id) => sum + app.state.artifacts[id].artifact.MemoryBytes,
      0,
    );
  return (
    <>
      <h2 class="sub-title" tabindex="-1">
        {app.t('label.languagesModels')}
      </h2>
      <p class="memory-total">
        <Icon name="memory-stick" />
        <span>{app.t('label.memoryTotal')}</span>
        <span id="memory-total" class="value">
          {formatBytes(app, memory())}
        </span>
      </p>
      <section class="settings-group" aria-labelledby="languages-label">
        <h3 id="languages-label">{app.t('label.languages')}</h3>
        <div class="plan-slot">{app.screen() !== 'setup' && rows}</div>
      </section>
      <Translators app={app} />
      <section class="settings-group" aria-labelledby="model-downloads-label">
        <h3 id="model-downloads-label" tabindex="-1">
          {app.t('label.modelDownloads')}
        </h3>
        <div class="plan-foot">{app.screen() !== 'setup' && all}</div>
        <DownloadGroups app={app} downloads={d} />
      </section>
    </>
  );
}
