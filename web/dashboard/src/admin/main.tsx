import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import '../styles.css';
import './admin.css';
import { Console } from './Console';

const root = document.getElementById('root');
if (!root) {
  throw new Error('missing #root element');
}
createRoot(root).render(
  <StrictMode>
    <Console />
  </StrictMode>,
);
