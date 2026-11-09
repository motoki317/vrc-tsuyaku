'use strict';
const $ = id => document.getElementById(id);
const SVG = 'http://www.w3.org/2000/svg';
// Lists put My voice first, because translating the player's own voice is the app's main feature.
const FLOWS = ['self', 'world'];
// On Translation, position carries the flow: Others' voices on the left, My voice on the right.
const SIDES = ['world', 'self'];
const KEY = {self: 'Self', world: 'World'};
const ICON = {self: 'mic', world: 'users'};
const FIRST_TARGETS = ['ja', 'ja-easy', 'en', 'ko', 'zh-hans', 'zh-hant'];
// The first preset is the app's default template, which /api/state carries.
const PRESETS = [
  ['value.presetBoth', () => state.defaultTemplate],
  ['value.presetTranslation', () => '{{.Translation}}'],
  ['value.presetOriginal', () => '{{.Original}}\n{{.Translation}}'],
];
const TOKENS = ['{{.Original}}', '{{.Translation}}', '{{.SourceLang}}', '{{.TargetLang}}'];
const LOG_LIMIT = 300;
const SWITCH_KINDS = ['off', 'on', 'starting', 'error'];

let strings = {};
let lang = 'en';
let state = null;
let screen = '';
let sub = 'models';
let connection = 'ok';
let lastEvent = 0;
let liveSince = Infinity;
let refreshTimer = 0;
let saves = Promise.resolve();
let devices = null;
let devicesError = false;
let lastId = 0;
// A download request is in flight for these artifacts.
const pending = new Set();
// Artifacts that downloaded or waited since the queue was last empty. Progress counts against them only.
const batch = new Set();
const downloadErrors = new Map();
const flowBusy = {};
const flowErrors = {};
const flowClicks = {};
const knownFailures = new Set();
// Next-step ring: per flow, the downloads that this page's presses started while the flow was not ready, the
// flows that one of them made ready while off, and each flow's readiness at the last state.
const awaited = {world: new Set(), self: new Set()};
const ringing = new Set();
const wasReady = {};

const uid = prefix => `${prefix}-${++lastId}`;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

function h(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (value == null || value === false) continue;
    if (/^(aria-|data-|role$|lang$|for$)/.test(key)) node.setAttribute(key, value);
    else node[key] = value;
  }
  node.append(...children.flat().filter(child => child != null && child !== false));
  return node;
}

function icon(name, className = '') {
  const svg = document.createElementNS(SVG, 'svg');
  svg.setAttribute('class', `icon ${className}`.trim());
  svg.setAttribute('aria-hidden', 'true');
  const use = document.createElementNS(SVG, 'use');
  use.setAttribute('href', '#i-' + name);
  svg.append(use);
  return svg;
}

function setIcon(svg, name) {
  svg.firstChild.setAttribute('href', '#i-' + name);
}

function setDisabled(node, disabled) {
  if (disabled) node.setAttribute('aria-disabled', 'true');
  else node.removeAttribute('aria-disabled');
}

const isDisabled = node => node.getAttribute('aria-disabled') === 'true';

function setText(node, text) {
  if (node.textContent !== text) node.textContent = text;
}

// An error line keeps the function that builds its content, so a language switch rebuilds it.
function setError(node, build) {
  node.relabel = () => node.replaceChildren(...(build ? [build()].flat() : []));
  node.setAttribute('data-relabel', '');
  node.relabel();
  node.hidden = !build;
}

// Strings

function t(key, vars = {}) {
  const text = strings[lang]?.[key] ?? strings.en?.[key] ?? key;
  return text.replace(/\{(\w+)\}/g, (match, name) => name in vars ? String(vars[name]) : match);
}

const hasString = key => key in (strings[lang] || {});

function browserLanguage() {
  for (const tag of navigator.languages || [navigator.language || '']) {
    if (/^ja\b/i.test(tag)) return 'ja';
    if (/^en\b/i.test(tag)) return 'en';
  }
  return 'en';
}

const resolveLanguage = setting => setting || browserLanguage();

// While settings.yaml cannot be read, the menu changes only this page, and Replace saves the choice.
let unsavedLanguage = null;
// The choice whose save is in flight, so a state from before the save does not switch the page back.
let pendingLanguage = null;
const languageSetting = () => pendingLanguage ?? unsavedLanguage ?? state?.settings.UI.Language ?? '';

let displayNames = null;
let collator = null;

function setLanguage(next) {
  lang = next;
  displayNames = new Intl.DisplayNames([lang], {type: 'language'});
  collator = new Intl.Collator(lang);
  document.documentElement.lang = lang;
  for (const node of document.querySelectorAll('[data-i18n]')) node.textContent = t(node.dataset.i18n);
  // The rail's labels reserve their bold width (see style.css).
  for (const node of document.querySelectorAll('.rail [data-i18n]')) node.dataset.label = node.textContent;
  for (const node of document.querySelectorAll('[data-i18n-aria-label]')) node.setAttribute('aria-label', t(node.dataset.i18nAriaLabel));
  const name = t(lang === 'ja' ? 'value.languageJapanese' : 'value.languageEnglish');
  setText($('ui-language-name'), name);
  $('ui-language').setAttribute('aria-label', t('label.uiLanguageName', {name}));
  for (const node of document.querySelectorAll('[data-relabel]')) node.relabel();
  if (connection !== 'ok') showConnection();
}

// BCP 47 tags of the app's language codes, for Intl and the lang attribute.
const LANGUAGE_TAGS = {'zh-hans': 'zh-Hans', 'zh-hant': 'zh-Hant'};

function languageName(code) {
  if (code === 'ja-easy') return t('value.jaEasy');
  if (code === 'auto') return t('value.auto');
  try {
    return displayNames.of(LANGUAGE_TAGS[code] || code) || code;
  } catch {
    return code;
  }
}

// A phrase line carries its language so screen readers pronounce it correctly.
function langAttribute(code) {
  if (!code || code === 'auto') return null;
  return code === 'ja-easy' ? 'ja' : LANGUAGE_TAGS[code] || code;
}

function number(value, digits) {
  return new Intl.NumberFormat(lang, {minimumFractionDigits: digits, maximumFractionDigits: digits}).format(value);
}

function unitOf(bytes) {
  if (bytes >= 1e9) return {scale: 1e9, digits: 1, size: 'value.gigabytes', progress: 'value.progressGigabytes'};
  return {scale: 1e6, digits: bytes >= 1e7 ? 0 : 1, size: 'value.megabytes', progress: 'value.progressMegabytes'};
}

function formatSize(bytes) {
  const unit = unitOf(bytes);
  return t(unit.size, {n: number(bytes / unit.scale, unit.digits)});
}

function formatProgress(done, total) {
  const unit = unitOf(total);
  return t(unit.progress, {done: number(done / unit.scale, unit.digits), total: number(total / unit.scale, unit.digits)});
}

const percent = fraction => new Intl.NumberFormat(lang, {style: 'percent', maximumFractionDigits: 0}).format(fraction);
const clock = time => new Date(time).toLocaleTimeString(lang, {hour: '2-digit', minute: '2-digit', hourCycle: 'h23'});
const flowLabel = flow => t({self: 'label.self', world: 'label.world'}[flow]);

// An error string names the player's next action. An unknown code shows the server's text.
function errorText(code, text) {
  return code && code !== 'unknown' && hasString('error.code.' + code) ? t('error.code.' + code) : text;
}

// API

class RequestError extends Error {
  constructor(status, message, code = '') {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function api(path, options = {}) {
  let response;
  try {
    response = await fetch('/api/' + path, {...options, headers: {'Content-Type': 'application/json'}});
  } catch {
    if (connection === 'ok') setConnection('reconnecting');
    throw new RequestError(0, t('error.request'));
  }
  if (connection === 'reconnecting') setConnection('ok');
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new RequestError(response.status, body.error || `HTTP ${response.status}`, body.code);
  }
  return response;
}

async function refresh() {
  apply(await (await api('state')).json());
}

function scheduleRefresh() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => refresh().catch(() => {}), 100);
}

// Saves run one at a time, and each one changes the settings that the previous one returned.
// While settings.yaml cannot be read, a save would overwrite it, so only Replace passes overwrite.
function saveSettings(change, {overwrite = false} = {}) {
  const put = async () => {
    if (state.settingsError && !overwrite) throw new RequestError(409, t('error.notSaved'));
    const next = structuredClone(state.settings);
    change(next);
    apply(await (await api('settings', {method: 'PUT', body: JSON.stringify(next)})).json());
  };
  const run = saves.then(async () => {
    try {
      await put();
    } catch (e) {
      if (e.code !== 'settings_changed') throw e;
      // settings.yaml changed after the server last read it, so the server reloaded it and saved nothing. The change
      // applies once more, to the reloaded settings. An edit that made the file invalid shows the settings.yaml screen.
      await refresh();
      if (state.settingsError) throw e;
      await put();
    }
  });
  saves = run.catch(() => {});
  return run;
}

// A refused save shows the string for its code, or "Not saved" for a code that has none.
const saveRefusal = e => e.status ? errorText(e.code, t('error.notSaved')) : t('error.request');

function announce(text) {
  if (!text) return;
  // Clearing first makes screen readers repeat a message that equals the previous one.
  $('announce').textContent = '';
  setTimeout(() => { $('announce').textContent = text; }, 50);
}

// Catalog helpers

const artifactSize = artifact => artifact.Sources.reduce((sum, source) => sum + source.Size, 0);
const settingsOf = flow => state.settings.Flows[KEY[flow]];
const required = flow => state.required?.[flow] || [];
const active = flow => ['starting', 'running'].includes(state.flows[flow]?.state);

function artifactKind(value) {
  if (value.state === 'installed') return value.error ? 'leftover' : 'installed';
  if (value.state === 'downloading') {
    if (!value.progress.Phase) return 'queued';
    return value.progress.Phase === 'download' ? 'downloading' : 'verifying';
  }
  return value.error ? 'failed' : 'missing';
}

const startable = id => ['missing', 'failed'].includes(artifactKind(state.artifacts[id]));
const inProgress = id => pending.has(id) || ['queued', 'downloading', 'verifying'].includes(artifactKind(state.artifacts[id]));
const totalSize = ids => ids.reduce((sum, id) => sum + artifactSize(state.artifacts[id].artifact), 0);
// A refused download request shows on its row while the download stays missing.
const requestError = id => artifactKind(state.artifacts[id]) === 'missing' ? downloadErrors.get(id) : undefined;

