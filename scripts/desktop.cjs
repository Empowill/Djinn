const { spawn } = require('node:child_process');
const http = require('node:http');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const vite = spawn(process.execPath, [path.join(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4317'], {cwd:root,stdio:'inherit'});
let electron; let stopped=false;
function stop(){if(stopped)return;stopped=true;vite.kill();electron?.kill();}
process.on('SIGINT',stop);process.on('SIGTERM',stop);process.on('exit',stop);
const deadline=Date.now()+30000;
function ready(){ const req=http.get('http://127.0.0.1:4317',r=>{r.resume();electron=spawn(require('electron'),[root],{cwd:root,stdio:'inherit',env:{...process.env,DJINN_DEV_URL:'http://127.0.0.1:4317'}});electron.on('exit',code=>{stop();process.exit(code||0)});});req.on('error',()=>{if(Date.now()>deadline){stop();process.exit(1)}else setTimeout(ready,250)}); }
ready();
