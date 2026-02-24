import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requirePrincipal } from "./auth";

type EnrollVerifyBody = {
  challenge_id?: string;
  code?: string;
};

type ChallengeInitBody = {
  purpose?: string;
};

type ChallengeVerifyBody = {
  challenge_id?: string;
  code?: string;
};

export function registerTwoFactorRoutes(app: FastifyInstance, services: RuntimeServices): void {
  app.post("/v1/2fa/enroll/init", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "usage",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }

    const enrollment = await services.twoFactor.initEnrollment(principal.userId);
    return {
      challenge_id: enrollment.challengeId,
      method: enrollment.method,
      secret: enrollment.secret,
    };
  });

  app.post<{ Body: EnrollVerifyBody }>("/v1/2fa/enroll/verify", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "usage",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }

    const challengeId = request.body?.challenge_id;
    const code = request.body?.code;
    if (!challengeId || !code) {
      return reply.code(400).send({ error: "challenge_id and code are required" });
    }

    const verified = await services.twoFactor.verifyEnrollment(principal.userId, challengeId, code);
    if (!verified) {
      return reply.code(400).send({ error: "invalid challenge or code" });
    }

    await services.userSettings.updateForUser(principal.userId, { twoFactorEnabled: true });
    return { two_factor_enabled: true };
  });

  app.post<{ Body: ChallengeInitBody }>("/v1/2fa/challenge/init", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "usage",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }
    const challenge = await services.twoFactor.initChallenge(principal.userId, request.body?.purpose ?? "sensitive_action");
    return {
      challenge_id: challenge.challengeId,
      expires_in_seconds: challenge.expiresInSeconds,
    };
  });

  app.post<{ Body: ChallengeVerifyBody }>("/v1/2fa/challenge/verify", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "usage",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }
    const challengeId = request.body?.challenge_id;
    const code = request.body?.code;
    if (!challengeId || !code) {
      return reply.code(400).send({ error: "challenge_id and code are required" });
    }
    const success = await services.twoFactor.verifyChallenge(challengeId, code);
    return { success };
  });
}
