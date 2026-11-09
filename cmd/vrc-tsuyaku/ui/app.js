'use strict';
// The app puts the API token in the URL fragment, which the browser never sends to a server.
const token = location.hash.slice(1);
const $ = id => document.getElementById(id);
const FLOWS = ['self', 'world'];
const TITLE = {self: 'Self flow', world: 'World flow'};
const SETTINGS_KEY = {self: 'Self', world: 'World'};
const NEXT_STEP = {
  self: 'Check the microphone and the chatbox address, then press Try again.',
  world: 'Start VRChat if it is closed, then press Try again.',
};
const LOG_LIMIT = 256;

let state = null;
let devices = null;
let lastEvent = 0;
let refreshTimer = 0;
let queue = Promise.resolve();
let logLive = false;
const busy = new Set();
const flowBusy = {};
const downloadErrors = {};
const flowErrors = {};
const downloadStarts = {};
const flowClicks = {};

function enqueue(task) {
  const run = queue.then(task);
  queue = run.catch(() => {});
  return run;
}

function h(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (/^(aria-|data-|role$)/.test(key)) node.setAttribute(key, value);
    else node[key] = value;
  }
  node.append(...children.flat().filter(child => child != null && child !== false));
  return node;
}

// Repeated button labels such as Download and Start need their subject for screen readers.
const hidden = text => h('span', {className: 'visually-hidden'}, text);

class RequestError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}

function requestNextStep(error) {
  return error.status ? 'Check that vrc-tsuyaku can write to its data folder, then try again.' :
    'Check that vrc-tsuyaku is still running, then try again.';
}

