// Visual proof of the signed-in Hive chat surface.
//
// Runs AFTER the `owui-setup` Playwright project, which is what mints the
// session: it walks the real "Continue with Hive" journey through GoTrue's
// authorize endpoint, the console's consent screen and back, installs the
// hive_jwt_forward Functions filter, and saves the result as storage state.
// This file loads that state into a fresh browser and photographs the surface.
//
// WHY THIS IS A PLAIN SCRIPT AND NOT ANOTHER SPEC
//
// Nothing here asserts product behaviour, so nothing here wants a test
// reporter. It wants stamp.mjs, which is the one implementation of the proof
// footer this repository stamps into a capture, and which is an .mjs module in
// apps/agent-console. A spec would have to reach across apps into an untyped
// module from TypeScript; a script imports it in one line. The captures also
// have to land in docs/proof/, and a Playwright HTML reporter CLEARS its own
// output directory before writing, which silently deleted a whole capture pass
// on PR #951.
//
// WHAT IT REFUSES TO DO
//
// It never passes without an image. A missing chat input, a sign-in page where
// a signed-in surface was expected, or an assistant turn that never arrives all
// exit non-zero, because the point of a proof job is proof, and a green run
// with an empty capture directory is worse than a red one: it carries
// authority it did not earn.

import { mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { commitStamp, loadChromium, shoot } from "../../../../agent-console/proof/harness/stamp.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));

const OWUI_URL = (process.env.OWUI_URL ?? "http://localhost:3003").replace(/\/$/, "");
const OUT_DIR = process.env.PROOF_OUT;
const NOTE = process.env.PROOF_NOTE ?? "";
const STATE = process.env.PROOF_STORAGE_STATE ?? join(HERE, ".auth", "owui-user.json");
// Names the capture files, so two scenarios' images never collide in the
// release the proof script uploads them to.
const SCENARIO = (process.env.PROOF_SCENARIO ?? "chat").trim() || "chat";
// The prompt is deliberately one whose right answer is one word, so "the model
// replied" is checkable rather than eyeballed. Same reasoning, and the same
// question, as 01-chat-send-stream.spec.ts.
//
// Both are overridable, because one question cannot serve two claims. The
// default proves the surface is healthy, which is what every chat pull request
// asks for. A feature capture needs a question the model cannot answer from
// memory, and the answer to one of those is not a single word a pattern can
// check; see REQUIRE_SOURCES below for what carries the assertion there.
const PROMPT = process.env.PROOF_PROMPT ?? "What colour is a banana? Reply with only the single colour word.";
const REPLY_PATTERN = new RegExp(process.env.PROOF_REPLY_PATTERN ?? "yellow", "i");

// The web tools claim (issue #1718). Both off by default, so the capture that
// runs on every chat pull request is unchanged by their existence.
//
// REQUIRE_SOURCES makes the assistant turn's own source list the assertion. A
// turn that rendered sources called a tool, and a turn that called a tool on a
// message where nothing was toggled is the whole claim under proof. It is also
// the half issue #1621 reported missing, so it is the element that has to be in
// the frame rather than merely inferred from a good answer.
//
// CONTROL_MODEL is the negative half: an alias the catalog reports as not tool
// capable, where the same question must settle with no source list at all. It
// is what separates "the tools are offered when the route can serve them" from
// "the tools are offered unconditionally".
const REQUIRE_SOURCES = process.env.PROOF_REQUIRE_SOURCES === "1";

// The knowledge-work claim (issue #1505). Off by default, so every other chat
// pull request's capture is unchanged by its existence.
//
// When on, the capture stops being a photograph of a chat that was already
// possible and becomes a photograph of the whole journey the issue said an
// ordinary customer could not start: create a knowledge base, put a document
// in it, and get an answer that could only have come from that document. The
// account is the same ordinary signed-in user every other scenario uses, with
// no admin role and no hand-granted permission, which is the half that matters
// here: the permission the fix grants reaches this run through the deployment's
// own configuration or the New project button answers 401 in the frame.
const CAPTURE_PROJECT = process.env.PROOF_CAPTURE_PROJECT === "1";
// The document's single load-bearing fact. Invented rather than looked up, so
// a model that answers it from memory is not possible and a correct answer is
// evidence of retrieval rather than of recall.
const PROJECT_DOC_TEXT =
  process.env.PROOF_PROJECT_DOC_TEXT ??
  "Hive demo field note KB-1505.\n\nBorehole KB-1505 measured 47 milligrams per litre of dissolved silica on 2026-09-01.\n";
const PROJECT_DOC_NAME = process.env.PROOF_PROJECT_DOC_NAME ?? "kb-1505-field-note.txt";
const CONTROL_MODEL = (process.env.PROOF_CONTROL_MODEL ?? "").trim();
// Which alias the captured turn is allowed to run on.
//
// This is a money guard, not a cosmetic one. The workflow runs on every pull
// request touching a chat path, and the composer's model is whatever Open WebUI
// resolved, which is not necessarily what the deployment asked for: on run
// 33677630521 the run tenant could not see any free alias, Open WebUI silently
// fell back to the first one it could, and the captured turn ran on a paid
// model. Nothing failed, nothing said so, and the only evidence was a model
// name in a screenshot. The owner's 2026-08-30 directive is that CI does not
// spend on paid aliases, so this asserts the name rather than trusting the
// configuration to have taken.
const MODEL_PATTERN = new RegExp(process.env.PROOF_MODEL_PATTERN ?? "free", "i");

// The custom-instructions claim (issue #1363). Off by default, so the capture
// that runs on every chat pull request is unchanged by its existence.
//
// Set it to the instruction text and the run writes that text into Settings
// before it sends anything, photographs the saved pane, and then holds the
// assistant's reply to PROOF_REPLY_PATTERN. That pairing is the whole proof:
// an instruction nobody could satisfy by accident, and a reply to an unrelated
// question that satisfies it anyway. A screenshot of a filled-in textarea alone
// would prove a textarea, which is not the claim.
const SET_INSTRUCTIONS = (process.env.PROOF_SET_INSTRUCTIONS ?? "").trim();

if (!OUT_DIR) {
  console.error("::error::PROOF_OUT is not set, so there is nowhere to write the capture");
  process.exit(2);
}

const log = [];
function record(line) {
  const stamped = `${new Date().toISOString()}  ${line}`;
  log.push(stamped);
  console.log(stamped);
}

/**
 * A URL with its query string and fragment removed.
 *
 * The journey this harness photographs passes through an OAuth authorize
 * round trip, and those URLs carry `code`, `authorization_id` and an
 * access_token fragment. `npm run lint:proof-tokens` would catch a bare one
 * in this log and fail the build, which is the right backstop and the wrong
 * place to discover it. Dropping everything after the path means the log
 * still says where the browser was and can never say with what.
 */
function safeUrl(raw) {
  try {
    const url = new URL(raw);
    return `${url.origin}${url.pathname}`;
  } catch {
    return "(unparseable url)";
  }
}

/**
 * The assistant turn's "N Sources" control, which Open WebUI renders only when
 * the turn carries sources at all (Citations.svelte). Its aria-label is the
 * stable half: the visible text is "1 Source" or "3 Sources", while the label
 * is always "Toggle N source(s)".
 */
function sourcesToggle(scope) {
  return scope.getByRole("button", { name: /toggle \d+ sources?/i });
}

/**
 * Sends PROMPT in the composer and returns the request Open WebUI made, so the
 * caller can read what was actually on the wire rather than infer it from the
 * page. `features` is where the composer's toggles land, which is what makes
 * "the globe was off" a checked fact instead of a claim about pixels.
 */
async function sendPrompt(page) {
  await page.locator("#chat-input").fill(PROMPT);
  const pending = page.waitForRequest(
    (request) =>
      request.method() === "POST" && request.url().includes("/api/chat/completions"),
    { timeout: 30_000 },
  );
  await page.keyboard.press("Enter");
  return pending;
}

/**
 * Waits for the assistant turn to finish streaming and returns it with its
 * text. The Copy button appears on an assistant turn only once the stream has
 * finished, which is what makes it the signal that a reply arrived rather than
 * that a spinner did.
 */
async function settledTurn(page) {
  const turn = page.getByRole("listitem").last();
  await turn.getByRole("button", { name: "Copy" }).waitFor({ state: "visible", timeout: 180_000 });
  const text = (await turn.innerText()).replace(/\s+/g, " ").trim();
  return { turn, text };
}

/**
 * Creates a knowledge base as the signed-in ordinary user, puts a document in
 * it, and leaves the browser on a new chat bound to it (issue #1505).
 *
 * Every control it touches is addressed by the id the Hive-authored surface
 * gives it (ProjectsList.svelte, ProjectDetail.svelte) rather than by visible
 * text, so a copy change does not silently turn this into a capture of nothing.
 *
 * It throws rather than skipping at every step. The whole claim is that the
 * journey is possible for this account, so a missing create control or a 401
 * on create is the finding, not a reason to photograph the next screen.
 */
async function createProjectWithDocument(page, shots) {
  const name = `KB-1505 proof ${new Date().toISOString().replace(/[:.]/g, "-")}`;

  // The account's own role, measured rather than argued. It is the load bearing
  // claim of this whole capture: an admin's create call answers 200 too, so
  // without this line nothing in the frames or the log separates "the
  // permission reached this deployment through its own configuration" from
  // "this account never needed the permission". Read from the session endpoint
  // the shell itself reads, on the page's origin, with the session's own token.
  const role = await page.evaluate(async () => {
    const res = await fetch("/api/v1/auths/", {
      headers: { authorization: `Bearer ${localStorage.getItem("token") ?? ""}` },
    });
    if (!res.ok) return `(unreadable: ${res.status})`;
    return (await res.json())?.role ?? "(absent)";
  });
  record(`signed-in account role, read from the session: ${role}`);
  if (role !== "user") {
    throw new Error(
      `the capturing account's role is "${role}", not "user", so this run cannot show that an ORDINARY customer can create a knowledge base`,
    );
  }

  await page.goto(`${OWUI_URL}/projects`, { waitUntil: "domcontentloaded" });
  const newProject = page.locator("#projects-new-button");
  try {
    await newProject.waitFor({ state: "visible", timeout: 30_000 });
  } catch {
    throw new Error(
      "the Projects page offers no create control to this account, which is the state issue #1505 reported on /knowledge",
    );
  }
  await newProject.click();
  await page.locator("#projects-create-name").fill(name);
  // The response, not the page, because a 401 from POST /api/v1/knowledge/create
  // is exactly what the permission fix is about and the dialog reports it only
  // as "Request failed (401)" in small red text a screenshot can miss.
  const created = page.waitForResponse(
    (response) =>
      response.url().includes("/api/v1/knowledge/create") && response.request().method() === "POST",
    { timeout: 60_000 },
  );
  await page.getByRole("button", { name: /^create$/i }).click();
  const createResponse = await created;
  record(`POST /api/v1/knowledge/create answered ${createResponse.status()}`);
  if (!createResponse.ok()) {
    throw new Error(
      `creating a knowledge base answered ${createResponse.status()}, so an ordinary account still cannot author one (issue #1505)`,
    );
  }

  await page.waitForURL(/\/projects\/[^/]+$/, { timeout: 30_000 });
  const projectId = new URL(page.url()).pathname.split("/").pop();
  record(`project created: ${name} (${projectId})`);
  shots.push(await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "01b-project-created", note: NOTE }));

  const docPath = join(tmpdir(), PROJECT_DOC_NAME);
  writeFileSync(docPath, PROJECT_DOC_TEXT);
  await page.locator('input[type="file"]').first().setInputFiles(docPath);
  // The file list is rendered from the collection's own files endpoint after
  // the upload and the add both return, so the name appearing there means the
  // document reached the store rather than merely the browser.
  await page.getByText(PROJECT_DOC_NAME, { exact: false }).first().waitFor({
    state: "visible",
    timeout: 180_000,
  });
  record(`document added to the project: ${PROJECT_DOC_NAME}`);
  shots.push(await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "01c-document-added", note: NOTE }));

  await page.locator("#project-new-chat-button").click();
  await page.waitForURL(/[?&]project=/, { timeout: 30_000 });
  await page.locator("#chat-input").waitFor({ state: "visible", timeout: 60_000 });
  record(`new chat bound to the project: ${safeUrl(page.url())}?project=${projectId}`);
}

