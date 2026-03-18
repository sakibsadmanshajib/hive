import { Type, type Static } from "@sinclair/typebox";

export const ResponsesBodySchema = Type.Object(
  {
    input: Type.Optional(Type.String()),
    model: Type.Optional(Type.String()),
    instructions: Type.Optional(Type.String()),
  },
  { additionalProperties: false },
);

export type ResponsesBody = Static<typeof ResponsesBodySchema>;
