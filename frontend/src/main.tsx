import React from 'react';
import {createRoot} from 'react-dom/client';
import App from './App';
declare const __WB_VERSION__:string;
(window as any).WorkBuddyPanel={version:__WB_VERSION__,sdk:'v8.0.15'};
createRoot(document.getElementById('root')!).render(<App/>);
