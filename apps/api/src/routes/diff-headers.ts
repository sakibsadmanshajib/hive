import type { FastifyReply } from "fastify";

export function setNoDispatchDiffHeaders(reply: FastifyReply): void {
  reply.header("x-model-routed", "");
  reply.header("x-provider-used", "");
  reply.header("x-provider-model", "");
  reply.header("x-actual-credits", "0");
}