const translatorOf = id => state.languages.translators.find(translator => translator.id === id);

// Every-flow artifacts, the selected translator among them, download first, smallest first. My voice's recognizer
// comes next, so My voice is ready and Next unlocks before Others' voices' recognizer finishes.
function downloadOrder(ids) {
  const everyFlow = [...state.languages.commonArtifacts, translatorOf(state.settings.Translator)?.artifact];
  const rank = id => everyFlow.includes(id) ? 0 : required('self').includes(id) ? 1 : 2;
  return [...new Set(ids)].sort((a, b) => rank(a) - rank(b) || artifactSize(state.artifacts[a].artifact) - artifactSize(state.artifacts[b].artifact));
}

const neededIds = () => downloadOrder(FLOWS.flatMap(required));
const byName = ids => [...ids].sort((a, b) => collator.compare(state.artifacts[a].artifact.Name, state.artifacts[b].artifact.Name));

function batchProgress(ids) {
  let done = 0, total = 0;
  for (const id of ids) {
    if (!batch.has(id)) continue;
    const value = state.artifacts[id], size = artifactSize(value.artifact);
    total += size;
    if (value.state === 'installed') done += size;
    else if (value.state === 'downloading') done += Math.min(value.progress.Downloaded, size);
  }
  return {done, total, fraction: total ? done / total : 0};
}

const ready = flow => required(flow).length > 0 && required(flow).every(id => state.artifacts[id]?.state === 'installed');

// A flow rings when it turns ready while off and one of the downloads that it now needs came from a press on this
// page while the flow waited. Downloads that only another page started do not ring.
// The ring ends when the switch leaves "off", for example at the player's press.
function trackReady(flow) {
  const now = ready(flow), before = wasReady[flow];
  wasReady[flow] = now;
  if (now && before === false && [...awaited[flow]].some(id => required(flow).includes(id))) ringing.add(flow);
  if (now) awaited[flow].clear();
  if (flowView(flow, 'bar').kind !== 'off') ringing.delete(flow);
}

// Downloads

async function startDownloads(ids, opener) {
  if (state.settingsError) return;
  ids = downloadOrder(ids).filter(id => startable(id) && !pending.has(id));
  if (!ids.length) return;
  // One dialog per license that the settings do not hold yet, covering every download of this press that needs it.
  const keyOf = id => state.artifacts[id].artifact.AcceptanceKey;
  const unaccepted = ids.filter(id => keyOf(id) && !state.settings.LicensesAccepted?.[keyOf(id)]);
  for (const key of new Set(unaccepted.map(keyOf))) {
    if (!(await confirmLicense(unaccepted.filter(id => keyOf(id) === key), opener))) return;
  }
  const accepted = new Set(unaccepted);
  for (const id of ids) {
    pending.add(id);
    batch.add(id);
    downloadErrors.delete(id);
    for (const flow of FLOWS) if (required(flow).includes(id)) awaited[flow].add(id);
  }
  render();
  for (const id of ids) {
    try {
      await api('downloads/' + id, {method: 'POST', body: JSON.stringify({acceptLicense: accepted.has(id)})});
    } catch (e) {
      if (e.status) downloadErrors.set(id, e);
    } finally {
      pending.delete(id);
    }
  }
  await refresh().catch(() => render());
}

const licenseCache = new Map();

// The dialog's download rows show what the player accepts the license for.
function confirmLicense(ids, opener) {
  const dialog = $('license-dialog');
  const artifact = state.artifacts[ids[0]].artifact;
  const license = artifact.Licenses.find(value => value.Text) || artifact.Licenses[0];
  $('license-title').textContent = license.Name;
  renderList($('license-downloads'), licenseCache, ids, 'license');
  $('license-text').textContent = license.Text || license.URL;
  $('license-text').scrollTop = 0;
  return new Promise(resolve => {
    const accept = () => { dialog.returnValue = 'accept'; dialog.close(); };
    $('license-accept').addEventListener('click', accept);
    dialog.addEventListener('close', () => {
      $('license-accept').removeEventListener('click', accept);
      // Cancel returns focus to the control that opened the dialog. Accept keeps it there for the progress.
      opener?.focus();
      resolve(dialog.returnValue === 'accept');
    }, {once: true});
    dialog.returnValue = '';
    dialog.showModal();
    $('license-text').focus();
  });
}

function downloadAllView() {
  const ids = neededIds();
  const missing = ids.filter(startable);
  if (ids.some(inProgress)) return {kind: 'running', ids, ...batchProgress(ids)};
  if (ids.some(id => artifactKind(state.artifacts[id]) === 'failed')) return {kind: 'retry', ids: missing, bytes: totalSize(missing)};
  if (missing.length) return {kind: 'idle', ids: missing, bytes: totalSize(missing)};
  return {kind: 'none'};
}

// Flow control: one button per place that changes its look with the flow's state and keeps focus.

function flowView(flow, context) {
  const ids = required(flow);
  const kinds = ids.map(id => artifactKind(state.artifacts[id]));
  const missing = ids.filter(startable);
  const failed = ids.find((id, i) => kinds[i] === 'failed');
  if (failed) return {kind: 'failed', ids: missing, error: state.artifacts[failed]};
  if (kinds.some(kind => kind === 'downloading' || kind === 'verifying')) return {kind: 'downloading', ...batchProgress(ids)};
  if (kinds.includes('queued') || ids.some(id => pending.has(id))) return {kind: 'queued'};
  if (missing.length) return {kind: 'missing', ids: missing, bytes: totalSize(missing)};
  // The switch states show only on Translation: turning a flow on or off is not a language setting.
  if (context === 'plan') return {kind: 'ready'};
  const run = state.flows[flow], enabled = settingsOf(flow).Enabled;
  if (run.state === 'starting') return {kind: 'starting', checked: true};
  if (run.state === 'running') return {kind: 'on', checked: true};
  if (run.state === 'error') return {kind: 'error', checked: enabled, error: run};
  return {kind: enabled ? 'on' : 'off', checked: enabled};
}

class FlowControl {
  constructor(flow, context) {
    this.flow = flow;
    this.context = context;
    this.thumbIcon = icon('check');
    this.glyph = icon('download', 'glyph');
    this.retryGlyph = icon('rotate-ccw', 'retry-glyph');
    this.text = h('span', {className: 'fc-text'});
    this.ring = h('span', {className: 'ring'}, this.ringText = h('span', {className: 'ring-text'}));
    this.stateText = h('span', {className: 'visually-hidden', id: uid('fc-state')});
    this.button = h('button', {type: 'button', className: 'fc', 'data-flow': flow},
      h('span', {className: 'track'}, h('span', {className: 'thumb'}, this.thumbIcon)),
      this.glyph, this.text, this.ring, this.retryGlyph, this.stateText);
    this.errorText = h('span', {id: uid('fc-error')});
    this.retry = h('button', {type: 'button', className: 'button secondary compact'}, icon('rotate-ccw'), this.retryLabel = h('span'));
    this.error = h('div', {className: 'fc-error', hidden: true}, icon('circle-alert'), this.errorText, this.retry);
    this.button.addEventListener('click', event => this.press(event));
    this.retry.addEventListener('click', () => changeFlow(this.flow, 'start'));
  }

  update() {
    const flow = this.flow, view = flowView(flow, this.context), button = this.button, label = flowLabel(flow);
    let name = label, status = '', action = null, error = '';
    button.dataset.kind = view.kind;
    setText(this.text, '');
    switch (view.kind) {
    case 'missing':
      setIcon(this.glyph, 'download');
      setText(this.text, formatSize(view.bytes));
      name = t('action.downloadFor', {size: formatSize(view.bytes), flow: label});
      action = 'download';
      break;
    case 'queued':
      setIcon(this.glyph, 'clock');
      name = t('state.flowState', {flow: label, state: t('state.queued')});
      break;
    case 'downloading':
      this.ring.style.setProperty('--p', view.fraction);
      setText(this.ringText, percent(view.fraction));
      name = t('state.flowState', {flow: label, state: t('state.downloadingPercent', {percent: percent(view.fraction)})});
      break;
    case 'failed':
      setIcon(this.glyph, 'circle-alert');
      name = t('action.retryFor', {flow: label});
      error = errorText(view.error.code, view.error.error);
      action = 'download';
      break;
    case 'ready':
      setIcon(this.glyph, 'check');
      name = t('state.flowState', {flow: label, state: t('state.ready')});
      break;
    case 'starting':
      setIcon(this.thumbIcon, 'loader-circle');
      status = t('state.starting');
      break;
    case 'error':
      // Not `check`: a switch in error is on but not working.
      setIcon(this.thumbIcon, 'circle-alert');
      error = errorText(view.error.code, view.error.error);
      action = view.checked ? 'stop' : 'start';
      break;
    default:
      setIcon(this.thumbIcon, 'check');
      action = view.checked ? 'stop' : 'start';
    }
    const request = flowErrors[flow];
    if (!error && request && SWITCH_KINDS.includes(view.kind)) error = errorText(request.code, request.message);
    // While settings.yaml is invalid, the server only accepts a stop.
    if (state.settingsError && action !== 'stop') action = null;
    if (flowBusy[flow] || (action === 'download' && view.ids.some(id => pending.has(id)))) action = null;
    this.action = action;
    this.view = view;
    // Next-step ring: turning this switch on is what the player should do next. On Translation, that holds for
    // both off switches until the log holds a bubble.
    const empty = this.context === 'bar' && !phrases.size;
    button.classList.toggle('next', view.kind === 'off' && action === 'start' && (empty || ringing.has(flow)));
    if (SWITCH_KINDS.includes(view.kind)) {
      button.setAttribute('role', 'switch');
      button.setAttribute('aria-checked', String(view.checked));
      button.classList.toggle('checked', view.checked);
    } else {
      button.removeAttribute('role');
      button.removeAttribute('aria-checked');
      button.classList.remove('checked');
    }
    button.setAttribute('aria-label', name);
    setText(this.stateText, status);
    setDisabled(button, !action);
    const showRetry = view.kind === 'error' && !state.settingsError;
    // Try again hides once the flow starts, and focus must not drop to the page with it.
    if ((!showRetry || !error) && this.error.contains(document.activeElement)) button.focus();
    this.retry.hidden = !showRetry;
    setText(this.retryLabel, t('action.tryAgain'));
    setText(this.errorText, error);
    this.error.hidden = !error;
    button.setAttribute('aria-describedby', [this.stateText.id, error && this.errorText.id].filter(Boolean).join(' '));
  }

