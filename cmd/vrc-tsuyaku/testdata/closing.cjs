const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');

test('every pagehide sends a closing beacon, even before startup fetches finish', () => {
  const listeners = {}, beacons = [];
  const context = vm.createContext({
    document: {createElement: () => ({append() {}, setAttribute() {}})},
    window: {addEventListener: (type, listener) => { listeners[type] = listener; }},
    navigator: {sendBeacon: (...args) => { beacons.push(args); return false; }},
    fetch: () => new Promise(() => {}),
  });
  vm.runInContext(readFileSync('ui/app.js', 'utf8'), context);
  for (const exitState of ['', 'waiting', 'exited']) {
    vm.runInContext(`exitState = ${JSON.stringify(exitState)}`, context);
    for (const persisted of [false, true]) listeners.pagehide({persisted});
  }
  assert.deepEqual(beacons, Array.from({length: 6}, () => ['/api/closing']));
});
