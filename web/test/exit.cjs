const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');

const source = readFileSync('public/app.js', 'utf8').replace(/\nstart\(\);\s*$/, '');
const drain = () => new Promise(resolve => setImmediate(resolve));
const response = (body = {}, status = 200) => ({ok: status < 400, status, json: async () => body});
function eventStream() {
  let controller;
  const body = new ReadableStream({start(value) { controller = value; }});
  return {response: {...response(), body},
    send: text => controller.enqueue(new TextEncoder().encode(text)),
    close: () => controller.close(), fail: () => controller.error(new Error('stream lost'))};
}

function page() {
  const nodes = new Map(), requests = [], timers = new Map(), logged = [];
  let nextTimer = 0, respond, closes = 0, reloads = 0;
  const document = {hidden: false, activeElement: null};
  function element() {
    const attributes = new Map(), classes = new Set();
    return {hidden: false, open: false, dataset: {}, children: [], textContent: '',
      append() {}, replaceChildren(...children) { this.children = children; this.textContent = children.join(''); },
      setAttribute: (name, value) => attributes.set(name, String(value)),
      getAttribute: name => attributes.get(name) ?? null, removeAttribute: name => attributes.delete(name),
      classList: {toggle(name, on) { if (on) classes.add(name); else classes.delete(name); }, contains: name => classes.has(name)},
      firstChild: {setAttribute: (name, value) => attributes.set(name, value)},
      querySelector: () => node('exit-icon'),
      focus() { document.activeElement = this; }, close() { this.open = false; },
    };
  }
  function node(id) {
    if (!nodes.has(id)) nodes.set(id, Object.assign(element(), {id}));
    return nodes.get(id);
  }
  document.createElement = document.createElementNS = element;
  document.getElementById = node;
  document.querySelector = () => node('menu');
  document.querySelectorAll = selector => selector === 'dialog[open]'
    ? [...nodes.values()].filter(node => node.id.endsWith('-dialog') && node.open) : [node('bar-item')];
  node('screen-exited').hidden = true;
  const snapshot = {launch: 'test', instance: {state: ''}, artifacts: {}, required: {self: [], world: []},
    flows: {self: {state: 'stopped'}, world: {state: 'stopped'}},
    settings: {UI: {Language: 'en'}, Flows: {Self: {Enabled: false}, World: {Enabled: false}}}};
  const context = vm.createContext({
    document, AbortSignal, AbortController, TextDecoderStream,
    window: {close: () => closes++}, location: {reload: () => reloads++},
    setTimeout: (fn, delay) => {timers.set(++nextTimer, {fn, delay}); return nextTimer;},
    clearTimeout: id => timers.delete(id),
    fetch: async (path, options = {}) => {
      requests.push({path, method: options.method || 'GET'});
      return respond ? respond(path, options) : response(snapshot);
    },
  });
  vm.runInContext(source, context);
  const ui = vm.runInContext(`
    render = () => {};
    renderMeters = () => {};
    listMicrophones = () => {};
    logEvent = event => logged(event);
    closePending = () => {};
    setLanguage = value => { lang = value; };
    ({stream, exit: exitApp, showExited, apply, scheduleRefresh,
      status: () => ({exitState, connection, lastEvent, levelTimer, levelRequest}),
      setState: value => { state = value; },
      setStrings: value => { strings = value; },
      setLastEvent: value => { lastEvent = value; },
      poll: () => { screen = 'settings'; sub = 'audio'; syncLevelPolling(); },
      waiting: () => { state.instance = {state: 'waiting'}; $('instance-dialog').open = true;
        instanceView.timer = setTimeout(renderInstances, 1000); }});
  `, context);
  context.logged = event => logged.push(event);
  ui.setState(snapshot);
  ui.setStrings({en: JSON.parse(readFileSync('public/i18n/en.json', 'utf8'))});
  return {...ui, node, document, requests, timers, logged, snapshot,
    respond: fn => {respond = fn;}, closes: () => closes, reloads: () => reloads,
    async settle() { await drain(); await drain(); },
    async tick(delay) {
      const timer = [...timers].find(([, value]) => value.delay === delay);
      assert.ok(timer, `expected a ${delay}ms timer`);
      timers.delete(timer[0]);
      timer[1].fn();
      await drain(); await drain();
    },
  };
}