  press(event) {
    if (isDisabled(this.button) || !this.action) return;
    // A second click within half a second is a double click, not a request to toggle back.
    if (event.timeStamp - (flowClicks[this.flow] ?? -Infinity) < 500) return;
    flowClicks[this.flow] = event.timeStamp;
    if (this.action === 'download') startDownloads(this.view.ids, this.button);
    else {
      ringing.delete(this.flow);
      changeFlow(this.flow, this.action);
    }
  }
}

async function changeFlow(flow, action) {
  if (flowBusy[flow]) return;
  flowBusy[flow] = action;
  delete flowErrors[flow];
  render();
  try {
    apply(await (await api(`flows/${flow}/${action}`, {method: 'POST'})).json());
  } catch (e) {
    // The state already holds a refused start, so only other refusals need their own line.
    if (e.status && e.code !== 'missing_downloads') flowErrors[flow] = e;
    await refresh().catch(() => {});
  } finally {
    delete flowBusy[flow];
    render();
  }
}

// Download row: the same component in Setup's downloads list (place "plan"), in Settings' downloads lists ("settings")
// and in the license dialog ("license"), which shows no action. Settings → About lists the licenses.

function downloadRow(cache, id, place) {
  let row = cache.get(id);
  if (row) return row;
  const artifact = state.artifacts[id].artifact;
  // The row takes focus when its action disappears under the keyboard.
  row = h('li', {className: 'download', tabIndex: -1});
  row.lead = h('span', {className: 'lead'}, row.leadIcon = icon('check'), row.warnIcon = icon('circle-alert', 'warn'), row.leadText = h('span', {className: 'visually-hidden'}));
  row.nameText = h('span', {className: 'name'}, artifact.Name);
  row.sizeText = h('span', {className: 'size value'});
  row.flows = h('span', {className: 'flows'});
  // The open action leaves the page for the file manager, so it ends with `arrow-up-right`.
  row.action = h('button', {type: 'button', className: 'icon-button'}, row.actionIcon = icon('download'), icon('arrow-up-right', 'small exit'));
  row.bar = h('div', {className: 'bar'}, row.fill = h('span', {className: 'fill'}));
  row.barText = h('span', {className: 'bar-text value'});
  row.progress = h('div', {className: 'progress'}, row.bar, row.barText);
  row.error = h('p', {className: 'error-text'});
  row.append(...[row.lead, row.nameText, row.sizeText, place !== 'plan' && row.flows, place !== 'license' && row.action,
    row.progress, row.error].filter(Boolean));
  row.action.addEventListener('click', () => {
    const action = row.action.dataset.action;
    if (isDisabled(row.action)) return;
    if (action === 'download') startDownloads([id], row.action);
    else if (action === 'delete') openDelete(id, row.action);
    else if (action === 'folder') openTarget('data', row.action, e => { row.openRefusal = e; updateRow(row, id, place); });
  });
  cache.set(id, row);
  return row;
}

function updateRow(row, id, place) {
  const value = state.artifacts[id], kind = artifactKind(value), name = value.artifact.Name;
  row.dataset.kind = kind;
  setIcon(row.leadIcon, {installed: 'check', leftover: 'check', missing: 'circle-dashed', queued: 'clock', failed: 'circle-alert'}[kind] || 'loader-circle');
  row.leadIcon.classList.toggle('spin', kind === 'downloading' || kind === 'verifying');
  setText(row.leadText, t({installed: 'state.installed', leftover: 'state.leftover', missing: 'state.missing', queued: 'state.queued', failed: 'state.failed', downloading: 'state.downloading', verifying: 'state.verifying'}[kind]));
  setText(row.sizeText, formatSize(artifactSize(value.artifact)));
  if (kind === 'downloading') {
    const fraction = value.progress.Total ? value.progress.Downloaded / value.progress.Total : 0;
    row.fill.style.setProperty('--p', fraction);
    setText(row.barText, percent(fraction));
  } else if (kind === 'verifying') {
    setText(row.barText, t('state.verifying'));
  }
  row.progress.hidden = kind !== 'downloading' && kind !== 'verifying';
  const users = FLOWS.filter(flow => required(flow).includes(id));
  const running = users.filter(active);
  if (place !== 'plan') {
    if (row.flows.dataset.signature !== users.join()) {
      row.flows.replaceChildren(...users.map(flow => h('span', {'data-flow': flow, role: 'img'}, icon(ICON[flow]))));
      row.flows.dataset.signature = users.join();
    }
    // A running flow's icon takes the flow's "on" style.
    for (const node of row.flows.children) {
      const flow = node.dataset.flow, on = running.includes(flow);
      node.classList.toggle('on', on);
      node.setAttribute('aria-label', t(on ? 'state.inUseBy' : 'state.usedBy', {flow: flowLabel(flow)}));
    }
  }
  let action = null, actionIcon = 'download', actionName = '';
  if (kind === 'missing' || kind === 'failed') {
    action = 'download';
    actionIcon = kind === 'failed' ? 'rotate-ccw' : 'download';
    actionName = t(kind === 'failed' ? 'action.retryDownload' : 'action.download', {name});
  } else if (kind === 'leftover') {
    action = 'folder';
    actionIcon = 'folder-open';
    actionName = t('action.openData');
  } else if (kind === 'installed' && place === 'settings' && !running.length) {
    // A download in use has no action. Its "Used by" icons in the "on" style say why.
    action = 'delete';
    actionIcon = 'trash-2';
    actionName = t('action.deleteName', {name});
  }
  if (!action && document.activeElement === row.action) row.focus();
  row.action.hidden = !action;
  // The click handler and the style read the action from here.
  row.action.dataset.action = action || '';
  if (action) {
    setIcon(row.actionIcon, actionIcon);
    row.action.setAttribute('aria-label', actionName);
    const blocked = action === 'download' && (pending.has(id) || !!state.settingsError);
    setDisabled(row.action, blocked);
  }
  // The open action exists only on a leftover row, so its refusal leaves with that state.
  if (kind !== 'leftover') row.openRefusal = null;
  const request = requestError(id), refusal = row.openRefusal;
  const error = kind === 'failed' ? errorText(value.code, value.error) :
    refusal ? errorText(refusal.code, refusal.message) :
    kind === 'leftover' ? t('error.leftover') :
    request ? errorText(request.code, request.message) : '';
  setText(row.error, error);
  row.error.hidden = !error;
  row.error.classList.toggle('warning', kind === 'leftover' && !refusal);
}

function renderList(list, cache, ids, place, head = null) {
  const rows = [head, ...ids.map(id => downloadRow(cache, id, place))].filter(Boolean);
  if (rows.length !== list.children.length || rows.some((row, i) => list.children[i] !== row)) list.replaceChildren(...rows);
  for (const [i, id] of ids.entries()) updateRow(rows[head ? i + 1 : i], id, place);
}

// The flow icons' column header. Each icon carries its own name, so screen readers skip the header.
const usedByHead = () => h('li', {className: 'download download-head', 'aria-hidden': 'true'}, h('span', {className: 'used-by'}));

// Language plan: one instance, shown in Setup and in Settings → Languages and models. Settings adds each flow's
// recognizer.

const plan = {rows: {}, cache: new Map(), settingsCache: new Map(), head: usedByHead()};

// A select, or a radio group whose value is its checked radio's.
function commitOnSettle(field, commit, read = () => field.value) {
  let timer = 0;
  const settle = () => {
    clearTimeout(timer);
    if (field.dataset.dirty !== 'changed') return;
    field.dataset.dirty = 'saving';
    commit(read()).finally(() => {
      if (field.dataset.dirty === 'saving') delete field.dataset.dirty;
      render();
    });
  };
  // Keyboard steps through a closed select or a radio group fire change. Committing each step would restart the
  // flows each time.
  field.addEventListener('change', () => {
    field.dataset.dirty = 'changed';
    clearTimeout(timer);
    timer = setTimeout(settle, 500);
  });
  // Arrow keys move focus between a group's radios, so only focus that leaves the field commits.
  field.addEventListener('focusout', event => { if (!field.contains(event.relatedTarget)) settle(); });
  field.settle = settle;
}

function buildPlan() {
  const rows = FLOWS.map(flow => {
    const control = new FlowControl(flow, 'plan');
    const spoken = h('select', {className: 'select'});
    const target = h('select', {className: 'select'});
    const row = {control, spoken, target, label: h('span', {className: 'row-label'}), saveError: h('p', {className: 'error-text', hidden: true})};
    // The spoken language selects the recognizer, so its group lists one choice, checked, in the same radio form as
    // the translator choices.
    row.recognizer = modelCard('recognizer-' + flow);
    row.recognizer.input.checked = true;
    row.recognizerLabel = h('span', {className: 'model-label'});
    row.recognizerGroup = h('div', {className: 'options', role: 'radiogroup'}, row.recognizer.root);
    row.model = h('div', {className: 'model'}, row.recognizerLabel, row.recognizerGroup);
    for (const [select, field] of [[spoken, 'Spoken'], [target, 'Target']]) {
      commitOnSettle(select, value => saveSettings(s => { s.Flows[KEY[flow]][field] = value; })
        .then(() => setError(row.saveError, null))
        .catch(e => {
          delete select.dataset.dirty;
          setError(row.saveError, () => e.status ? errorText(e.code, e.message) : t('error.request'));
        }));
    }
    plan.rows[flow] = row;
    return h('div', {className: 'plan-row', 'data-flow': flow},
      h('div', {className: 'plan-head'}, icon(ICON[flow], 'flow-icon'), row.label, control.button),
      h('div', {className: 'chain'}, spoken, h('span', {className: 'link'}, icon('arrow-right', 'muted'), target)),
      control.error, row.saveError, row.model);
  });
  plan.all = h('button', {type: 'button', className: 'button download-all'},
    plan.allFill = h('span', {className: 'fill'}),
    plan.allIcon = icon('download'), plan.allLabel = h('span', {className: 'label'}), plan.allLeft = h('span', {className: 'left'}),
    plan.allSize = h('span', {className: 'size'}));
  plan.all.addEventListener('click', () => {
    if (isDisabled(plan.all)) return;
    startDownloads(downloadAllView().ids, plan.all);
  });
  plan.summaryLabel = h('span');
  plan.list = h('ul', {className: 'download-list'});
  plan.more = h('button', {type: 'button', className: 'button secondary compact more'}, plan.moreLabel = h('span'), icon('arrow-right'));
  plan.more.addEventListener('click', () => {
    show('settings', 'downloads');
    $('sub-downloads').querySelector('.sub-title').focus();
  });
  plan.details = h('details', {className: 'plan-downloads'},
    h('summary', {}, icon('chevron-down', 'chevron'), plan.summaryLabel), plan.list, plan.more);
  plan.rowsRoot = h('div', {className: 'plan-rows'}, rows);
}

