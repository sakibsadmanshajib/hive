export type ProviderFetchRequest = {
  provider: string;
  url: string;
  init: Omit<RequestInit, "signal">;
  timeoutMs: number;
  maxRetries: number;
};

function isRetryableStatus(status: number): boolean {
  return status === 429 || status >= 500;
}

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === "AbortError";
}

function toProviderFetchError(provider: string, error: unknown): Error {
  if (error instanceof Error) {
    return new Error(`${provider} request failed: ${error.message}`);
  }
  return new Error(`${provider} request failed: ${String(error)}`);
}

export async function fetchWithRetry(request: ProviderFetchRequest): Promise<Response> {
  const totalAttempts = Math.max(0, request.maxRetries) + 1;

  for (let attempt = 0; attempt < totalAttempts; attempt += 1) {
    const controller = new AbortController();
    const timeoutHandle = setTimeout(() => controller.abort(), request.timeoutMs);

    try {
      const response = await fetch(request.url, {
        ...request.init,
        signal: controller.signal,
      });

      if (!response.ok && isRetryableStatus(response.status) && attempt < totalAttempts - 1) {
        continue;
      }

      return response;
    } catch (error) {
      const timedOut = isAbortError(error);
      const retryableError = timedOut || error instanceof TypeError;
      const canRetry = attempt < totalAttempts - 1;

      if (retryableError && canRetry) {
        continue;
      }

      if (timedOut) {
        throw new Error(`${request.provider} request timed out after ${request.timeoutMs}ms`);
      }

      throw toProviderFetchError(request.provider, error);
    } finally {
      clearTimeout(timeoutHandle);
    }
  }

  throw new Error(`${request.provider} request failed after retries exhausted`);
}
