package com.hive.sdktests;

import static org.junit.jupiter.api.Assertions.*;

import com.openai.client.OpenAIClient;
import com.openai.client.okhttp.OkHttpOpenAIClient;
import com.openai.errors.NotFoundException;
import com.openai.models.chat.completions.ChatCompletionCreateParams;
import org.junit.jupiter.api.Test;

class UnsupportedEndpointTest {

    private static final String BASE_URL =
            System.getenv("HIVE_BASE_URL") != null
                    ? System.getenv("HIVE_BASE_URL")
                    : "http://localhost:8080/v1";

    private OpenAIClient createClient() {
        return OkHttpOpenAIClient.builder().baseUrl(BASE_URL).apiKey("test-key").build();
    }

    @Test
    void chatCompletionsThrows404WithUnsupportedEndpointError() {
        OpenAIClient client = createClient();

        NotFoundException ex =
                assertThrows(
                        NotFoundException.class,
                        () -> {
                            ChatCompletionCreateParams params =
                                    ChatCompletionCreateParams.builder()
                                            .model("gpt-4o")
                                            .addUserMessage("hello")
                                            .build();
                            client.chat().completions().create(params);
                        });

        assertEquals(404, ex.statusCode());
        String body = ex.body();
        assertTrue(body.contains("unsupported_endpoint"), "Error type should be unsupported_endpoint");
        assertTrue(body.contains("endpoint_not_available"), "Error code should be endpoint_not_available");
        assertTrue(body.contains("planned but not yet available"), "Message should mention planned status");

        // Provider-blind assertions
        String bodyLower = body.toLowerCase();
        assertFalse(bodyLower.contains("provider"), "Message should not mention provider");
        assertFalse(bodyLower.contains("upstream"), "Message should not mention upstream");
    }

    @Test
    void fineTuningThrows404WithExplicitlyUnsupportedError() {
        OpenAIClient client = createClient();

        // Fine-tuning is explicitly unsupported - use raw HTTP since Java SDK
        // fine-tuning API may differ across versions
        java.net.http.HttpClient httpClient = java.net.http.HttpClient.newHttpClient();
        java.net.http.HttpRequest request =
                java.net.http.HttpRequest.newBuilder()
                        .uri(java.net.URI.create(BASE_URL + "/fine_tuning/jobs"))
                        .header("Content-Type", "application/json")
                        .header("Authorization", "Bearer test-key")
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
