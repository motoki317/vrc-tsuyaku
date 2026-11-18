import { selectValue } from './select.js';
import { createEffect, createMemo, createSignal, deep, flush, For, untrack } from 'solid-js';
import { errorMessage, langTag } from './i18n.js';
import { Icon } from './icon.jsx';
import { Saved, savedMark, settledSetting } from './setting.jsx';

const labels = {
  SourceLang: 'label.language',
  Source: 'label.word',
  TargetLang: 'label.language',
  Target: 'label.word',
  Reading: 'label.reading',
};
const keys = Object.keys(labels);
const complete = row => !!(row.Source.trim() && row.Target.trim());
const trim = row => Object.fromEntries(keys.map(k => [k, row[k].trim()]));
const same = (a, b) => keys.every(k => a[k] === b[k]);
const fieldName = (app, row, key) =>
  app.t('label.glossaryField', {
    field: app.t(labels[key]),
    n: key.startsWith('Source') || (key === 'Reading' && row.SourceLang === 'ja') ? 1 : 2,
  });

export function Glossary({ app }) {
  let nextId = 0;
  const [rows, setRows] = createSignal([]);
  const saved = savedMark(app);
  const editor = settledSetting(
    app,
    (s, rows) => {
      const entries = rows.filter(complete).map(trim);
      if (entries.length === s.Glossary.length && entries.every((e, i) => same(e, s.Glossary[i]))) return false;
      s.Glossary = entries;
    },
    saved,
    { keepOnError: true, clearOnInput: true },
  );
  const codes = createMemo(() =>
    app.state.languages.targets
      .filter(c => !app.state.languages.variants[c])
      .sort((a, b) => app.languageName(a).localeCompare(app.languageName(b), app.language())),
  );
  createEffect(
    () => editor.draft() === undefined && deep(app.settings().Glossary),
    incoming => {
      if (!incoming) return;
      // External saves replace complete entries in order. Unfinished rows keep their place and focus.
      let i = 0;
      const next = untrack(rows).flatMap(row => {
        if (!complete(row)) return [{ ...row, ...trim(row) }];
        return i < incoming.length ? [{ id: row.id, ...incoming[i++] }] : [];
      });
      while (i < incoming.length) next.push({ id: ++nextId, ...incoming[i++] });
      setRows(next);
    },
  );
  const refused = () => {
    const loc = editor.error()?.location;
    return loc && Object.hasOwn(labels, loc.field) ? rows().filter(complete)[loc.index] : null;
  };
  createEffect(editor.error, e => {
    if (!e) return;
    untrack(() => {
      const row = refused();
      if (row)
        app.announce(
          app.t('value.glossaryError', {
            entry: app.t('label.glossaryEntry', { n: rows().indexOf(row) + 1 }),
            field: fieldName(app, row, e.location.field),
            message: errorMessage(app.t, e, 'error.notSaved'),
          }),
        );
    });
  });
  function change(next) {
    setRows(next);
    const prior = editor.draft();
    if (blocked() && prior.length === next.length && next.every((row, i) => same(trim(row), trim(prior[i])))) return;
    editor.input(next);
  }
  function focus(row) {
    flush();
    document.getElementById(row ? 'glossary-' + row.id + '-Source' : 'glossary-add')?.focus();
  }
  function add() {
    const self = app.settings().Flows.Self,
      variants = app.state.languages.variants;
    const spoken = variants[self.Spoken] || self.Spoken,
      target = variants[self.Target] || self.Target;
    const SourceLang = codes().includes(spoken) ? spoken : codes()[0];
    const row = {
      id: ++nextId,
      SourceLang,
      Source: '',
      TargetLang: codes().includes(target) && target !== SourceLang ? target : codes().find(c => c !== SourceLang),
      Target: '',
      Reading: '',
    };
    change([...rows(), row]);
    focus(row);
  }
  function remove(i) {
    const next = rows().filter((_, n) => n !== i);
    change(next);
    focus(next[i]);
    editor.settle();
  }
  const blocked = () => editor.error()?.code?.startsWith('glossary_');
  const events = editor.events();
  const save = () => {
    if (!blocked()) editor.settle();
  };
  createEffect(
    () => [app.screen(), app.sub()],
    (_, previous) => {
      if (previous) untrack(save);
    },
  );
  function entry(row, index) {
    const [missing, setMissing] = createSignal([]);
    const edit = (key, value) => {
      if (row()[key] === value) return;
      const changed = { ...row(), [key]: value };
      if (changed.SourceLang !== 'ja' && changed.TargetLang !== 'ja') changed.Reading = '';
      if (value.trim()) setMissing(list => list.filter(k => k !== key));
      change(rows().map(r => (r.id === changed.id ? changed : r)));
    };
    function field(key) {
      const lang = key.endsWith('Lang'),
        id = () => 'glossary-' + row().id + '-' + key;
      const error = () => {
        if (
          refused()?.id === row().id &&
          (editor.error().location.field === key || (lang && editor.error().code === 'glossary_same_language'))
        ) {
          return errorMessage(app.t, editor.error(), 'error.notSaved');
        }
        return missing().includes(key) ? app.t('error.code.glossary_required') : '';
      };
      const attrs = () => ({
        id: id(),
        class: lang ? 'select' : 'input',
        autocomplete: 'off',
        spellcheck: 'false',
        disabled: !!app.state.settingsError,
        'aria-describedby': id() + '-error',
        'aria-label': fieldName(app, row(), key),
        'aria-invalid': error() ? 'true' : undefined,
        onInput: e => edit(key, e.currentTarget.value),
        onChange: e => {
          edit(key, e.currentTarget.value);
          if (!blocked()) events.onChange(e);
        },
      });
      const choices = () => (codes().includes(row()[key]) ? codes() : [...codes(), row()[key]]);
      return (
        <div
          class="glossary-field"
          hidden={key === 'Reading' && row().SourceLang !== 'ja' && row().TargetLang !== 'ja'}
        >
          <label class="field-label" for={id()}>
            {app.t(labels[key])}
          </label>
          {lang ? (
            <select {...attrs()} ref={selectValue(() => row()[key], choices)}>
              <For each={choices()}>{code => <option value={code}>{app.languageName(code)}</option>}</For>
            </select>
          ) : (
            <input
              {...attrs()}
              value={row()[key]}
              lang={key === 'Reading' ? 'ja' : langTag(row()[key + 'Lang']) || ''}
            />
          )}
          <p id={id() + '-error'} class="error-text" hidden={!error()}>
            {error()}
          </p>
        </div>
      );
    }
    const reading = field('Reading'),
      legend = () => 'glossary-entry-' + row().id;
    function side(key) {
      return (
        <div class="glossary-side">
          {field(key + 'Lang')}
          <div class="glossary-words">
            {field(key)}
            {(key === 'Source') === (row().SourceLang === 'ja') && reading}
          </div>
        </div>
      );
    }
    return (
      <li
        onFocusOut={e => {
          const left = !e.currentTarget.contains(e.relatedTarget);
          queueMicrotask(() => {
            if (left && rows().includes(row())) setMissing(['Source', 'Target'].filter(key => !row()[key].trim()));
            save();
          });
        }}
        onKeyDown={e => {
          if (e.key === 'Enter' && !e.isComposing && e.target.tagName === 'INPUT') {
            e.preventDefault();
            save();
          }
        }}
      >
        <fieldset class="glossary-entry" aria-labelledby={legend()}>
          <legend>
            <span id={legend()}>{app.t('label.glossaryEntry', { n: index() + 1 })}</span>
            <button
              type="button"
              class="icon-button"
              disabled={!!app.state.settingsError}
              aria-label={app.t('action.removeEntry', { n: index() + 1 })}
              onClick={() => remove(index())}
            >
              <Icon name="trash-2" />
            </button>
          </legend>
          <div class="glossary-pair">
            {side('Source')}
            <Icon name="arrow-left-right" extra="glossary-link" />
            {side('Target')}
          </div>
        </fieldset>
      </li>
    );
  }
  return (
    <>
      <h2 id="glossary-label" class="sub-title" tabindex="-1">
        {app.t('label.glossary')}
      </h2>
      <p id="glossary-description" class="hint glossary-description">
        {app.t('note.glossary')}
      </p>
      <ul
        id="glossary-list"
        class="glossary-list"
        aria-labelledby="glossary-label"
        aria-describedby="glossary-description glossary-error"
      >
        <For each={rows()} keyed={row => row.id}>
          {entry}
        </For>
      </ul>
      <p id="glossary-error" class="error-text" role="status" hidden={!editor.error() || !!refused()}>
        {!refused() && errorMessage(app.t, editor.error(), 'error.notSaved')}
      </p>
      <div class="field-row">
        <button
          type="button"
          id="glossary-add"
          class="button secondary"
          disabled={!!app.state.settingsError || rows().length >= app.state.glossaryLimit}
          onClick={add}
        >
          {app.t('action.addEntry')}
        </button>
        <Saved app={app} id="glossary" field={saved} />
      </div>
    </>
  );
}
