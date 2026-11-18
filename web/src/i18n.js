import en from '../i18n/en.json';
import ja from '../i18n/ja.json';

const catalogs = { en, ja };
export const flowLabel = (app, flow) => app.t(flow === 'self' ? 'label.self' : 'label.world');
export const browserLanguage = () =>
  (navigator.languages || [navigator.language])
    .map(tag => tag.split('-')[0].toLowerCase())
    .find(code => code in catalogs) || 'en';

export function translate(language, key, vars = {}) {
  return (catalogs[language][key] ?? en[key] ?? key).replace(/\{(\w+)\}/g, (match, name) => vars[name] ?? match);
}

export function eventError(t, code, text) {
  const key = 'error.code.' + code;
  return key in en ? t(key) : text;
}

export function errorMessage(t, error, fallback) {
  if (!error) return '';
  if (!error.status) return t('error.request');
  return eventError(t, error.code, fallback ? t(fallback) : error.message);
}

export const langTag = code =>
  code === 'auto' ? undefined : { 'ja-easy': 'ja', 'zh-hans': 'zh-Hans', 'zh-hant': 'zh-Hant' }[code] || code;
