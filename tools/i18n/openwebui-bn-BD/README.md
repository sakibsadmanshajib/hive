# Open WebUI bn-BD translation (staged for upstream contribution)

A complete Bengali (Bangladesh, `bn-BD`) translation fill for Open WebUI's
i18n locale file, staged here for submission to the upstream project. This is
**not** wired into Hive; it is a contribution artifact prepared for a separate
upstream pull request.

## Files

| File | Purpose |
|------|---------|
| `translation.json` | The complete, merged `bn-BD` locale (100% coverage). This is the file to copy into the Open WebUI fork. |
| `en-US.reference.json` | The English source locale at the pinned upstream commit, kept for validation only. Do **not** submit this. |
| `validate_placeholders.py` | Standalone validator: valid JSON, identical key set vs en-US, zero placeholder mismatches, full coverage. |

## Source

- **Upstream repo**: https://github.com/open-webui/open-webui
- **Pinned commit (SHA)**: `02dc3e689ceac915a870b373318b99c029ddf603`
- **Source file**: `src/lib/i18n/locales/en-US/translation.json` (2357 keys)
- **Base translation**: `src/lib/i18n/locales/bn-BD/translation.json` (402 keys translated at pin)

In Open WebUI's i18n format the JSON **key is the English source string** and the
**value is the translation**. An empty-string value means "not yet translated".

## Coverage

| | Keys translated | Coverage |
|---|---|---|
| **Before** (upstream `bn-BD` at pin) | 402 / 2357 | 17.1% |
| **After** (this file) | 2357 / 2357 | 100.0% |

1955 previously-empty values were filled. The original 402 translations were
preserved (with two exceptions noted under "Flagged keys" below).

## Validation result

`validate_placeholders.py` run against the staged files:

```
  en-US keys           : 2357
  bn-BD keys           : 2357
  key set identical    : True
  placeholder mismatch : 0
  empty values         : 0
  coverage             : 100.0%

RESULT: PASS
```

Run it yourself:

```bash
cd tools/i18n/openwebui-bn-BD
python3 validate_placeholders.py
```

## Methodology

1. **Source acquisition** — shallow, sparse clone of upstream at the pinned SHA;
   only the `en-US` and `bn-BD` locale files were checked out.
2. **Analysis** — confirmed identical 2357-key sets, identified the 1955 empty
   `bn-BD` values and the 115 keys containing `{{ }}` interpolation placeholders.
3. **Translation** — the 1955 empty strings were translated to natural
   Bangladeshi Bengali for a tech-literate developer audience, under a shared
   style spec:
   - natural BD product Bangla, not stiff literal translation;
   - established tech terms kept in Latin script where BD users expect them
     (API, token, prompt, model, URL, JSON, OAuth, endpoint, proxy, GPU, etc.);
   - brand / product / model names kept in Latin (OpenAI, Ollama, Whisper,
     YouTube, Google Drive, etc.);
   - all placeholders (`{{name}}`, `{{COUNT}}`, `{single}`, `%s`), code spans,
     date-format tokens, URLs and markdown preserved verbatim;
   - punctuation semantics preserved (trailing colon / period / ellipsis kept);
   - UI brevity for buttons, menu items and toggles;
   - Latin numerals by default (matching the existing translations, which use
     Bengali numerals in only 3 of 402 entries).
4. **Merge** — translations merged back into a copy of the upstream `bn-BD`
   file, preserving original key order and the existing 402 translations.
5. **Validation** — `validate_placeholders.py` enforces JSON validity, key-set
   parity with en-US, full coverage, and zero placeholder mismatches.

## Flagged pre-existing keys

A review of the original 402 translations surfaced **28 questionable entries**.
Per policy the pre-existing translations were **left unchanged**, with **three
exceptions**: entries where either the placeholder was broken (two cases) or the
translation was semantically opposite to the source (one case). Those three were
corrected and are listed first below, explicitly (not silently). The remaining 25 are
documented for the upstream maintainers to decide on; they are content or
terminology issues, not crashes, and were left as-is in `translation.json`.

### Corrected (broken placeholders / wrong meaning — 3)

| Key | Was | Now | Why |
|-----|-----|-----|-----|
| `{{ models }}` | `{{ মডেল}}` | `{{ models }}` | Placeholder token was translated; it must stay verbatim or interpolation breaks. |
| `Write a summary in 50 words that summarizes {{topic}}.` | `...[topic or keyword]...` | `{{topic}} এর একটি সারসংক্ষেপ ৫০ শব্দের মধ্যে লিখুন।` | `{{topic}}` placeholder was dropped and replaced with literal text. |
| `Yesterday` | `আগামী` | `গতকাল` | Translation was semantically opposite ("upcoming/future" vs. "yesterday"); corrected. |

### Flagged, left unchanged (content / terminology — 25)

These are recorded for upstream review. They were **not** modified in the
submitted file:

