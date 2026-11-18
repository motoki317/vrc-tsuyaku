import { For } from 'solid-js';
import { Icon } from './icon.jsx';

const links = [
  ['', 'label.sourceCode'],
  ['/releases', 'label.releases'],
  ['/blob/main/LICENSE', 'label.appLicense'],
  [undefined, 'label.thirdPartyNotices'],
];
const updates = {
  checking: ['loader-circle', 'spin', 'state.checkingUpdates'],
  current: ['check', '', 'state.upToDate'],
  failed: ['circle-alert', 'muted', 'state.updateFailed'],
};

export function About({ app, downloads }) {
  const build = () => app.state.app,
    update = () => app.state.update;
  const line = () => updates[update().state];
  return (
    <>
      <h2 class="sub-title about-title" tabindex="-1">
        {app.t('label.product')}
      </h2>
      <p class="about-description">{app.t('value.description')}</p>
      <p id="app-version" class="about-version">
        <span>{build().version || app.t('value.developmentBuild')}</span>
        {build().revision && <span class="mono">{build().revision.slice(0, 7)}</span>}
        {build().time && (
          <time datetime={build().time}>
            {new Intl.DateTimeFormat(app.language(), { dateStyle: 'medium' }).format(new Date(build().time))}
          </time>
        )}
        {build().modified && <span>{app.t('state.modified')}</span>}
      </p>
      <p id="app-update" class="about-update" role="status">
        {update().state === 'available' ? (
          <>
            <Icon name="circle-arrow-up" extra="link" />
            <a href={update().url} target="_blank" rel="noreferrer">
              {app.t('state.versionAvailable', { version: update().version })}
              <Icon name="arrow-up-right" extra="small" />
            </a>
          </>
        ) : (
          <>
            <Icon name={line()[0]} extra={line()[1]} />
            <span>{app.t(line()[2])}</span>
          </>
        )}
      </p>
      <p class="about-links">
        {links.map(([path, label]) => (
          <a href={path === undefined ? '/notices.txt' : build().repository + path} target="_blank" rel="noreferrer">
            <span>{app.t(label)}</span>
            <Icon name="arrow-up-right" extra="small" />
          </a>
        ))}
      </p>
      <h3 class="field-label" id="licenses-label">
        {app.t('label.softwareUsed')}
      </h3>
      <ul id="licenses" class="license-list" aria-labelledby="licenses-label">
        <For each={downloads.byName()}>
          {id => {
            const artifact = () => app.state.artifacts[id].artifact;
            return (
              <li>
                <span class="name">{artifact().Name}</span>
                <span class="links">
                  <For each={artifact().Licenses}>
                    {license => (
                      <span class="license">
                        <a href={license.URL} target="_blank" rel="noreferrer">
                          {license.Name}
                          <Icon name="arrow-up-right" extra="small" />
                        </a>
                        {license.Credit && <span class="credit">{license.Credit}</span>}
                      </span>
                    )}
                  </For>
                </span>
              </li>
            );
          }}
        </For>
      </ul>
    </>
  );
}
