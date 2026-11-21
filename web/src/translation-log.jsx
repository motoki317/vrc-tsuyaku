import { createEffect, createMemo, For, onSettled } from 'solid-js';
import { Icon } from './icon.jsx';
import { flowIcon } from './downloads.js';
import { eventError, flowLabel, langTag } from './i18n.js';
import { pillLabels } from './log.js';

const delivery = {
  sending: ['clock', 'state.sending'],
  sent: ['message-square-text', 'state.sentToChatbox'],
  failed: ['circle-alert', 'state.notSent'],
  unfinished: ['circle-slash', 'state.unfinished'],
};

function Phrase({ app, row, first, clock }) {
  const self = row.flow === 'self';
  const original = (
    <p class={'line ' + (self ? 'primary' : 'secondary')} lang={langTag(row.source)}>
      {row.original}
    </p>
  );
  const translation = (
    <p class={'line ' + (self ? 'secondary' : 'primary')} lang={langTag(row.target)}>
      {row.translation ??
        (row.unfinished ? (
          <span role="img" aria-label={app.t('state.unfinished')} class="unfinished">
            <Icon name="circle-slash" />
          </span>
        ) : (
          <span class="dots" role="img" aria-label={app.t('state.translating')}>
            <i />
            <i />
            <i />
          </span>
        ))}
    </p>
  );
  const deliveryText = () => {
    if (row.status === 'sent') return app.t('state.sent');
    return row.status === 'failed' ? app.t('state.notSent') : '';
  };
  const error = () => (self && row.code === 'template' ? eventError(app.t, row.code, row.error) : '');
  return (
    <li class={'msg ' + row.flow + (!self && first() ? ' first' : '')}>
      {!self && (
        <span class="avatar" aria-hidden="true">
          <Icon name="users" />
        </span>
      )}
      <div class="bubble">{self ? [original, translation] : [translation, original]}</div>
      <span class="meta">
        <time dateTime={row.time}>{clock(row.time)}</time>
        {self && (
          <span class="delivery" hidden={!row.status} data-status={row.status}>
            <Icon name={delivery[row.status]?.[0] || 'clock'} />
            <span aria-hidden="true">{deliveryText()}</span>
            <span class="visually-hidden">{row.status && app.t(delivery[row.status][1])}</span>
          </span>
        )}
      </span>
      <p class="send-error" hidden={!error()}>
        {error()}
      </p>
    </li>
  );
}

function Pill({ app, row, clock }) {
  return (
    <li class={'pill ' + row.pill + (row.resolved ? ' resolved' : '')}>
      <span class="pill-body">
        <span role="img" aria-label={flowLabel(app, row.flow)}>
          <Icon name={flowIcon[row.flow]} />
        </span>
        {row.pill === 'error' && <Icon name="circle-alert" />}
        <span>{app.t(pillLabels[row.pill])}</span>
        <time dateTime={row.time}>{clock(row.time)}</time>
      </span>
    </li>
  );
}

export function TranslationLog({ app }) {
  const log = app.log,
    rows = log.rows;
  const format = createMemo(
    () => new Intl.DateTimeFormat(app.language(), { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }),
  );
  const clock = time => format().format(new Date(time));
  const world = i => rows[i]?.flow === 'world' && !!rows[i].phraseId;
  let viewport,
    column,
    unreadButton,
    lastTop = 0;
  function bottom() {
    viewport.scrollTop = viewport.scrollHeight;
    lastTop = viewport.scrollTop;
    if (document.activeElement === unreadButton) viewport.focus();
    log.follow(true);
  }
  function scroll() {
    if (!viewport.clientHeight) return;
    const top = viewport.scrollTop;
    // New content can move the bottom away without a scroll up. Keep following in that case.
    if (viewport.scrollHeight - top - viewport.clientHeight < 40) {
      if (log.unread()) bottom();
      else log.follow(true);
    } else if (top < lastTop) log.follow(false);
    lastTop = viewport.scrollTop;
  }
  createEffect(app.screen, screen => {
    if (screen === 'translation') bottom();
  });
  createEffect(
    () => rows.at(-1),
    row => {
      if (row && log.following && viewport.clientHeight) bottom();
    },
  );
  onSettled(() => {
    const resize = new ResizeObserver(() => {
      if (log.following && viewport.clientHeight) bottom();
    });
    resize.observe(viewport);
    resize.observe(column);
    return () => resize.disconnect();
  });
  const avatar = name => (
    <span class="avatar">
      <svg class="icon">
        <use href={'#i-' + name} />
      </svg>
    </span>
  );
  return (
    <>
      <div
        id="log"
        class="log"
        role="log"
        aria-live="off"
        tabindex="0"
        aria-label={app.t('label.log')}
        ref={el => (viewport = el)}
        onScroll={scroll}
      >
        <div class="log-column" ref={el => (column = el)}>
          <ol id="log-list" class="log-list">
            <For each={rows}>
              {(row, i) =>
                row.pill ? (
                  <Pill app={app} row={row} clock={clock} />
                ) : (
                  <Phrase app={app} row={row} first={() => !world(i() - 1)} clock={clock} />
                )
              }
            </For>
          </ol>
          <div id="ghosts" class="ghosts" aria-hidden="true" hidden={!log.empty()}>
            <div class="ghost world">
              {avatar('users')}
              <span class="ghost-bubble" />
            </div>
            <div class="ghost self">
              <span class="ghost-bubble" />
              {avatar('mic')}
            </div>
          </div>
          {['world', 'self'].map(flow => (
            <div
              id={'dots-' + flow}
              class={'msg ' + flow + ' speaking' + (flow === 'world' && !world(rows.length - 1) ? ' first' : '')}
              aria-hidden="true"
              hidden={!log.speech[flow]}
            >
              {flow === 'world' && avatar('users')}
              <div class="bubble">
                <span class="dots">
                  <i />
                  <i />
                  <i />
                </span>
              </div>
            </div>
          ))}
        </div>
      </div>
      <button
        type="button"
        id="unread"
        class="unread"
        hidden={!log.unread()}
        ref={el => (unreadButton = el)}
        aria-label={app.t('action.latest', { count: log.unread() })}
        onClick={bottom}
      >
        <Icon name="arrow-down" />
        <span id="unread-count">{log.unread()}</span>
      </button>
    </>
  );
}
