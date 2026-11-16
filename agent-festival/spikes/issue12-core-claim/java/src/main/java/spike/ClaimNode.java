package spike;

import com.openjiuwen.core.context.ModelContext;
import com.openjiuwen.core.session.NodeSessionApi;
import com.openjiuwen.core.workflow.WorkflowComponent;

import java.util.HashMap;
import java.util.Map;

/**
 * Business-node wrapper. Core has no pre-execution hook and re-runs nodes on
 * resume, so the node itself asks the control plane first: an existing formal
 * result is returned as-is, a live owner is joined, otherwise the inner work
 * runs and its result is committed exactly once under the owner generation.
 */
final class ClaimNode extends WorkflowComponent {

    /** Inner work of a node; kId is the knowledge item used, if any. */
    record Out(Map<String, Object> result, String kId) {
    }

    /** What the inner work may read about its own execution. */
    record Exec(String task, String o, long gen, boolean kDisabled) {
    }

    interface Inner {
        Out run(Map<String, Object> inputs, NodeSessionApi session, Exec exec) throws Exception;
    }

    /** Thrown by inner work when the control plane fences a stale owner. */
    static final class Fenced extends RuntimeException {
        Fenced(String message) {
            super(message);
        }
    }

    private final String node;
    private final Inner inner;
    private final Worker.Config cfg;

    ClaimNode(String node, Worker.Config cfg, Inner inner) {
        this.node = node;
        this.cfg = cfg;
        this.inner = inner;
    }

    @Override
    @SuppressWarnings("unchecked")
    public Object invoke(Object inputs, NodeSessionApi session, ModelContext context) {
        Map<String, Object> in = inputs instanceof Map<?, ?> m ? (Map<String, Object>) m : Map.of();
        String task = session.getSessionId();
        String iter = String.valueOf(in.getOrDefault("iter", "-"));
        if (cfg.naive) {
            // control group: plain Core component, no claim/commit
            String o = task + "/" + node + "/" + iter;
            Worker.log("RUN    " + o + " (naive: no control plane; Core executableId=" + session.getExecutableId() + ")");
            cfg.count("inner:" + node, task, o);
            try {
                return inner.run(in, session, new Exec(task, o, 0, false)).result();
            } catch (RuntimeException e) {
                throw e;
            } catch (Exception e) {
                throw new IllegalStateException(e);
            }
        }
        boolean announcedJoin = false;
        while (true) {
            Map<String, Object> claim = cfg.ctl("/claim", Map.of("task", task, "node", node, "iter", iter,
                    "owner", cfg.owner, "lease_ms", cfg.leaseMs));
            String status = (String) claim.get("status");
            String o = (String) claim.get("o");
            if (status.equals("result")) {
                Worker.log("REUSE  " + o + " -> " + Http.json(claim.get("result")) + " (inner not executed; Core executableId=" + session.getExecutableId() + ")");
                return claim.get("result");
            }
            if (status.equals("busy")) {
                if (!announcedJoin) {
                    Worker.log("JOIN   " + o + " held by " + claim.get("owner") + " gen=" + claim.get("owner_gen") + "; waiting");
                    announcedJoin = true;
                }
                Worker.sleep(200);
                continue;
            }
            long gen = ((Number) claim.get("owner_gen")).longValue();
            Worker.log("OWN    " + o + " gen=" + gen + " (Core executableId=" + session.getExecutableId() + ")");
            Object result = runAndCommit(task, o, gen, in, session);
            if (result != null) {
                return result;
            }
            // fenced: another generation owns o now; wait for its formal result
        }
    }

    private Object runAndCommit(String task, String o, long gen, Map<String, Object> in, NodeSessionApi session) {
        boolean kDisabled = false;
        while (true) {
            cfg.crashPoint(node, "before_inner");
            cfg.count("inner:" + node, task, o);
            Out out;
            try {
                out = inner.run(in, session, new Exec(task, o, gen, kDisabled));
            } catch (Fenced f) {
                Worker.log("FENCED " + o + " gen=" + gen + ": " + f.getMessage());
                return null;
            } catch (RuntimeException e) {
                throw e;
            } catch (Exception e) {
                throw new IllegalStateException(e);
            }
            cfg.crashPoint(node, "after_inner_before_commit");
            Map<String, Object> body = new HashMap<>();
            body.put("o", o);
            body.put("owner", cfg.owner);
            body.put("owner_gen", gen);
            body.put("result", out.result());
            body.put("k_id", out.kId() == null ? "" : out.kId());
            Map<String, Object> commit = cfg.ctl("/commit", body);
            switch ((String) commit.get("status")) {
                case "committed" -> {
                    Worker.log("COMMIT " + o + " gen=" + gen + " -> " + Http.json(out.result()));
                    cfg.crashPoint(node, "after_commit");
                    return out.result();
                }
                case "duplicate" -> {
                    Worker.log("DUP    " + o + " already committed; adopting existing result");
                    return commit.get("result");
                }
                case "stale_owner" -> {
                    Worker.log("FENCED " + o + " gen=" + gen + " < current " + commit.get("current_gen") + "; own result discarded");
                    return null;
                }
                case "k_invalid" -> {
                    Worker.log("KSTALE " + o + " knowledge " + out.kId() + " invalid at commit; re-running without it");
                    kDisabled = true;
                }
                default -> throw new IllegalStateException("unexpected commit reply " + commit);
            }
        }
    }
}
