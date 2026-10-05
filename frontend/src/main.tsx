import React from 'react';
import {createRoot} from 'react-dom/client';
import App from './App';
(window as any).WorkBuddyPanel={version:'v8.0.15-2.0.0-rebuild.1',sdk:'v8.0.15'};
createRoot(document.getElementById('root')!).render(<App/>);
