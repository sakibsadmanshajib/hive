/*
 * Visual proof for issue #1623: one composer, no pack toggle, and the kind of
 * task decided from what was written rather than from a control.
 *
 * WHAT IS REAL AND WHAT IS STUBBED, stated before anything is claimed.
 *
 * Real: the whole front end. The container, the bundle built by the same
 * `npm run build` the deploy runs, the composer, the Cowork row, the submit
 * path, the request body the browser puts on the wire, the transcript turn and
 * the progress line it renders.
 *
 * Stubbed: two responses. The model list, because submitHandler refuses a
 * submission before it reaches the Cowork branch when no model is selected and
 * a standalone container has no provider. And the answer to POST
 * /api/v1/hive/agent/tasks, because there is no edge-api behind this
 * container.
 *
 * The stub does not decide anything this proof claims. The `pack` it answers
 * with, for each of the two instruction strings below, is the value
 * apps/control-plane/internal/agenttask/infer.go returns for that exact
 * string, and both strings are asserted against that function in
 * infer_test.go, which runs in CI. So the frame shows the front end rendering
 * a decision the Go inference is mechanically held to, not a number this
 * script invented. The half that is unstubbed and is the actual regression
 * evidence is recorded too: the request body carries no `pack` key at all.
 */
import { chromium } from 'playwright';

const OUT = '/out';
const BASE = process.env.OWUI_URL;
const password = process.env.PROOF_PASSWORD;
const email = 'proof1623@hive.invalid';

// Keep these byte for byte identical to the two strings pinned in
// apps/control-plane/internal/agenttask/infer_test.go.
const CODING = 'Refactor the retry helper in server.go and run the test suite.';
const KNOWLEDGE =
  'Write a one page brief on how prepaid credit billing works for a new team member.';
const EXPECTED = { [CODING]: 'coding-pack', [KNOWLEDGE]: 'knowledge-work-pack' };

const log = [];
const say = (line) => log.push(line);

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 2 });
page.on('console', (m) => say(`console:${m.type()}: ${m.text()}`));

const signup = await fetch(`${BASE}/api/v1/auths/signup`, {
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ name: 'Proof 1623', email, password })
});
const account = await signup.json();
if (!account.token) throw new Error(`signup failed: ${signup.status}`);
say(`signup status ${signup.status}, session token received (redacted)`);

await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded', timeout: 120000 });
await page.context().addCookies([{ name: 'token', value: account.token, url: BASE }]);
await page.evaluate((t) => localStorage.setItem('token', t), account.token);

// A model, so submitHandler reaches the Cowork branch instead of refusing.
await page.route('**/api/models**', (route) =>
  route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      data: [{ id: 'hive-auto', name: 'Hive Auto', object: 'model', owned_by: 'hive' }]
    })
  })
);

const submissions = [];
let taskSeq = 0;
await page.route('**/api/v1/hive/agent/tasks', async (route) => {
  const request = route.request();
  if (request.method() !== 'POST') return route.continue();
  const body = JSON.parse(request.postData() ?? '{}');
  const pack = EXPECTED[body.instructions];
  submissions.push({ body, pack });
  if (!pack) {
    return route.fulfill({ status: 500, contentType: 'application/json', body: '{}' });
  }
  const now = new Date().toISOString();
  return route.fulfill({
    status: 201,
    contentType: 'application/json',
    body: JSON.stringify({
      id: `00000000-0000-4000-8000-00000000000${++taskSeq}`,
      pack,
      instructions: body.instructions,
      status: 'queued',
      created_at: now,
      updated_at: now
    })
  });
});
// The follower polls status and events; answer both so the turn settles
// instead of shimmering through the screenshot.
await page.route('**/api/v1/hive/agent/tasks/*', async (route) => {
  const url = route.request().url();
  if (url.endsWith('/events') || url.includes('/events?')) {
    return route.fulfill({ status: 200, contentType: 'application/json', body: '{"events":[]}' });
  }
  return route.continue();
});

await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded', timeout: 120000 });
await page.waitForTimeout(8000);

const coworkSegment = page.locator('[data-hive-mode="cowork"]').first();
await coworkSegment.waitFor({ state: 'visible', timeout: 30000 });
await coworkSegment.click();
await page.waitForTimeout(1500);

