import solid from '@solidjs/vite-plugin';

export default {
  // Chromium exposes an extra accessibility node when the body handles delegated clicks.
  plugins: [solid({ solid: { delegateEvents: false } })],
  build: {
    modulePreload: false,
    license: { fileName: 'licenses.json' },
    rolldownOptions: { output: { entryFileNames: 'app.js', assetFileNames: 'style.css' } },
  },
  test: { environment: 'jsdom' },
};