| Key | Current bn-BD | Issue |
|-----|---------------|-------|
| `Continue Response` | `যাচাই করুন` | Means "Verify", not "Continue Response". |
| `Read Aloud` | `পড়াশোনা করুন` | Means "Study", not read-aloud (TTS). |
| `Personalization` | `ডিজিটাল বাংলা` | Unrelated ("Digital Bangla"). |
| `Positive attitude` | `পজিটিভ আক্রমণ` | "আক্রমণ" = attack, not attitude. |
| `Attention to detail` | `বিস্তারিত বিশেষতা` | Garbled; does not convey the phrase. |
| `Refused when it shouldn't have` | `যদি উপযুক্ত নয়, তবে রেজিগেনেট করা হচ্ছে` | Talks about "regenerating"; wrong for a feedback label. |
| `Playground` | `খেলাঘর` | Literal "playhouse"; wrong for the dev Playground feature. |
| `Brave Search API Key` | `সাহসী অনুসন্ধান API কী` | Brand "Brave" translated to "সাহসী" (courageous). |
| `Enter Brave Search API Key` | `সাহসী অনুসন্ধান API কী লিখুন` | Same brand mistranslation. |
| `Title cannot be an empty string.` | `শিরোনাম অবশ্যই একটি পাশাপাশি শব্দ হতে হবে।` | Garbled; loses "cannot be empty". |
| `Reranking Model` | `রির্যাক্টিং মডেল` | Broken transliteration ("reracting"). |
| `Embedding Model` | `ইমেজ ইমেবডিং মডেল` | Spurious "ইমেজ" (image) + misspelling. |
| `Embedding Model Engine` | `ইমেজ ইমেবডিং মডেল ইঞ্জিন` | Spurious "ইমেজ" + misspelling. |
| `Pull a model from Ollama.com` | `Ollama.com থেকে একটি টেনে আনুন আনুন` | Dropped "model"; duplicated verb. |
| `API Key` | `এপিআই কোড` | "Key" rendered as "কোড" (code); inconsistent. |
| `Saving chat logs ... through` (long sentence) | `মাধ্যমে` | Entire sentence reduced to one word ("through"). |
| `Feel free to add specific details` | `নির্দিষ্ট বিবরণ যোগ করতে বিনা দ্বিধায়` | Sentence hangs, no verb closure. |
| `Enter model tag (e.g. {{modelTag}})` | `মডেল ট্যাগ লিখুন (e.g. {{modelTag}})` | "e.g." left in English vs "যেমন" elsewhere. |
| `e.g. '30s','10m'. Valid time units are 's', 'm', 'h'.` | `...অনুমোদিত অনুমোদিত...` | Duplicated word. |
| `Previous 30 days` | `পূর্ব ৩০ দিন` | Bengali numerals + stiff "পূর্ব"; "গত ৩০ দিন" reads better. |
| `Previous 7 days` | `পূর্ব ৭ দিন` | Same. |
| `Ollama API` | `Ollama API` | Left fully English vs "OpenAI এপিআই"; pick one convention. |
| `Bad Response` | `খারাপ প্রতিক্রিয়া` | "Response" rendered inconsistently across entries. |
| `Good Response` | `ভালো সাড়া` | Same terminology inconsistency. |
| `before` | `পূর্ববর্তী` | "পূর্ববর্তী" = previous; standalone "before" likely "আগে". |

A machine-readable copy of the full flag list (with suggestions) is available in
the preparation artifact `flagged.json` (not committed; reproducible from the
audit step).

## How the owner submits the upstream PR

Open WebUI accepts locale contributions as a normal GitHub pull request. The
project uses i18next-style locale files under `src/lib/i18n/locales/<locale>/`.

1. **Read the contribution guidelines first**
   - https://github.com/open-webui/open-webui/blob/main/CONTRIBUTING.md
   - Translation notes in the i18n directory README, if present:
     `src/lib/i18n/README.md`.

2. **Fork** the upstream repo to your account:
   ```bash
   gh repo fork open-webui/open-webui --clone
   cd open-webui
   ```

3. **Sync to the pinned commit (or latest main)** and create a branch:
   ```bash
   git checkout main && git pull upstream main
   git checkout -b i18n/bn-BD-complete-translation
   ```
   > If the upstream `en-US` key set has changed since commit
   > `02dc3e689ceac915a870b373318b99c029ddf603`, re-run
   > `validate_placeholders.py` against the new `en-US/translation.json` and
   > fill any newly-added keys before submitting.

4. **Copy the translated file into place** (path is the destination in the fork):
   ```bash
   cp <hive>/tools/i18n/openwebui-bn-BD/translation.json \
      src/lib/i18n/locales/bn-BD/translation.json
   ```

5. **Validate inside the fork** (the repo may also have its own i18n parse/lint;
   run it if present, e.g. `npm run i18n:parse` or `npm run lint`):
   ```bash
   python3 <hive>/tools/i18n/openwebui-bn-BD/validate_placeholders.py \
     --en src/lib/i18n/locales/en-US/translation.json \
     --bn src/lib/i18n/locales/bn-BD/translation.json
   ```

6. **Commit and push**:
   ```bash
   git add src/lib/i18n/locales/bn-BD/translation.json
   git commit -m "i18n: complete Bengali (bn-BD) translation"
   git push -u origin i18n/bn-BD-complete-translation
   ```

7. **Open the PR** against `open-webui/open-webui:main`:
   ```bash
   gh pr create --repo open-webui/open-webui --base main \
     --title "i18n: complete Bengali (bn-BD) translation" \
     --body "Fills bn-BD from 17.1% to 100% coverage (1955 strings). Placeholders preserved and validated. Two pre-existing broken placeholders fixed; 26 further pre-existing content/terminology issues listed in the PR description for maintainer review."
   ```
   In the PR description, paste the "Flagged, left unchanged" table so
   maintainers can decide on the remaining 26 entries.

8. **Respond to review** — i18n PRs may be checked by native reviewers; expect
   wording feedback and address it on the branch.