function spokenChoices(flow) {
  const spoken = state.languages.spoken.filter(language => flow === 'world' || !language.auto);
  const first = spoken.filter(language => language.code === 'ja');
  const rest = spoken.filter(language => language.code !== 'ja')
    .sort((a, b) => b.auto - a.auto || collator.compare(languageName(a.code), languageName(b.code)));
  return {first, rest};
}

function fillSelect(select, signature, build, value) {
  // Rebuilding a focused select would close its open list.
  if (select.dataset.signature !== signature && document.activeElement !== select) {
    select.replaceChildren(...build());
    select.dataset.signature = signature;
  }
  if (!select.dataset.dirty && select.value !== value) select.value = value;
}

const recognizerOf = flow => state.languages.spoken.find(language => language.code === settingsOf(flow).Spoken).recognizer;

// The engine shares a recognizer between flows that need the same one, so each model counts once.
function memoryEstimate() {
  const ids = new Set([...FLOWS.map(recognizerOf), translatorOf(state.settings.Translator).artifact]);
  return [...ids].reduce((sum, id) => sum + state.artifacts[id].artifact.MemoryBytes, 0);
}

function renderPlan() {
  const setup = screen === 'setup';
  const parent = setup ? $('screen-setup') : $('sub-models');
  const slot = parent.querySelector('.plan-slot'), foot = parent.querySelector('.plan-foot');
  if (plan.rowsRoot.parentNode !== slot) slot.append(plan.rowsRoot);
  if (plan.all.parentNode !== foot) foot.append(plan.all, plan.details);
  for (const flow of FLOWS) {
    const row = plan.rows[flow];
    setText(row.label, flowLabel(flow));
    row.spoken.setAttribute('aria-label', t('label.spoken', {flow: flowLabel(flow)}));
    row.target.setAttribute('aria-label', t('label.target', {flow: flowLabel(flow)}));
    const {first, rest} = spokenChoices(flow);
    const option = language => new Option(languageName(language.code), language.code);
    fillSelect(row.spoken, JSON.stringify([lang, [...first, ...rest].map(language => language.code)]),
      () => [...first.map(option), rest.length ? h('optgroup', {label: t('label.european')}, rest.map(option)) : null].filter(Boolean),
      settingsOf(flow).Spoken);
    const targets = state.languages.targets;
    const top = FIRST_TARGETS.filter(code => targets.includes(code));
    const others = targets.filter(code => !top.includes(code)).sort((a, b) => collator.compare(languageName(a), languageName(b)));
    fillSelect(row.target, JSON.stringify([lang, targets]),
      () => [...top.map(code => new Option(languageName(code), code)), h('hr'), ...others.map(code => new Option(languageName(code), code))],
      settingsOf(flow).Target);
    row.control.update();
    row.model.hidden = setup;
    setText(row.recognizerLabel, t('label.recognizer'));
    row.recognizerGroup.setAttribute('aria-label', t('label.recognizerFor', {flow: flowLabel(flow)}));
    updateModelCard(row.recognizer, recognizerOf(flow));
  }
  setText($('memory-total'), formatSize(memoryEstimate()));
  renderDownloadAll();
  // The list holds what Download all fetches. Settings adds any download in progress, so its progress stays in view
  // after a language change, and leaves every other download to Settings → Downloads.
  const needed = neededIds();
  const ids = byName(setup ? needed : Object.keys(state.artifacts).filter(id => needed.includes(id) || inProgress(id)));
  setText(plan.summaryLabel, t('label.downloads'));
  plan.more.hidden = setup;
  if (setup) renderList(plan.list, plan.cache, ids, 'plan');
  else {
    setText(plan.head.firstChild, t('label.usedBy'));
    setText(plan.moreLabel, t('action.allDownloads'));
    renderList(plan.list, plan.settingsCache, ids, 'settings', plan.head);
  }
  // A failure, or a refused request, opens the section that holds it, once per failure.
  for (const id of ids) {
    if (artifactKind(state.artifacts[id]) !== 'failed' && !requestError(id)) knownFailures.delete(id);
    else if (!knownFailures.has(id)) {
      knownFailures.add(id);
      plan.details.open = true;
    }
  }
  const anyReady = FLOWS.some(ready);
  const next = $('next');
  setDisabled(next, !anyReady);
  next.classList.toggle('secondary', !anyReady);
  plan.all.classList.toggle('secondary', setup && anyReady);
}

// Progress samples of the running batch, for the time left.
const rate = {total: 0, since: 0, samples: []};

// The time left is the remaining bytes over the average rate of the last 10 s, once 3 s of samples exist.
function timeLeft(done, total) {
  const now = Date.now();
  if (rate.total !== total) Object.assign(rate, {total, since: now, samples: []});
  rate.samples.push({time: now, done});
  rate.samples = rate.samples.filter(sample => sample.time >= now - 10000);
  const first = rate.samples[0];
  const speed = now > first.time ? (done - first.done) / (now - first.time) * 1000 : 0;
  if (now - rate.since < 3000 || speed <= 0) return '';
  const seconds = (total - done) / speed;
  return seconds < 60 ? t('value.underMinute') : t('value.minutesLeft', {n: number(Math.round(seconds / 60), 0)});
}

function renderDownloadAll() {
  const view = downloadAllView(), button = plan.all;
  if (view.kind !== 'running') rate.total = 0;
  if (view.kind === 'none') {
    if (document.activeElement === button) (screen === 'setup' ? $('next') : plan.details.querySelector('summary')).focus();
    button.hidden = true;
    return;
  }
  button.hidden = false;
  button.dataset.kind = view.kind;
  setDisabled(button, view.kind === 'running' || !!state.settingsError);
  if (view.kind === 'running') {
    const progress = formatProgress(view.done, view.total), left = timeLeft(view.done, view.total);
    plan.allFill.style.setProperty('--p', view.fraction);
    setText(plan.allLabel, progress);
    setText(plan.allLeft, left);
    setText(plan.allSize, percent(view.fraction));
    button.setAttribute('aria-label', left ?
      t('state.downloadAllLeft', {progress, percent: percent(view.fraction), left}) :
      t('state.downloadAll', {progress, percent: percent(view.fraction)}));
  } else {
    setIcon(plan.allIcon, view.kind === 'retry' ? 'rotate-ccw' : 'download');
    setText(plan.allLabel, t(view.kind === 'retry' ? 'action.retry' : 'action.downloadAll'));
    setText(plan.allLeft, '');
    setText(plan.allSize, formatSize(view.bytes));
    button.removeAttribute('aria-label');
  }
}

// Switch bar

const bar = {};

function buildSwitchBar() {
  const cards = SIDES.map(flow => {
    const control = new FlowControl(flow, 'bar');
    // The chip wraps after its arrow when the two names do not fit on one line.
    const source = h('span'), target = h('span');
    const chip = h('button', {type: 'button', className: 'chip'},
      h('span', {className: 'chip-part'}, source, icon('arrow-right', 'small')), h('span', {className: 'chip-part'}, target));
    chip.addEventListener('click', () => {
      if (isDisabled(chip)) return;
      show('settings', 'models');
      plan.rows[flow].spoken.focus();
    });
    const label = h('span', {className: 'card-label'});
    bar[flow] = {control, chip, source, target, label};
    return h('div', {className: 'card-wrap', 'data-flow': flow},
      h('div', {className: 'card'}, icon(ICON[flow], 'flow-icon'), h('span', {className: 'card-text'}, label, chip), control.button),
      control.error);
  });
  $('switchbar').firstElementChild.replaceChildren(...cards);
}

function renderSwitchBar() {
  for (const flow of SIDES) {
    const {control, chip, source, target, label} = bar[flow];
    const settings = settingsOf(flow), names = {source: languageName(settings.Spoken), target: languageName(settings.Target)};
    setText(label, flowLabel(flow));
    setText(source, names.source);
    setText(target, names.target);
    // The name adds the flow, so a screen reader tells the two chips apart.
    chip.setAttribute('aria-label', t('action.changeLanguages', {flow: flowLabel(flow), pair: t('value.pair', names)}));
    setDisabled(chip, !!state.settingsError);
    control.update();
  }
}

// Translation log

const phrases = new Map();
const speech = {world: {open: false, timer: 0}, self: {open: false, timer: 0}};
let unread = 0;
// Whether the log keeps its newest bubble in view. Only scrolling sets it, never a measurement after a layout
// change: a resize or a new bubble makes a log that was at the bottom measure as scrolled up.
let following = true;
let lastScrollTop = 0;

const log = () => $('log');
const atBottom = () => log().scrollHeight - log().scrollTop - log().clientHeight < 40;

// A scroll event can come after a layout change that it did not cause, so only a move up, which the player makes,
// stops following. Reaching the bottom follows again.
function trackScroll() {
  const top = log().scrollTop;
  if (atBottom()) following = true;
  else if (top < lastScrollTop) following = false;
  lastScrollTop = top;
  if (unread && following) scrollLogToBottom();
}

function scrollLogToBottom() {
  following = true;
  log().scrollTop = log().scrollHeight;
  unread = 0;
  renderUnread();
}

function renderUnread() {
  const button = $('unread');
  // The button hides once the player reaches the newest bubble, and focus stays on the log it scrolled.
  if (unread === 0 && document.activeElement === button) log().focus();
  button.hidden = unread === 0;
  setText($('unread-count'), String(unread));
  button.setAttribute('aria-label', t('action.latest', {count: unread}));
}

