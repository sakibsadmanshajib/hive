import { AiService } from "./ai-service";
import { ApiKeyService } from "./api-key-service";
import { CreditService } from "./credit-service";
import { ModelService } from "./model-service";
import { PaymentService } from "./payment-service";
import { UsageService } from "./usage-service";

export type DomainServices = {
  models: ModelService;
  credits: CreditService;
  usage: UsageService;
  payments: PaymentService;
  ai: AiService;
  auth: ApiKeyService;
  webhookSecrets: {
    bkash: string;
    sslcommerz: string;
  };
};

export function createDomainServices(): DomainServices {
  const models = new ModelService();
  const credits = new CreditService();
  const usage = new UsageService();
  const payments = new PaymentService(credits);
  const ai = new AiService(models, credits, usage);
  const auth = new ApiKeyService();
  const demoIssuedKey = auth.issueKey("user-1", ["chat", "image", "usage", "billing"]);
  void demoIssuedKey;

  return {
    models,
    credits,
    usage,
    payments,
    ai,
    auth,
    webhookSecrets: {
      bkash: "bkash-secret",
      sslcommerz: "sslcommerz-secret",
    },
  };
}
