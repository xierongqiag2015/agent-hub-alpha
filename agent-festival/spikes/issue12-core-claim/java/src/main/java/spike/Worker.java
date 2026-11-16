package spike;

import com.openjiuwen.core.context.ModelContext;
import com.openjiuwen.core.session.NodeSessionApi;
import com.openjiuwen.core.session.WorkflowSession;
import com.openjiuwen.core.session.checkpointer.Checkpointer;
import com.openjiuwen.core.session.checkpointer.CheckpointerFactory;
import com.openjiuwen.core.session.interaction.InteractiveInput;
import com.openjiuwen.core.workflow.Workflow;
import com.openjiuwen.core.workflow.WorkflowComponent;
import com.openjiuwen.core.workflow.WorkflowOutput;
import com.openjiuwen.core.workflow.component.SubWorkflowComponentImpl;
import com.openjiuwen.core.workflow.component.loop.LoopComponentImpl;
import com.openjiuwen.core.workflow.component.loop.LoopGroup;

import java.net.http.HttpTimeoutException;
import java.time.LocalTime;
import java.time.format.DateTimeFormatter;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.Map;

/**
 * One worker process running the credit graph on openJiuwen Core:
 * start -> evidence -> kyc(sub-workflow) -> loop(supplement x3) -> judge -> credit_request -> await_receipt -> end.
 * Arguments are key=value; see Config.
 */
public final class Worker {

    static final String WORKFLOW_ID = "credit-flow-v1";
    private static final DateTimeFormatter TS = DateTimeFormatter.ofPattern("HH:mm:ss.SSS");
    private static String owner = "?";

    static final class Config {
        final Map<String, String> args = new HashMap<>();
        String task;
        String owner;
        String ctlUrl;
        String bankUrl;
        String crash;
        long leaseMs;
        boolean naive;

        Config(String[] argv) {
            for (String a : argv) {
                int i = a.indexOf('=');
                args.put(a.substring(0, i), a.substring(i + 1));
            }
            task = get("task", "T1");
            owner = get("owner", "W1") + "@pid" + ProcessHandle.current().pid();
            ctlUrl = get("ctl", "http://127.0.0.1:18080");
            bankUrl = get("bank", "http://127.0.0.1:18081");
            crash = get("crash", "");
            leaseMs = Long.parseLong(get("lease_ms", "1500"));
            naive = Boolean.parseBoolean(get("naive", "false"));
        }

        String get(String key, String def) {
            return args.getOrDefault(key, def);
        }

        long num(String key, long def) {
            return Long.parseLong(get(key, String.valueOf(def)));
        }

        Map<String, Object> ctl(String path, Map<String, Object> body) {
            return Http.post(ctlUrl + path, body);
        }

        void count(String name, String task, String o) {
            ctl("/count", Map.of("name", name, "actor", owner, "task", task, "o", o));
        }

        /** Simulated kill -9: no finally blocks, no shutdown hooks, no flush. */
        void crashPoint(String node, String point) {
            if (crash.equals(node + ":" + point)) {
                log("CRASH  at " + node + ":" + point + " (Runtime.halt, equivalent to kill -9)");
                System.out.flush();
                Runtime.getRuntime().halt(137);
            }
        }
    }

    static void log(String message) {
        System.out.println(LocalTime.now().format(TS) + " [" + owner + "] " + message);
        System.out.flush();
    }