// Mutations keep the newest bubble in view while the player follows the log.
function changeLog(change, counts) {
  change();
  const list = $('log-list');
  while (list.children.length > LOG_LIMIT) {
    const oldest = list.firstElementChild;
    if (oldest.phraseId) phrases.delete(oldest.phraseId);
    oldest.remove();
  }
  // The typing dots follow the list, so they join a run of world bubbles as the next bubble would.
  $('dots-world').classList.toggle('first', !isWorldPhrase(list.lastElementChild));
  // The ghosts and the empty log's rings leave with the first bubble.
  if ($('ghosts').hidden !== phrases.size > 0) {
    $('ghosts').hidden = phrases.size > 0;
    renderSwitchBar();
  }
  if (following) scrollLogToBottom();
  else if (counts) {
    unread++;
    renderUnread();
  }
}

// A world bubble after another world bubble continues its run, so it drops the avatar.
const isWorldPhrase = node => !!node?.phraseId && node.classList.contains('world');

function addPhrase(e, original) {
  const flow = e.flow, self = flow === 'self';
  const phrase = {id: e.phraseId, flow, source: e.sourceLang, target: e.targetLang, original, translation: null, status: '', error: null, time: e.time};
  phrase.originalLine = h('p', {className: 'line', lang: langAttribute(phrase.source)}, original);
  phrase.translationLine = h('p', {className: 'line', lang: langAttribute(phrase.target)});
  phrase.deliveryIcon = icon('clock');
  phrase.deliveryState = h('span', {'aria-hidden': 'true'});
  phrase.deliveryText = h('span', {className: 'visually-hidden'});
  phrase.delivery = h('span', {className: 'delivery', hidden: true}, phrase.deliveryIcon, phrase.deliveryState, phrase.deliveryText);
  phrase.errorLine = h('p', {className: 'send-error', hidden: true});
  const lines = self ? [phrase.originalLine, phrase.translationLine] : [phrase.translationLine, phrase.originalLine];
  lines[0].classList.add('primary');
  lines[1].classList.add('secondary');
  const time = h('time', {dateTime: e.time});
  // The bubble shows no language: its two lines show them, and the chip shows the current pair.
  const meta = h('span', {className: 'meta'}, time, self && phrase.delivery);
  const first = !self && !isWorldPhrase($('log-list').lastElementChild);
  const node = h('li', {className: `msg ${flow}${first ? ' first' : ''}`, 'data-relabel': ''},
    !self && h('span', {className: 'avatar', 'aria-hidden': 'true'}, icon('users')),
    h('div', {className: 'bubble'}, lines), meta, phrase.errorLine);
  node.phraseId = phrase.id;
  node.relabel = () => {
    setText(time, clock(phrase.time));
    renderPhrase(phrase);
  };
  phrase.node = node;
  phrases.set(phrase.id, phrase);
  $('log-list').append(node);
  node.relabel();
  return phrase;
}

function dots() {
  return h('span', {className: 'dots', role: 'img', 'aria-label': t('state.translating')}, h('i'), h('i'), h('i'));
}

function renderPhrase(phrase) {
  const line = phrase.translationLine;
  if (phrase.translation !== null) setText(line, phrase.translation);
  else if (phrase.unfinished) line.replaceChildren(h('span', {role: 'img', 'aria-label': t('state.unfinished'), className: 'unfinished'}, icon('circle-slash')));
  else line.replaceChildren(dots());
  if (phrase.flow !== 'self') return;
  const status = phrase.status;
  phrase.delivery.hidden = !status;
  if (status) {
    // The sent mark is the Chatbox sub-screen's icon, so the bubble shows where its text went. The outcome of a
    // send also shows as a word, like LINE's 既読.
    setIcon(phrase.deliveryIcon, {sending: 'clock', sent: 'message-square-text', failed: 'circle-alert', unfinished: 'circle-slash'}[status]);
    phrase.delivery.dataset.status = status;
    setText(phrase.deliveryState, {sent: t('state.sent'), failed: t('state.notSent')}[status] || '');
    setText(phrase.deliveryText, t({sending: 'state.sending', sent: 'state.sentToChatbox', failed: 'state.notSent', unfinished: 'state.unfinished'}[status]));
  }
  // A template error skips one phrase and keeps the flow on, so only the bubble can show it. Any other send error
  // stops the flow, and the error line under the switch card shows it once.
  const error = phrase.error?.code === 'template' ? errorText(phrase.error.code, phrase.error.text) : '';
  setText(phrase.errorLine, error);
  phrase.errorLine.hidden = !error;
}

// A pill holds only the state. The error line under the switch card holds the error and Try again.
function addPill(e) {
  const flow = e.flow;
  const kind = e.kind === 'error' ? 'error' : e.text === 'running' ? 'on' : 'off';
  const text = h('span'), time = h('time', {dateTime: e.time});
  const node = h('li', {className: `pill ${kind}`, 'data-flow': flow, 'data-relabel': ''},
    h('span', {className: 'pill-body'}, h('span', {role: 'img', 'aria-label': flowLabel(flow)}, icon(ICON[flow])),
      kind === 'error' && icon('circle-alert'), text, time));
  node.relabel = () => {
    node.querySelector('[role=img]').setAttribute('aria-label', flowLabel(flow));
    setText(text, t({error: 'state.stoppedByError', on: 'state.on', off: 'state.off'}[kind]));
    setText(time, clock(e.time));
  };
  node.relabel();
  // Once the flow is on again, its earlier errors no longer need attention.
  if (kind === 'on') {
    for (const pill of $('log-list').querySelectorAll(`.pill.error[data-flow="${flow}"]`)) pill.classList.add('resolved');
  }
  $('log-list').append(node);
  const announcement = t('value.announce', {flow: flowLabel(flow), text: text.textContent});
  return kind === 'error' ? `${announcement} ${errorText(e.code, e.text)}` : announcement;
}

function closePending(flow) {
  for (const phrase of phrases.values()) {
    if (flow && phrase.flow !== flow) continue;
    if (phrase.translation === null) phrase.unfinished = true;
    else if (phrase.status === 'sending') phrase.status = 'unfinished';
    else continue;
    renderPhrase(phrase);
  }
  for (const name of flow ? [flow] : FLOWS) setSpeech(name, false, 0);
}

function setSpeech(flow, open, closesAt) {
  const s = speech[flow];
  clearTimeout(s.timer);
  s.open = open;
  const node = $('dots-' + flow);
  const hide = () => changeLog(() => { node.hidden = true; }, false);
  if (open) changeLog(() => { node.hidden = false; }, false);
  else if (closesAt > Date.now()) s.timer = setTimeout(hide, closesAt - Date.now());
  else hide();
}

function logEvent(e) {
  const flow = e.flow;
  if (!flow) return;
  const live = Date.parse(e.time) >= liveSince;
  if (e.kind === 'speech') {
    setSpeech(flow, e.text === 'start', Date.parse(e.time) + 2000);
    return;
  }
  changeLog(() => {
    let phrase = e.phraseId ? phrases.get(e.phraseId) : null;
    if (e.kind === 'recognized') {
      addPhrase(e, e.text);
      // The dots stand for the phrase that the recognizer just finished, unless the next one already started.
      if (!speech[flow].open) setSpeech(flow, false, 0);
    } else if (e.kind === 'translated') {
      phrase ||= addPhrase(e, e.original);
      phrase.translation = e.text;
      phrase.unfinished = false;
      if (flow === 'self') {
        phrase.status = 'sending';
        if (screen === 'settings' && sub === 'chatbox') schedulePreview();
      }
      renderPhrase(phrase);
      if (live) announce(t('value.announce', {flow: flowLabel(flow), text: e.text}));
    } else if (e.kind === 'sent' && phrase) {
      phrase.status = 'sent';
      renderPhrase(phrase);
    } else if (e.kind === 'error' && e.phraseId) {
      if (!phrase) return;
      phrase.status = 'failed';
      phrase.error = {code: e.code, text: e.text};
      renderPhrase(phrase);
    } else if (e.kind === 'error' || (e.kind === 'state' && e.text !== 'starting')) {
      if (e.text !== 'running') closePending(flow);
      const text = addPill(e);
      if (live) announce(text);
    }
  }, e.kind === 'recognized');
}

function latestSelfPhrase() {
  return [...phrases.values()].reverse().find(phrase => phrase.flow === 'self' && phrase.translation !== null);
}

// Model cards. The gain icon says what a translator trades: accuracy or speed.

function modelCard(name, value = '', gainIcon = '') {
  const headId = uid('model'), detailId = uid('model');
  const card = {
    input: h('input', {type: 'radio', name, value, 'aria-labelledby': headId, 'aria-describedby': detailId}),
    name: h('span', {className: 'option-name'}),
    tag: h('span', {className: 'tag', hidden: true}),
    stateIcon: icon('check'),
    stateText: h('span'),
    memory: h('span', {className: 'value'}),
    gain: h('span'),
  };
  card.state = h('span', {className: 'detail'}, card.stateIcon, card.stateText);
  card.root = h('label', {className: 'option'}, card.input,
    h('span', {className: 'option-body'},
      h('span', {className: 'option-head', id: headId}, card.name, card.tag),
      h('span', {className: 'option-detail', id: detailId}, card.state,
        h('span', {className: 'detail'}, icon('memory-stick'), card.memory),
        gainIcon && h('span', {className: 'detail'}, icon(gainIcon), card.gain))));
  return card;
}

function updateModelCard(card, id) {
  const value = state.artifacts[id], artifact = value.artifact, installed = value.state === 'installed';
  setText(card.name, artifact.Name);
  card.state.classList.toggle('installed', installed);
  setIcon(card.stateIcon, installed ? 'check' : 'download');
  // The size alone does not say that the model is missing, so the download icon says it to screen readers.
  if (installed) card.stateIcon.setAttribute('aria-hidden', 'true');
  else card.stateIcon.removeAttribute('aria-hidden');
  card.stateIcon.setAttribute('role', 'img');
  card.stateIcon.setAttribute('aria-label', t('state.missing'));
  setText(card.stateText, installed ? t('state.installed') : formatSize(artifactSize(artifact)));
  setText(card.memory, t('value.memory', {size: formatSize(artifact.MemoryBytes)}));
}

// Settings: the translator, which both flows share

// The last failed save, until a save succeeds.
let translatorRefusal = null;
let translatorCards = [];

