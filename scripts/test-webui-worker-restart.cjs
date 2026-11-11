#!/usr/bin/env node
'use strict';
// Focused command-menu regression test. No worker or service is restarted.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assets = process.env.WEBUI_ASSETS || path.resolve(__dirname, '../internal/admin/static');
(async () => {
 const browser = await chromium.launch({headless:true});
 try {
  const page = await browser.newPage();
  const errors=[]; page.on('pageerror', error => errors.push(error.message));
  await page.route('https://webui.test/**', route => route.fulfill({contentType:'text/html',body:`<!doctype html><button id="show-commands">Commands</button><textarea id="prompt"></textarea><section id="command-panel" hidden><h2 id="command-title"></h2><button id="close-commands">Close</button><input id="command-search"><div id="command-content"></div><div id="command-suggestions"></div></section>`}));
  await page.goto('https://webui.test/');
  await page.evaluate(() => {
   window.CodexFormat={clean:String,markdown:String,node:(tag,cls,text)=>{const el=document.createElement(tag);if(cls)el.className=cls;if(text!==undefined)el.textContent=text;return el;}};
  });
  for (const file of ['webui-commands.js','webui-command-ui.js']) await page.addScriptTag({content:fs.readFileSync(path.join(assets,file),'utf8')});
  await page.evaluate(() => {
   window.calls=[];window.nativeCalls=[];window.failRestart=false;
   window.state={authenticated:true,generation:1,selected:{session_id:'session-a'},connected:false,loading:true,turn:'active-turn'};
   window.commandUI=window.CodexCommandUI.create({state:window.state,isClaude:()=>true,composerHidden:()=>false,saveDraft:()=>{},updateControls:()=>{},cancelGatewayCommand:()=>{},rpc:(...args)=>{window.nativeCalls.push(args);throw new Error('Restart must never call the model');},gatewayCommand:async(name,options)=>{
    window.calls.push({name,options});
    if(options.prepare)return {restart:{session_id:'session-a',worker_id:'pi',worker_name:'Raspberry Pi <img src=x onerror="window.injected=true">',runtime_id:'claude',generation:7}};
    if(window.failRestart)throw new Error('Connection lost. Request may have been accepted; check worker status.');
    await new Promise(resolve=>setTimeout(resolve,50));
    return {text:'Worker restart requested.'};
   }});
  });
  const run = args => page.evaluate(args => window.commandUI.handle('/tgrestartworker'+(args?' '+args:'')),args||'');
  const button = page.getByRole('button',{name:'Restart worker',exact:true});
  // Available during an active turn or a broken native connection; safe Cancel.
  await run();await button.waitFor();
  assert.match(await page.locator('#command-content').innerText(),/Raspberry Pi.*ALL sessions/s);
  assert.equal(await page.locator('#command-content img').count(),0);
  await page.getByRole('button',{name:'Cancel',exact:true}).click();
  assert.equal((await page.evaluate(()=>window.calls)).length,1);
  // One explicit confirmation produces one fixed maintenance request.
  await run();await button.click();await page.locator('#command-content').getByText('Worker restart requested.',{exact:true}).waitFor();
  const calls=await page.evaluate(()=>window.calls);
  const posts=calls.filter(call=>!call.options.prepare);
  assert.equal(posts.length,1);assert.equal(posts[0].name,'tgrestartworker');
  assert.equal(posts[0].options.restart.worker_id,'pi');assert.equal(posts[0].options.restart.generation,7);
  assert.match(posts[0].options.requestID,/^[a-f0-9-]{36}$/);
  assert.deepEqual(await page.evaluate(()=>window.nativeCalls),[]);
  // Changing sessions after opening a confirmation cannot retarget it.
  await run();await button.waitFor();
  await page.evaluate(()=>{window.state.selected={session_id:'session-b'};});
  await button.click();await page.getByText('The selected session changed. Start a new confirmation.',{exact:true}).waitFor();
  assert.equal(await page.evaluate(()=>window.calls.filter(call=>!call.options.prepare).length),1);
  await page.evaluate(()=>{window.state.selected={session_id:'session-a'};window.state.generation++;window.commandUI.sessionChanged();window.failRestart=true;});
  // Lost acknowledgement never triggers an automatic retry.
  await run();await button.click();await page.getByText('Connection lost. Request may have been accepted; check worker status.',{exact:true}).waitFor();
  await page.waitForTimeout(150);
  assert.equal(await page.evaluate(()=>window.calls.filter(call=>!call.options.prepare).length),2);
  const before=await page.evaluate(()=>window.calls.length);
  await run('other-host');await page.getByText('Use /tgrestartworker without arguments.',{exact:true}).waitFor();
  await page.evaluate(()=>{window.state.selected=null;});await run();await page.getByText('Select a session first.',{exact:true}).waitFor();
  assert.equal(await page.evaluate(()=>window.calls.length),before);
  assert.equal(await page.evaluate(()=>window.injected),undefined);
  assert.deepEqual(errors,[]);
  console.log('Worker restart WebUI confirmation, cancellation, fixed target and no-retry checks passed.');
 } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