    static void sleep(long ms) {
        try {
            Thread.sleep(ms);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    static final class Pass extends WorkflowComponent {
        @Override
        public Object invoke(Object inputs, NodeSessionApi session, ModelContext context) {
            return inputs;
        }
    }

    private static boolean truthy(Object v) {
        return Boolean.parseBoolean(String.valueOf(v));
    }

    static Workflow build(Config cfg) {
        ClaimNode evidence = new ClaimNode("evidence", cfg, (in, s, ex) -> {
            Map<String, Object> facts = new LinkedHashMap<>();
            facts.put("assets_musd", 35);
            facts.put("recent_overdue", false);
            facts.put("long_overdue", false);
            Map<String, Object> k = cfg.ctl("/k/find", Map.of("node", "evidence"));
            if (truthy(k.get("found")) && !ex.kDisabled()) {
                String kId = (String) k.get("id");
                if (truthy(cfg.get("invalidate_k_after_find", "false"))) {
                    log("INJECT knowledge " + kId + " invalidated after lookup, before commit");
                    cfg.ctl("/k/invalidate", Map.of("id", kId, "actor", "injector"));
                }
                facts.put("source", "K:" + kId);
                return new ClaimNode.Out(facts, kId);
            }
            cfg.count("model:s2", ex.task(), ex.o());
            facts.put("source", "S2-investigation");
            return new ClaimNode.Out(facts, null);
        });

        Workflow kycFlow = new Workflow();
        kycFlow.getCard().setId("kyc-subflow-v1");
        kycFlow.setStartComp("kyc_start", new Pass(), Map.of("applicant", "${applicant}"), null);
        kycFlow.addWorkflowComp("kyc_check", new ClaimNode("kyc_check", cfg, (in, s, ex) -> {
            log("SUBWF  kyc_check sees session=" + s.getSessionId() + " executableId=" + s.getExecutableId());
            return new ClaimNode.Out(Map.of("kyc", "passed", "applicant", String.valueOf(in.get("applicant"))), null);
        }), Map.of("applicant", "${kyc_start.applicant}"), null);
        kycFlow.setEndComp("kyc_end", new Pass(), Map.of("kyc", "${kyc_check.kyc}"), null);
        kycFlow.addConnection("kyc_start", "kyc_check");
        kycFlow.addConnection("kyc_check", "kyc_end");

        LoopGroup loop = new LoopGroup();
        loop.addWorkflowComp("supplement", new ClaimNode("supplement", cfg, (in, s, ex) ->
                        new ClaimNode.Out(Map.of("round", in.get("iter"), "doc", "statement-" + in.get("iter")), null)),
                null, Map.of("iter", "${loop.index}"), null, null, null, null);
        loop.startComp("supplement");
        loop.endComp("supplement");

        ClaimNode judge = new ClaimNode("judge", cfg, (in, s, ex) -> {
            cfg.count("model:s1", ex.task(), ex.o());
            sleep(cfg.num("judge_delay_ms", 0));
            int assets = ((Number) in.get("assets")).intValue();
            boolean grant = assets > 30 && !truthy(in.get("recent")) && !truthy(in.get("long"));
            return new ClaimNode.Out(Map.of("verdict", grant ? "grant-50000" : "uncovered"), null);
        });

        ClaimNode request = new ClaimNode("credit_request", cfg, (in, s, ex) -> {
            Map<String, Object> payload = Map.of("applicant", ex.task(), "amount_minor", "5000000", "currency", "USD");
            if (cfg.naive) {
                // control group: direct call, no business-request identity, no idempotency key
                String key = "naive-" + java.util.UUID.randomUUID();
                log("SEND   direct call, fresh key " + key.substring(0, 14) + "... (no B, no permit)");
                Map<String, Object> r = Http.post(cfg.bankUrl + "/apply", Map.of("key", key, "amount", "5000000"));
                cfg.crashPoint("credit_request", "after_api_before_observe");
                return new ClaimNode.Out(Map.of("b", key, "state", "SUCCEEDED", "ref", r.get("ref")), null);
            }
            String b = (String) cfg.ctl("/b/bind", Map.of("o", ex.o(), "owner", cfg.owner, "payload", payload)).get("b");
            long pause = cfg.num("pre_dispatch_pause_ms", 0);
            if (pause > 0) {
                log("PAUSE  " + pause + "ms before dispatch (GC pause / network stall; lease may expire)");
                sleep(pause);
            }
            Map<String, Object> d = cfg.ctl("/b/dispatch", Map.of("b", b, "owner", cfg.owner, "owner_gen", ex.gen()));
            String decision = (String) d.get("decision");
            if (decision.equals("fenced")) {
                throw new ClaimNode.Fenced("dispatch refused for stale generation");
            }
            while (!decision.equals("done")) {
                if (decision.equals("send")) {
                    long a = ((Number) d.get("a")).longValue();
                    cfg.crashPoint("credit_request", "after_dispatch_commit");
                    Map<String, Object> obs = new HashMap<>(Map.of("b", b, "a", a, "actor", cfg.owner));
                    try {
                        log("SEND   " + b + " attempt=" + a + " idempotency_key=" + b);
                        Map<String, Object> r = Http.post(cfg.bankUrl + "/apply", Map.of("key", b,
                                "amount", "5000000", "delay_ms", cfg.num("bank_delay_ms", 0),
                                "mode", cfg.get("bank_mode", "final")), cfg.num("bank_timeout_ms", 10_000));
                        cfg.crashPoint("credit_request", "after_api_before_observe");
                        obs.put("outcome", "pending".equals(r.get("status")) ? "accepted" : "succeeded");
                        obs.put("ref", r.get("ref"));
                    } catch (HttpTimeoutException timeout) {
                        log("TIMEOUT " + b + " after " + cfg.num("bank_timeout_ms", 0) + "ms: outcome unknown, not failure");
                        obs.put("outcome", "timeout");
                    }
                    cfg.ctl("/b/observe", obs);
                }
                Map<String, Object> g = cfg.ctl("/b/get", Map.of("b", b));
                if (!"UNRESOLVED".equals(g.get("state")) && !"DISPATCH_COMMITTED".equals(g.get("state"))) {
                    break;
                }
                sleep(cfg.num("recover_interval_ms", 500));
                d = cfg.ctl("/b/recover", Map.of("b", b, "actor", cfg.owner));
                decision = (String) d.get("decision");
                log("RECOVER " + b + " -> " + Http.json(d));
            }
            Map<String, Object> g = cfg.ctl("/b/get", Map.of("b", b));
            return new ClaimNode.Out(Map.of("b", b, "state", g.get("state"), "ref", g.get("ref")), null);
        });

        ClaimNode await = new ClaimNode("await_receipt", cfg, (in, s, ex) -> {
            String b = String.valueOf(in.get("b"));
            if (cfg.naive) {
                return new ClaimNode.Out(Map.of("receipt", "SUCCEEDED", "ref", "see bank"), null);
            }
            Map<String, Object> g = cfg.ctl("/b/get", Map.of("b", b));
            if (!"SUCCEEDED".equals(g.get("state")) && !"DECLINED".equals(g.get("state"))) {
                log("AWAIT  " + b + " state=" + g.get("state") + "; interrupting workflow (checkpoint)");
                Object delivered = s.interact("awaiting provider receipt for " + b);
                log("RESUME " + b + " with input " + delivered + "; re-reading authoritative state");
                g = cfg.ctl("/b/get", Map.of("b", b));
            }
            return new ClaimNode.Out(Map.of("receipt", g.get("state"), "ref", g.get("ref")), null);
        });

        Workflow wf = new Workflow();
        wf.getCard().setId(WORKFLOW_ID);
        wf.setStartComp("start", new Pass(), Map.of("applicant", "${applicant}"), null);
        wf.addWorkflowComp("evidence", evidence, Map.of("applicant", "${start.applicant}"), null);
        wf.addWorkflowComp("kyc", new SubWorkflowComponentImpl(kycFlow), Map.of("applicant", "${start.applicant}"), null);
        wf.addWorkflowComp("loop", new LoopComponentImpl(loop, Map.of("docs", "${supplement.doc}")),
                Map.of("loop_type", "number", "loop_number", 3), null);
        wf.addWorkflowComp("judge", judge, Map.of("assets", "${evidence.assets_musd}",
                "recent", "${evidence.recent_overdue}", "long", "${evidence.long_overdue}", "docs", "${loop.docs}"), null);
        wf.addWorkflowComp("credit_request", request, Map.of("verdict", "${judge.verdict}"), null);
        wf.addWorkflowComp("await_receipt", await, Map.of("b", "${credit_request.b}"), null);
        wf.setEndComp("end", new Pass(), Map.of("verdict", "${judge.verdict}",
                "receipt", "${await_receipt.receipt}", "ref", "${await_receipt.ref}"), null);
        wf.addConnection("start", "evidence");
        wf.addConnection("evidence", "kyc");
        wf.addConnection("kyc", "loop");
        wf.addConnection("loop", "judge");
        wf.addConnection("judge", "credit_request");
        wf.addConnection("credit_request", "await_receipt");
        wf.addConnection("await_receipt", "end");
        return wf;
    }

    public static void main(String[] argv) {
        Config cfg = new Config(argv);
        owner = cfg.owner;
        Checkpointer cp = CheckpointerFactory.create("persistence",
                Map.of("db_type", "sqlite", "db_path", cfg.get("cpdb", "checkpoint.db")));
        CheckpointerFactory.setDefaultCheckpointer(cp);
        Workflow wf = build(cfg);
        WorkflowSession session = new WorkflowSession(null, cfg.task, Map.of());
        String resume = cfg.get("resume", "");
        log("START  task=" + cfg.task + " workflow=" + WORKFLOW_ID + (resume.isEmpty() ? "" : " resume=" + resume)
                + (cfg.crash.isEmpty() ? "" : " crash=" + cfg.crash));
        WorkflowOutput out;
        if (resume.isEmpty()) {
            out = wf.invoke(Map.of("applicant", cfg.task), session, null);
        } else {
            InteractiveInput input = new InteractiveInput();
            input.update("await_receipt", resume);
            out = wf.invoke(input, session, null);
        }
        log("END    state=" + out.getState() + " result=" + Http.json(out.getResult()));
        if ("halt".equals(cfg.get("after_interrupt", "")) && String.valueOf(out.getState()).equals("INPUT_REQUIRED")) {
            log("CRASH  after interrupt (Runtime.halt)");
            Runtime.getRuntime().halt(137);
        }
        Runtime.getRuntime().halt(0);
    }
}
