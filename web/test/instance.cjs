const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');

const source = readFileSync('public/app.js', 'utf8').replace(/\nstart\(\);\s*$/, '');
const now = Date.parse('2026-11-15T12:00:00Z');
const info = (slot, changes = {}) => ({slot, pid: 4200 + slot, url: `http://127.0.0.1:${50000 + slot}/`,
  started: new Date(now - 37 * 60000).toISOString(), state: '', error: '', code: '', ...changes});
const waiting = (...others) => ({state: 'waiting', others});
const response = (body, status = 200) => ({ok: status < 400, status, json: async () => body});
const drain = () => new Promise(resolve => setImmediate(resolve));

function page() {
  const nodes = new Map(), requests = [], timers = new Map();
  const document = {};
  let nextTimer = 0, snapshot, respond;
  function element(tag = 'div') {
    const attributes = new Map();
    return {
      tagName: tag.toUpperCase(), children: [], dataset: {}, events: {}, open: false, hidden: false, root: false,
      get isConnected() { return this.parentNode ? this.parentNode.isConnected : this.root; },
      get textContent() { return this.children.map(child => typeof child === 'string' ? child : child.textContent).join(''); },
      set textContent(text) { this.replaceChildren(text); },
      append(...children) {
        for (const child of children) {
          if (typeof child !== 'string') { child.remove(); child.parentNode = this; }
          this.children.push(child);
        }
      },
      replaceChildren(...children) {
        for (const child of this.children) if (typeof child !== 'string') child.parentNode = null;
        this.children = [];
        this.append(...children);
      },
      remove() {
        if (this.parentNode) this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1);
        this.parentNode = null;
      },
      setAttribute(name, value) { attributes.set(name, String(value)); },
      getAttribute(name) { return attributes.get(name) ?? null; },
      removeAttribute(name) { attributes.delete(name); },
      addEventListener(type, fn) { this.events[type] = fn; },
      querySelector(selector) { return selector === 'summary' ? node('instance-summary') : null; },
      focus() { document.activeElement = this; },
      showModal() { this.open = true; },
      close() { this.open = false; this.events.close?.(); },
    };
  }
  function node(id) {
    if (!nodes.has(id)) nodes.set(id, Object.assign(element(), {id, root: true}));
    return nodes.get(id);
  }
  document.body = node('body');
  document.activeElement = document.body;
  document.createElement = element;
  document.createElementNS = (_namespace, tag) => element(tag);
  document.getElementById = node;
  document.querySelectorAll = () => [];
  node('screen-exited').hidden = true;
  const context = vm.createContext({
    document, AbortSignal,
    Date: class extends Date { static now() { return now; } },
    setTimeout: fn => { timers.set(++nextTimer, fn); return nextTimer; },
    clearTimeout: id => timers.delete(id),
    fetch: async (path, options = {}) => {
      requests.push({path, method: options.method || 'GET'});
      return respond ? respond(path, options) : response(snapshot);
    },
  });
  vm.runInContext(source, context);
  const ui = vm.runInContext(`
    render = renderInstances;
    setLanguage = value => { lang = value; };
    lockControls = () => {};
    syncLevelPolling = () => {};
    ({apply, render: renderInstances, build: buildInstances, stop: stopInstances,
      strings: value => { strings = value; }, exitState: () => exitState});
  `, context);
  ui.strings(Object.fromEntries(['en', 'ja'].map(lang => [lang, JSON.parse(readFileSync('public/i18n/' + lang + '.json', 'utf8'))])));
  ui.build();
  return {...ui, node, document, requests, timers,
    update(instance, locale = 'en') {
      snapshot = {launch: 'test', instance, artifacts: {}, required: {self: [], world: []},
        flows: {self: {state: 'stopped'}, world: {state: 'stopped'}},
        settings: {UI: {Language: locale}, Flows: {Self: {Enabled: false}, World: {Enabled: false}}}};
      ui.apply(snapshot);
    },
    respond: fn => { respond = fn; },
    snapshot: () => snapshot,
    rows: () => node('instance-list').children,
    async settle() { await drain(); await drain(); },
  };
}

test('the first state opens the modal with count, plain addresses and localized relative start times', () => {
  for (const locale of ['en', 'ja']) {
    const ui = page();
    const address = '<img src=x onerror=alert(1)>';
    ui.update(waiting(info(0, {url: address}), info(1)), locale);
    assert.equal(ui.node('instance-dialog').open, true);
    assert.equal(ui.node('instance-dialog').opener, null);
    assert.equal(ui.node('instance-details').open, false);
    assert.match(ui.node('instance-title').textContent, /2/);
    assert.equal(ui.document.activeElement, ui.node('instance-replace'));
    assert.equal(ui.rows()[0].pid.textContent, 'PID 4200');
    assert.equal(ui.rows()[0].url.tagName, 'P');
    assert.equal(ui.rows()[0].url.textContent, address);
    assert.equal(ui.rows()[0].started.dateTime, info(0).started);
    const ago = new Intl.RelativeTimeFormat(locale, {numeric: 'auto'}).format(-37, 'minute');
    assert.equal(ui.rows()[0].started.textContent, locale === 'en' ? `Started ${ago}` : `${ago}に起動`);
    assert.equal(ui.rows()[0].stop.getAttribute('aria-label'), locale === 'en' ? 'Stop PID 4200' : 'PID 4200を終了');
    let prevented = false;
    ui.node('instance-dialog').events.cancel({preventDefault() { prevented = true; }});
    assert.equal(prevented, true);
    for (let i = 0; i < 4; i++) {
      prevented = false;
      ui.node('instance-dialog').events.keydown({key: 'Escape', preventDefault() { prevented = true; }});
      assert.equal(prevented, true);
    }
    prevented = false;
    ui.node('instance-dialog').events.keydown({key: 'Tab', preventDefault() { prevented = true; }});
    assert.equal(prevented, false);
    assert.equal(ui.node('instance-dialog').open, true);
    const row = ui.rows()[0];
    row.stop.focus();
    ui.render();
    assert.equal(ui.rows()[0], row);
    assert.equal(ui.document.activeElement, row.stop);
    ui.update({state: ''}, locale);
    assert.equal(ui.node('instance-dialog').open, false);
    assert.equal(ui.timers.size, 0);
  }
});

