#!/usr/bin/env node
'use strict';
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assets=process.env.WEBUI_ASSETS||path.resolve(__dirname,'../internal/admin/static');
(async()=>{
 const browser=await chromium.launch({headless:true});
 try{
  const page=await browser.newPage();const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.route('https://webui.test/**',route=>route.fulfill({contentType:'text/html',body:'<button id="show-commands">Commands</button><textarea id="prompt"></textarea><section id="command-panel" hidden><h2 id="command-title"></h2><button id="close-commands">Close</button><input id="command-search"><div id="command-content"></div><div id="command-suggestions"></div></section>'}));
  await page.goto('https://webui.test/');
  await page.evaluate(()=>{window.CodexFormat={clean:String,markdown:String,node:(tag,cls,text)=>{const e=document.createElement(tag);if(cls)e.className=cls;if(text!==undefined)e.textContent=text;return e;}};});
  for(const file of ['webui-commands.js','webui-command-ui.js'])await page.addScriptTag({content:fs.readFileSync(path.join(assets,file),'utf8')});
  await page.evaluate(()=>{
   window.calls=[];window.failMCP=false;
   window.state={authenticated:true,generation:1,selected:{session_id:'claude-session',codex_thread_id:'native-thread'},connected:true,loading:false,turn:'running-turn'};
   window.commandUI=window.CodexCommandUI.create({state:window.state,isClaude:()=>true,agentLabel:()=> 'Claude Code',composerHidden:()=>false,saveDraft:()=>{},updateControls:()=>{},cancelGatewayCommand:()=>{},applyResult:async()=>{},rpc:async(method,params)=>{
    window.calls.push({method,params});if(window.failMCP)throw new Error('MCP request was not confirmed. Run /mcp to check status.');
    return {text:'Claude Code MCP servers\n• playwright <img src=x onerror="window.injected=true"> — connected · 12 tools'};
   }});
  });
  for(const args of ['', 'verbose','disable playwright','reconnect all','enable playwright']){
   await page.evaluate(args=>window.commandUI.handle('/mcp'+(args?' '+args:'')),args);
   await page.waitForFunction(()=>!window.commandUI.pending);
   assert.match(await page.locator('#command-content').innerText(),/Claude Code MCP servers/);
   assert.equal(await page.locator('#command-content img').count(),0);
  }
  const calls=await page.evaluate(()=>window.calls);
  assert.equal(calls.length,5);
  assert(calls.every(call=>call.method==='gateway/command'&&call.params.name==='mcp'));
  assert.deepEqual(calls.map(call=>call.params.args),['','verbose','disable playwright','reconnect all','enable playwright']);
  assert.equal(await page.evaluate(()=>window.state.turn),'running-turn');
  await page.evaluate(()=>{window.failMCP=true;window.commandUI.handle('/mcp disable playwright');});
  await page.getByText('MCP request was not confirmed. Run /mcp to check status.',{exact:true}).waitFor();
  await page.waitForTimeout(100);assert.equal(await page.evaluate(()=>window.calls.length),6);
  assert.equal(await page.evaluate(()=>window.injected),undefined);assert.deepEqual(errors,[]);
  console.log('Claude /mcp browser routing, active-turn handling, safe output and no-replay checks passed.');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
