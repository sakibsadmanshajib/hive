import { Type, type Static } from "@sinclair/typebox";

export const ImagesGenerationsBodySchema = Type.Object(
  {
    model: Type.Optional(Type.String()),
    prompt: Type.Optional(Type.String()),
    n: Type.Optional(Type.Integer()),
    size: Type.Optional(Type.String()),
    response_format: Type.Optional(
      Type.Union([Type.Literal("url"), Type.Literal("b64_json")]),
    ),
    user: Type.Optional(Type.String()),
    quality: Type.Optional(Type.String()),
    style: Type.Optional(Type.String()),
  },
  { additionalProperties: false },
);

export type ImagesGenerationsBody = Static<
  typeof ImagesGenerationsBodySchema
>;