test('the primary button posts every row once and stays disabled through stopping states', async () => {
  const ui = page(), releases = [];
  ui.update(waiting(info(0), info(1)));
  ui.respond((path, options) => options.method === 'POST' ? new Promise(resolve => releases.push(resolve)) : response(ui.snapshot()));
  ui.node('instance-replace').events.click();
  ui.node('instance-replace').events.click();
  assert.deepEqual(ui.requests, [{path: '/api/instance/stop/0', method: 'POST'}, {path: '/api/instance/stop/1', method: 'POST'}]);
  assert.equal(ui.node('instance-replace').getAttribute('aria-disabled'), 'true');
  assert.equal(ui.node('instance-replace-text').textContent, 'Stopping the earlier VRC\u00a0Tsuyaku…');
  assert.equal(ui.rows()[0].stop.textContent, 'Stopping…');
  assert.equal(ui.rows()[0].stop.getAttribute('aria-label'), 'Stop PID 4200');
  ui.update(waiting(info(0, {state: 'stopping'}), info(1, {state: 'stopping'})));
  releases.forEach(resolve => resolve(response({}, 202)));
  await ui.settle();
  assert.equal(ui.node('instance-replace').getAttribute('aria-disabled'), 'true');
  ui.update(waiting(info(0)));
  assert.equal(ui.node('instance-replace').getAttribute('aria-disabled'), null);
});

test('a batch skips already stopping rows and one refused request does not skip its neighbors', async () => {
  const ui = page();
  const values = [info(0, {state: 'stopping'}), info(1), info(2)];
  ui.update(waiting(...values));
  ui.respond((path, options) => options.method !== 'POST' ? response(ui.snapshot()) :
    path.endsWith('/1') ? response({error: 'request refused', code: 'instance_not_stopped'}, 409) : response({}, 202));
  await ui.stop(values);
  assert.deepEqual(ui.requests.filter(request => request.method === 'POST'), [
    {path: '/api/instance/stop/1', method: 'POST'}, {path: '/api/instance/stop/2', method: 'POST'},
  ]);
  assert.equal(ui.rows()[1].error.textContent, 'It did not stop. Close its console window.');
  assert.equal(ui.node('instance-details').open, true);
});

test('row errors open Details, clear without collapsing it, and do not carry into a replacement PID', () => {
  const ui = page();
  const failed = info(0, {code: 'instance_not_stopped', error: 'server diagnostic'});
  ui.update(waiting(failed));
  assert.equal(ui.rows()[0].error.textContent, 'It did not stop. Close its console window.');
  assert.equal(ui.node('instance-details').open, true);
  ui.node('instance-details').open = false;
  ui.render();
  assert.equal(ui.node('instance-details').open, false);
  ui.node('instance-details').open = true;
  ui.update(waiting(info(0)));
  assert.equal(ui.node('instance-details').open, true);
  assert.equal(ui.rows()[0].error.hidden, true);
  ui.rows()[0].stop.focus();
  const old = ui.rows()[0];
  ui.update(waiting(info(0, {pid: 9999, code: 'unknown', error: 'unrecognized failure'})));
  assert.notEqual(ui.rows()[0], old);
  assert.equal(ui.rows()[0].error.textContent, 'unrecognized failure');
  assert.equal(ui.document.activeElement, ui.node('instance-summary'));
});

test('a row Stop posts only its slot and the red button starts beside the other instances', async () => {
  const ui = page();
  ui.update(waiting(info(0), info(1)));
  ui.rows()[1].stop.events.click();
  await ui.settle();
  assert.deepEqual(ui.requests.filter(request => request.method === 'POST'), [{path: '/api/instance/stop/1', method: 'POST'}]);
  ui.respond((path, options) => {
    if (path === '/api/instance/start') return response({...ui.snapshot(), instance: {state: ''}});
    return response(ui.snapshot());
  });
  ui.node('instance-start').events.click();
  ui.node('instance-start').events.click();
  await ui.settle();
  assert.equal(ui.requests.filter(request => request.path === '/api/instance/start').length, 1);
  assert.equal(ui.node('instance-dialog').open, false);
});

test('Exit this one closes the modal before its exit request finishes and reveals the exited screen', async () => {
  const ui = page();
  ui.update(waiting(info(0)));
  let finish;
  ui.respond((path, options) => {
    if (path === '/api/exit') return new Promise(resolve => { finish = resolve; });
    if (path === '/' && options.method === 'HEAD') throw new Error('server stopped');
    return response(ui.snapshot());
  });
  ui.node('instance-exit').events.click();
  assert.equal(ui.exitState(), 'waiting');
  assert.equal(ui.node('instance-dialog').open, false);
  ui.render();
  assert.equal(ui.node('instance-dialog').open, false);
  finish(response({}, 202));
  await ui.settle();
  assert.equal(ui.exitState(), 'exited');
  assert.equal(ui.node('screen-exited').hidden, false);
  assert.equal(ui.document.activeElement, ui.node('exited-title'));
});
