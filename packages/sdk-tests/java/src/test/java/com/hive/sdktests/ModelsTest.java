package com.hive.sdktests;

import static org.junit.jupiter.api.Assertions.*;

import com.openai.client.OpenAIClient;
import com.openai.client.okhttp.OpenAIOkHttpClient;
import com.openai.models.models.ModelListPage;
import org.junit.jupiter.api.Test;

class ModelsTest {

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
    void listModelsReturnsValidResponse() {
        OpenAIClient client = createClient();
        ModelListPage response = client.models().list();

        assertNotNull(response);
        assertNotNull(response.data());
    }
}