async function api(path, options = {}) {
  let response;
  try {
    response = await fetch('/api/' + path, {...options, headers: {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'}});
  } catch {
    throw new RequestError(0, 'vrc-tsuyaku is not responding.');
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new RequestError(response.status, body.error || `HTTP ${response.status}`);
  }
  return response;
}

function announce(text) {
  if (!text) return;
  // Clearing first makes screen readers repeat a message that equals the previous one.
  $('announce').textContent = '';
  setTimeout(() => { $('announce').textContent = text; }, 50);
}

function size(bytes) {
  const mib = bytes / 1048576;
  if (mib >= 1000) return (mib / 1024).toFixed(1) + ' GB';
  return (mib >= 10 ? Math.round(mib) : mib.toFixed(1)) + ' MB';
}

const artifactBytes = artifact => artifact.Sources.reduce((sum, source) => sum + source.Size, 0);

function needs(flow, artifact) {
  return state.required[flow].includes(artifact.ID);
}

function artifacts() {
  const rank = ({artifact}) => FLOWS.every(flow => needs(flow, artifact)) ? 0 : needs('self', artifact) ? 1 : 2;
  return Object.values(state.artifacts).sort((a, b) => rank(a) - rank(b) || a.artifact.Name.localeCompare(b.artifact.Name));
}

function acceptance({artifact}) {
  if (!artifact.AcceptanceKey) return 'none';
  if (state.settings.LicensesAccepted?.[artifact.AcceptanceKey]) return 'saved';
  return $('accept-' + artifact.ID)?.checked ? 'checked' : 'needed';
}

function gatedLicense(artifact) {
  return artifact.Licenses.find(license => license.Text) || artifact.Licenses[0];
}

function apply(next) {
  const previous = state;
  state = next;
  if (previous) {
    for (const flow of FLOWS) if (previous.flows[flow].state !== next.flows[flow].state) delete flowErrors[flow];
    for (const [id, value] of Object.entries(next.artifacts)) {
      if (value.state !== 'missing' || previous.artifacts[id]?.state !== value.state) delete downloadErrors[id];
    }
    announce(changes(previous, next));
  } else {
    fillFields();
  }
  render();
}

// After this, the fields belong to the user. Rendering never overwrites what they type.
function fillFields() {
  const microphone = state.settings.Flows.Self.Microphone;
  $('address').value = state.settings.Chatbox.Address;
  $('microphone').replaceChildren(new Option('System default microphone', ''));
  if (microphone) $('microphone').append(new Option(microphone, microphone));
  $('microphone').value = microphone;
}

function changes(previous, next) {
  const messages = [];
  // The log speaks every other state change, so only the start, which it does not show, is spoken here.
  for (const flow of FLOWS) {
    if (previous.flows[flow].state !== 'starting' && next.flows[flow].state === 'starting') messages.push(`${TITLE[flow]} is starting.`);
  }
  const started = [];
  for (const [id, after] of Object.entries(next.artifacts)) {
    const before = previous.artifacts[id];
    if (!before || before.state === after.state) continue;
    const name = after.artifact.Name;
    if (after.state === 'installed') messages.push(`${name} is ready.`);
    else if (after.state === 'downloading') started.push(name);
    else if (after.error) messages.push(`${name} did not download: ${after.error}`);
  }
  if (started.length) messages.push(started.length > 1 ? `${started.length} downloads started.` : `${started[0]} is downloading.`);
  const ready = s => Object.values(s.artifacts).every(value => value.state === 'installed');
  if (!ready(previous) && ready(next)) messages.push('All downloads are ready. You can start a flow.');
  return messages.join(' ');
}

async function refresh() {
  apply(await (await api('state')).json());
}

function scheduleRefresh() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => refresh().catch(() => {}), 100);
}

function render() {
  $('main').hidden = false;
  const banner = $('settings-banner'), bannerFocused = banner.contains(document.activeElement);
  banner.hidden = !state.settingsError;
  // Hiding the focused control sends keyboard focus to the start of the page.
  if (bannerFocused && banner.hidden) $('downloads-title').focus();
  if (state.settingsError) $('settings-error').textContent = 'Details: ' + state.settingsError;
  for (const control of [$('microphone'), $('refresh-mics'), $('address')]) control.disabled = !!state.settingsError;
  $('address-current').textContent = state.settings.Chatbox.Address;
  renderDownloads();
  for (const flow of FLOWS) {
    const settings = state.settings.Flows[SETTINGS_KEY[flow]];
    $(flow + '-languages').textContent = `${settings.Spoken} → ${settings.Target}`;
    renderFlow(flow);
  }
}

function setMessage(node, tone, ...content) {
  const children = content.flat().filter(Boolean).map(child => child instanceof Node ? child : document.createTextNode(child));
  node.dataset.tone = tone || '';
  if (children.length === node.childNodes.length && children.every((child, i) => child.isEqualNode(node.childNodes[i]))) return;
  node.replaceChildren(...children);
}

// The app's errors are technical, so the plain summary and next step come first.
function problem(summary, nextStep, details) {
  return [h('p', {}, h('strong', {}, summary), nextStep && ' ' + nextStep), h('p', {className: 'details'}, 'Details: ' + details)];
}

function setBlocked(button, blocked) {
  if (blocked) button.setAttribute('aria-disabled', 'true');
  else button.removeAttribute('aria-disabled');
}

// Downloads

function renderDownloads() {
  const list = artifacts();
  const ready = list.filter(value => value.state === 'installed').length;
  const allReady = ready === list.length;
  const details = $('downloads-details');
  // The list collapses only when nothing in it needs the user.
  if (!details.dataset.initialized || !allReady) details.open = !allReady;
  details.dataset.initialized = 'true';
  details.classList.toggle('all-ready', allReady);
  const tag = $('downloads-tag');
  tag.textContent = allReady ? 'All ready' : `${ready} of ${list.length} ready`;
  tag.dataset.tone = allReady ? 'ok' : 'muted';
  renderGates(list);
  renderDownloadAll(list);
  for (const value of list) renderDownloadRow(value);
}

function renderGates(list) {
  for (const value of list.filter(value => value.artifact.AcceptanceKey)) {
    const {artifact} = value, license = gatedLicense(artifact);
    let gate = $('gate-' + artifact.ID);
    if (!gate) {
      const checkbox = h('input', {type: 'checkbox', id: 'accept-' + artifact.ID});
      checkbox.addEventListener('change', render);
      gate = h('div', {id: 'gate-' + artifact.ID, className: 'gate'},
        h('h3', {}, license.Name),
        h('p', {}, `Accept this license to download ${artifact.Name}.`),
        h('div', {className: 'license-text', tabIndex: 0, role: 'region', 'aria-label': license.Name}, h('pre', {}, license.Text)),
        h('label', {className: 'check'}, checkbox, ` I have read and accept the ${license.Name}.`),
        h('p', {className: 'accepted message', 'data-tone': 'ok'}));
      $('license-gates').append(gate);
    }
    const accepted = state.settings.LicensesAccepted?.[artifact.AcceptanceKey];
    gate.hidden = value.state !== 'missing';
    gate.querySelector('.license-text').hidden = !!accepted;
    gate.querySelector('.check').hidden = !!accepted;
    const note = gate.querySelector('.accepted');
    note.hidden = !accepted;
    if (accepted) note.textContent = `You accepted the ${license.Name} on ${new Date(accepted).toLocaleDateString()}.`;
  }
}

function downloadBlocker(value) {
  if (state.settingsError) return 'Fix the settings problem at the top of the page first.';
  if (acceptance(value) === 'needed') return `Accept the ${gatedLicense(value.artifact).Name} above first.`;
  return '';
}

function renderDownloadAll(list) {
  const missing = list.filter(value => value.state === 'missing');
  const button = $('download-all'), reason = $('download-all-reason'), focused = document.activeElement === button;
  button.hidden = missing.length === 0;
  reason.hidden = button.hidden;
  if (button.hidden) {
    const next = list.find(value => value.state === 'downloading');
    if (focused && next) $('download-' + next.artifact.ID).focus();
    return;
  }
  const bytes = missing.reduce((sum, value) => sum + artifactBytes(value.artifact), 0);
  button.textContent = `${missing.length === list.length ? 'Download all' : 'Download the rest'} (${size(bytes)})`;
  const blocker = missing.map(downloadBlocker).find(Boolean) || '';
  reason.textContent = blocker;
  setBlocked(button, !!blocker || missing.some(value => busy.has(value.artifact.ID)));
}

function licenseLinks(artifact) {
  const links = artifact.Licenses.map(license => {
    const link = h('a', {href: license.URL, target: '_blank', rel: 'noreferrer'}, license.Name);
    return artifact.Licenses.length > 1 ? [link, ` for ${license.Covers}`] : [link];
  });
  return [artifact.Licenses.length > 1 ? 'Licenses: ' : 'License: ', ...links.flatMap((link, i) => i ? [', ', ...link] : link)];
}

function downloadRow(artifact) {
  let row = $('download-' + artifact.ID);
  if (row) return row;
  const flows = FLOWS.filter(flow => needs(flow, artifact));
  const button = h('button', {type: 'button', className: 'secondary', 'aria-describedby': `download-${artifact.ID}-message`});
  button.addEventListener('click', () => download([state.artifacts[artifact.ID]]));
  row = h('li', {id: 'download-' + artifact.ID, className: 'download', tabIndex: -1},
    h('div', {className: 'download-info'},
      h('p', {id: `download-${artifact.ID}-name`, className: 'download-name'}, artifact.Name),
      h('p', {className: 'download-meta'}, flows.length === 2 ? 'Both flows' : TITLE[flows[0]], ` · ${size(artifactBytes(artifact))} · `, ...licenseLinks(artifact))),
    h('div', {className: 'download-state'}, h('span', {className: 'tag'}), button),
    h('div', {className: 'download-progress'}, h('progress', {'aria-labelledby': `download-${artifact.ID}-name`}), h('p', {className: 'progress-text'})),
    h('div', {id: `download-${artifact.ID}-message`, className: 'message'}));
  $('download-list').append(row);
  return row;
}

function renderDownloadRow(value) {
  const {artifact, progress} = value, id = artifact.ID;
  const row = downloadRow(artifact);
  const tag = row.querySelector('.tag'), button = row.querySelector('button');
  const failed = value.state === 'missing' && !!value.error;
  const waiting = value.state === 'downloading' && !progress.Phase;
  const checking = value.state === 'downloading' && progress.Phase && progress.Phase !== 'download';
  [tag.textContent, tag.dataset.tone] =
    value.state === 'installed' ? ['Ready', 'ok'] :
    failed ? ['Failed', 'error'] :
    waiting ? ['Waiting', 'neutral'] :
    checking ? ['Checking', 'busy'] :
    value.state === 'downloading' ? ['Downloading', 'busy'] : ['Not downloaded', 'muted'];

  const focused = document.activeElement === button;
  button.hidden = value.state !== 'missing';
  if (focused && button.hidden) row.focus();
  button.replaceChildren(busy.has(id) ? 'Starting…' : failed ? 'Try again' : 'Download', hidden(` ${artifact.Name}`));
  const blocker = downloadBlocker(value);
  setBlocked(button, !!blocker || busy.has(id));

  const box = row.querySelector('.download-progress');
  box.hidden = value.state !== 'downloading';
  if (!box.hidden) {
    const bar = box.querySelector('progress');
    bar.max = progress.Total || 1;
    bar.value = progress.Downloaded;
    bar.hidden = waiting;
    box.querySelector('.progress-text').textContent =
      waiting ? 'Starts after the current download.' :
      checking ? 'Checking the downloaded files.' :
      `${size(progress.Downloaded)} of ${size(progress.Total)}${timeLeft(id, progress)}`;
  } else {
    delete downloadStarts[id];
  }

  const message = row.querySelector('.message');
  const error = value.error;
  const requestError = downloadErrors[id];
  // An installed artifact reports an error only when old files could not be removed.
  if (value.state === 'missing' && requestError) setMessage(message, 'error', problem('Could not start the download.', requestNextStep(requestError), requestError.message));
  else if (error && value.state === 'installed') setMessage(message, 'warning', problem('Ready, but some old files remain.', 'You can delete the folder named in the details.', error));
  else if (error) setMessage(message, 'error', problem('Download failed.', 'Check the internet connection and free disk space, then press Try again.', error));
  else setMessage(message, blocker && !button.hidden ? 'muted' : '', !button.hidden && blocker);
}

function timeLeft(id, progress) {
  const now = performance.now();
  let start = downloadStarts[id];
  if (!start || progress.Downloaded < start.bytes) start = downloadStarts[id] = {time: now, bytes: progress.Downloaded};
  const seconds = (now - start.time) / 1000, rate = (progress.Downloaded - start.bytes) / seconds;
  if (seconds < 3 || rate <= 0) return '';
  const left = (progress.Total - progress.Downloaded) / rate;
  return left < 60 ? ' · less than a minute left' : ` · about ${Math.round(left / 60)} min left`;
}

async function download(values) {
  values = values.filter(value => value.state === 'missing' && !busy.has(value.artifact.ID));
  const blocker = values.map(downloadBlocker).find(Boolean);
  if (blocker) return announce(blocker);
  for (const value of values) busy.add(value.artifact.ID);
  render();
  for (const value of values) {
    const id = value.artifact.ID;
    try {
      await api('downloads/' + id, {method: 'POST', body: JSON.stringify({acceptLicense: acceptance(value) === 'checked'})});
      delete downloadErrors[id];
    } catch (e) {
      downloadErrors[id] = e;
    } finally {
      busy.delete(id);
    }
  }
  await refresh().catch(() => render());
}

$('download-all').addEventListener('click', () => download(artifacts()));

// Flows

function microphoneName(id) {
  if (!id) return 'the system default microphone';
  return devices?.find(device => device.ID === id)?.Name || id;
}

const isActive = run => run.state === 'starting' || run.state === 'running';

function flowBlocker(flow) {
  if (state.settingsError) return 'Fix the settings problem at the top of the page first.';
  const missing = artifacts().filter(value => needs(flow, value.artifact) && value.state !== 'installed');
  if (!missing.length) return '';
  const which = missing.length > 2 ? `${missing.length} downloads` : missing.map(value => value.artifact.Name).join(' and ');
  return missing.every(value => value.state === 'downloading') ? `Available when ${which} finish.` : `Get ${which} in step 1 first.`;
}

function renderFlow(flow) {
  const run = state.flows[flow];
  const enabled = state.settings.Flows[SETTINGS_KEY[flow]].Enabled;
  const active = isActive(run);
  const blocker = active ? '' : flowBlocker(flow);
  // A flow that could not start for lack of downloads needs the downloads, not a retry.
  const failed = run.state === 'error' && !blocker;

  const tag = $(flow + '-tag');
  [tag.textContent, tag.dataset.tone] =
    run.state === 'running' ? ['Running', 'ok'] :
    run.state === 'starting' ? ['Starting', 'busy'] :
    blocker ? ['Cannot start yet', 'muted'] :
    failed ? ['Error', 'error'] :
    ['Off', 'neutral'];

  // Each button keeps one action, so a redraw between press and release cannot turn Start into Stop.
  const start = $(flow + '-start'), stop = $(flow + '-stop'), focused = document.activeElement;
  start.hidden = active;
  stop.hidden = !active && !failed && !enabled;
  // Hiding the focused button sends keyboard focus to the start of the page.
  if (focused === start && start.hidden) stop.focus();
  if (focused === stop && stop.hidden) start.focus();
  start.replaceChildren(flowBusy[flow] === 'start' ? 'Starting…' : failed ? 'Try again' : 'Start', hidden(` the ${TITLE[flow]}`));
  stop.replaceChildren(flowBusy[flow] === 'stop' ? 'Stopping…' : 'Stop', hidden(` the ${TITLE[flow]}`));
  setBlocked(start, !!blocker || !!flowBusy[flow]);
  setBlocked(stop, !!flowBusy[flow]);

  const message = $(flow + '-message');
  const requestError = flowErrors[flow];
  if (requestError) setMessage(message, 'error', problem(`Could not ${requestError.action} the ${TITLE[flow]}.`, requestNextStep(requestError.error), requestError.error.message));
  else if (failed) setMessage(message, 'error', problem('Stopped by an error.', NEXT_STEP[flow], run.error));
  else if (run.state === 'starting') setMessage(message, 'busy', h('span', {className: 'spinner', 'aria-hidden': 'true'}), 'Loading the speech and translation models…');
  else if (run.state === 'running') setMessage(message, 'ok', flow === 'self' ?
    `Listening to ${microphoneName(state.settings.Flows.Self.Microphone)}. Sending to ${state.settings.Chatbox.Address}.` :
    'Listening to VRChat.');
  else setMessage(message, blocker ? 'muted' : '', blocker);

  const launch = $(flow + '-launch');
  launch.hidden = !!blocker && !enabled;
  launch.textContent = enabled ? 'Starts by itself whenever vrc-tsuyaku opens, until you press Stop.' : 'Once started, it also starts whenever vrc-tsuyaku opens.';
}

async function changeFlow(flow, action) {
  if (flowBusy[flow]) return;
  if ($(`${flow}-${action}`).getAttribute('aria-disabled') === 'true') return announce($(flow + '-message').textContent);
  flowBusy[flow] = action;
  delete flowErrors[flow];
  render();
  try {
    await enqueue(async () => {
      if (flow === 'self' && action === 'start') {
        if (!(await saveSelf())) return;
      }
      apply(await (await api(`flows/${flow}/${action}`, {method: 'POST'})).json());
    });
  } catch (e) {
    flowErrors[flow] = {action, error: e};
  } finally {
    delete flowBusy[flow];
    render();
  }
}

for (const button of document.querySelectorAll('[data-flow]')) {
  button.addEventListener('click', event => {
    const flow = button.dataset.flow;
    // The opposite button can gain focus before a second press.
    if (event.timeStamp - (flowClicks[flow] ?? -Infinity) < 500) return;
    flowClicks[flow] = event.timeStamp;
    changeFlow(flow, button.dataset.action);
  });
}

// Self flow settings. Every change saves at once, so Start always uses what the fields show.

function fieldMessage(field, tone, ...content) {
  const message = $(field + '-message');
  setMessage(message, tone, ...content);
  if (tone === 'error' && field === 'address') $('address-details').open = true;
  if (tone !== 'warning') announce(message.textContent);
}

function clearFieldError(field) {
  if (!$(field).hasAttribute('aria-invalid')) return;
  $(field).removeAttribute('aria-invalid');
  fieldMessage(field, '', '');
}

async function saveSelf(field) {
  if (!field) {
    for (const name of ['microphone', 'address']) if (!(await saveSelf(name))) return false;
    return true;
  }
  const saved = state.settings;
  const value = field === 'address' ? $(field).value.trim() : $(field).value;
  const previous = field === 'address' ? saved.Chatbox.Address : saved.Flows.Self.Microphone;
  if (value === previous) {
    clearFieldError(field);
    return true;
  }
  const restarting = isActive(state.flows.self);
  const next = structuredClone(saved);
  delete next.LicensesAccepted;
  if (field === 'address') next.Chatbox.Address = value;
  else next.Flows.Self.Microphone = value;
  try {
    apply(await (await api('settings', {method: 'PUT', body: JSON.stringify(next)})).json());
    clearFieldError(field);
    fieldMessage(field, 'ok', restarting ? 'Saved. The Self flow restarts with the new setting.' : 'Saved.');
    return true;
  } catch (e) {
    $(field).setAttribute('aria-invalid', 'true');
    const nextStep = e.status === 400 && field === 'address' ? 'Enter an IP address and port, such as 127.0.0.1:9000.' : requestNextStep(e);
    fieldMessage(field, 'error', problem('Not saved.', nextStep, e.message));
    $(field).focus();
    return false;
  }
}

const queueSave = event => enqueue(() => saveSelf(event.target.id));

$('microphone').addEventListener('change', queueSave);
$('address').addEventListener('change', queueSave);
$('address').addEventListener('keydown', event => { if (event.key === 'Enter') queueSave(event); });

function renderMicrophones() {
  const select = $('microphone'), selected = select.value;
  select.replaceChildren(new Option('System default microphone', ''), ...(devices || []).map(device => new Option(device.Name || device.ID, device.ID)));
  if (devices && ![...select.options].some(option => option.value === selected)) {
    select.append(new Option(`${selected} (not connected)`, selected));
    fieldMessage('microphone', 'warning', 'This microphone is not connected. Connect it and press Refresh list, or choose another.');
  } else if ($('microphone-message').dataset.tone === 'warning') {
    fieldMessage('microphone', '', '');
  }
  select.value = selected;
}

async function listMicrophones() {
  try {
    devices = await (await api('microphones')).json() || [];
    renderMicrophones();
    render();
  } catch (e) {
    fieldMessage('microphone', 'error', problem('Could not list the microphones.', e.status ? 'Press Refresh list to try again.' : requestNextStep(e), e.message));
  }
}

$('refresh-mics').addEventListener('click', listMicrophones);

$('replace-settings').addEventListener('click', async () => {
  const next = structuredClone(state.settings);
  delete next.LicensesAccepted;
  try {
    apply(await (await api('settings', {method: 'PUT', body: JSON.stringify(next)})).json());
    announce('settings.yaml now holds the default settings.');
  } catch (e) {
    setMessage($('replace-message'), 'error', problem('Not replaced.', requestNextStep(e), e.message));
  }
});

// Translations

const entries = new Map();

function logLine(event, className, ...content) {
  const list = $('log-list');
  const time = new Date(event.time);
  const item = h('li', {className, 'data-flow': event.flow},
    h('p', {className: 'meta'}, h('span', {className: 'flow-label'}, event.flow === 'self' ? 'Self' : 'World'), ' ',
      h('time', {dateTime: time.toISOString()}, time.toLocaleTimeString())),
    ...content);
  list.append(item);
  while (list.children.length > LOG_LIMIT) {
    const oldest = list.firstChild;
    if (oldest.dataset.phraseId) entries.delete(Number(oldest.dataset.phraseId));
    oldest.remove();
  }
  $('log-empty').hidden = true;
  return item;
}

function addEntry(event, original) {
  const entry = {id: event.phraseId, flow: event.flow, targetLang: event.targetLang, translation: null, done: false};
  entry.node = logLine(event, 'entry',
    h('p', {className: 'original', lang: event.sourceLang}, original),
    h('p', {className: 'translation', 'data-pending': 'true'}, 'Translating…'),
    event.flow === 'self' && h('p', {className: 'delivery'}));
  entry.node.dataset.phraseId = event.phraseId;
  entries.set(event.phraseId, entry);
  return entry;
}

function translateEntry(entry, text) {
  entry.translation = text;
  const node = entry.node.querySelector('.translation');
  node.textContent = text;
  node.lang = entry.targetLang;
  node.removeAttribute('data-pending');
  if (entry.flow === 'self') {
    entry.node.querySelector('.delivery').textContent = 'Sending to the VRChat chatbox…';
    entry.done = false;
  } else entry.done = true;
}

function deliverEntry(id) {
  const entry = entries.get(id);
  if (!entry) return;
  const delivery = entry.node.querySelector('.delivery');
  delivery.textContent = 'Sent to the VRChat chatbox';
  delivery.dataset.tone = 'ok';
  entry.done = true;
}

function closePending(flow, missed) {
  for (const entry of entries.values()) {
    if (entry.flow !== flow || entry.done) continue;
    const untranslated = entry.translation === null;
    entry.node.querySelector(untranslated ? '.translation' : '.delivery').textContent =
      missed ? 'Unknown: this page missed updates while it was disconnected.' :
      untranslated ? 'Not translated: the flow stopped.' : 'Not sent: the flow stopped.';
    entry.done = true;
  }
}

function logEvent(event) {
  const flow = event.flow;
  if (!flow) return;
  // Entries grow in place as they are translated and sent, so following is checked around every change.
  const log = $('log'), following = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  if (event.kind === 'recognized') {
    addEntry(event, event.text);
  } else if (event.kind === 'translated') {
    translateEntry(entries.get(event.phraseId) || addEntry(event, event.original), event.text);
  } else if (event.kind === 'sent') {
    deliverEntry(event.phraseId);
  } else if (event.kind === 'error' || (event.kind === 'state' && event.text !== 'starting')) {
    if (event.text !== 'running') closePending(flow);
    const text = event.kind === 'error' ? (event.operation === 'start' ? 'Could not start: ' : 'Stopped by an error: ') + event.text : event.text === 'running' ? 'Started.' : 'Stopped.';
    logLine(event, 'system' + (event.kind === 'error' ? ' error' : ''), h('p', {className: 'system-text'}, text));
  }
  if (following) log.scrollTop = log.scrollHeight;
}

// Connection

function setConnection(kind) {
  // The banner is an alert, so rewriting it on every retry would repeat the announcement.
  if ($('connection').dataset.state === kind) return;
  const label = {connecting: 'Connecting…', connected: 'Connected', lost: 'Not connected', stale: 'Not connected', token: 'Not connected'}[kind];
  $('connection').textContent = label;
  $('connection').dataset.state = kind;
  const banner = $('connection-banner');
  banner.hidden = kind === 'connected' || kind === 'connecting';
  banner.replaceChildren(...{
    lost: [h('h2', {}, 'vrc-tsuyaku is not responding'), h('p', {}, 'Trying again every few seconds. If you quit vrc-tsuyaku, start it again. It opens a new page.')],
    stale: [h('h2', {}, 'This page is from an earlier run of vrc-tsuyaku'), h('p', {}, 'Use the page that opened when you last started vrc-tsuyaku, or open the address it printed when it started.')],
    token: [h('h2', {}, 'This address is incomplete'), h('p', {}, 'Open the complete address that vrc-tsuyaku printed when it started, including the part after #.')],
  }[kind] || []);
}

async function stream() {
  let first = true;
  for (;;) {
    try {
      const response = await api('events');
      setConnection('connected');
      await refresh();
      if (first) listMicrophones();
      first = false;
      const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
      let buffered = '';
      for (;;) {
        const {done, value} = await reader.read();
        if (done) throw new RequestError(0, 'Event stream closed');
        buffered += value;
        let newline;
        while ((newline = buffered.indexOf('\n')) >= 0) {
          const line = buffered.slice(0, newline);
          buffered = buffered.slice(newline + 1);
          if (!line) continue;
          const event = JSON.parse(line);
          if (event.id <= lastEvent) continue;
          // IDs count up by one, so a jump means the history no longer held what this page missed.
          if (lastEvent && event.id > lastEvent + 1) for (const flow of FLOWS) closePending(flow, true);
          lastEvent = event.id;
          if (['state', 'error', 'download', 'settings'].includes(event.kind)) scheduleRefresh();
          logEvent(event);
        }
        // The first events replay the app's history at once. Reading all of it aloud would bury what is new.
        if (!logLive) {
          logLive = true;
          setTimeout(() => $('log').setAttribute('aria-live', 'polite'), 1000);
        }
      }
    } catch (e) {
      if (e.status === 401) return setConnection('stale');
      setConnection('lost');
      await new Promise(resolve => setTimeout(resolve, 2000));
    }
  }
}

if (token) stream();
else setConnection('token');

window.addEventListener('hashchange', () => {
  if (location.hash.slice(1) !== token) location.reload();
});
