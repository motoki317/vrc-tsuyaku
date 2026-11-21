import { createMemo, createSignal, createStore, onCleanup } from 'solid-js';
import { eventError, flowLabel } from './i18n.js';

export const pillLabels = { error: 'state.stoppedByError', on: 'state.on', off: 'state.off' };
export const skippedLabel = (app, flow) =>
  app.t('value.announce', { flow: flowLabel(app, flow), text: app.t('state.skipped') });

const logLimit = 300;

function updatePhrase(list, e) {
  const { kind, phraseId, flow, time, text } = e;
  const index = list.findIndex(row => row.phraseId === phraseId);
  let phrase;
  if (index !== -1) {
    phrase = list[index];
  } else {
    if (!['recognized', 'translated', 'merged', 'skipped'].includes(kind)) return;
    const row = { phraseId, flow, time };
    phrase =
      kind === 'skipped'
        ? { ...row, skipped: true }
        : {
            ...row,
            message: phraseId,
            source: e.sourceLang,
            target: e.targetLang,
            original: kind === 'recognized' ? text : e.original,
          };
  }
  const previousMessage = phrase.message;
  if (kind === 'translated' || kind === 'merged') {
    phrase.translation = text;
    phrase.unfinished = false;
    if (flow === 'self' && (kind === 'translated' || !phrase.status)) phrase.status = 'sending';
    if (kind === 'merged') {
      // The first phrase can be missing from the replay or have aged out of the log.
      const shown = list.findIndex(row => row.message === e.into && row.phraseId !== phraseId);
      phrase.message = e.into;
      phrase.original = e.original;
      if (shown >= 0) {
        list[shown] = phrase;
        if (index !== -1) list.splice(index, 1);
        return;
      }
    }
  } else if (kind === 'sent') phrase.status = 'sent';
  else if (kind === 'error') {
    phrase.status = 'failed';
    phrase.code = e.code;
    phrase.error = text;
  }
  if (index === -1 || previousMessage !== phrase.message) {
    const key = phrase.message ?? phrase.phraseId;
    const next = list.findIndex(row => (row.message ?? row.phraseId) > key);
    const destination = next === -1 ? list.length : next;
    if (index !== -1) {
      // A merge uses an earlier phrase's ID, so the row only moves earlier.
      // Leave an already ordered row ahead of later state pills.
      if (destination >= index) return;
      list.splice(index, 1);
    }
    list.splice(destination, 0, phrase);
  }
}

export function createLog(app) {
  const [rows, setRows] = createStore([]);
  const [speech, setSpeech] = createStore({});
  const [unread, setUnread] = createSignal(0);
  const empty = createMemo(() => !rows.some(row => row.phraseId));
  const timers = {};
  let following = true;

  function follow(value) {
    following = value;
    if (value) setUnread(0);
  }
  function silence(flow) {
    clearTimeout(timers[flow]);
    setSpeech(s => {
      s[flow] = '';
    });
  }
  function closePending(flow) {
    setRows(list => {
      for (const row of list) {
        if (!row.phraseId || (flow && row.flow !== flow)) continue;
        if (row.translation == null) row.unfinished = true;
        else if (row.status === 'sending') row.status = 'unfinished';
      }
    });
    for (const name of flow ? [flow] : ['world', 'self']) silence(name);
  }
  function event(e, liveSince) {
    const { flow, kind, phraseId, time, text } = e;
    if (!flow) return;
    let announcement;
    if (kind === 'speech') {
      clearTimeout(timers[flow]);
      const delay = Date.parse(time) + 2000 - Date.now();
      if (text === 'start')
        setSpeech(s => {
          s[flow] = 'open';
        });
      else if (delay > 0) {
        setSpeech(s => {
          if (s[flow]) s[flow] = 'ending';
        });
        timers[flow] = setTimeout(() => silence(flow), delay);
      } else silence(flow);
    } else if (!phraseId && (kind === 'error' || (kind === 'state' && text !== 'starting'))) {
      let pill;
      if (kind === 'error') pill = 'error';
      else pill = text === 'running' ? 'on' : 'off';
      if (pill !== 'on') closePending(flow);
      setRows(list => {
        if (pill === 'on') {
          for (const row of list) {
            if (row.flow === flow && row.pill === 'error') row.resolved = true;
          }
        }
        list.push({ flow, time, pill });
        if (list.length > logLimit) list.shift();
      });
      announcement = app.t('value.announce', { flow: flowLabel(app, flow), text: app.t(pillLabels[pill]) });
      if (pill === 'error') announcement += ' ' + eventError(app.t, e.code, text);
    } else if (phraseId) {
      setRows(list => {
        updatePhrase(list, e);
        if (list.length > logLimit) list.shift();
      });
      if (kind === 'recognized' || kind === 'skipped') {
        setSpeech(s => {
          if (s[flow] !== 'open') {
            clearTimeout(timers[flow]);
            s[flow] = '';
          }
        });
        if (!following) setUnread(n => n + 1);
      }
      if (kind === 'translated') announcement = app.t('value.announce', { flow: flowLabel(app, flow), text });
      else if (kind === 'skipped') announcement = skippedLabel(app, flow);
    }
    if (announcement && Date.parse(time) >= liveSince) app.announce(announcement);
  }
  onCleanup(() => Object.values(timers).forEach(clearTimeout));
  return {
    rows,
    speech,
    empty,
    event,
    closePending,
    unread,
    follow,
    get following() {
      return following;
    },
  };
}
