const DEFAULT_ALLOWED_ORIGINS = [
  "http://127.0.0.1:3000",
  "http://localhost:3000",
  "http://127.0.0.1:3001",
  "http://localhost:3001",
];

export function readAllowedOrigins(): Set<string> {
  const configured = process.env.CORS_ALLOWED_ORIGINS
    ?.split(",")
    .map((origin) => origin.trim())
    .filter(Boolean);

  return new Set(configured && configured.length > 0 ? configured : DEFAULT_ALLOWED_ORIGINS);
}

export function isAllowedBrowserOrigin(origin: string | undefined): boolean {
  if (!origin) {
    return false;
  }

  return readAllowedOrigins().has(origin);
}
