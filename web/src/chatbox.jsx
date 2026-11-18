import { createEffect, createMemo, createSignal, For, flush, untrack } from 'solid-js';
import { errorMessage } from './i18n.js';
import { Saved, savedMark, settledSetting } from './setting.jsx';

const tokens = ['{{.Original}}', '{{.Translation}}', '{{.SourceLang}}', '{{.TargetLang}}'];
const presets = ['value.presetBoth', 'value.presetTranslation', 'value.presetOriginal', 'value.presetCustom'];

function chatboxField(app, key, saved, text = false) {
  return settledSetting(
    app,
    (s, raw) => {
      const value = key === 'Address' ? raw.trim() : raw;
      if (s.Chatbox[key] === value) return false;
      s.Chatbox[key] = value;
    },
    saved,
    { keepOnError: text },
  );
}

function textField(app, key, saved) {
  const field = chatboxField(app, key, saved, true);
  const [focused, setFocused] = createSignal(false);
  // Keep the selection intact if settings.yaml changes while the player is typing.
  const shown = createMemo(last => field.draft() ?? (focused() ? last : app.settings().Chatbox[key]));
  return { ...field, shown, setFocused };
}

function Address({ app, field, saved }) {
  return (
    <div class="field-group">
      <div class="label-row">
        <label class="field-label" for="address">
          {app.t('label.address')}
        </label>
        <Saved app={app} id="address" field={saved} />
      </div>
      <div class="field-row">
        <input
          id="address"
          class="input mono"
          autocomplete="off"
          spellcheck="false"
          aria-describedby="address-error"
          value={field.shown()}
          onFocus={() => field.setFocused(true)}
          aria-invalid={field.error() ? 'true' : undefined}
          onInput={e => field.input(e.currentTarget.value)}
          onBlur={() => {
            field.setFocused(false);
            field.settle();
          }}
          onKeyDown={e => {
            if (e.key === 'Enter') field.settle();
          }}
        />
      </div>
      <p id="address-error" class="error-text" hidden={!field.error()}>
        {field.error()?.status === 400 ? app.t('error.address') : errorMessage(app.t, field.error(), 'error.notSaved')}
      </p>
    </div>
  );
}

function createPreview(app, box, template) {
  const [example, setExample] = createSignal(),
    [error, setError] = createSignal();
  const body = createMemo(() => {
    // A merged phrase stays at the older bubble's position in the log.
    const phrase = app.log.rows.reduce(
      (latest, row) =>
        row.flow === 'self' && row.translation != null && row.phraseId > (latest?.phraseId ?? 0) ? row : latest,
      null,
    );
    return JSON.stringify({
      template: template(),
      readingBase: box().ReadingBase,
      readingFactor: box().ReadingFactor,
      original: phrase?.original ?? app.t('value.sampleOriginal'),
      translation: phrase?.translation ?? app.t('value.sampleTranslation'),
      sourceLang: phrase?.source ?? 'ja',
      targetLang: phrase?.target ?? 'en',
    });
  });
  let wanted, answered, pending;
  function load() {
    wanted = body();
    if (pending) return pending;
    if (answered === wanted) return Promise.resolve(!error());
    pending = (async () => {
      try {
        for (;;) {
          const sent = wanted;
          try {
            const reply = await app.api('chatbox/preview', { method: 'POST', body: sent });
            const result = await reply.json();
            const { readingBase: base, readingFactor: factor } = JSON.parse(sent);
            setExample({ ...result, base, factor });
            if (sent !== wanted) continue;
            setError(null);
            answered = sent;
            return true;
          } catch (e) {
            if (sent !== wanted) continue;
            if (e.status) setError({ template: JSON.parse(sent).template, message: e.message });
            answered = e.status && e.status < 500 ? sent : null;
            return false;
          }
        }
      } finally {
        pending = null;
      }
    })();
    return pending;
  }
  createEffect(
    () => !app.phase() && app.screen() === 'settings' && app.sub() === 'chatbox' && body(),
    active => {
      if (active) untrack(load);
    },
  );
  return { example, detail: () => (error()?.template === template() ? error().message : null), load };
}

