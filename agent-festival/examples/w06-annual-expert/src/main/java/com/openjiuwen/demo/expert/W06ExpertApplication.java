package com.openjiuwen.demo.expert;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.openjiuwen.core.singleagent.agents.ReActAgent;
import com.openjiuwen.service.adapters.agentcore.agentfw.JiuwenCoreAgentHandler;
import com.openjiuwen.service.spec.spi.AgentHandler;
import java.nio.file.Files;
import java.nio.file.Path;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;

/**
 * Same wiring as the W03/W04 ExpertApplication (4004ab4): the platform layer
 * (BoundExpertHandler, IngressIdentityFilter, ModelBinding, reused unchanged)
 * authorizes the call with the control service, checks model binding and
 * execution profile, passes only {binding, material} to the business layer and
 * requires one JSON object back. Only the business agent differs: a W06 agent.
 */
@SpringBootApplication(scanBasePackages = "com.openjiuwen.service.app")
public class W06ExpertApplication {
    private static final ObjectMapper JSON = new ObjectMapper();

    public static void main(String[] args) { SpringApplication.run(W06ExpertApplication.class, args); }

    @Bean JsonNode expertConfiguration() throws Exception {
        String path = System.getenv("REPORT_EXPERT_CONFIG");
        if (path == null) { throw new IllegalStateException("REPORT_EXPERT_CONFIG required"); }
        return JSON.readTree(Files.readString(Path.of(path)));
    }

    @Bean AgentHandler reportExpertHandler(JsonNode expertConfiguration) throws Exception {
        String agentID = expertConfiguration.path("agent_id").asText();
        String role = expertConfiguration.path("role").asText();
        ModelBinding.Settings settings = ModelBinding.read(System.getenv(), expertConfiguration);
        ReActAgent agent = W06BusinessAgents.build(agentID, role, settings);
        return new BoundExpertHandler(expertConfiguration, agent, new JiuwenCoreAgentHandler(agent), settings);
    }

    @Bean IngressIdentityFilter ingressIdentityFilter(JsonNode expertConfiguration) {
        return new IngressIdentityFilter(expertConfiguration);
    }
}
