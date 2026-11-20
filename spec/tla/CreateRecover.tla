----------------------------- MODULE CreateRecover -----------------------------
(* sind create cluster X (pkg/cluster/create.go, Create) of a monitored
   cluster in a realm that has another monitored cluster, so the mesh and
   the monitoring stack exist and stay.  Any step can fail, and the process
   can be killed between any two steps.  A create that fails rolls back: it
   runs deleteClusterResources, the removal of sind delete cluster, which
   DeleteRetry models.  This module takes the steps of a delete from there.
   The user then runs sind delete cluster X, up to MaxRuns times, and
   creates X again, up to MaxCreates times.

   Question: once a delete, or the rollback of a create, ends without error
   and without a warning, is every trace of X gone, whatever a create or a
   delete before it left?

   A delete of a cluster without containers looks for the DNS records of
   the cluster's domain, and does nothing on the mesh without them.  This
   is right only while a known_hosts entry of X has a DNS record of X
   beside it (KnownHasRecords).  A delete keeps the rule: it removes the
   entries first.  A create must keep it too:
   - DNSFirst = TRUE: registerNodes adds the DNS records, and only then
     the known_hosts entries.
   - DNSFirst = FALSE: it adds both at once, and each can fail alone.  A
     create that is killed, or whose rollback fails, between the two can
     leave entries without a record.

   Not in the model: a realm in which X is the only cluster, where the
   mesh and the stack come and go with it (TestCreate_FaultSweep runs it on
   the code), and a docker call that the daemon completes after sind
   killed its client. *)
EXTENDS DeleteRetry

CONSTANTS MaxCreates, DNSFirst

VARIABLES step,     \* the step of the create: "none" when no create runs
          todo,     \* the parts of a step that run at once and are left
          creates

cvars == <<step, todo, creates>>
run   == <<pc, res, monitored, warned, failed, runs>>

CInit == /\ ctr = {} /\ net = FALSE /\ vol = FALSE
         /\ known = {} /\ dns = {} /\ stack = {} /\ journal = FALSE
         /\ pc = "idle" /\ res = [c |-> {}, n |-> FALSE, v |-> FALSE]
         /\ monitored = FALSE /\ warned = FALSE /\ failed = FALSE /\ runs = 0
         /\ step = "none" /\ todo = {} /\ creates = 0

\* The user starts a create when no delete runs.
CStart == /\ step = "none" /\ pc \in {"idle", "done"} /\ creates < MaxCreates
          /\ step' = "preflight" /\ creates' = creates + 1 /\ pc' = "idle"
          /\ UNCHANGED <<world, res, monitored, warned, failed, runs, todo>>

\* preflightCheck: a container, a network or a volume of X is a conflict.
\* It does not look at the mesh or the monitoring stack.  A create that
\* ends here made nothing, and rolls nothing back.
Preflight == /\ step = "preflight"
             /\ \/ step' = "none"
                \/ ctr = {} /\ ~net /\ ~vol /\ step' = "network"
             /\ UNCHANGED <<world, run, todo, creates>>

\* createResources: the network, then at once the volumes and the journal
\* directory of the cluster.
Network == /\ step = "network"
           /\ net' = TRUE /\ step' = "resources" /\ todo' = {"volumes", "journal"}
           /\ UNCHANGED <<ctr, vol, known, dns, stack, journal, run, creates>>

Resources == /\ step = "resources"
             /\ IF todo = {}
                THEN step' = "nodes" /\ UNCHANGED <<world, todo>>
                ELSE \E part \in todo :
                       /\ todo' = todo \ {part}
                       /\ vol' = (vol \/ part = "volumes")
                       /\ journal' = (journal \/ part = "journal")
                       /\ UNCHANGED <<ctr, net, known, dns, stack, step>>
             /\ UNCHANGED <<run, creates>>

\* setupNodes: a container for each node.
Node == /\ step = "nodes"
        /\ IF ctr = Nodes
           THEN /\ step' = "register" /\ todo' = {"dns", "known", "stack"}
                /\ UNCHANGED ctr
           ELSE /\ \E n \in Nodes \ ctr : ctr' = ctr \cup {n}
                /\ UNCHANGED <<step, todo>>
        /\ UNCHANGED <<net, vol, known, dns, stack, journal, run, creates>>

\* registerNodes (the DNS records and the known_hosts entries) and
\* ensureMonitoring (the sync of Alloy with the nodes), at once.
Register == /\ step = "register"
            /\ IF todo = {}
               THEN step' = "none" /\ UNCHANGED <<world, todo>>
               ELSE \E part \in todo :
                      /\ part = "known" /\ DNSFirst => "dns" \notin todo
                      /\ todo' = todo \ {part}
                      /\ dns' = IF part = "dns" THEN Nodes ELSE dns
                      /\ known' = IF part = "known" THEN Nodes ELSE known
                      /\ stack' = IF part = "stack" THEN stack \cup ctr ELSE stack
                      /\ UNCHANGED <<ctr, net, vol, journal, step>>
            /\ UNCHANGED <<run, creates>>

\* A step fails, or the user ends the create (Ctrl-C): the parts that are
\* left do not run, and the rollback starts.  It is a run of the delete,
\* from its listing on.
Fail == /\ step \in {"network", "resources", "nodes", "register"}
        /\ step' = "none" /\ todo' = {}
        /\ pc' = "list" /\ warned' = FALSE /\ failed' = FALSE
        /\ UNCHANGED <<world, res, monitored, runs, creates>>

\* The create is killed: no rollback.
CKill == /\ step # "none"
         /\ step' = "none" /\ todo' = {}
         /\ UNCHANGED <<world, run, creates>>

\* The steps of a delete, and of a rollback, while no create runs.
Delete == step = "none" /\ Next /\ UNCHANGED cvars

CNext == CStart \/ Preflight \/ Network \/ Resources \/ Node \/ Register
         \/ Fail \/ CKill \/ Delete

CSpec == CInit /\ [][CNext]_<<vars, cvars>>

\* What lets a delete find the known_hosts entries of a cluster without
\* containers.
KnownHasRecords == known # {} => dns # {}
================================================================================