test('an exit record waits with the spinner, then shows the exited screen and keeps the tab-close behavior', async () => {
  const ui = page(), events = eventStream(), heads = [];
  ui.setLastEvent(42);
  ui.waiting();
  ui.respond((path, options) => {
    if (path === '/api/events') return events.response;
    if (options.method === 'HEAD') return new Promise((resolve, reject) => heads.push({resolve, reject}));
    return response(ui.snapshot);
  });
  const running = ui.stream();
  events.send('\n{"ki');
  events.send('nd":"exit"}\n{"id":999,"kind":"state"}\n');
  await ui.settle();
  assert.equal(ui.status().exitState, 'waiting');
  assert.equal(ui.status().lastEvent, 42);
  assert.equal(ui.node('exit-icon').getAttribute('href'), '#i-loader-circle');
  assert.equal(ui.node('exit-icon').classList.contains('spin'), true);
  assert.equal(ui.node('main').inert, true);
  assert.equal(ui.node('instance-dialog').open, false);
  assert.equal(ui.node('screen-exited').hidden, true);
  assert.equal(heads.length, 1);
  heads[0].resolve(response());
  await ui.settle();
  await ui.tick(250);
  assert.equal(heads.length, 2);
  heads[1].reject(new Error('server stopped'));
  await running;
  assert.equal(ui.status().exitState, 'exited');
  assert.equal(ui.node('screen-exited').hidden, false);
  assert.equal(ui.document.activeElement, ui.node('exited-title'));
  assert.deepEqual(ui.logged, []);
  assert.equal(ui.requests.some(request => request.method === 'POST'), false);
  ui.showExited();
  assert.equal([...ui.timers.values()].filter(timer => timer.delay === 3000).length, 1);
  await ui.tick(3000);
  assert.equal(ui.closes(), 1);
  await ui.tick(500);
  assert.equal(ui.node('exited-close').textContent, 'Close this tab.');
  assert.equal(heads.length, 2);
  assert.equal(ui.timers.size, 0);
});

test('an exit notice during an Exit press shares one wait, even if the exit request loses its answer', async () => {
  for (const losesAnswer of [false, true]) {
    const ui = page(), events = eventStream();
    let finishPost, finishHead;
    ui.respond((path, options) => {
      if (path === '/api/events') return events.response;
      if (path === '/api/exit') return new Promise((resolve, reject) => {finishPost = () => losesAnswer ? reject(new Error('lost answer')) : resolve(response({}, 202));});
      if (options.method === 'HEAD') return new Promise((_resolve, reject) => {finishHead = reject;});
      return response(ui.snapshot);
    });
    const streaming = ui.stream();
    await ui.settle();
    const exiting = ui.exit();
    ui.exit();
    events.send('{"kind":"exit"}\n');
    await ui.settle();
    finishPost();
    await ui.settle();
    assert.equal(ui.requests.filter(request => request.method === 'HEAD').length, 1);
    assert.equal(ui.requests.filter(request => request.path === '/api/exit').length, 1);
    finishHead(new Error('server stopped'));
    await Promise.all([streaming, exiting]);
    assert.equal(ui.status().exitState, 'exited');
    assert.equal([...ui.timers.values()].filter(timer => timer.delay === 3000).length, 1);
  }
});

test('three failed reconnects stop level and state polling, then probe until the server returns', async () => {
  const ui = page();
  let levelSignal;
  ui.respond((path, options) => {
    if (path === '/api/levels') return new Promise((_resolve, reject) => {
      levelSignal = options.signal;
      levelSignal.addEventListener('abort', () => reject(new Error('aborted')));
    });
    throw new Error('server stopped');
  });
  ui.poll();
  ui.scheduleRefresh();
  const running = ui.stream();
  await ui.settle();
  assert.equal(ui.status().exitState, '');
  assert.equal(ui.status().connection, 'reconnecting');
  assert.equal(ui.node('connection').hidden, false);
  await ui.tick(2000);
  assert.equal(ui.status().exitState, '');
  await ui.tick(2000);
  await running;
  assert.equal(ui.status().exitState, 'exited');
  assert.equal(ui.node('screen-exited').hidden, false);
  assert.equal(levelSignal.aborted, true);
  assert.equal(ui.status().levelTimer, 0);
  assert.equal(ui.status().levelRequest, null);
  assert.equal(ui.requests.filter(request => request.path === '/api/events').length, 3);
  assert.deepEqual([...ui.timers.values()].map(timer => timer.delay), [3000, 2000]);
  ui.scheduleRefresh();
  ui.apply({launch: 'later-launch'});
  assert.equal(ui.reloads(), 0);
  assert.equal(ui.timers.size, 2);
  await ui.tick(2000);
  assert.equal(ui.requests.filter(request => request.method === 'HEAD').length, 1);
  assert.equal(ui.reloads(), 0);
  ui.respond(() => response());
  await ui.tick(2000);
  assert.equal(ui.requests.filter(request => request.method === 'HEAD').length, 2);
  assert.equal(ui.reloads(), 1);
  assert.equal(ui.requests.filter(request => request.path === '/api/events').length, 3);
  assert.deepEqual([...ui.timers.values()].map(timer => timer.delay), [3000]);
});

