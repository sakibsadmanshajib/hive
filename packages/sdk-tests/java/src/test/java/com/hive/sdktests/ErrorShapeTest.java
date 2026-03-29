package com.hive.sdktests;

import static org.junit.jupiter.api.Assertions.*;

import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
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

    @Test
    void unsupportedEndpointReturnsCorrectErrorEnvelope() throws Exception {
        HttpClient client = HttpClient.newHttpClient();
        HttpRequest request =
                HttpRequest.newBuilder()
                        .uri(URI.create(BASE_URL + "/chat/completions"))
                        .header("Content-Type", "application/json")
                        .POST(HttpRequest.BodyPublishers.ofString("{\"model\":\"gpt-4o\",\"messages\":[]}"))
                        .build();

        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());

        assertEquals(404, response.statusCode());
        assertTrue(
                response.headers().firstValue("content-type").orElse("").contains("application/json"),
                "Content-Type should be application/json");

        JsonObject body = JsonParser.parseString(response.body()).getAsJsonObject();
        assertTrue(body.has("error"), "Response should have 'error' field");

        JsonObject error = body.getAsJsonObject("error");
        assertTrue(error.has("message"), "Error should have 'message'");
        assertTrue(error.has("type"), "Error should have 'type'");
        assertTrue(error.has("param"), "Error should have 'param'");
        assertTrue(error.has("code"), "Error should have 'code'");

        assertTrue(error.get("message").isJsonPrimitive(), "message should be a string");
        assertTrue(error.get("type").isJsonPrimitive(), "type should be a string");
        assertTrue(error.get("param").isJsonNull(), "param should be null");
        assertTrue(error.get("code").isJsonPrimitive(), "code should be a string");
    }
}