/**
 * Opens Settings, writes `text` into the custom-instructions box and saves.
 *
 * Every step is fatal on failure rather than skipped. A run that could not find
 * the control, or could not save, must not go on to photograph a reply and
 * present it as proof that instructions were applied: the reply would then be
 * evidence of nothing, and it would look identical to a working one.
 *
 * The placeholder is the selector because it belongs to this control and to no
 * other. The pane's heading is a translated string and its position moves; the
 * placeholder is Hive's own and is asserted by
 * vendor/open-webui/src/lib/hive/customInstructions.test.ts, so a rename breaks
 * a unit test before it breaks this capture.
 */
async function writeCustomInstructions(page, text) {
  await page.getByRole("button", { name: /user menu|open user profile menu/i }).first().click();
  const settingsItem = page.getByText(/^settings$/i).first();
  await settingsItem.waitFor({ state: "visible", timeout: 15_000 });
  await settingsItem.click();

  const box = page.getByPlaceholder(/for example: be concise/i);
  await box.waitFor({ state: "visible", timeout: 20_000 });
  // Enabled only once the read from edge-api has landed, so waiting for it is
  // what stops this filling a box that is about to be overwritten by the load.
  await box.and(page.locator(":not([disabled])")).waitFor({ state: "visible", timeout: 20_000 });
  await box.fill(text);
  record(`custom instructions typed: ${JSON.stringify(text)}`);
  return box;
}