test('two failures recover without exiting and a successful reconnect resets the failure count', async () => {
  const ui = page(), first = eventStream(), second = eventStream();
  let attempts = 0;
  ui.respond(path => {
    if (path === '/api/events') {
      attempts++;
      if (attempts === 3) return first.response;
      if (attempts === 5) return second.response;
      throw new Error('temporary failure');
    }
    return response(ui.snapshot);
  });
  const running = ui.stream();
  await ui.settle();
  await ui.tick(2000);
  await ui.tick(2000);
  assert.equal(attempts, 3);
  assert.equal(ui.status().exitState, '');
  assert.equal(ui.status().connection, 'ok');
  assert.equal(ui.node('connection').hidden, true);
  assert.equal(ui.node('screen-exited').hidden, true);
  first.fail();
  await ui.settle();
  await ui.tick(2000);
  assert.equal(attempts, 4);
  assert.equal(ui.status().exitState, '');
  await ui.tick(2000);
  assert.equal(attempts, 5);
  assert.equal(ui.status().connection, 'ok');
  assert.equal(ui.status().exitState, '');
  ui.showExited();
  second.close();
  await running;
});

test('a reconnect delay that outlives the exited screen sends no later request', async () => {
  const ui = page();
  ui.respond(() => {throw new Error('temporary failure');});
  const running = ui.stream();
  await ui.settle();
  ui.showExited();
  await ui.tick(2000);
  await running;
  assert.equal(ui.requests.length, 1);
});

test('a failed Exit request leaves reconnection running when the stream also drops', async () => {
  const ui = page(), first = eventStream(), recovered = eventStream();
  let attempts = 0, rejectPost;
  ui.respond(path => {
    if (path === '/api/events') return ++attempts === 1 ? first.response : recovered.response;
    if (path === '/api/exit') return new Promise((_resolve, reject) => {rejectPost = reject;});
    return response(ui.snapshot);
  });
  const streaming = ui.stream();
  await ui.settle();
  const exiting = ui.exit();
  first.fail();
  await ui.settle();
  rejectPost(new Error('temporary failure'));
  await exiting;
  assert.equal(ui.status().exitState, '');
  assert.equal(ui.status().connection, 'reconnecting');
  await ui.tick(2000);
  assert.equal(attempts, 2);
  assert.equal(ui.status().connection, 'ok');
  ui.showExited();
  recovered.close();
  await streaming;
});

test('a late Exit response cannot undo an exited screen or start polling after reconnect failures', async () => {
  for (const losesAnswer of [false, true]) {
    const ui = page(), events = eventStream();
    let attempts = 0, finishPost;
    ui.respond(path => {
      if (path === '/api/events') {
        if (++attempts === 1) return events.response;
        throw new Error('server stopped');
      }
      if (path === '/api/exit') return new Promise((resolve, reject) => {finishPost = () => losesAnswer ? reject(new Error('lost answer')) : resolve(response({}, 202));});
      return response(ui.snapshot);
    });
    const streaming = ui.stream();
    await ui.settle();
    const exiting = ui.exit();
    events.fail();
    await ui.settle();
    await ui.tick(2000);
    await ui.tick(2000);
    await streaming;
    finishPost();
    await exiting;
    assert.equal(ui.status().exitState, 'exited');
    assert.equal(ui.node('screen-exited').hidden, false);
    assert.equal(ui.requests.filter(request => request.method === 'HEAD').length, 0);
    assert.deepEqual([...ui.timers.values()].map(timer => timer.delay), [3000, 2000]);
  }
});

test('an exit poll already in flight cannot schedule another after the exited screen', async () => {
  const ui = page();
  let finishHead;
  ui.respond((path, options) => {
    if (options.method === 'HEAD') return new Promise(resolve => {finishHead = resolve;});
    return response({}, 202);
  });
  const exiting = ui.exit();
  await ui.settle();
  ui.showExited();
  finishHead(response());
  await exiting;
  assert.equal(ui.requests.filter(request => request.method === 'HEAD').length, 1);
  assert.deepEqual([...ui.timers.values()].map(timer => timer.delay), [3000, 2000]);
});

test('exit dismisses every open dialog so the exited heading can receive focus', async () => {
  for (const notice of [false, true]) {
    const ui = page(), events = eventStream();
    for (const name of ['license', 'delete', 'replace']) ui.node(name + '-dialog').open = true;
    if (notice) {
      ui.respond(path => {
        if (path === '/api/events') return events.response;
        if (path === '/') throw new Error('server stopped');
        return response(ui.snapshot);
      });
      const streaming = ui.stream();
      events.send('{"kind":"exit"}\n');
      await streaming;
    } else ui.showExited();
    for (const name of ['license', 'delete', 'replace']) assert.equal(ui.node(name + '-dialog').open, false);
    assert.equal(ui.document.activeElement, ui.node('exited-title'));
  }
});
