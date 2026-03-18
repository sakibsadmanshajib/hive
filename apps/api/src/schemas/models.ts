import { Type, type Static } from "@sinclair/typebox";

export const ModelsParamsSchema = Type.Object(
  {
    model: Type.String(),
  },
  { additionalProperties: false },
);

export type ModelsParams = Static<typeof ModelsParamsSchema>;