say('');
say('STEP 1  the composer in Cowork mode, before anything is typed');
say(`  Chat/Cowork segments present   -> ${await page.locator('[data-hive-mode]').count()}`);
say(`  pack segments present          -> ${await page.locator('[data-hive-pack]').count()}`);
say(`  pack radiogroups present       -> ${await page.locator('[data-hive-composer-pack]').count()}`);
say(`  cowork row present             -> ${await page.locator('[data-hive-cowork-row]').count()}`);
say(`  inference disclosure present   -> ${await page.locator('[data-hive-pack-inferred]').count()}`);
say(`  clickable "Knowledge work"     -> ${await page.getByRole('button', { name: /Knowledge work/ }).count()}`);
say(`  clickable "Coding"             -> ${await page.getByRole('button', { name: /^Coding$/ }).count()}`);
say(`  cowork row text                -> ${(await page.locator('[data-hive-cowork-row]').innerText()).replace(/\n/g, ' | ')}`);
await page.screenshot({ path: `${OUT}/1623-01-composer-no-pack-toggle.png` });

const submit = async (text) => {
  const input = page.locator('#chat-input, [contenteditable="true"]').first();
  await input.click();
  await input.fill(text);
  await page.waitForTimeout(400);
  await input.press('Enter');
  await page.waitForTimeout(6000);
};

say('');
say('STEP 2  a coding shaped request, with nothing chosen by the user');
say(`  typed: ${CODING}`);
await submit(CODING);
say(`  request body the browser sent  -> ${JSON.stringify(submissions.at(-1)?.body)}`);
say(`  "pack" key present in body     -> ${Object.prototype.hasOwnProperty.call(submissions.at(-1)?.body ?? {}, 'pack')}`);
say(`  pack the inference returns     -> ${submissions.at(-1)?.pack}`);
const transcript1 = await page.locator('body').innerText();
say(`  transcript says                -> ${(transcript1.match(/Hive ran this as a [a-z ]+ task\./) ?? ['(not found)'])[0]}`);
say(`  cowork row now says            -> ${(await page.locator('[data-hive-cowork-row]').innerText()).replace(/\n/g, ' | ')}`);
say(`  correction button offers       -> ${await page.locator('[data-hive-pack-override]').getAttribute('data-hive-pack-override')}`);
await page.screenshot({ path: `${OUT}/1623-02-coding-request-inferred.png` });

say('');
say('STEP 3  a knowledge shaped request, in a new conversation, again nothing chosen');
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded', timeout: 120000 });
await page.waitForTimeout(6000);
await page.locator('[data-hive-mode="cowork"]').first().click();
await page.waitForTimeout(1200);
say(`  typed: ${KNOWLEDGE}`);
await submit(KNOWLEDGE);
say(`  request body the browser sent  -> ${JSON.stringify(submissions.at(-1)?.body)}`);
say(`  "pack" key present in body     -> ${Object.prototype.hasOwnProperty.call(submissions.at(-1)?.body ?? {}, 'pack')}`);
say(`  pack the inference returns     -> ${submissions.at(-1)?.pack}`);
const transcript2 = await page.locator('body').innerText();
say(`  transcript says                -> ${(transcript2.match(/Hive ran this as a [a-z ]+ task\./) ?? ['(not found)'])[0]}`);
await page.screenshot({ path: `${OUT}/1623-03-knowledge-request-inferred.png` });

say('');
say('STEP 4  correcting a guess, which is what makes being wrong cheap');
await page.locator('[data-hive-pack-override]').first().click();
await page.waitForTimeout(800);
say(`  cowork row now says            -> ${(await page.locator('[data-hive-cowork-row]').innerText()).replace(/\n/g, ' | ')}`);
say(`  pending override               -> ${await page.locator('[data-hive-pack-pending]').getAttribute('data-hive-pack-pending')}`);
await page.screenshot({ path: `${OUT}/1623-04-correction-pending.png` });

say('');
say(`url under test: ${BASE}  (throwaway local container, no credential in any URL)`);
console.log(log.join('\n'));
await browser.close();
