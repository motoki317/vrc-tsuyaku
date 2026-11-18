import { createEffect, createSignal, flush, onSettled, Show } from 'solid-js';
import { errorMessage } from './i18n.js';
import { createDownloads } from './downloads.js';
import { DownloadAll, DownloadDialogs } from './download-list.jsx';
import { Setup, Models, PlanRows } from './plan.jsx';
import { About } from './about.jsx';
import { FolderButtons, ReplaceDialog, SettingsError } from './settings-error.jsx';
import { Glossary } from './glossary.jsx';
import { Chatbox } from './chatbox.jsx';
import { Audio } from './audio.jsx';
import { SwitchBar } from './flow-control.jsx';
import { TranslationLog } from './translation-log.jsx';
import { Instances } from './instances.jsx';

import { Icon } from './icon.jsx';

const locales = [
  ['', 'value.languageBrowser'],
  ['en', 'value.languageEnglish'],
  ['ja', 'value.languageJapanese'],
];
const sections = [
  ['models', 'label.languagesModels', 'languages'],
  ['audio', 'label.audio', 'mic'],
  ['glossary', 'label.glossary', 'book-a'],
  ['chatbox', 'label.chatbox', 'message-square-text'],
  ['about', 'label.about', 'info'],
];

export function Frame({ app }) {
  const downloads = createDownloads(app);
  let menu, menuButton, exitButton, exitedHeading, rail, lastChoice;
  const [menuOpen, setMenuOpen] = createSignal(false);
  const [languageError, setLanguageError] = createSignal(null);
  const [replaceError, setReplaceError] = createSignal();
  const [exitError, setExitError] = createSignal(null);
  const name = () => app.t(app.language() === 'ja' ? 'value.languageJapanese' : 'value.languageEnglish');
  const screen = () => (app.state.settingsError ? 'settings-error' : app.screen());
  const waiting = () => app.phase() === 'waiting',
    exited = () => app.phase() === 'exited';
  const failed = () => Object.values(app.state.flows ?? {}).some(flow => flow.state === 'error');
  const update = () => app.state.update?.state === 'available';
  const marked = (label, mark) =>
    mark ? app.t('state.tabState', { tab: app.t(label), state: app.t(mark) }) : undefined;
  createEffect(
    () => [app.language(), app.t('label.product')],
    ([lang, title]) => {
      document.documentElement.lang = lang;
      document.querySelector('head > title').textContent = title;
    },
  );
  createEffect(screen, value => {
    document.body.dataset.screen = value;
  });
  createEffect(waiting, pending => {
    if (pending) setExitError(null);
  });
  createEffect(exited, done => {
    if (done) exitedHeading.focus();
  });
  createEffect(
    () => [screen(), app.sub()],
    ([current]) => {
      if (current !== 'settings') return;
      function reveal() {
        const item = rail.querySelector('[aria-current=page]');
        if (!item) return;
        const box = rail.getBoundingClientRect(),
          at = item.getBoundingClientRect(),
          pad = parseFloat(getComputedStyle(rail).paddingLeft);
        if (at.left < box.left + pad) rail.scrollLeft -= box.left + pad - at.left;
        else if (at.right > box.right - pad) rail.scrollLeft += at.right - box.right + pad;
      }
      reveal();
      const observer = new ResizeObserver(reveal);
      observer.observe(rail);
      return () => observer.disconnect();
    },
  );
  function closeMenu(focus) {
    setMenuOpen(false);
    if (focus) menuButton.focus();
  }
  async function selectLanguage(value) {
    closeMenu(true);
    const saving = app.selectLanguage(value);
    if (!saving) return;
    lastChoice = saving;
    try {
      await saving;
      if (lastChoice === saving) setLanguageError(null);
    } catch (e) {
      if (lastChoice === saving) {
        setLanguageError(e);
        app.announce(errorMessage(app.t, e, 'error.notSaved'));
      }
    }
  }
  function toggleMenu() {
    if (menuOpen()) return closeMenu(false);
    setLanguageError(null);
    setMenuOpen(true);
    flush();
    (menu.querySelector('[aria-checked=true]') || menu.firstElementChild).focus();
  }
  function keydown(e) {
    const items = [...menu.children],
      i = items.indexOf(document.activeElement);
    const next = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: items.length - 1 }[e.key];
    if (next !== undefined) {
      e.preventDefault();
      items[(next + items.length) % items.length].focus();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      closeMenu(true);
    } else if (e.key === 'Tab') closeMenu(false);
  }
  async function exit() {
    setExitError(null);
    try {
      await app.exit();
    } catch (e) {
      setExitError(e);
      app.announce(errorMessage(app.t, e));
    }
  }
  onSettled(() => {
    function outside(e) {
      if (!e.target.closest('.menu')) {
        closeMenu(false);
        setLanguageError(null);
      }
      if (!exitButton.parentElement.contains(e.target)) setExitError(null);
    }
    document.addEventListener('click', outside);
    return () => document.removeEventListener('click', outside);
  });
  function Screens() {
    const rows = <PlanRows app={app} downloads={downloads} />;
    const all = <DownloadAll app={app} downloads={downloads} />;
    return (
      <>
        <section id="screen-setup" class="screen setup" hidden={screen() !== 'setup'}>
          <Setup app={app} downloads={downloads} rows={rows} all={all} />
        </section>
        <section id="screen-translation" class="screen translation" hidden={screen() !== 'translation'}>
          <TranslationLog app={app} />
        </section>
        <section id="screen-settings" class="screen settings" hidden={screen() !== 'settings'}>
          <nav class="rail" aria-label={app.t('label.settingsSections')} ref={el => (rail = el)}>
            {sections.map(([key, label, icon]) => (
              <button
                type="button"
                class="rail-item"
                data-sub={key}
                aria-current={app.sub() === key ? 'page' : undefined}
                aria-label={key === 'about' ? marked(label, update() && 'state.updateAvailable') : undefined}
                onClick={() => app.setSub(key)}
              >
                <Icon name={icon} />
                <span data-label={app.t(label)}>{app.t(label)}</span>
                {key === 'about' && <span class="update-dot" aria-hidden="true" hidden={!update()}></span>}
              </button>
            ))}
          </nav>
          {sections.map(([key]) => (
            <div id={'sub-' + key} class="sub" hidden={app.sub() !== key}>
              {key === 'models' && <Models app={app} downloads={downloads} rows={rows} all={all} />}
              {key === 'audio' && <Audio app={app} />}
              {key === 'chatbox' && <Chatbox app={app} />}
              {key === 'glossary' && <Glossary app={app} />}
              {key === 'about' && <About app={app} downloads={downloads} />}
            </div>
          ))}
          <FolderButtons app={app} />
        </section>
        <SettingsError app={app} error={replaceError} report={setReplaceError} />
        <SwitchBar app={app} downloads={downloads} />
      </>
    );
  }
  return (
    <>
      <header class="topbar">
        <div class="topbar-inner">
          <h1 class="brand">
            <span class="mark" aria-hidden="true"></span>
            <span class="product">{app.t('label.product')}</span>
          </h1>
          <nav
            id="tabs"
            class="tabs"
            aria-label={app.t('label.screens')}
            hidden={!app.screen() || ['setup', 'settings-error'].includes(screen()) || exited()}
            inert={!!app.phase()}
          >
            <button
              type="button"
              class={failed() ? 'tab failed' : 'tab'}
              data-screen="translation"
              aria-current={app.screen() === 'translation' ? 'page' : undefined}
              aria-label={marked('label.translation', failed() && 'state.stoppedByError')}
              onClick={() => app.setScreen('translation')}
            >
              <Icon name={failed() ? 'circle-alert' : 'captions'} />
              <span>{app.t('label.translation')}</span>
            </button>
            <button
              type="button"
              class="tab"
              data-screen="settings"
              aria-current={app.screen() === 'settings' ? 'page' : undefined}
              aria-label={marked('label.settings', update() && 'state.updateAvailable')}
              onClick={() => app.setScreen('settings')}
            >
              <Icon name="settings" />
              <span>{app.t('label.settings')}</span>
              <span class="update-dot" aria-hidden="true" hidden={!update()}></span>
            </button>
          </nav>
          <div class="bar-item menu" inert={!!app.phase()} hidden={exited()}>
            <button
              type="button"
              id="ui-language"
              class="bar-button"
              aria-haspopup="menu"
              aria-expanded={String(menuOpen())}
              aria-controls="ui-language-menu"
              aria-label={app.t('label.uiLanguageName', { name: name() })}
              ref={el => (menuButton = el)}
              onClick={toggleMenu}
            >
              <Icon name="globe" />
              <span id="ui-language-name" class="bar-label">
                {name()}
              </span>
              <Icon name="chevron-down" extra="small" />
            </button>
            <div
              id="ui-language-menu"
              class="menu-list"
              role="menu"
              aria-label={app.t('label.uiLanguage')}
              hidden={!menuOpen()}
              onKeyDown={keydown}
              ref={el => (menu = el)}
            >
              {locales.map(([value, key]) => (
                <button
                  type="button"
                  role="menuitemradio"
                  data-value={value}
                  lang={value || undefined}
                  aria-checked={String(app.languageSetting() === value)}
                  onClick={() => selectLanguage(value)}
                >
                  {app.t(key)}
                </button>
              ))}
            </div>
            <p id="ui-language-error" class="bar-error error-text" hidden={!languageError()}>
              {errorMessage(app.t, languageError(), 'error.notSaved')}
            </p>
          </div>
          <div class="bar-item" hidden={exited()}>
            <button
              type="button"
              id="exit"
              class="bar-button"
              aria-label={app.t('action.exit')}
              ref={el => (exitButton = el)}
              onClick={exit}
            >
              <Icon name={app.phase() ? 'loader-circle' : 'power'} extra={app.phase() ? 'spin' : ''} />
              <span class="bar-label">{app.t('action.exit')}</span>
            </button>
            <p id="exit-error" class="bar-error error-text" hidden={!exitError()}>
              {errorMessage(app.t, exitError())}
            </p>
          </div>
        </div>
      </header>
      <div id="connection" class="connection" hidden={!app.reconnecting() || !!app.phase()}>
        <Icon name="wifi-off" />
        <span class="spinner-slot" aria-hidden="true">
          <svg class="icon spin">
            <use href="#i-loader-circle" />
          </svg>
        </span>
        <span id="connection-text" role="status">
          {app.reconnecting() ? app.t('state.reconnecting') : ''}
        </span>
      </div>
      <main id="main" inert={!!app.phase()} hidden={exited()}>
        <Show when={app.state.launch}>
          <Screens />
        </Show>
      </main>
      <section id="screen-exited" class="screen exited" hidden={!exited()}>
        <Icon name="power" extra="large" />
        <h2 id="exited-title" tabindex="-1" ref={el => (exitedHeading = el)}>
          {app.t('title.exited')}
        </h2>
        <p id="exited-close" role="status">
          {app.closeHint() ? app.t('action.closeTab') : ''}
        </p>
      </section>
      <DownloadDialogs app={app} downloads={downloads} />
      <ReplaceDialog app={app} report={setReplaceError} />
      <Instances app={app} exit={exit} />
      <div id="announce" class="visually-hidden" aria-live="polite">
        {app.announcement()}
      </div>
    </>
  );
}
