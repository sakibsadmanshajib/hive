import { Type, type Static } from "@sinclair/typebox";

const MessageSchema = Type.Object(
  {
    role: Type.String(),
    content: Type.String(),
  },
  { additionalProperties: false },
);

export const ChatCompletionsBodySchema = Type.Object(
  {
    model: Type.Optional(Type.String()),
    messages: Type.Optional(Type.Array(MessageSchema)),
    stream: Type.Optional(Type.Boolean()),
    temperature: Type.Optional(Type.Number()),
    top_p: Type.Optional(Type.Number()),
    n: Type.Optional(Type.Integer()),
    stop: Type.Optional(
      Type.Union([Type.String(), Type.Array(Type.String())]),
    ),
    max_completion_tokens: Type.Optional(Type.Integer()),
    presence_penalty: Type.Optional(Type.Number()),
    frequency_penalty: Type.Optional(Type.Number()),
    logprobs: Type.Optional(Type.Boolean()),
    top_logprobs: Type.Optional(Type.Integer()),
    response_format: Type.Optional(
      Type.Object(
        {
          type: Type.String(),
        },
        { additionalProperties: false },
      ),
    ),
    seed: Type.Optional(Type.Integer()),
    tools: Type.Optional(Type.Array(Type.Any())),
    tool_choice: Type.Optional(
      Type.Union([
        Type.String(),
        Type.Object(
          {
            type: Type.String(),
            function: Type.Object(
              { name: Type.String() },
              { additionalProperties: false },
            ),
          },
          { additionalProperties: false },
        ),
      ]),
    ),
    parallel_tool_calls: Type.Optional(Type.Boolean()),
    user: Type.Optional(Type.String()),
    reasoning_effort: Type.Optional(Type.String()),
    stream_options: Type.Optional(
      Type.Object(
        {
          include_usage: Type.Optional(Type.Boolean()),
        },
        { additionalProperties: false },
      ),
    ),
  },
  { additionalProperties: false },
);

export type ChatCompletionsBody = Static<typeof ChatCompletionsBodySchema>;
