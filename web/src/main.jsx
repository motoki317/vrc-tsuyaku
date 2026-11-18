import { render } from '@solidjs/web';
import { createClient } from './client.js';
import { Frame } from './frame.jsx';
import sprite from '../icons.svg?raw';
import './style.css';

const icons = new DOMParser().parseFromString(sprite, 'image/svg+xml').documentElement;
document.body.prepend(document.importNode(icons, true));
render(() => {
  const app = createClient();
  window.addEventListener('pagehide', app.closing);
  app.listen(app.log.event, app.log.closePending);
  return <Frame app={app} />;
}, document.body);
