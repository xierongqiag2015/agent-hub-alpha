package com.openjiuwen.demo.w06;

import com.agenthub.w06.agents.assetquality.AssetQualityAgentFactory;
import com.agenthub.w06.agents.material.MaterialAgentFactory;
import com.agenthub.w06.agents.material.MaterialAgentProperties;
import com.agenthub.w06.agents.metrics.MetricAgentFactory;
import com.agenthub.w06.agents.metrics.MetricAgentProperties;
import com.agenthub.w06.agents.operation.AnalysisAgentProperties;
import com.agenthub.w06.agents.operation.OperationAgentFactory;
import com.agenthub.w06.agents.support.AgentProperties;
import com.agenthub.w06.agents.writer.WriterAgentFactory;
import com.agenthub.w06.agents.writer.WriterAgentProperties;
import com.openjiuwen.core.runner.Runner;
import com.openjiuwen.core.singleagent.agents.ReActAgent;
import com.openjiuwen.service.spec.dto.QueryResponse;
import com.openjiuwen.service.spec.dto.ServeRequest;
import com.openjiuwen.service.spec.spi.AgentHandler;
import com.openjiuwen.service.spec.spi.QueryStreamObserver;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;

/**
 * Hosts one W06 role agent, built by the W06 library factory unchanged, behind
 * the Runtime's A2A server. The Runtime owns the A2A task lifecycle; this
 * handler only runs the agent on the message text and returns its output.
 * W06_ROLE selects the factory; the model comes from the deployment.
 */
final class W06RoleHandler implements AgentHandler {
    private static final java.util.logging.Logger LOG = java.util.logging.Logger.getLogger(W06RoleHandler.class.getName());
    static final List<String> ROLES = List.of("material", "metrics", "operation", "asset-quality", "writer");
    private final Map<String, String> env;
    private ReActAgent agent;

    W06RoleHandler(Map<String, String> env) {
        this.env = env;
        for (String k : List.of("AGENT_SERVICE_ID", "W06_ROLE", "W06_API_KEY", "W06_API_BASE", "W06_MODEL_NAME")) {
            if (env.getOrDefault(k, "").isBlank()) throw new IllegalStateException("Required configuration missing: " + k);
        }
        if (!ROLES.contains(env.get("W06_ROLE"))) throw new IllegalStateException("W06_ROLE must be one of " + ROLES);
        if (env.get("W06_ROLE").equals("material") && env.getOrDefault("W06_SNAPSHOT_DIR", "").isBlank())
            throw new IllegalStateException("Required configuration missing: W06_SNAPSHOT_DIR");
    }

    private <P extends AgentProperties> P configure(P props) {
        props.setAgentId(env.get("AGENT_SERVICE_ID"));
        props.setAgentName(env.get("AGENT_SERVICE_ID"));
        props.setProvider(env.getOrDefault("W06_PROVIDER", "OpenAI"));
        props.setApiKey(env.get("W06_API_KEY"));
        props.setApiBase(env.get("W06_API_BASE"));
        props.setModelName(env.get("W06_MODEL_NAME"));
        props.setMaxIterations(Integer.parseInt(env.getOrDefault("W06_MAX_ITERATIONS", "8")));
        return props;
    }

    @Override public void start() {
        Runner.start();
        agent = switch (env.get("W06_ROLE")) {
            case "material" -> MaterialAgentFactory.build(configure(new MaterialAgentProperties()), Path.of(env.get("W06_SNAPSHOT_DIR")));
            case "metrics" -> MetricAgentFactory.build(configure(new MetricAgentProperties()));
            case "operation" -> OperationAgentFactory.build(configure(new AnalysisAgentProperties()));
            case "asset-quality" -> AssetQualityAgentFactory.build(configure(new AnalysisAgentProperties()));
            default -> WriterAgentFactory.build(configure(new WriterAgentProperties()));
        };
    }

    @Override public void stop() { Runner.stop(); }

    @Override public QueryResponse query(ServeRequest request) {
        String conversation = request.getConversationId();
        try {
            String input = request.lastUserQuery();
            if (input == null || input.isBlank()) throw new IllegalArgumentException("Empty message");
            Object raw = agent.invoke(Map.of("query", input, "conversation_id", conversation), null);
            Object output = raw instanceof Map<?, ?> map ? map.get("output") : raw;
            if (output == null || output.toString().isBlank()) {
                // W06 agents may stop on purpose (AskUserRail on ask_user): keep what they returned.
                String seen = String.valueOf(raw);
                LOG.warning("W06 " + env.get("W06_ROLE") + " returned no output; result: " + seen.substring(0, Math.min(500, seen.length())));
                throw new IllegalStateException("Agent returned no output");
            }
            return new QueryResponse(Map.of("role", "assistant", "content", output.toString()), conversation);
        } catch (Exception e) {
            throw new IllegalStateException("W06 " + env.get("W06_ROLE") + " agent failed", e);
        } finally {
            if (conversation != null) Runner.release(conversation);
        }
    }

    @Override public void streamQuery(ServeRequest request, QueryStreamObserver observer) {
        observer.onError(new UnsupportedOperationException("This capability declares non-streaming execution"));
    }
}