export function Chatbox({ app }) {
  const box = () => app.settings().Chatbox;
  const saved = savedMark(app),
    readingSaved = savedMark(app),
    addressSaved = savedMark(app);
  const address = textField(app, 'Address', addressSaved),
    template = textField(app, 'Template', saved);
  const preview = createPreview(app, box, template.shown);
  const [custom, setCustom] = createSignal();
  const templates = () => [app.state.defaultTemplate, tokens[1], tokens[0] + '\n' + tokens[1]];
  const selected = () => {
    const i = templates().indexOf(template.shown());
    return custom() === template.shown() || i < 0 ? 3 : i;
  };
  const detail = () => preview.detail() ?? (template.error()?.code === 'template' ? template.error().message : null);
  const invalid = () => detail() != null || !!template.error();
  let area, selection;
  async function saveTemplate() {
    const value = template.draft();
    if (
      value !== undefined &&
      (value === app.state.settings.Chatbox.Template || (await preview.load())) &&
      value === template.draft()
    )
      await template.settle();
  }
  function insert(text, replace) {
    const focused = document.activeElement === area;
    setCustom(template.shown());
    flush();
    area.focus();
    if (replace) area.select();
    else if (!focused && selection) area.setSelectionRange(...selection);
    // Native insertion keeps the player's undo history.
    if (!document.execCommand?.('insertText', false, text)) {
      area.setRangeText(text, area.selectionStart, area.selectionEnd, 'end');
      area.dispatchEvent(new Event('input', { bubbles: true }));
    }
  }
  function preset(i, button) {
    if (i === 3) {
      setCustom(template.shown());
      return;
    }
    insert(templates()[i], true);
    flush();
    setCustom(undefined);
    button.focus();
  }
  createEffect(template.shown, value => {
    setCustom(previous => (previous === value ? previous : undefined));
  });
  createEffect(
    () => [app.screen(), app.sub()],
    (_, previous) => {
      if (previous)
        untrack(() => {
          address.settle();
          saveTemplate();
        });
    },
  );
  const number = (n, min, max = min) =>
    new Intl.NumberFormat(app.language(), { minimumFractionDigits: min, maximumFractionDigits: max }).format(n);
  const seconds = n => app.t('value.seconds', { n: number(n, 0, 2) });
  function reading(key, property, label) {
    const id = 'reading-' + key,
      field = chatboxField(app, property, readingSaved);
    const text = () =>
      key === 'base' ? seconds(box()[property]) : app.t('value.factor', { n: number(box()[property], 1) });
    return (
      <div class="reading-control">
        <div class="level-heading">
          <label for={id}>{app.t(label)}</label>
          <span class="value" id={id + '-value'}>
            {text()}
          </span>
        </div>
        <input
          type="range"
          id={id}
          step={key === 'base' ? '0.5' : '0.1'}
          min={app.state.reading[key].min}
          max={app.state.reading[key].max}
          value={box()[property]}
          disabled={!!app.state.settingsError}
          aria-valuetext={text()}
          aria-describedby={id + '-error'}
          aria-invalid={field.error() ? 'true' : undefined}
          {...field.events(input => input.valueAsNumber)}
        />
        <p class="error-text" id={id + '-error'} hidden={!field.error()}>
          {errorMessage(app.t, field.error(), 'error.notSaved')}
        </p>
      </div>
    );
  }
  const example = preview.example,
    pages = () => example()?.pages || [];
  const formula = () => {
    if (!pages().length) return '';
    const { base, factor, reading, seconds: times } = example();
    const text = app.t('value.readingFormula', {
      base: seconds(base),
      count: app.t(reading.characters === 1 ? 'value.character' : 'value.characters', {
        n: number(reading.characters, 0),
      }),
      rate: seconds(reading.secondsPerCharacter),
      factor: number(factor, 0, 2),
    });
    return pages().length === 1 ? app.t('value.readingTotal', { total: seconds(times[0]), formula: text }) : text;
  };
  return (
    <>
      <h2 class="sub-title" tabindex="-1">
        {app.t('label.chatbox')}
      </h2>
      <Address app={app} field={address} saved={addressSaved} />
      <div class="field-group">
        <div class="label-row">
          <span class="field-label" id="message-label">
            {app.t('label.message')}
          </span>
          <Saved app={app} id="template" field={saved} />
        </div>
        <div id="presets" class="segmented" role="group" aria-labelledby="message-label">
          {presets.map((key, i) => (
            <button
              type="button"
              class="segment"
              aria-pressed={String(selected() === i)}
              onClick={e => preset(i, e.currentTarget)}
            >
              {app.t(key)}
            </button>
          ))}
        </div>
        <div id="custom" hidden={selected() !== 3}>
          <div id="tokens" class="chips" role="group" aria-label={app.t('label.insert')}>
            {tokens.map(token => (
              <button
                type="button"
                class="chip mono"
                aria-label={app.t('action.insertToken', { token })}
                onMouseDown={e => e.preventDefault()}
                onClick={() => insert(token, false)}
              >
                {token}
              </button>
            ))}
          </div>
          <textarea
            id="template"
            class="input mono template"
            rows="3"
            spellcheck="false"
            aria-labelledby="message-label"
            aria-describedby="template-error"
            value={template.shown()}
            aria-invalid={invalid() ? 'true' : undefined}
            ref={el => (area = el)}
            onFocus={() => template.setFocused(true)}
            onInput={e => {
              template.input(e.currentTarget.value);
              setCustom(e.currentTarget.value);
            }}
            onBlur={() => {
              template.setFocused(false);
              selection = [area.selectionStart, area.selectionEnd];
              saveTemplate();
            }}
          />
        </div>
        <div id="template-error" class="error-text" hidden={!invalid()}>
          {detail() != null ? (
            <>
              <span>{app.t('error.templateInvalid')}</span>
              <code class="mono">{detail()}</code>
            </>
          ) : (
            errorMessage(app.t, template.error(), 'error.notSaved')
          )}
        </div>
      </div>
      <section class="field-group" aria-labelledby="reading-label">
        <div class="label-row">
          <h3 class="field-label" id="reading-label">
            {app.t('label.readingTime')}
          </h3>
          <Saved app={app} id="reading" field={readingSaved} />
        </div>
        {reading('base', 'ReadingBase', 'label.readingBase')}
        {reading('factor', 'ReadingFactor', 'label.readingFactor')}
      </section>
      <section class="field-group" aria-labelledby="preview-label">
        <h3 class="field-label" id="preview-label">
          {app.t('label.preview')}
        </h3>
        <div id="preview" class="preview">
          <For each={pages()} keyed={false}>
            {(page, i) => (
              <div class="page">
                <span class="page-count" hidden={pages().length === 1}>
                  {pages().length > 1
                    ? app.t('value.page', { n: i + 1, total: pages().length, seconds: seconds(example().seconds[i]) })
                    : ''}
                </span>
                <p>{page()}</p>
                <span
                  class="page-timer"
                  aria-hidden="true"
                  style={{ 'animation-duration': example().seconds[i] + 's' }}
                />
              </div>
            )}
          </For>
        </div>
        <p id="reading-example" class="hint" hidden={!pages().length}>
          {formula()}
        </p>
      </section>
    </>
  );
}
