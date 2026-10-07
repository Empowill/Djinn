const { chromium } = require('playwright');
const fs = require('node:fs/promises');
const path = require('node:path');
(async()=>{const browser=await chromium.launch({headless:true});try{const page=await browser.newPage({viewport:{width:512,height:512},deviceScaleFactor:2});const svg=await fs.readFile(path.join(__dirname,'../build/icon.svg'),'utf8');await page.setContent(`<style>html,body{margin:0;background:transparent;width:512px;height:512px}svg{display:block;width:512px;height:512px}</style>${svg}`);await page.screenshot({path:path.join(__dirname,'../build/icon.png'),omitBackground:true});}finally{await browser.close()}})().catch(e=>{console.error(e.message);process.exit(1)});
