import { expect, test } from 'vitest';
import { swappedLanguages } from '../src/languages.js';

const languages = {
  spoken: ['ja', 'en', 'de', 'pt', 'sv', 'auto'].map(code => ({ code, auto: code === 'auto' })),
  targets: ['ja', 'ja-easy', 'en', 'de', 'ko'],
  variants: { 'ja-easy': 'ja' },
};

test.each(['self', 'world'])('%s swaps supported languages and maps target variants to their spoken base', flow => {
  for (const [Spoken, Target, base] of [
    ['ja', 'en', 'en'],
    ['en', 'ja', 'ja'],
    ['de', 'en', 'en'],
    ['en', 'ja-easy', 'ja'],
    ['ja', 'ja-easy', 'ja'],
    ['en', 'en', 'en'],
  ]) {
    expect(swappedLanguages(languages, flow, { Spoken, Target })).toEqual({ Spoken: base, Target: Spoken });
  }
  for (const [Spoken, Target] of [
    ['auto', 'en'],
    ['pt', 'ja'],
    ['sv', 'ja'],
    ['en', 'ko'],
  ]) {
    expect(swappedLanguages(languages, flow, { Spoken, Target })).toBeNull();
  }
  expect(
    swappedLanguages({ ...languages, targets: [...languages.targets, 'auto'] }, flow, { Spoken: 'auto', Target: 'en' }),
  ).toBeNull();
  expect(swappedLanguages(languages, flow, { Spoken: 'en', Target: 'ja' }, 'invalid')).toBeNull();
});
