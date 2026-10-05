import {build} from 'esbuild';
await build({entryPoints:['src/main.tsx'],bundle:true,outfile:'../panel.js',format:'iife',platform:'browser',target:['es2022'],minify:true,legalComments:'inline',define:{'process.env.NODE_ENV':'"production"'}});
console.log('Built panel.js and panel.css from modular React source.');
