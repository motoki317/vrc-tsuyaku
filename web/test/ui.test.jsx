import { render } from '@solidjs/web';
import { flush } from 'solid-js';
import { expect, test, vi } from 'vitest';
import page from '../index.html?raw';
import { Frame } from '../src/frame.jsx';
import { createClient } from '../src/client.js';
import { fixture, response, settle } from './fixture.js';

vi.mock('../i18n/en.json', async importOriginal => {
  const { default: catalog } = await importOriginal();
  return { default: Object.fromEntries(Object.keys(catalog).map(key => [key, '⟦' + key + '⟧'])) };
});

test('the entry and rendered screens take their labels from the catalog and About links to the notices', async () => {
  const doc = new DOMParser().parseFromString(page, 'text/html');
  const state = fixture();
  for (const row of Object.values(state.artifacts)) row.artifact.Name = 'fixture artifact';
  state.settings.Glossary = [{ SourceLang: 'en', TargetLang: 'ja', Source: 'word', Target: '訳', Reading: '' }];
  function check(root) {
    expect(root.querySelector('[title]')).toBeNull();
    const text = root.ownerDocument.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    while (text.nextNode()) {
      const node = text.currentNode,
        value = node.textContent.trim();
      if (!value || node.parentElement.matches('script,style')) continue;
      if (node.parentElement.id === 'unread-count' && value === '0') continue;
      if (value === 'fixture artifact') continue;
      if (
        node.parentElement.matches('#tokens button') &&
        /^\{\{\.(Original|Translation|SourceLang|TargetLang)\}\}$/.test(value)
      )
        continue;
      expect(value, node.parentElement.outerHTML).toMatch(/^(?:⟦[\w.]+⟧)+$/);
    }
    for (const attr of ['aria-label', 'placeholder', 'alt', 'label']) {
      for (const el of root.querySelectorAll('[' + attr + ']'))
        expect(el.getAttribute(attr), el.outerHTML).toMatch(/^⟦[\w.]+⟧$/);
    }
  }
  check(doc.documentElement);
  document.head.innerHTML = '<title></title>';
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  vi.spyOn(window, 'close').mockImplementation(() => {});
  const fetch = vi.fn(() => response([]));
  vi.stubGlobal('fetch', fetch);
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.spyOn(Intl.DisplayNames.prototype, 'of').mockImplementation(() => '⟦fixture.language⟧');
  let app;
  const dispose = render(() => {
    app = createClient();
    return <Frame app={app} />;
  }, document.body);
  try {
    app.apply(state);
    flush();
    await settle();
    check(document.documentElement);
    app.setScreen('settings');
    flush();
    check(document.documentElement);
    expect(document.querySelector('.about-links a[href="/notices.txt"]').textContent).toBe('⟦label.thirdPartyNotices⟧');

    const select = document.querySelector('.plan-row[data-flow=self] select');
    fetch.mockResolvedValueOnce(response({ code: 'disk_write' }, 500));
    select.value = 'ja';
    select.dispatchEvent(new Event('input', { bubbles: true }));
    select.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await settle();
    expect(document.querySelector('#language-error-self').textContent).toBe('⟦error.code.disk_write⟧');

    const failed = structuredClone(state);
    for (const id of ['runtime', 'model', 'en-asr']) failed.artifacts[id].state = 'installed';
    Object.assign(failed.artifacts['ja-asr'], { error: 'network', code: 'download_http' });
    failed.flows.self = { state: 'error', error: 'no microphone', code: 'microphone_unavailable' };
    app.apply(failed);
    flush();
    expect(document.querySelector('.download[data-kind=failed] .error-text').textContent).toBe(
      '⟦error.code.download_http⟧',
    );
    expect(document.querySelector('#fc-error-bar-self').textContent).toBe('⟦error.code.microphone_unavailable⟧');
    check(document.documentElement);

    const dialog = document.querySelector('#instance-dialog');
    dialog.showModal = () => {
      dialog.open = true;
    };
    dialog.close = () => {
      dialog.open = false;
    };
    app.apply({
      ...failed,
      instance: {
        state: 'waiting',
        others: [{ slot: 0, pid: 42, started: new Date().toISOString(), url: '⟦fixture.url⟧' }],
      },
    });
    flush();
    expect(dialog.open).toBe(true);
    check(document.documentElement);

    fetch.mockRejectedValueOnce(Error('offline'));
    await expect(app.refresh()).rejects.toThrow();
    flush();
    expect(document.querySelector('#connection').hidden).toBe(false);
    check(document.documentElement);

    fetch.mockImplementation(url => (url === '/' ? Promise.reject(Error('exited')) : response()));
    await app.exit();
    await vi.advanceTimersByTimeAsync(3500);
    flush();
    expect(document.querySelector('#screen-exited').hidden).toBe(false);
    expect(document.querySelector('#exited-close').textContent).toBe('⟦action.closeTab⟧');
    check(document.documentElement);
  } finally {
    dispose();
    document.body.replaceChildren();
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  }
});
