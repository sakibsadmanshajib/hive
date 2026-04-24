package com.hive.sdktests;

import static org.junit.jupiter.api.Assertions.*;

import com.openai.client.OpenAIClient;
import com.openai.client.okhttp.OpenAIOkHttpClient;
import com.openai.errors.NotFoundException;
import com.openai.models.chat.completions.ChatCompletionCreateParams;
import org.junit.jupiter.api.Test;

class UnsupportedEndpointTest {

    private static final String BASE_URL =
            System.getenv("HIVE_BASE_URL") != null
                    ? System.getenv("HIVE_BASE_URL")
                    : "http://localhost:8080/v1";

    private static final String API_KEY =
            System.getenv("HIVE_API_KEY") != null
                    ? System.getenv("HIVE_API_KEY")
                    : "test-key";

    private OpenAIClient createClient() {
        return OpenAIOkHttpClient.builder().baseUrl(BASE_URL).apiKey(API_KEY).build();
    }

    @Test
    void modelsRetrieveThrows404WithUnsupportedEndpointError() throws Exception {
        // GET /v1/models/{model} is planned_for_launch. The Java SDK's
        // Models API shape differs across versions, so we call it over raw
        // HTTP to stay SDK-version-agnostic and assert on the JSON envelope.
        java.net.http.HttpClient httpClient = java.net.http.HttpClient.newHttpClient();
        java.net.http.HttpRequest request =
                java.net.http.HttpRequest.newBuilder()
                        .uri(java.net.URI.create(BASE_URL + "/models/hive-default"))
                        .header("Authorization", "Bearer " + API_KEY)
                        .GET()
                        .build();

        java.net.http.HttpResponse<String> response =
                httpClient.send(request, java.net.http.HttpResponse.BodyHandlers.ofString());

        assertEquals(404, response.statusCode());

        String body = response.body();
        assertTrue(body.contains("\"type\":\"unsupported_endpoint\""),
                "Expected unsupported_endpoint type in body: " + body);
        assertTrue(body.contains("\"code\":\"endpoint_not_available\""),
                "Expected endpoint_not_available code in body: " + body);
        assertTrue(body.contains("planned") || body.contains("not yet available"),
                "Message should mention planned status: " + body);

        // Provider-blind assertions
        String bodyLower = body.toLowerCase();
        assertFalse(bodyLower.contains("provider"), "Body should not mention provider");
        assertFalse(bodyLower.contains("upstream"), "Body should not mention upstream");
    }

    @Test
    void fineTuningThrows404WithExplicitlyUnsupportedError() {
        // Fine-tuning is explicitly unsupported - use raw HTTP since Java SDK
        // fine-tuning API may differ across versions
        java.net.http.HttpClient httpClient = java.net.http.HttpClient.newHttpClient();
        java.net.http.HttpRequest request =
                java.net.http.HttpRequest.newBuilder()
                        .uri(java.net.URI.create(BASE_URL + "/fine_tuning/jobs"))
                        .header("Content-Type", "application/json")
                        .header("Authorization", "Bearer " + API_KEY)
                        .POST(
                                java.net.http.HttpRequest.BodyPublishers.ofString(
                                        "{\"model\":\"gpt-4o\",\"training_file\":\"file-abc123\"}"))
                        .build();

        try {
            java.net.http.HttpResponse<String> response =
                    httpClient.send(request, java.net.http.HttpResponse.BodyHandlers.ofString());
            assertEquals(404, response.statusCode());
            assertTrue(response.body().contains("unsupported_endpoint"));
            assertTrue(response.body().contains("endpoint_unsupported"));
        } catch (Exception e) {
            fail("HTTP request should not throw: " + e.getMessage());
        }
    }
}
