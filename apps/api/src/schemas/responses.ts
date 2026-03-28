import { Type, type Static } from "@sinclair/typebox";

const InputItemSchema = Type.Object({
  type: Type.Optional(Type.String()),
  role: Type.Optional(Type.String()),
  content: Type.Optional(Type.Union([Type.String(), Type.Array(Type.Any())])),
}, { additionalProperties: true });

export const ResponsesBodySchema = Type.Object(
  {
    model: Type.String(),
    input: Type.Union([Type.String(), Type.Array(InputItemSchema)]),
    instructions: Type.Optional(Type.String()),
    temperature: Type.Optional(Type.Number()),
    max_output_tokens: Type.Optional(Type.Integer()),
    tools: Type.Optional(Type.Array(Type.Any())),
    tool_choice: Type.Optional(Type.Any()),
    text: Type.Optional(Type.Any()),
    user: Type.Optional(Type.String()),
  },
  { additionalProperties: false },
);

export type ResponsesBody = Static<typeof ResponsesBodySchema>;
