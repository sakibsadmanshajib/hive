import type { FastifyInstance, FastifyRequest, FastifyReply } from "fastify";
import { sendApiError } from "./api-error";
import { setNoDispatchDiffHeaders } from "./diff-headers";

function stubHandler(endpoint: string) {
  return (_request: FastifyRequest, reply: FastifyReply) => {
    setNoDispatchDiffHeaders(reply);
    sendApiError(reply, 404,
      `The ${endpoint} endpoint is not yet supported. Please check our roadmap for availability.`,
      { code: "unsupported_endpoint" },
    );
  };
}

export function registerV1StubRoutes(app: FastifyInstance): void {
  // Audio (3 routes)
  app.post("/v1/audio/speech", stubHandler("/v1/audio/speech"));
  app.post("/v1/audio/transcriptions", stubHandler("/v1/audio/transcriptions"));
  app.post("/v1/audio/translations", stubHandler("/v1/audio/translations"));

  // Files (5 routes)
  app.get("/v1/files", stubHandler("/v1/files"));
  app.post("/v1/files", stubHandler("/v1/files"));
  app.get("/v1/files/:file_id", stubHandler("/v1/files/:file_id"));
  app.delete("/v1/files/:file_id", stubHandler("/v1/files/:file_id"));
  app.get("/v1/files/:file_id/content", stubHandler("/v1/files/:file_id/content"));

  // Uploads (4 routes)
  app.post("/v1/uploads", stubHandler("/v1/uploads"));
  app.get("/v1/uploads/:upload_id", stubHandler("/v1/uploads/:upload_id"));
  app.post("/v1/uploads/:upload_id/cancel", stubHandler("/v1/uploads/:upload_id/cancel"));
  app.post("/v1/uploads/:upload_id/parts", stubHandler("/v1/uploads/:upload_id/parts"));

  // Batches (4 routes)
  app.get("/v1/batches", stubHandler("/v1/batches"));
  app.post("/v1/batches", stubHandler("/v1/batches"));
  app.get("/v1/batches/:batch_id", stubHandler("/v1/batches/:batch_id"));
  app.post("/v1/batches/:batch_id/cancel", stubHandler("/v1/batches/:batch_id/cancel"));

  // Completions (1 route - legacy)
  app.post("/v1/completions", stubHandler("/v1/completions"));

  // Fine-tuning (6 routes)
  app.get("/v1/fine_tuning/jobs", stubHandler("/v1/fine_tuning/jobs"));
  app.post("/v1/fine_tuning/jobs", stubHandler("/v1/fine_tuning/jobs"));
  app.get("/v1/fine_tuning/jobs/:fine_tuning_job_id", stubHandler("/v1/fine_tuning/jobs/:fine_tuning_job_id"));
  app.post("/v1/fine_tuning/jobs/:fine_tuning_job_id/cancel", stubHandler("/v1/fine_tuning/jobs/:fine_tuning_job_id/cancel"));
  app.get("/v1/fine_tuning/jobs/:fine_tuning_job_id/events", stubHandler("/v1/fine_tuning/jobs/:fine_tuning_job_id/events"));
  app.get("/v1/fine_tuning/jobs/:fine_tuning_job_id/checkpoints", stubHandler("/v1/fine_tuning/jobs/:fine_tuning_job_id/checkpoints"));

  // Moderations (1 route)
  app.post("/v1/moderations", stubHandler("/v1/moderations"));
}
