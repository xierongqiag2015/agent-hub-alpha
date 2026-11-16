package spike;

import com.fasterxml.jackson.databind.ObjectMapper;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.net.http.HttpTimeoutException;
import java.time.Duration;
import java.util.Map;

/** Minimal JSON-over-HTTP client for the Go control plane and the bank stub. */
final class Http {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final HttpClient CLIENT = HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(5)).build();

    private Http() {
    }

    @SuppressWarnings("unchecked")
    static Map<String, Object> post(String url, Map<String, Object> body, long timeoutMs) throws HttpTimeoutException {
        try {
            HttpRequest request = HttpRequest.newBuilder(URI.create(url))
                    .timeout(Duration.ofMillis(timeoutMs))
                    .header("Content-Type", "application/json")
                    .POST(HttpRequest.BodyPublishers.ofString(JSON.writeValueAsString(body)))
                    .build();
            HttpResponse<String> response = CLIENT.send(request, HttpResponse.BodyHandlers.ofString());
            if (response.statusCode() != 200) {
                throw new IllegalStateException(url + " -> " + response.statusCode() + " " + response.body());
            }
            return JSON.readValue(response.body(), Map.class);
        } catch (HttpTimeoutException e) {
            throw e;
        } catch (Exception e) {
            throw new IllegalStateException("POST " + url + " failed: " + e, e);
        }
    }

    static Map<String, Object> post(String url, Map<String, Object> body) {
        try {
            return post(url, body, 30_000);
        } catch (HttpTimeoutException e) {
            throw new IllegalStateException("POST " + url + " timed out", e);
        }
    }

    static String json(Object value) {
        try {
            return JSON.writeValueAsString(value);
        } catch (Exception e) {
            return String.valueOf(value);
        }
    }
}
