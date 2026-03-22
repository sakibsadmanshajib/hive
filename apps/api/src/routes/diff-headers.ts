import type { FastifyReply } from "fastify";

export function setNoDispatchDiffHeaders(reply: FastifyReply): void {
  reply
    .header("x-model-routed", "")
    .header("x-provider-used", "")
    .header("x-provider-model", "")
    .header("x-actual-credits", "0");
}