function renderTranslator() {
  const group = $('translators'), translators = state.languages.translators, saved = state.settings.Translator;
  const signature = JSON.stringify(translators.map(translator => translator.id));
  if (group.dataset.signature !== signature) {
    translatorCards = translators.map(translator => modelCard('translator', translator.id, translator.speed > 1 ? 'gauge' : 'target'));
    group.replaceChildren(...translatorCards.map(card => card.root));
    group.dataset.signature = signature;
  }
  for (const [i, translator] of translators.entries()) {
    const card = translatorCards[i];
    updateModelCard(card, translator.artifact);
    card.tag.hidden = !translator.default;
    setText(card.tag, t('state.default'));
    // The catalog gives each model's speed relative to the most accurate one.
    setText(card.gain, translator.speed > 1 ? t('value.faster', {n: Math.floor(translator.speed)}) : t('value.moreAccurate'));
    if (!group.dataset.dirty) card.input.checked = translator.id === saved;
  }
  const error = $('translator-error'), refusal = translatorRefusal;
  setError(error, refusal && (() => saveRefusal(refusal)));
  group.setAttribute('aria-describedby', ['translator-hint', !error.hidden && error.id].filter(Boolean).join(' '));
}

// Settings: Microphone

// The last failed save, until a save succeeds.
let microphoneRefusal = null;

function renderMicrophone() {
  const select = $('microphone'), saved = settingsOf('self').Microphone;
  const value = select.dataset.dirty ? select.value : saved;
  const ids = (devices || []).map(device => device.ID);
  const choices = [['', t('value.systemDefault')], ...(devices || []).map(device => [device.ID, device.Name || device.ID])];
  if (value && !ids.includes(value)) choices.push([value, value]);
  if (saved && saved !== value && !ids.includes(saved)) choices.push([saved, saved]);
  fillSelect(select, JSON.stringify([lang, choices]), () => choices.map(([id, name]) => new Option(name, id)), value);
  const status = $('microphone-state'), error = $('microphone-error'), refusal = microphoneRefusal;
  status.hidden = !devices || !saved || ids.includes(saved);
  setError(error, devicesError ? refreshHint : refusal && (() => saveRefusal(refusal)));
  // A description that names a hidden node still reads its text.
  select.setAttribute('aria-describedby', [!status.hidden && status.id, !error.hidden && error.id].filter(Boolean).join(' '));
}

// The hint shows the refresh button's icon where it names the button.
function refreshHint() {
  const [before, after] = t('error.microphones').split('{refresh}');
  return [before, h('span', {className: 'inline-icon', role: 'img', 'aria-label': t('action.refresh')}, icon('refresh-cw')), after];
}

async function listMicrophones() {
  try {
    devices = await (await api('microphones')).json() || [];
    devicesError = false;
  } catch (e) {
    if (!e.status) return;
    devicesError = true;
  }
  render();
}

// The mark at the end of the field's label stays 1.5 s, then fades out.
function flashSaved(id) {
  const mark = $(id + '-saved');
  clearTimeout(mark.timer);
  mark.classList.remove('fade');
  mark.hidden = false;
  mark.timer = setTimeout(() => {
    mark.classList.add('fade');
    mark.timer = setTimeout(() => { mark.hidden = true; }, 400);
  }, 1500);
  announce(t('state.saved'));
}

// Settings: Chatbox

function setFieldError(input, errorNode, build) {
  if (build) input.setAttribute('aria-invalid', 'true');
  else input.removeAttribute('aria-invalid');
  setError(errorNode, build);
}

// A text field saves on blur, on Enter and when the page leaves it. Its dirty mark turns "saving" for the save,
// so a second trigger meanwhile sends nothing. The mark stays when the save fails or the player edited the text.
function settleField(input, text, saved) {
  if (input.dataset.dirty !== 'saving' || input.value !== text) return;
  if (saved) delete input.dataset.dirty;
  else input.dataset.dirty = 'changed';
}

async function saveAddress() {
  const input = $('address'), text = input.value, value = text.trim();
  if (input.dataset.dirty === 'saving') return;
  if (value === state.settings.Chatbox.Address) {
    delete input.dataset.dirty;
    setFieldError(input, $('address-error'));
    return;
  }
  input.dataset.dirty = 'saving';
  try {
    await saveSettings(s => { s.Chatbox.Address = value; });
    settleField(input, text, true);
    setFieldError(input, $('address-error'));
    flashSaved('address');
  } catch (e) {
    settleField(input, text, false);
    setFieldError(input, $('address-error'), () => e.status === 400 ? t('error.address') : saveRefusal(e));
  }
}

let previewTimer = 0;
let previewRequest = 0;
let preview = {template: null, pages: null, error: null};
// The last failed save, until a save succeeds. A preview error shows first.
let templateRefusal = null;

function previewMessage() {
  const phrase = latestSelfPhrase(), settings = settingsOf('self');
  if (phrase) return {original: phrase.original, translation: phrase.translation, sourceLang: phrase.source, targetLang: phrase.target};
  return {original: t('value.sampleOriginal'), translation: t('value.sampleTranslation'), sourceLang: settings.Spoken, targetLang: settings.Target};
}

async function runPreview() {
  clearTimeout(previewTimer);
  const template = $('template').value, request = ++previewRequest;
  let result;
  try {
    const response = await api('chatbox/preview', {method: 'POST', body: JSON.stringify({template, ...previewMessage()})});
    result = {template, pages: (await response.json()).pages || [], error: null};
  } catch (e) {
    if (!e.status) return preview;
    result = {template, pages: null, error: e.message};
  }
  if (request === previewRequest) {
    preview = result;
    renderPreview();
  }
  return result;
}

function schedulePreview() {
  clearTimeout(previewTimer);
  previewTimer = setTimeout(runPreview, 300);
}

function renderPreview() {
  const input = $('template'), refusal = templateRefusal;
  // The server checks a saved template against its own sample, so it can refuse one that the preview accepts.
  const detail = preview.error !== null && preview.template === input.value ? preview.error
    : refusal?.code === 'template' ? refusal.message : null;
  if (detail !== null) {
    setFieldError(input, $('template-error'), () => [h('span', {}, t('error.templateInvalid')), h('code', {className: 'mono'}, detail)]);
  } else {
    setFieldError(input, $('template-error'), refusal && (() => saveRefusal(refusal)));
  }
  const pages = preview.pages || [];
  const box = $('preview');
  if (preview.pages) {
    box.replaceChildren(...pages.map((page, i) => h('div', {className: 'page'},
      pages.length > 1 && h('span', {className: 'page-count'}, t('value.page', {n: i + 1, total: pages.length})),
      h('p', {}, page))));
  }
  box.hidden = !preview.pages;
}

async function saveTemplate() {
  const input = $('template'), value = input.value;
  if (input.dataset.dirty === 'saving') return;
  if (value === state.settings.Chatbox.Template) {
    delete input.dataset.dirty;
    templateRefusal = null;
    renderPreview();
    return;
  }
  input.dataset.dirty = 'saving';
  const result = preview.template === value ? preview : await runPreview();
  if (result.error !== null || result.template !== input.value) {
    settleField(input, value, false);
    return;
  }
  try {
    await saveSettings(s => { s.Chatbox.Template = value; });
    settleField(input, value, true);
    templateRefusal = null;
    flashSaved('template');
  } catch (e) {
    settleField(input, value, false);
    templateRefusal = e;
  }
  renderPreview();
}

// The browser's own text insertion keeps the edit on the undo stack. A hidden textarea cannot take focus,
// so it shows for the insertion, and the next render hides it again.
// The selection saved at blur applies only when the textarea lost focus; a token chip keeps it focused.
function insertText(input, text, selectAll) {
  const focused = document.activeElement === input;
  $('custom').hidden = false;
  input.focus();
  if (selectAll) input.select();
  else if (!focused && input.savedSelection) input.setSelectionRange(...input.savedSelection);
  if (!document.execCommand('insertText', false, text)) {
    input.setRangeText(text, input.selectionStart, input.selectionEnd, 'end');
    input.dispatchEvent(new Event('input'));
  }
}

// The player's choice of Custom holds until a preset is chosen, even when the template equals a preset.
let customChosen = false;

function buildChatbox() {
  const input = $('template');
  const presets = PRESETS.map(([key, template]) => {
    const button = h('button', {type: 'button', className: 'segment', 'data-i18n': key});
    // Focus returns to the preset, so the template saves on the textarea's blur.
    button.addEventListener('click', () => {
      insertText(input, template(), true);
      customChosen = false;
      button.focus();
      renderPresets();
    });
    return button;
  });
  const custom = h('button', {type: 'button', className: 'segment', 'data-i18n': 'value.presetCustom'});
  custom.addEventListener('click', () => {
    customChosen = true;
    renderPresets();
  });
  $('presets').replaceChildren(...presets, custom);
  $('tokens').replaceChildren(...TOKENS.map(token => {
    const button = h('button', {type: 'button', className: 'chip mono'}, token);
    button.addEventListener('mousedown', event => event.preventDefault());
    button.addEventListener('click', () => insertText(input, token, false));
    button.dataset.relabel = '';
    button.relabel = () => button.setAttribute('aria-label', t('action.insertToken', {token}));
    return button;
  }));
  input.addEventListener('input', () => {
    input.dataset.dirty = 'changed';
    customChosen = true;
    renderPresets();
    schedulePreview();
  });
  input.addEventListener('blur', () => {
    input.savedSelection = [input.selectionStart, input.selectionEnd];
    saveTemplate();
  });
  const address = $('address');
  address.addEventListener('input', () => { address.dataset.dirty = 'changed'; });
  address.addEventListener('blur', () => saveAddress());
  address.addEventListener('keydown', event => { if (event.key === 'Enter') saveAddress(); });
}

// A preset is marked when it equals the template, and Custom when none does or the player chose Custom.
// The insert chips and the textarea show only under Custom.
function renderPresets() {
  const input = $('template');
  const value = input.dataset.dirty ? input.value : state.settings.Chatbox.Template;
  const match = customChosen ? -1 : PRESETS.findIndex(([, template]) => template() === value);
  const selected = match < 0 ? PRESETS.length : match;
  for (const [i, button] of [...$('presets').children].entries()) button.setAttribute('aria-pressed', String(i === selected));
  $('custom').hidden = match >= 0;
}

function syncField(input, value, onChange) {
  // A field belongs to the player while it has focus or unsaved text.
  if (document.activeElement === input || input.dataset.dirty || input.value === value) return;
  input.value = value;
  onChange?.();
}

function renderChatbox() {
  syncField($('address'), state.settings.Chatbox.Address);
  syncField($('template'), state.settings.Chatbox.Template, () => {
    customChosen = false;
    preview.template = null;
    runPreview();
  });
  renderPresets();
}

