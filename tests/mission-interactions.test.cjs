'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),Module=require('node:module'),ts=require('typescript');
function load(relative){const filename=path.resolve(__dirname,'..',relative),m=new Module(filename,module);m.filename=filename;m.paths=Module._nodeModulePaths(path.dirname(filename));const req=m.require.bind(m);m.require=(id)=>{const source=path.resolve(path.dirname(filename),id+'.ts');return id.startsWith('.')&&fs.existsSync(source)?load(path.relative(path.resolve(__dirname,'..'),source)):req(id)};m._compile(ts.transpileModule(fs.readFileSync(filename,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,filename);return m.exports;}
const {restoreMissionInteractions,restoreLegacyReports}=load('src/mission-interactions.ts');
const {taskFixture}=require('./workflow-fixture.cjs'),w=require('../electron/workflow.cjs');
const time='2026-10-07T11:00:00Z';
function mission(overrides={}){const steps=w.createDefaultSteps('task-1');return taskFixture({steps,activeStepId:steps[0].id,selectedStepId:steps[0].id,...overrides});}
function publication(t,type,data,extra={}){return {taskId:t.id,stepId:t.activeStepId,runId:'child',timestamp:time,type,data:{...data,agentId:'lead',parentRunId:'root'},...extra};}
test('acknowledged interactions restore without lifecycle replay, preserving answered decisions and human edits',()=>{
 let t=mission({runId:'root'});
 const events=[publication(t,'question',{id:'q',title:'Choisir',context:'Le PDF complet ou la page ?',options:[{id:'a',label:'PDF',description:'Export'}],blocking:true}),publication(t,'work_item',{id:'a',title:'ET-3083',status:'ready'}),publication(t,'report',{id:'report',status:'ready',summary:'Compteur corrigé',completed:['Code modifié'],remaining:['Recette'],evidence:['Jest PASS']}),publication(t,'step_result',{status:'needs_input',summary:'Arbitrage requis',completed:['Exploration'],remaining:['Choix PDF'],evidence:[]}),publication(t,'artifact',{id:'doc',type:'markdown',title:'Rapport',content:'# Rapport complet'})];
 let restored=restoreMissionInteractions(t,events);
 assert.equal(restored.questions[0].context,'Le PDF complet ou la page ?');assert.equal(restored.workItems[0].status,'ready');assert.equal(restored.reports[0].summary,'Compteur corrigé');assert.equal(restored.steps[0].report.status,'needs_input');assert.equal(restored.stepResult.runId,'root');assert.equal(restored.artifacts[0].type,'document');assert.equal(restored.runId,'root');assert.equal(restored.steps[0].status,'pending','recovery never approves or starts a stage');
 restored.questions[0].answer='PDF';restored.artifacts[0].content='Human decision';restored.artifacts[0].editedBy='human';
 restored=restoreMissionInteractions(restored,events);assert.equal(restored.questions.length,1);assert.equal(restored.questions[0].answer,'PDF');assert.equal(restored.artifacts[0].content,'Human decision');
 const newer=publication(t,'step_result',{status:'ready',summary:'Décision acquise',completed:['Exploration','PDF choisi'],remaining:[],evidence:[]},{timestamp:'2026-10-07T11:01:00Z'});
 restored=restoreMissionInteractions(restored,[newer]);assert.equal(restored.stepResult.status,'ready');
 const stale=publication(t,'work_item',{id:'a',title:'Stale',status:'blocked',parentRunId:'old'});assert.equal(restoreMissionInteractions(restored,[stale]).workItems[0].title,'ET-3083');
});
test('recover an old complete markdown artifact, leaving human quotations and partial frames alone',()=>{
 const t=mission();
 const marker='DJINN_EVENT:'+JSON.stringify({type:'artifact',data:{id:'mission-status',type:'markdown',title:'Recettes séparées',content:'# ET-3083\nPrêt\n# ET-4020\nEn cours'}});
 t.events=[{id:'m',type:'note',title:'Chef',detail:marker,time,stepId:t.activeStepId,agentId:'lead',actor:'agent'},{id:'h',type:'note',title:'Vous',detail:marker.replace('mission-status','quoted'),time,actor:'human'},{id:'partial',type:'note',title:'Chef',detail:'DJINN_EVENT:{"type":"artifact"',time,actor:'agent'}];
 const restored=restoreLegacyReports(t);assert.equal(restored.artifacts.length,1);assert.equal(restored.artifacts[0].id,'mission-status');assert.match(restored.artifacts[0].content,/ET-4020/);
});
test('restored action publications never claim a live server after native ownership has closed',()=>{
 const t=mission();const restored=restoreMissionInteractions(t,[publication(t,'action',{id:'recipe',kind:'server',title:'ET-3083',status:'ready',url:'http://localhost:3000',createdAt:time,updatedAt:time,workItemId:'a'})]);
 assert.equal(restored.actions[0].status,'stopped');assert.equal(restored.actions[0].url,undefined);assert.equal(restored.actions[0].workItemId,'a');
});
test('legacy native notes without actor recover reports only with agent and run provenance',()=>{
 const t=mission();
 const marker='DJINN_EVENT:'+JSON.stringify({type:'artifact',data:{id:'native-report',type:'markdown',title:'Dernier rapport',content:'Recette date à vérifier'}});
 t.events=[{id:'native',type:'note',title:'Chef',detail:marker,time,stepId:t.activeStepId,agentId:'lead',runId:'native-run'},
 {id:'anonymous',type:'note',title:'Note',detail:marker.replace('native-report','anonymous'),time},
 {id:'human',type:'note',title:'Vous',detail:marker.replace('native-report','human-quote'),time,actor:'human',agentId:'lead',runId:'native-run'}];
 const restored=restoreLegacyReports(t);
 assert.deepEqual(restored.artifacts.map(a=>a.id),['native-report']);
 assert.equal(restored.artifacts[0].content,'Recette date à vérifier');
});
