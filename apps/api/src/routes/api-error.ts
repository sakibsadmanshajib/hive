import type { FastifyReply } from "fastify";

export type OpenAIErrorType =
  | "invalid_request_error"
  | "authentication_error"
  | "permission_error"
  | "not_found_error"
  | "rate_limit_error"
  | "insufficient_quota"
  | "server_error";

export const STATUS_TO_TYPE: Record<number, OpenAIErrorType> = {
  400: "invalid_request_error",
  401: "authentication_error",
  402: "insufficient_quota",
  403: "permission_error",
  404: "not_found_error",
  429: "rate_limit_error",
};

export type ApiErrorOpts = {
  type?: OpenAIErrorType;
  param?: string | null;
  code?: string | null;
};

export function sendApiError(
  reply: FastifyReply,
  status: number,
  message: string,
  opts?: ApiErrorOpts,
): void {
  reply.code(status).send({
    error: {
      message,
      type: opts?.type ?? STATUS_TO_TYPE[status] ?? "server_error",
      param: opts?.param ?? null,
      code: opts?.code ?? null,
    },
  });
}
