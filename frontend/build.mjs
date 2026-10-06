import {build} from 'esbuild';
import fs from 'node:fs';
await build({entryPoints:['src/main.tsx'],bundle:true,outfile:'../panel.js',format:'iife',platform:'browser',target:['es2022'],minify:true,legalComments:'inline',define:{'process.env.NODE_ENV':'"production"','__WB_VERSION__':JSON.stringify(fs.readFileSync('../VERSION','utf8').trim())}});
console.log('Built panel.js and panel.css from modular React source.');