// Settings: Downloads. The groups split the catalog: what the current selection needs, what else is installed, and
// the rest. A row that changes group keeps its element, so it can keep focus (see render).

const downloads = {cache: new Map(), head: usedByHead()};

function renderDownloads() {
  const ids = byName(Object.keys(state.artifacts));
  const needed = new Set(FLOWS.flatMap(required));
  const installed = id => state.artifacts[id].state === 'installed';
  const groups = {
    needed: ids.filter(id => needed.has(id)),
    unneeded: ids.filter(id => !needed.has(id) && installed(id)),
    missing: ids.filter(id => !needed.has(id) && !installed(id)),
  };
  setText($('installed-size'), formatSize(totalSize(ids.filter(installed))));
  setText(downloads.head.firstChild, t('label.usedBy'));
  for (const [name, group] of Object.entries(groups)) {
    const section = $('downloads-' + name);
    section.hidden = !group.length;
    renderList(section.querySelector('ul'), downloads.cache, group, 'settings', name === 'needed' ? downloads.head : null);
  }
}

// Delete dialog

function openDelete(id, opener) {
  const dialog = $('delete-dialog'), artifact = state.artifacts[id].artifact;
  const users = FLOWS.filter(flow => required(flow).includes(id));
  dialog.relabel = () => {
    setText($('delete-title'), t('title.delete', {name: artifact.Name}));
    setText($('delete-size'), formatSize(artifactSize(artifact)));
    $('delete-flows').replaceChildren(...users.map(flow => h('span', {className: 'blocked-flow', role: 'img', 'aria-label': t('state.blockedBy', {flow: flowLabel(flow)})},
      icon(ICON[flow]), icon('circle-slash', 'slash'))));
  };
  dialog.setAttribute('data-relabel', '');
  dialog.relabel();
  $('delete-flows').hidden = users.length === 0;
  setError($('delete-error'), null);
  dialog.target = id;
  dialog.opener = opener;
  dialog.showModal();
  dialog.querySelector('[data-close]').focus();
}

async function confirmDelete() {
  const dialog = $('delete-dialog'), button = $('delete-confirm');
  if (isDisabled(button)) return;
  setDisabled(button, true);
  try {
    apply(await (await api('downloads/' + dialog.target, {method: 'DELETE'})).json());
    // A selection change on another page can take the row out of Languages and models' list, so focus goes to the
    // list's summary.
    if (!dialog.opener.isConnected) dialog.opener = plan.details.querySelector('summary');
    dialog.close();
  } catch (e) {
    setError($('delete-error'), () => e.status === 409 ? t('error.inUse') : errorText(e.code, e.message));
  } finally {
    setDisabled(button, false);
  }
}

// Settings: About

// A license's Credit is the text that the license requires on a user interface.
function licenseRow(artifact) {
  return h('li', {}, h('span', {className: 'name'}, artifact.Name), h('span', {className: 'links'}, artifact.Licenses.map(license =>
    h('span', {className: 'license'},
      h('a', {href: license.URL, target: '_blank', rel: 'noreferrer'}, license.Name, icon('arrow-up-right', 'small')),
      license.Credit && h('span', {className: 'credit'}, license.Credit)))));
}

// A build without commit information shows only its release version, or "Development build".
function versionLine(app) {
  return [
    h('span', {}, app.version || t('value.developmentBuild')),
    app.revision && h('span', {className: 'mono'}, app.revision.slice(0, 7)),
    app.time && h('time', {dateTime: app.time}, new Intl.DateTimeFormat(lang, {dateStyle: 'medium'}).format(new Date(app.time))),
    app.modified && h('span', {}, t('state.modified')),
  ].filter(Boolean);
}

const UPDATE_LINE = {
  checking: ['loader-circle', 'spin', 'state.checkingUpdates'],
  current: ['check', '', 'state.upToDate'],
  failed: ['circle-alert', 'muted', 'state.updateFailed'],
};

function updateLine(update) {
  if (update.state === 'available') {
    return [icon('circle-arrow-up', 'link'), h('a', {href: update.url, target: '_blank', rel: 'noreferrer'},
      t('state.versionAvailable', {version: update.version}), icon('arrow-up-right', 'small'))];
  }
  const [name, className, key] = UPDATE_LINE[update.state];
  return [icon(name, className), h('span', {}, t(key))];
}

// A node rebuilds only when its inputs change, so the update line, a live region, announces only a change.
function rebuild(node, inputs, build) {
  const signature = JSON.stringify([lang, inputs]);
  if (node.dataset.signature === signature) return;
  node.dataset.signature = signature;
  node.replaceChildren(...build());
}

// /api/state carries the downloads as a map without the catalog's order, so About sorts them as the downloads list
// does.
function renderAbout() {
  for (const link of $('sub-about').querySelectorAll('[data-path]')) link.href = state.app.repository + link.dataset.path;
  rebuild($('app-version'), state.app, () => versionLine(state.app));
  rebuild($('app-update'), state.update, () => updateLine(state.update));
  const ids = byName(Object.keys(state.artifacts));
  rebuild($('licenses'), ids, () => ids.map(id => licenseRow(state.artifacts[id].artifact)));
}

// Open buttons and settings.yaml recovery

// showRefusal receives the refusal, or null after a successful open.
async function openTarget(target, button, showRefusal) {
  if (isDisabled(button)) return;
  setDisabled(button, true);
  try {
    await api('open/' + target, {method: 'POST'});
    showRefusal(null);
  } catch (e) {
    if (e.status) showRefusal(e);
  } finally {
    setDisabled(button, false);
  }
}

async function replaceSettings() {
  const button = $('replace-confirm');
  if (isDisabled(button)) return;
  setDisabled(button, true);
  try {
    // The app holds the defaults after a failed load and the last valid settings after a failed reload.
    await saveSettings(s => { if (unsavedLanguage !== null) s.UI.Language = unsavedLanguage; }, {overwrite: true});
    setError($('replace-error'), null);
    // The settings.yaml screen hides, so focus goes to the screen that appears.
    $('replace-dialog').opener = screen === 'setup' ? $('screen-setup').querySelector('select') : document.querySelector('.tab[aria-current="page"]');
  } catch (e) {
    setError($('replace-error'), () => e.status ? errorText(e.code, e.message) : t('error.request'));
  } finally {
    setDisabled(button, false);
    $('replace-dialog').close();
  }
}

// Screens and history

function flushFields() {
  for (const field of document.querySelectorAll('select, [role="radiogroup"]')) field.settle?.();
  if ($('address').dataset.dirty) saveAddress();
  if ($('template').dataset.dirty) saveTemplate();
}

// A screen change adds no history entry, so Exit can close the tab (see showExited).
function show(nextScreen, nextSub = sub) {
  if (nextScreen === screen && nextSub === sub) return;
  flushFields();
  screen = nextScreen;
  sub = nextSub;
  render();
  if (screen === 'translation') scrollLogToBottom();
  if (screen === 'settings') revealRailItem();
  // The preview shows the latest My voice phrase.
  if (screen === 'settings' && sub === 'chatbox') runPreview();
}

// A tab or rail item that carries a mark adds the mark's state to its accessible name.
function markName(node, label, mark) {
  if (mark) node.setAttribute('aria-label', t('state.tabState', {tab: t(label), state: t(mark)}));
  else node.removeAttribute('aria-label');
}

// Below 720 px, the rail is a row that scrolls sideways. Only the row scrolls, so the page keeps its position.
function revealRailItem() {
  const rail = document.querySelector('.rail'), item = rail.querySelector('[aria-current="page"]');
  const box = rail.getBoundingClientRect(), at = item.getBoundingClientRect(), pad = parseFloat(getComputedStyle(rail).paddingLeft);
  if (at.left < box.left + pad) rail.scrollLeft -= box.left + pad - at.left;
  else if (at.right > box.right - pad) rail.scrollLeft += at.right - box.right + pad;
}

function renderFrame() {
  const settingsError = !!state.settingsError;
  $('tabs').hidden = screen === 'setup' || settingsError;
  for (const tab of document.querySelectorAll('.tab')) {
    if (tab.dataset.screen === screen) tab.setAttribute('aria-current', 'page');
    else tab.removeAttribute('aria-current');
  }
  // A flow error shows on every screen through the Translation tab.
  const failed = FLOWS.some(flow => state.flows[flow]?.state === 'error');
  const tab = document.querySelector('.tab[data-screen="translation"]');
  setIcon(tab.querySelector('.icon'), failed ? 'circle-alert' : 'captions');
  tab.classList.toggle('failed', failed);
  markName(tab, 'label.translation', failed && 'state.stoppedByError');
  // An available update shows on every screen through the Settings tab, and in Settings through About.
  const update = state.update.state === 'available';
  for (const dot of document.querySelectorAll('.update-dot')) {
    const item = dot.parentElement;
    dot.hidden = !update;
    markName(item, item.querySelector('[data-i18n]').dataset.i18n, update && 'state.updateAvailable');
  }
  for (const item of document.querySelectorAll('.rail-item[data-sub]')) {
    if (item.dataset.sub === sub) item.setAttribute('aria-current', 'page');
    else item.removeAttribute('aria-current');
  }
  $('screen-setup').hidden = screen !== 'setup' || settingsError;
  $('screen-translation').hidden = screen !== 'translation' || settingsError;
  $('screen-settings').hidden = screen !== 'settings' || settingsError;
  for (const name of ['models', 'downloads', 'microphone', 'chatbox', 'about']) $('sub-' + name).hidden = sub !== name;
  $('screen-settings-error').hidden = !settingsError;
  setText($('settings-error-text'), state.settingsError || '');
  $('switchbar').hidden = !(settingsError || screen === 'translation');
  document.body.dataset.screen = settingsError ? 'settings-error' : screen;
}

function render() {
  if (!state || exitState === 'exited') return;
  const focused = document.activeElement;
  renderFrame();
  renderPlan();
  renderSwitchBar();
  renderTranslator();
  renderMicrophone();
  renderChatbox();
  renderDownloads();
  renderAbout();
  renderMenu();
  // A row that moves to another list leaves the document for a moment, which drops its focus. Focus goes back to the
  // same control, or to its row when the move also took the control away.
  if (focused !== document.body && document.activeElement === document.body && focused.isConnected) {
    focused.focus({preventScroll: true});
    if (document.activeElement === document.body) focused.closest('.download')?.focus({preventScroll: true});
  }
}

