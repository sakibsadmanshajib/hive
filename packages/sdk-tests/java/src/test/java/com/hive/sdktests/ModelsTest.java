package com.hive.sdktests;

import static org.junit.jupiter.api.Assertions.*;

import com.openai.client.OpenAIClient;
import com.openai.client.okhttp.OkHttpOpenAIClient;
import com.openai.models.models.ModelListPage;
import org.junit.jupiter.api.Test;

class ModelsTest {

    private static final String BASE_URL =
            System.getenv("HIVE_BASE_URL") != null
                    ? System.getenv("HIVE_BASE_URL")
                    : "http://localhost:8080/v1";

    private OpenAIClient createClient() {
        return OkHttpOpenAIClient.builder().baseUrl(BASE_URL).apiKey("test-key").build();
    }

    @Test
    void listModelsReturnsValidResponse() {
        OpenAIClient client = createClient();
        ModelListPage response = client.models().list();

        assertNotNull(response);
        assertNotNull(response.data());
    }
}