async function saveSettings(page) {
  const save = page.getByRole("button", { name: /^save$/i }).last();
  await save.waitFor({ state: "visible", timeout: 15_000 });
  await save.click();
  // The pane reports a save failure as a toast and keeps the modal open. If one
  // appears, this capture has to stop: a run that swallowed it would go on to
  // photograph a reply shaped by nothing.
  const failure = page.getByText(/could not be saved|sign in again/i).first();
  if (await failure.isVisible({ timeout: 3_000 }).catch(() => false)) {
    throw new Error(`saving the settings pane reported a failure: ${await failure.innerText()}`);
  }
  record("settings saved");
}

async function main() {
  mkdirSync(OUT_DIR, { recursive: true });
  const chromium = loadChromium();
  const browser = await chromium.launch();
  const context = await browser.newContext({
    storageState: STATE,
    viewport: { width: 1440, height: 900 },
    // The stack under proof serves plain http on the runner; this is here for
    // the JWKS front's local authority, which the browser may meet on a
    // redirect. It narrows nothing about what is being proven: the surface is
    // the chat page, not the transport.
    ignoreHTTPSErrors: true,
  });
  const page = await context.newPage();
  const shots = [];

  try {
    record(`commit under proof: ${commitStamp()}`);
    record(`chat origin: ${OWUI_URL}`);
    record(`session: storage state minted by owui.setup.ts through the real Continue with Hive journey`);

    await page.goto(`${OWUI_URL}/`, { waitUntil: "domcontentloaded" });
    record(`landed on ${safeUrl(page.url())}`);

    // The negative half matters as much as the positive one, and it has two
    // shapes. A signed-out browser either gets Open WebUI's own sign-in page on
    // the same path, or, because this deployment sets OAUTH_AUTO_REDIRECT, is
    // bounced straight off the chat origin into the authorize chain. A
    // screenshot alone cannot tell either from a signed-in surface, and a
    // capture of a login page would be published as proof of chat.
    const landedOrigin = new URL(page.url()).origin;
    if (landedOrigin !== new URL(OWUI_URL).origin) {
      throw new Error(
        `the browser was redirected off the chat origin to ${landedOrigin}, which is the sign-in chain, so the storage state carries no live session`,
      );
    }
    const signInButton = page.getByRole("button", { name: /continue with hive/i });
    if (await signInButton.isVisible().catch(() => false)) {
      throw new Error(
        "the chat origin served the sign-in page, so the storage state carries no live session",
      );
    }
    const composer = page.locator("#chat-input");
    await composer.waitFor({ state: "visible", timeout: 60_000 });
    record("composer is present, so the session is live");

    shots.push(await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "01-signed-in", note: NOTE }));

    // The knowledge-work journey, before anything is asked. It ends on a new
    // chat bound to the project it just created, so everything below (the alias
    // guard, the untouched toggles, the turn itself and its source list) runs
    // against that chat unchanged.
    if (CAPTURE_PROJECT) {
      await createProjectWithDocument(page, shots);
    }

    // Custom instructions (issue #1363), written and saved BEFORE anything is
    // sent, so the turn captured further down is a turn the stored text had a
    // chance to shape. Writing them afterwards would photograph the same two
    // things in an order that proves nothing.
    if (SET_INSTRUCTIONS) {
      await writeCustomInstructions(page, SET_INSTRUCTIONS);
      shots.push(
        await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "01b-instructions-set", note: NOTE }),
      );
      await saveSettings(page);

      // Reopened from scratch rather than trusting the pane still on screen.
      // The text has to come back from edge-api on a fresh read, which is what
      // separates "the save reached Hive's backend" from "the textarea still
      // holds what was typed into it".
      await page.reload({ waitUntil: "domcontentloaded" });
      const reread = await writeCustomInstructions(page, SET_INSTRUCTIONS);
      const persisted = await reread.inputValue();
      if (persisted.trim() !== SET_INSTRUCTIONS) {
        throw new Error(
          `the instructions did not survive a reload: read back ${JSON.stringify(persisted)}`,
        );
      }
      record("instructions read back from the backend after a reload");
      shots.push(
        await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "01c-instructions-persisted", note: NOTE }),
      );
      await page.keyboard.press("Escape");
      await page.locator("#chat-input").waitFor({ state: "visible", timeout: 30_000 });
    }

    // The model picker, because "which models does this tenant actually see"
    // is the question a catalog or visibility change is answered with, and it
    // is invisible in a shot of an empty composer.
    const picker = page.getByRole("button", { name: /^select(ed)? .*model/i });
    if (await picker.isVisible().catch(() => false)) {
      // What the composer will actually send to, read before anything is
      // opened, because opening the picker changes what the control reads.
      const selectedModel = (await picker.innerText()).replace(/\s+/g, " ").trim();
      record(`model in the composer: ${selectedModel}`);
      if (!MODEL_PATTERN.test(selectedModel)) {
        throw new Error(
          `the composer is set to "${selectedModel}", which does not match ${MODEL_PATTERN}. ` +
            "Capturing a turn on an unexpected alias would spend on it once per pull request; " +
            "check the run tenant's tenant_model_visibility grant and OWUI_DEFAULT_MODEL.",
        );
      }
      await picker.click();
      await page.getByRole("option").first().waitFor({ state: "visible", timeout: 15_000 });
      const models = await page.getByRole("option").allInnerTexts();
      record(`model picker lists ${models.length} model(s): ${models.join(", ").replace(/\s+/g, " ").trim()}`);
      shots.push(await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "02-model-picker", note: NOTE }));
      await page.keyboard.press("Escape");
    } else {
      // Fatal, unlike the shot itself. Without the picker there is no way to
      // read which alias the turn is about to be billed to, and sending one
      // blind is the thing the guard above exists to prevent.
      throw new Error(
        "the model picker control was not visible, so the alias this turn would run on cannot be read",
      );
    }

    // What the user did NOT do, photographed before the message is sent. Open
    // WebUI renders the globe as a chip beside the composer only while web
    // search is on (MessageInput.svelte), so an off toggle is an absence, and
    // an absence is exactly what a screenshot cannot distinguish from a missing
    // feature. The control itself lives in the Integrations menu and carries
    // aria-pressed, so opening that menu turns the absence into a readable
    // state. Recorded on every run; the shot is taken only for the feature
    // capture, so the default capture's image set is unchanged.
    const integrations = page.locator("#integration-menu-button");
    if (await integrations.isVisible().catch(() => false)) {
      await integrations.click();
      const webSearchToggle = page.getByRole("button", { name: /(enable|disable) web search/i }).first();
      if (await webSearchToggle.isVisible().catch(() => false)) {
        const pressed = await webSearchToggle.getAttribute("aria-pressed");
        const label = await webSearchToggle.getAttribute("aria-label");
        record(`web search toggle before sending: aria-label="${label}" aria-pressed=${pressed}`);
        if (pressed === "true") {
          throw new Error(
            "the web search toggle was already on before the message was sent, so this capture cannot show what the model does with nothing toggled",
          );
        }
        if (REQUIRE_SOURCES) {
          shots.push(await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "02b-web-toggle-off", note: NOTE }));
        }
      } else {
        record("the Integrations menu carries no web search control on this surface");
      }
      await page.keyboard.press("Escape");
    } else {
      record("no Integrations menu on this surface, so there is no globe to have toggled");
    }

    const chatRequest = await sendPrompt(page);
    let outgoing = null;
    try {
      outgoing = chatRequest.postDataJSON();
    } catch {
      outgoing = null;
    }
    const features = outgoing?.features ?? {};
    record(
      `chat completion request left the browser: model=${outgoing?.model ?? "(unreadable)"} features=${JSON.stringify(features)}`,
    );
    // The wire, not the page. This is the assertion that the turn below was not
    // helped along by the toggle the feature exists to make unnecessary.
    if (features.web_search === true) {
      throw new Error(
        "the outgoing request carried features.web_search = true, so this turn used Open WebUI's own toggled search rather than the model's own tool call",
      );
    }

    const { turn: assistantTurn, text: replyText } = await settledTurn(page);
    record(`assistant turn settled: ${replyText.slice(0, 400)}`);
    if (!REPLY_PATTERN.test(replyText)) {
      throw new Error(
        `the assistant turn settled but does not answer the question (expected ${REPLY_PATTERN}), so the surface rendered something other than a working completion`,
      );
    }

    shots.push(await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "03-streamed-reply", note: NOTE }));

    // Put the account back the way it was found. The fixture user is reused
    // across runs, and an instruction left behind would quietly shape every
    // later capture on it, including the default scenario's. Best effort: a
    // failure here is worth a log line and not worth failing a capture that has
    // already proven what it came to prove.
    if (SET_INSTRUCTIONS) {
      try {
        await writeCustomInstructions(page, "");
        await saveSettings(page);
        record("custom instructions cleared, so later runs start from an empty box");
        await page.keyboard.press("Escape");
      } catch (error) {
        record(`could not clear the custom instructions afterwards: ${error.message}`);
      }
    }

    // The feature half. A source list on this turn means a tool call was
    // executed and its result came back into the turn, and the turn was sent
    // with nothing toggled, which is the claim. Issue #1621 is the reason the
    // list itself is the assertion rather than a good-looking answer: a correct
    // search that renders no sources is the exact symptom it reported.
    if (REQUIRE_SOURCES) {
      const toggle = sourcesToggle(assistantTurn);
      try {
        await toggle.waitFor({ state: "visible", timeout: 30_000 });
      } catch {
        throw new Error(
          "the assistant turn settled with no source list, so either no tool was called on a turn that needed live data, or a tool was called and its citations did not render (issue #1621)",
        );
      }
      record(`source list on the assistant turn: ${await toggle.getAttribute("aria-label")}`);
      await toggle.click();
      const entries = assistantTurn.getByRole("button", { name: /^view source:/i });
      await entries.first().waitFor({ state: "visible", timeout: 15_000 });
      const names = (await entries.allInnerTexts()).map((t) => t.replace(/\s+/g, " ").trim());
      record(`sources: ${names.join(" | ")}`);
      shots.push(await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "04-sources-expanded", note: NOTE }));
    }

    // The control. Same question, same untouched toggle, on an alias whose
    // every enabled route reports tools_supported = false, so the patch offers
    // it nothing. No source list here is what makes the offer above capability
    // gated rather than unconditional.
    if (CONTROL_MODEL) {
      record(`control turn on ${CONTROL_MODEL}, which the catalog reports as not tool capable`);
      await page.goto(`${OWUI_URL}/`, { waitUntil: "domcontentloaded" });
      await page.locator("#chat-input").waitFor({ state: "visible", timeout: 60_000 });
      await page.getByRole("button", { name: /^select(ed)? .*model/i }).click();
      // The picker labels an option with the alias's DISPLAY name, not its id:
      // "deepseek-v4-flash" is offered as "Deepseek V4 Flash". Matching the id
      // verbatim finds nothing, so the hyphens are relaxed to any separator.
      const controlPattern = new RegExp(
        CONTROL_MODEL.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/-/g, "[\\s-]?"),
        "i",
      );
      const option = page.getByRole("option", { name: controlPattern }).first();
      try {
        await option.waitFor({ state: "visible", timeout: 15_000 });
      } catch {
        const offered = (await page.getByRole("option").allInnerTexts())
          .map((t) => t.replace(/\s+/g, " ").trim().slice(0, 40))
          .join(" | ");
        throw new Error(
          `the model picker offers nothing matching ${controlPattern}, so the control turn cannot be sent. It lists: ${offered}`,
        );
      }
      await option.click();

      const controlRequest = await sendPrompt(page);
      let controlBody = null;
      try {
        controlBody = controlRequest.postDataJSON();
      } catch {
        controlBody = null;
      }
      const controlModel = controlBody?.model ?? "";
      const controlFeatures = controlBody?.features ?? {};
      record(
        `control request left the browser: model=${controlModel || "(unreadable)"} features=${JSON.stringify(controlFeatures)}`,
      );
      // Asserted, because a picker click that silently did not take would send
      // the control question to the tool capable alias and then read its
      // rendered sources as a failure of the gate.
      if (!controlModel.toLowerCase().includes(CONTROL_MODEL.toLowerCase())) {
        throw new Error(
          `the control turn was sent to "${controlModel}" rather than ${CONTROL_MODEL}, so the model switch did not take`,
        );
      }
      if (controlFeatures.web_search === true) {
        throw new Error("the control turn carried features.web_search = true, so it is not the same untouched condition");
      }

      const { turn: controlTurn, text: controlText } = await settledTurn(page);
      record(`control turn settled: ${controlText.slice(0, 400)}`);
      if (await sourcesToggle(controlTurn).isVisible().catch(() => false)) {
        throw new Error(
          "the control turn rendered a source list on an alias the catalog reports as not tool capable, so tool advertisement is not capability gated",
        );
      }
      record("control turn rendered no source list, so the tools were not offered there");
      shots.push(await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "05-control-no-sources", note: NOTE }));
    }

    record(`captured ${shots.length} screenshot(s)`);
  } catch (error) {
    record(`::error::${error instanceof Error ? error.message : String(error)}`);
    // A failure shot is evidence too, and it is the only evidence of what the
    // page actually looked like when the run went red.
    await shoot(page, { outDir: OUT_DIR, scenario: SCENARIO, name: "99-failure", note: NOTE }).catch(() => {});
    throw error;
  } finally {
    writeFileSync(join(OUT_DIR, "proof-log.txt"), `${log.join("\n")}\n`);
    await context.close();
    await browser.close();
  }

  if (shots.length === 0) {
    throw new Error("no screenshots were captured");
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
});
