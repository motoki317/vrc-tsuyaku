import { setImmediate } from 'node:timers/promises';
import { flush } from 'solid-js';

export function fixture() {
  return {
    launch: 'test',
    instance: { state: '' },
    artifacts: Object.fromEntries(
      ['runtime', 'model', 'en-asr', 'ja-asr'].map((id, i) => [
        id,
        { state: 'missing', artifact: { Name: id, Sources: [{ Size: 1000 * (i + 1) }], MemoryBytes: 1e8 * (i + 1) } },
      ]),
    ),
    flows: { self: { state: 'stopped' }, world: { state: 'stopped' } },
    app: { repository: 'https://example.test' },
    update: { state: 'checking' },
    languages: {
      targets: ['en', 'ja', 'ja-easy'],
      variants: { 'ja-easy': 'ja' },
      commonArtifacts: ['runtime'],
      spoken: [
        { code: 'en', recognizer: 'en-asr' },
        { code: 'ja', recognizer: 'ja-asr' },
      ],
      translators: [{ id: 'small', artifact: 'model' }],
    },
    glossaryLimit: 100,
    defaultTemplate: '[{{.SourceLang}}] {{.Original}}\n[{{.TargetLang}}] {{.Translation}}',
    reading: { base: { min: 1, max: 10 }, factor: { min: 1, max: 8 } },
    settings: {
      Glossary: [],
      Chatbox: { Address: '127.0.0.1:9000', Template: '{{.Translation}}', ReadingBase: 2, ReadingFactor: 2 },
      UI: { Language: 'en' },
      Translator: 'small',
      Flows: { Self: { Spoken: 'en', Target: 'ja' }, World: { Spoken: 'ja', Target: 'en' } },
    },
  };
}
export const response = (body = {}, status = 200) => Response.json(body, { status });
export async function settle() {
  await setImmediate();
  flush();
}
export function stream() {
  let controller;
  const body = new ReadableStream({
    start(c) {
      controller = c;
    },
  });
  return {
    response: new Response(body),
    send: text => controller.enqueue(new TextEncoder().encode(text)),
    fail: () => controller.error(new Error('disconnected')),
    close: () => controller.close(),
  };
}