function apply(next) {
  // A later launch can reuse this page's port. Its event IDs restart at 1, so this page would skip them as seen.
  if (state && next.launch !== state.launch) {
    location.reload();
    return;
  }
  const first = !state;
  state = next;
  for (const [id, value] of Object.entries(next.artifacts)) {
    if (value.state === 'downloading') batch.add(id);
    if (value.state !== 'missing' || value.error) downloadErrors.delete(id);
  }
  if (!pending.size && !Object.values(next.artifacts).some(value => value.state === 'downloading')) batch.clear();
  FLOWS.forEach(trackReady);
  if (!next.settingsError) unsavedLanguage = null;
  const nextLang = resolveLanguage(languageSetting());
  if (nextLang !== lang || first) setLanguage(nextLang);
  if (first) screen = FLOWS.some(ready) ? 'translation' : 'setup';
  render();
  if (first && screen === 'translation') scrollLogToBottom();
}

// UI language menu

function renderMenu() {
  const saved = languageSetting();
  for (const item of $('ui-language-menu').children) item.setAttribute('aria-checked', String(item.dataset.value === saved));
}

// A failed save switches the page back to the saved language at once.
async function saveLanguage(value) {
  pendingLanguage = value;
  let refusal = null;
  try {
    await saveSettings(s => { s.UI.Language = value; });
  } catch (e) {
    refusal = e;
  }
  // A later choice owns the page.
  if (pendingLanguage !== value) return;
  pendingLanguage = null;
  const next = resolveLanguage(languageSetting());
  if (next !== lang) setLanguage(next);
  setError($('ui-language-error'), refusal && (() => saveRefusal(refusal)));
  if (refusal) announce($('ui-language-error').textContent);
  render();
}

function closeMenu(focusButton) {
  $('ui-language-menu').hidden = true;
  $('ui-language').setAttribute('aria-expanded', 'false');
  if (focusButton) $('ui-language').focus();
}

function buildMenu() {
  const button = $('ui-language'), menu = $('ui-language-menu'), items = [...menu.children];
  button.addEventListener('click', () => {
    if (!menu.hidden) return closeMenu(false);
    setError($('ui-language-error'), null);
    renderMenu();
    menu.hidden = false;
    button.setAttribute('aria-expanded', 'true');
    (items.find(item => item.getAttribute('aria-checked') === 'true') || items[0]).focus();
  });
  menu.addEventListener('keydown', event => {
    const i = items.indexOf(document.activeElement);
    const move = {ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: items.length - 1}[event.key];
    if (move !== undefined) {
      event.preventDefault();
      items[(move + items.length) % items.length].focus();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      closeMenu(true);
    } else if (event.key === 'Tab') {
      closeMenu(false);
    }
  });
  for (const item of items) {
    item.addEventListener('click', () => {
      const value = item.dataset.value, changed = value !== languageSetting();
      closeMenu(true);
      setLanguage(resolveLanguage(value));
      if (state?.settingsError) unsavedLanguage = value;
      else if (state && changed) saveLanguage(value);
      render();
    });
  }
  document.addEventListener('click', event => {
    if (event.target.closest('.menu')) return;
    if (!menu.hidden) closeMenu(false);
    setError($('ui-language-error'), null);
  });
}

// Connection

function showConnection() {
  // After the player presses Exit, the connection drops as the server stops. A refused exit shows the bar again.
  if (exitState) return;
  lockControls();
  const bar = $('connection');
  bar.hidden = connection === 'ok';
  bar.dataset.kind = connection;
  setText($('connection-text'), {reconnecting: t('state.reconnecting'), down: t('error.notAnswering')}[connection] || '');
  $('connection-text').setAttribute('role', connection === 'reconnecting' ? 'status' : 'alert');
}

function setConnection(kind) {
  if (connection === kind) return;
  connection = kind;
  showConnection();
  if (state) render();
}

async function stream() {
  let failures = 0, first = true;
  for (;;) {
    try {
      const response = await api('events');
      failures = 0;
      // History from before this moment loads silently.
      liveSince = Date.now() - 500;
      setConnection('ok');
      await refresh();
      if (first) listMicrophones();
      first = false;
      const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
      let buffered = '';
      for (;;) {
        const {done, value} = await reader.read();
        if (done) throw new RequestError(0, 'closed');
        buffered += value;
        let newline;
        while ((newline = buffered.indexOf('\n')) >= 0) {
          const line = buffered.slice(0, newline);
          buffered = buffered.slice(newline + 1);
          if (!line) continue;
          const event = JSON.parse(line);
          if (event.id <= lastEvent) continue;
          // IDs count up by one, so a jump means the history no longer held what this page missed.
          if (lastEvent && event.id > lastEvent + 1) closePending('');
          lastEvent = event.id;
          if (['state', 'error', 'download', 'settings', 'update'].includes(event.kind)) scheduleRefresh();
          logEvent(event);
        }
      }
    } catch (e) {
      if (exitState === 'exited') return;
      failures++;
      setConnection(failures > 2 ? 'down' : 'reconnecting');
      await sleep(2000);
    }
  }
}

// Exit

// '' until the player presses Exit, then 'waiting' until the server stops, then 'exited'.
let exitState = '';

// Once the connection bar reports that the server is not answering, its error disables the controls below it, and Exit too.
// A pending exit disables every control but Exit.
function lockControls() {
  const blocked = connection === 'down', waiting = exitState === 'waiting';
  $('main').inert = blocked || waiting;
  $('tabs').inert = waiting;
  document.querySelector('.menu').inert = waiting;
  setDisabled($('exit'), blocked);
  const mark = $('exit').querySelector('.icon');
  setIcon(mark, waiting ? 'loader-circle' : 'power');
  mark.classList.toggle('spin', waiting);
}

async function exitApp() {
  if (exitState || isDisabled($('exit'))) return;
  exitState = 'waiting';
  setError($('exit-error'), null);
  lockControls();
  try {
    await api('exit', {method: 'POST'});
  } catch (e) {
    exitState = '';
    showConnection();
    if (e.status) {
      setError($('exit-error'), () => errorText(e.code, e.message));
      announce($('exit-error').textContent);
    }
    return;
  }
  // The server answers before it shuts down, and has stopped once a request gets no answer.
  // Cleanup can outlast any fixed limit, such as a model that is still loading, so the page waits for the server.
  for (;;) {
    try {
      await fetch('/', {method: 'HEAD', cache: 'no-store', signal: AbortSignal.timeout(1000)});
    } catch {
      break;
    }
    await sleep(250);
  }
  showExited();
}

function showExited() {
  exitState = 'exited';
  for (const node of [$('tabs'), $('connection'), $('main'), ...document.querySelectorAll('.bar-item')]) node.hidden = true;
  $('screen-exited').hidden = false;
  $('exited-title').focus();
  // A script can close a tab only while the tab's history holds one entry. The page adds none, but a tab that showed
  // another page before the app's URL holds more, and the browser keeps it open.
  setTimeout(() => {
    window.close();
    setTimeout(() => setText($('exited-close'), t('action.closeTab')), 500);
  }, 3000);
}

// Start

function bind() {
  buildMenu();
  buildPlan();
  buildSwitchBar();
  buildChatbox();
  for (const tab of document.querySelectorAll('.tab')) tab.addEventListener('click', () => show(tab.dataset.screen));
  for (const item of document.querySelectorAll('.rail-item[data-sub]')) item.addEventListener('click', () => show('settings', item.dataset.sub));
  for (const button of document.querySelectorAll('[data-open]')) {
    const error = $(button.dataset.error);
    button.addEventListener('click', () => openTarget(button.dataset.open, button, e => setError(error, e && (() => errorText(e.code, e.message)))));
  }
  $('exit').addEventListener('click', exitApp);
  // The error covers the screen below the top bar, so it closes as the UI-language menu's error does.
  document.addEventListener('click', event => { if (!$('exit').parentElement.contains(event.target)) setError($('exit-error'), null); });
  $('next').addEventListener('click', () => { if (!isDisabled($('next'))) show('translation'); });
  $('unread').addEventListener('click', scrollLogToBottom);
  log().addEventListener('scroll', trackScroll);
  // A resize or a relabel changes the log's height outside changeLog.
  const keepBottom = new ResizeObserver(() => { if (following) log().scrollTop = log().scrollHeight; });
  keepBottom.observe(log());
  keepBottom.observe(log().querySelector('.log-column'));
  // The rail turns into a row when the window narrows, and loses its scroll position while the settings screen hides.
  new ResizeObserver(() => { if (screen === 'settings') revealRailItem(); }).observe(document.querySelector('.rail'));
  commitOnSettle($('microphone'), value => saveSettings(s => { s.Flows.Self.Microphone = value; })
    .then(() => { microphoneRefusal = null; flashSaved('microphone'); })
    .catch(e => { microphoneRefusal = e; }));
  commitOnSettle($('translators'), value => saveSettings(s => { s.Translator = value; })
    .then(() => { translatorRefusal = null; flashSaved('translator'); })
    .catch(e => { translatorRefusal = e; }), () => $('translators').querySelector(':checked')?.value);
  $('refresh-mics').addEventListener('click', listMicrophones);
  $('delete-confirm').addEventListener('click', confirmDelete);
  $('replace-settings').addEventListener('click', () => {
    $('replace-dialog').opener = $('replace-settings');
    $('replace-dialog').showModal();
    $('replace-dialog').querySelector('[data-close]').focus();
  });
  $('replace-confirm').addEventListener('click', replaceSettings);
  for (const dialog of document.querySelectorAll('dialog')) {
    dialog.addEventListener('close', () => { if (dialog.id !== 'license-dialog') dialog.opener?.focus(); });
  }
  for (const button of document.querySelectorAll('[data-close]')) button.addEventListener('click', () => button.closest('dialog').close());
}

async function start() {
  const [en, ja, sprite] = await Promise.all(['/i18n/en.json', '/i18n/ja.json', '/icons.svg'].map(path => fetch(path).then(r => r.text())));
  strings = {en: JSON.parse(en), ja: JSON.parse(ja)};
  const icons = new DOMParser().parseFromString(sprite, 'image/svg+xml').documentElement;
  document.body.prepend(document.importNode(icons, true));
  bind();
  setLanguage(browserLanguage());
  stream();
}

start();
