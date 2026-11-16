package spike;

import com.openjiuwen.core.context.ModelContext;
import com.openjiuwen.core.session.NodeSessionApi;
import com.openjiuwen.core.session.checkpointer.Checkpointer;
import com.openjiuwen.core.session.checkpointer.CheckpointerFactory;
import com.openjiuwen.core.session.interaction.InteractiveInput;
import com.openjiuwen.core.session.WorkflowSession;
import com.openjiuwen.core.workflow.Workflow;
import com.openjiuwen.core.workflow.WorkflowOutput;
import com.openjiuwen.core.workflow.WorkflowComponent;
import com.openjiuwen.core.workflow.component.loop.LoopComponentImpl;
import com.openjiuwen.core.workflow.component.loop.LoopGroup;

import java.util.Map;

/** API probe: loop index mapping, node identifiers, cross-process interrupt/resume. */
public final class Probe {

    static class Echo extends WorkflowComponent {
        @Override
        public Object invoke(Object inputs, NodeSessionApi session, ModelContext context) {
            System.out.println("[echo] session=" + session.getSessionId()
                    + " executableId=" + session.getExecutableId()
                    + " componentId=" + session.getComponentId()
                    + " inputs=" + inputs);
            return Map.of("seen", String.valueOf(inputs));
        }
    }

    static class Ask extends WorkflowComponent {
        @Override
        public Object invoke(Object inputs, NodeSessionApi session, ModelContext context) {
            System.out.println("[ask] entered, executableId=" + session.getExecutableId());
            Object answer = session.interact("need receipt");
            System.out.println("[ask] resumed with " + answer);
            return Map.of("answer", String.valueOf(answer));
        }
    }

    static class Pass extends WorkflowComponent {
        @Override
        public Object invoke(Object inputs, NodeSessionApi session, ModelContext context) {
            return inputs;
        }
    }

    public static void main(String[] args) {
        String mode = args[0];
        if (mode.equals("loop")) {
            LoopGroup group = new LoopGroup();
            group.addWorkflowComp("body", new Echo(), null,
                    Map.of("iter", "${loop.index}", "q", "${start.q}"), null, null, null, null);
            group.startComp("body");
            group.endComp("body");
            Workflow wf = new Workflow();
            wf.setStartComp("start", new Pass(), Map.of("q", "${q}"), null);
            wf.addWorkflowComp("loop", new LoopComponentImpl(group, Map.of("all", "${body.seen}")),
                    Map.of("loop_type", "number", "loop_number", 3), null);
            wf.setEndComp("end", new Pass(), Map.of("all", "${loop.all}"), null);
            wf.addConnection("start", "loop");
            wf.addConnection("loop", "end");
            WorkflowOutput out = wf.invoke(Map.of("q", "hello"), new WorkflowSession(null, "T-probe", Map.of()), null);
            System.out.println("[loop] state=" + out.getState() + " result=" + out.getResult());
            return;
        }
        Checkpointer cp = CheckpointerFactory.create("persistence",
                Map.of("db_type", "sqlite", "db_path", args[1]));
        CheckpointerFactory.setDefaultCheckpointer(cp);
        Workflow wf = new Workflow();
        if (args.length > 2) {
            wf.getCard().setId(args[2]);
        }
        wf.setStartComp("start", new Echo(), Map.of("q", "${q}"), null);
        wf.addWorkflowComp("ask", new Ask(), Map.of("q", "${start.seen}"), null);
        wf.setEndComp("end", new Pass(), Map.of("answer", "${ask.answer}"), null);
        wf.addConnection("start", "ask");
        wf.addConnection("ask", "end");
        WorkflowOutput out;
        if (mode.equals("first")) {
            out = wf.invoke(Map.of("q", "x"), new WorkflowSession(null, "T-int", Map.of()), null);
            System.out.println("[first] state=" + out.getState());
            Runtime.getRuntime().halt(137);
        } else {
            InteractiveInput in = new InteractiveInput();
            in.update("ask", "receipt-123");
            out = wf.invoke(in, new WorkflowSession(null, "T-int", Map.of()), null);
            System.out.println("[resume] state=" + out.getState() + " result=" + out.getResult());
        }
    }
}
