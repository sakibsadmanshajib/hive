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
  if (!Number.isInteger(request.maxRetries) || request.maxRetries < 0) {
    throw new Error(`${request.provider} request has invalid retry configuration: ${request.maxRetries}`);
  }
  if (!Number.isFinite(request.timeoutMs) || request.timeoutMs <= 0) {
    throw new Error(`${request.provider} request has invalid timeout configuration: ${request.timeoutMs}`);
  }

  for (let attempt = 0; ; attempt += 1) {
    const controller = new AbortController();
    const timeoutHandle = setTimeout(() => controller.abort(), request.timeoutMs);

    try {
      const response = await fetch(request.url, {
        ...request.init,
        signal: controller.signal,
      });

      const canRetry = attempt < request.maxRetries;
      if (!response.ok && isRetryableStatus(response.status) && canRetry) {
        continue;
      }

      return response;
    } catch (error) {
      const timedOut = isAbortError(error);
      const retryableError = timedOut || error instanceof TypeError;
      const canRetry = attempt < request.maxRetries;

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
}
