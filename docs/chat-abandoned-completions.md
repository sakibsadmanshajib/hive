# What the chat surface leaves behind when a completion is abandoned

Issue #916 measured 48 conversations on the shared demo account, every one of
them carrying an assistant turn with zero characters of content, and asked for
one explanation before treating that as expected. This is that explanation. It
was written from the vendored source and confirmed against the running stack on
2026-08-29.

Short answer: all three behaviours are the product working as built, none of
them is a broken backend, and the rows persist because nothing is supposed to
remove them except the person who owns them.

## The empty assistant turn is a client-side placeholder, and it outlives the request

When a message is sent, the front end writes the assistant turn into
`chat.history.messages` immediately, before any token arrives, and marks it
`done: false`. Content is appended to that turn as the stream arrives, and
`done` flips to `true` when the stream ends.

Nothing on the server owns that placeholder. If the client goes away mid-flight
(the tab closes, the sweep navigates on, the network drops), the last write the
server saw is the one containing the empty turn, and that is what stays on the
row. There is no server-side finaliser that revisits an abandoned chat to mark
its turn failed, and no reaper anywhere that deletes rows for being empty or
stale: no scheduled job in the chat backend, and no retention policy on the chat
tables.

So yes: a user who closes a tab mid-answer keeps a row whose assistant bubble
will never render anything. That is expected, and it is the same shape whether
the client abandoned the request or the model genuinely returned nothing, which
is the second observation in #916 and is a real and acknowledged limitation. An
empty bubble does not, by itself, tell a presenter whether the model, the
gateway or their own browser gave up.

The remedy available to the user is the ordinary one: delete the conversation.
That works, has always worked, and is covered in `docs/live-test-auth.md`.

## The duplicated model alias is the compare feature, not a picker fault

25 of the 48 rows carried `models: ["hive-auto", "hive-auto"]`, the same alias
twice, and #916 asked whether that is a legal state and whether it doubles the
dispatch. It is legal, and it does.

The "Add Model" control in the model picker
(`vendor/open-webui/src/lib/components/chat/ModelSelector.svelte:81-86`) appends
a new slot seeded with a copy of the previous one:

```js
selectedModels = [...selectedModels, selectedModels[selectedModels.length - 1] || ''];
```

That is the multi-model compare feature. The new slot is meant to be changed to
a second model; until it is, the array legitimately holds the same alias twice.
The submit path then iterates that array
(`Chat.svelte:2598`, `Chat.svelte:2649`), creating one assistant turn and
issuing one completion per entry. Two identical entries therefore produce two
assistant placeholders and two dispatches, which is precisely what the 25 rows
show, and it means two charges rather than one.

This also explains why exactly those 25 rows kept the title `New Chat`: title
generation runs off a completed answer, and none of these completed.

Nothing here is a defect to fix. Selecting the same model twice is a deliberate
comparison a user may want, and there is no sound way to tell that apart from an
accidental double-click at the picker. What made it look pathological in #916 is
whose finger was on the button: an interaction-coverage sweep clicks every
control on the page, including "Add Model", which is how an automated run
produced 25 duplicate-alias conversations that a human would rarely create.

## Why the rows accumulated on that account in particular

They accumulated because automated runs were pointed at the shared demo account,
which `docs/live-test-auth.md` had already forbidden and nothing enforced. That
gap is now closed at the session-minting choke point, and the recurrence
question is tracked in #848 rather than here.
