------------------------------ MODULE DeleteRetry ------------------------------
(* sind delete cluster X (pkg/cluster/delete.go, deleteClusterResources and
   removeClusterResources) in a realm that has other clusters, so the mesh
   stays and X is deregistered.  Any step can fail, and the process can be
   killed between any two steps.  The user then runs the same delete again,
   up to MaxRuns times.

   Question: once a run of delete ends without error and without a warning,
   is every trace of X gone: containers, network, volume, DNS records,
   monitoring targets and journal directories?

   FixMonitored and FixDNS select the candidate fixes:
   - FixMonitored: decide "X is monitored" from state that outlives the
     containers (the journal directory or the stack targets), not from the
     containers that the run removes.
   - FixDNS: deregister by the cluster's DNS suffix, not by the names of
     the containers found.
   Each fix also makes its traces count in the "nothing to delete" check. *)
EXTENDS Naturals, FiniteSets

CONSTANTS Nodes, MaxRuns, FixMonitored, FixDNS

VARIABLES ctr, net, vol, dns, stack, journal,  \* the world
          pc, res, monitored, warned, runs     \* one run of delete

vars == <<ctr, net, vol, dns, stack, journal, pc, res, monitored, warned, runs>>

Init == /\ ctr = Nodes /\ net = TRUE /\ vol = TRUE
        /\ dns = Nodes /\ stack = Nodes /\ journal = TRUE
        /\ pc = "idle" /\ res = [c |-> {}, n |-> FALSE, v |-> FALSE]
        /\ monitored = FALSE /\ warned = FALSE /\ runs = 0

World == <<ctr, net, vol, dns, stack, journal>>

\* The process ends: an error is returned, or it is killed (SIGHUP, OOM).
Abort == /\ pc \notin {"idle", "done"}
         /\ pc' = "idle"
         /\ UNCHANGED <<ctr, net, vol, dns, stack, journal, res, monitored, warned, runs>>

Start == /\ pc \in {"idle"} /\ runs < MaxRuns
         /\ pc' = "list" /\ runs' = runs + 1 /\ warned' = FALSE
         /\ UNCHANGED <<ctr, net, vol, dns, stack, journal, res, monitored>>

\* "Nothing to delete": today it looks at containers, network and volumes
\* only. With a fix, the traces that outlive them count too.
Empty == /\ ctr = {} /\ ~net /\ ~vol
         /\ FixMonitored => (stack = {} /\ ~journal)
         /\ FixDNS => dns = {}

\* ListClusterResources
List == /\ pc = "list"
        /\ res' = [c |-> ctr, n |-> net, v |-> vol]
        /\ pc' = IF Empty THEN "done" ELSE "dereg"
        /\ UNCHANGED <<ctr, net, vol, dns, stack, journal, monitored, warned, runs>>

\* DeregisterMesh: best effort, a failure is a warning.
Dereg == /\ pc = "dereg"
         /\ \/ /\ dns' = IF FixDNS THEN {} ELSE dns \ res.c
               /\ warned' = warned
            \/ /\ dns' = dns /\ warned' = TRUE
         /\ pc' = "mon"
         /\ UNCHANGED <<ctr, net, vol, stack, journal, res, monitored, runs>>

\* clusterMonitored: today, does a container of X mount the journal volume?
Mon == /\ pc = "mon"
       /\ monitored' = IF FixMonitored THEN journal \/ stack # {} ELSE ctr # {}
       /\ pc' = "ctrs"
       /\ UNCHANGED <<ctr, net, vol, dns, stack, journal, res, warned, runs>>

\* DeleteContainers: every container is tried; some can fail.
Ctrs == /\ pc = "ctrs"
        /\ \E gone \in SUBSET res.c :
             /\ ctr' = ctr \ gone
             /\ IF res.c \subseteq gone THEN pc' = "net" ELSE pc' = "idle"
        /\ UNCHANGED <<net, vol, dns, stack, journal, res, monitored, warned, runs>>

Net == /\ pc = "net"
       /\ net' = (net /\ ~res.n)
       /\ pc' = "vols"
       /\ UNCHANGED <<ctr, vol, dns, stack, journal, res, monitored, warned, runs>>

Vols == /\ pc = "vols"
        /\ vol' = (vol /\ ~res.v)
        /\ pc' = "forget"
        /\ UNCHANGED <<ctr, net, dns, stack, journal, res, monitored, warned, runs>>

\* forgetMonitoredNodes: sync the stack with the containers that exist and
\* remove the journal directories; failures are warnings.
Forget == /\ pc = "forget"
          /\ IF monitored
             THEN \/ /\ stack' = stack \cap ctr /\ journal' = FALSE /\ warned' = warned
                  \/ /\ stack' = stack /\ journal' = journal /\ warned' = TRUE
             ELSE UNCHANGED <<stack, journal, warned>>
          /\ pc' = "done"
          /\ UNCHANGED <<ctr, net, vol, dns, res, monitored, runs>>

\* A failed run lets the user run delete again.
Retry == /\ pc = "done" /\ warned /\ runs < MaxRuns
         /\ pc' = "idle"
         /\ UNCHANGED <<ctr, net, vol, dns, stack, journal, res, monitored, warned, runs>>

Next == Start \/ List \/ Dereg \/ Mon \/ Ctrs \/ Net \/ Vols \/ Forget \/ Abort \/ Retry

Spec == Init /\ [][Next]_vars

\* A run that ends without error and without a warning leaves nothing of X.
CleanAfterSuccess ==
  (pc = "done" /\ ~warned) =>
     /\ ctr = {} /\ ~net /\ ~vol
     /\ dns = {} /\ stack = {} /\ ~journal
================================================================================
