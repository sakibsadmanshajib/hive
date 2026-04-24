package com.hive.sdktests;

import static org.junit.jupiter.api.Assertions.*;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import org.junit.jupiter.api.Test;

class ErrorShapeTest {

    private static final String BASE_URL =
            System.getenv("HIVE_BASE_URL") != null
                    ? System.getenv("HIVE_BASE_URL")
                    : "http://localhost:8080/v1";

    private static final String API_KEY =
            System.getenv("HIVE_API_KEY") != null
                    ? System.getenv("HIVE_API_KEY")
                    : "test-key";

    @Test
    void unsupportedEndpointReturnsCorrectErrorEnvelope() throws Exception {
        // GET /v1/models/{model} is planned_for_launch — stable 404 with the
        // structured unsupported_endpoint error envelope.
        HttpClient client = HttpClient.newHttpClient();
        HttpRequest request =
                HttpRequest.newBuilder()
                        .uri(URI.create(BASE_URL + "/models/hive-default"))
                        .header("Authorization", "Bearer " + API_KEY)
                        .GET()
                        .build();

        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());

        assertEquals(404, response.statusCode());
        assertTrue(
                response.headers().firstValue("content-type").orElse("").contains("application/json"),
                "Content-Type should be application/json");

        String body = response.body();

        // Verify top-level "error" key exists
        assertTrue(body.contains("\"error\""), "Response should have 'error' field");

        // Verify error object has required fields per OpenAI error envelope
        assertTrue(body.contains("\"message\""), "Error should have 'message'");
        assertTrue(body.contains("\"type\""), "Error should have 'type'");
        assertTrue(body.contains("\"param\""), "Error should have 'param'");
        assertTrue(body.contains("\"code\""), "Error should have 'code'");

        // Verify param is null (not a string value)
        assertTrue(body.contains("\"param\":null") || body.contains("\"param\": null"),
                "param should be null");

        // Verify type and code have string values
        assertTrue(body.contains("\"type\":\"") || body.contains("\"type\": \""),
                "type should be a string");
        assertTrue(body.contains("\"code\":\"") || body.contains("\"code\": \""),
                "code should be a string");
    }
}
