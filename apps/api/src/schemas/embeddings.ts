import { Type, type Static } from "@sinclair/typebox";

export const EmbeddingsBodySchema = Type.Object(
  {
    model: Type.String(),
    input: Type.Union([Type.String(), Type.Array(Type.String(), { minItems: 1 })]),
    encoding_format: Type.Optional(Type.Union([Type.Literal("float"), Type.Literal("base64")])),
    dimensions: Type.Optional(Type.Integer({ minimum: 1 })),
    user: Type.Optional(Type.String()),
  },
  { additionalProperties: false },
);
export type EmbeddingsBody = Static<typeof EmbeddingsBodySchema>;
