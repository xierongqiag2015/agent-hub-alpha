package com.openjiuwen.demo.expert;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.openjiuwen.core.runner.Runner;
import com.openjiuwen.core.singleagent.agents.ReActAgent;
import com.openjiuwen.service.adapters.agentcore.agentfw.JiuwenCoreAgentHandler;
import com.openjiuwen.service.spec.dto.QueryResponse;
import com.openjiuwen.service.spec.dto.ServeRequest;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;

/**
 * Contract probe, no control service involved: feeds a business agent the exact
 * business-layer message BoundExpertHandler builds ({binding, material}, here
 * reconstructed from a delivered task's recorded binding) through the same
 * JiuwenCoreAgentHandler path, then applies BoundExpertHandler.parseOutput, the
 * platform's own acceptance of the business output.
 * Usage: W06ContractProbe w06|baseline <role> <agent id> <config json with model_binding_digest> <message file> <out file>
 */
public final class W06ContractProbe {
    private static final ObjectMapper JSON = new ObjectMapper();

    public static void main(String[] args) throws Exception {
        String mode = args[0], role = args[1], agentID = args[2];
        JsonNode config = JSON.readTree(Files.readString(Path.of(args[3])));
        String message = Files.readString(Path.of(args[4]));
        ModelBinding.Settings settings = ModelBinding.read(System.getenv(), config);
        Runner.start();
        ReActAgent agent = mode.equals("w06") ? W06BusinessAgents.build(agentID, role, settings)
                : ExpertApplication.configuredAgent(settings, agentID, role);
        JiuwenCoreAgentHandler delegate = new JiuwenCoreAgentHandler(agent);
        delegate.start();
        ServeRequest request = new ServeRequest();
        request.setConversationId("probe-" + UUID.randomUUID());
        request.setStream(false);
        request.setMessages(List.of(Map.of("role", "user", "content", message)));
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("mode", mode);
        out.put("role", role);
        out.put("message_chars", message.length());
        long started = System.nanoTime();
        Optional<Object> token = Optional.empty();
        try {
            token = delegate.prepareTask(request);
            QueryResponse response = delegate.query(request);
            String content = JSON.valueToTree(response.getResult()).path("content").asText();
            out.put("raw_output", content);
            try {
                out.put("output", BoundExpertHandler.parseOutput(content));
                out.put("platform_parse", "accepted");
            } catch (Exception e) {
                out.put("platform_parse", "rejected: " + e.getMessage());
            }
        } catch (Exception e) {
            out.put("error", e.toString());
        } finally {
            delegate.completeTask(token);
            out.put("seconds", Math.round((System.nanoTime() - started) / 1e8) / 10.0);
        }
        Files.writeString(Path.of(args[5]), JSON.writerWithDefaultPrettyPrinter().writeValueAsString(out));
        delegate.stop();
        System.exit(0);
    }
}
