"use strict";
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs/promises'),os=require('node:os'),path=require('node:path');
const {inspectTestEnvironment}=require('../electron/test-environment.cjs');
test('native preview diagnostics use fixed read-only commands and confined local probes',async()=>{
 const root=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'djinn-diagnostics-')));
 try {
  const dir=path.join(root,'.worktrees/ticket/app'); await fs.mkdir(dir,{recursive:true});
  await fs.writeFile(path.join(dir,'package.json'),JSON.stringify({scripts:{dev:'vite',test:'jest'}}));
  const commands=[],probes=[];
  const result=await inspectTestEnvironment(root,{directory:'.worktrees/ticket/app',urls:['http://localhost:3000']},{execute:async(...args)=>{commands.push(args);return {stdout:'console\t3000->3000/tcp'}},probe:async(url)=>{probes.push(url);return true}});
  assert.deepEqual(result.scripts,['dev','test']);
  assert.equal(result.containers.available,true);assert.equal(result.servers[0].reachable,true);
  assert.equal(commands[0][0],'docker');assert.deepEqual(commands[0][1],['ps','--format','{{.Names}}\t{{.Ports}}']);
  assert.equal(commands[0][2].shell,false);assert.ok(commands[0][2].env.DOCKER_HOST.startsWith('unix://'));assert.equal(commands[0][2].env.DOCKER_CONTEXT,undefined);
  assert.equal(probes[0],'http://127.0.0.1:3000/');
  for(const input of [{command:'rm -rf .'},{directory:'../'},{urls:['https://example.com']},{urls:['http://localhost:3000/api/delete']},{urls:['http://localhost:3000/?delete=true']}]) await assert.rejects(()=>inspectTestEnvironment(root,input));
  await fs.symlink(os.tmpdir(),path.join(root,'escape'));await assert.rejects(()=>inspectTestEnvironment(root,{directory:'escape'}),/sort du projet/);
 } finally {await fs.rm(root,{recursive:true,force:true});}
});
test('an unavailable Docker daemon is evidence, not a permission request or an unverified readiness claim',async()=>{
 const root=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'djinn-no-docker-')));
 try {
  const result=await inspectTestEnvironment(root,{}, {execute:async()=>{throw Object.assign(new Error('missing'),{code:'ENOENT'})}});
  assert.equal(result.containers.available,false);assert.match(result.containers.detail,/indisponible/);assert.deepEqual(result.servers,[]);
 } finally {await fs.rm(root,{recursive:true,force:true});}
});
