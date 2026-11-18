import { createSignal, onCleanup } from 'solid-js';

export function savedMark(app) {
  const [mark, setMark] = createSignal('');
  let fade;
  onCleanup(() => clearTimeout(fade));
  function flash() {
    clearTimeout(fade);
    setMark('saved');
    fade = setTimeout(() => {
      setMark('fade');
      fade = setTimeout(() => setMark(''), 400);
    }, 1500);
    app.announce(app.t('state.saved'));
  }
  return { mark, flash };
}

export function settledSetting(app, write, saved, { keepOnError = false, clearOnInput = false } = {}) {
  const [error, setError] = createSignal(null),
    [draft, setDraft] = createSignal();
  let pending, latest, value, timer;
  function input(next) {
    if (next === undefined) return;
    clearTimeout(timer);
    if (pending && next === value) return;
    (keepOnError ? latest : pending)?.discard();
    if (keepOnError) setDraft(next);
    if (clearOnInput) setError(null);
    value = next;
    latest = pending = app.edit(s => write(s, next), { keepOnError });
  }
  async function settle() {
    clearTimeout(timer);
    if (!pending) return;
    const edit = pending;
    pending = value = undefined;
    try {
      const changed = await edit.save();
      if (changed !== undefined && (!clearOnInput || latest === edit)) setError(null);
      if (keepOnError && latest !== edit) return;
      if (keepOnError) setDraft(undefined);
      if (changed) saved?.flash();
      return true;
    } catch (e) {
      if (!clearOnInput || latest === edit) setError(e);
      if (keepOnError && latest === edit) {
        pending = edit;
        value = draft();
      }
    }
  }
  function events(read) {
    const record = e => {
      if (read) input(read(e.target));
    };
    return {
      onInput: record,
      onChange: e => {
        record(e);
        clearTimeout(timer);
        timer = setTimeout(settle, 500);
      },
      onFocusOut: e => {
        if (!e.currentTarget.contains(e.relatedTarget)) settle();
      },
      onKeyDown: e => {
        if (e.key === 'Enter') settle();
      },
    };
  }
  onCleanup(() => {
    clearTimeout(timer);
    if (!keepOnError) settle();
  });
  return { input, settle, events, error, draft };
}

export function Saved({ app, id, field }) {
  return (
    <span
      class={'saved' + (field.mark() === 'fade' ? ' fade' : '')}
      id={id + '-saved'}
      aria-hidden="true"
      hidden={!field.mark()}
    >
      <svg class="icon">
        <use href="#i-check" />
      </svg>
      <span>{app.t('state.saved')}</span>
    </span>
  );
}
