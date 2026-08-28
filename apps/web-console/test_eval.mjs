import { chromium } from '@playwright/test';
async function run() {
  const b = await chromium.launch();
  const p = await b.newPage();
  await p.setContent('<div>hi</div>');
  try {
    await p.locator('.nonexistent').evaluateAll(nodes => nodes.length);
    console.log('did not throw');
  } catch(e) {
    console.log('threw', e.message);
  }
  await b.close();
}
run();
