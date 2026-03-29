package com.hive.sdktests;

import static org.junit.jupiter.api.Assertions.*;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import org.junit.jupiter.api.Test;

class HeadersTest {

    private static final String BASE_URL =
            System.getenv("HIVE_BASE_URL") != null
                    ? System.getenv("HIVE_BASE_URL")
                    : "http://localhost:8080/v1";

    @Test
    void successResponseIncludesCompatHeaders() throws Exception {
        HttpClient client = HttpClient.newHttpClient();
        HttpRequest request =
                HttpRequest.newBuilder().uri(URI.create(BASE_URL + "/models")).GET().build();

        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());

        assertEquals(200, response.statusCode());

        String requestId = response.headers().firstValue("x-request-id").orElse(null);
        assertNotNull(requestId, "x-request-id header should be present");
        assertFalse(requestId.isEmpty(), "x-request-id should not be empty");

        String version = response.headers().firstValue("openai-version").orElse(null);
        assertEquals("2020-10-01", version, "openai-version should be 2020-10-01");

        String processingMs = response.headers().firstValue("openai-processing-ms").orElse(null);
        assertNotNull(processingMs, "openai-processing-ms header should be present");
        int ms = Integer.parseInt(processingMs);
        assertTrue(ms >= 0, "openai-processing-ms should be non-negative");
    }

    @Test
    void errorResponseIncludesCompatHeaders() throws Exception {
        HttpClient client = HttpClient.newHttpClient();
        HttpRequest request =
                HttpRequest.newBuilder()
                        .uri(URI.create(BASE_URL + "/chat/completions"))
                        .header("Content-Type", "application/json")
                        .POST(HttpRequest.BodyPublishers.ofString("{}"))
                        .build();

        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());

        assertEquals(404, response.statusCode());

        String requestId = response.headers().firstValue("x-request-id").orElse(null);
        assertNotNull(requestId, "x-request-id should be present on error responses");
        assertFalse(requestId.isEmpty());

        assertEquals(
                "2020-10-01",
                response.headers().firstValue("openai-version").orElse(null));

        String processingMs = response.headers().firstValue("openai-processing-ms").orElse(null);
        assertNotNull(processingMs);
        assertTrue(Integer.parseInt(processingMs) >= 0);
    }
}
