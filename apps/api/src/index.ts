import { createApp } from "./server";

const app = createApp();

app.listen({ host: "0.0.0.0", port: Number(process.env.PORT ?? 8080) }).catch((error) => {
  app.log.error(error);
  process.exit(1);
});
