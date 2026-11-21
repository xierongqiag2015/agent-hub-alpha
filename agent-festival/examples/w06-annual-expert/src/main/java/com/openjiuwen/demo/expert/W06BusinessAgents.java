package com.openjiuwen.demo.expert;

import com.agenthub.w06.agents.metrics.MetricAgentFactory;
import com.agenthub.w06.agents.metrics.MetricAgentProperties;
import com.agenthub.w06.agents.operation.AnalysisAgentProperties;
import com.agenthub.w06.agents.operation.OperationAgentFactory;
import com.agenthub.w06.agents.support.AgentProperties;
import com.agenthub.w06.agents.writer.WriterAgentFactory;
import com.agenthub.w06.agents.writer.WriterAgentProperties;
import com.openjiuwen.core.singleagent.agents.ReActAgent;

/**
 * Annual node role -> W06 agent, configured from the approved model binding.
 * extract: the platform already hands the controlled material pages to the
 * business layer, so the W06 metrics agent reads them directly (the W06
 * snapshot-reading material agent is not needed here). quality stays with the
 * W03/W04 expert until W03 decides whether its Loop Forest fork is required.
 */
final class W06BusinessAgents {
    private W06BusinessAgents() { }

    static ReActAgent build(String agentID, String role, ModelBinding.Settings settings) {
        return switch (role) {
            case "extract" -> MetricAgentFactory.build(bound(new MetricAgentProperties(), agentID, settings));
            case "business" -> OperationAgentFactory.build(bound(new AnalysisAgentProperties(), agentID, settings));
            case "write" -> WriterAgentFactory.build(bound(new WriterAgentProperties(), agentID, settings));
            case "quality" -> throw new IllegalStateException("W06 asset-quality is not hosted yet: Loop Forest fork for quality pending W03");
            default -> throw new IllegalStateException("Unknown configured expert role");
        };
    }

    private static <P extends AgentProperties> P bound(P props, String agentID, ModelBinding.Settings settings) {
        props.setAgentId(agentID);
        props.setAgentName(agentID);
        props.setProvider("OpenAI");
        props.setApiKey(settings.key());
        props.setApiBase(settings.base());
        props.setModelName(settings.model());
        props.setTemperature(ModelBinding.TEMPERATURE);
        props.setMaxIterations(settings.normalIterations());
        return props;
    }
}
