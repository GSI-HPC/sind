------------------------------ MODULE DeleteRetry ------------------------------
(* sind delete cluster X (pkg/cluster/delete.go, deleteClusterResources and
   removeClusterResources) of a monitored cluster in a realm that has other
   clusters, so the mesh and the monitoring stack stay and X is taken out of
   them.  Any step can fail, and the process can be killed between any two
   steps.  The user then runs the same delete again, up to MaxRuns times.

   Question: once a run of delete ends without error and without a warning,
   is every trace of X gone: containers, network, volumes, known_hosts
   entries, DNS records, monitoring targets and the journal directory?

   The code removes the containers before it knows that the rest succeeds,
   so a later run cannot tell from them what is left.  Two traces tell it
   instead, each the last one that its part of the delete removes:
   - the DNS records of the cluster's domain: the nodes are still on the
     mesh.  They go only once the known_hosts entries are gone.
   - the journal directory of the cluster: the monitoring stack still has
     it.  It goes only once Alloy is synced.

   The constants select the code (all TRUE) or a design that the model
   shows to be wrong:
   - Traces = FALSE: a run looks only at the containers it finds.  It
     removes the DNS records and known_hosts entries of those, decides
     from them whether the cluster is monitored, and has nothing to do
     without containers, network and volumes.
   - KnownFirst = FALSE: the DNS records and the known_hosts entries are
     removed each on its own, so the records can go while entries stay.
   - JournalLast = FALSE: the journal directory is removed before Alloy
     is synced. *)
EXTENDS Naturals, FiniteSets

CONSTANTS Nodes, MaxRuns, Traces, KnownFirst, JournalLast

VARIABLES ctr, net, vol, known, dns, stack, journal,  \* the world
          pc, res, monitored, warned, failed, runs    \* one run of delete

world == <<ctr, net, vol, known, dns, stack, journal>>
vars  == <<ctr, net, vol, known, dns, stack, journal,
           pc, res, monitored, warned, failed, runs>>

Init == /\ ctr = Nodes /\ net = TRUE /\ vol = TRUE
        /\ known = Nodes /\ dns = Nodes /\ stack = Nodes /\ journal = TRUE
        /\ pc = "idle" /\ res = [c |-> {}, n |-> FALSE, v |-> FALSE]
        /\ monitored = FALSE /\ warned = FALSE /\ failed = FALSE /\ runs = 0

\* The process is killed (SIGKILL, OOM): the run ends at once, and says
\* nothing.
Kill == /\ pc \notin {"idle", "done"}
        /\ pc' = "idle"
        /\ UNCHANGED <<world, res, monitored, warned, failed, runs>>

Start == /\ pc = "idle" /\ runs < MaxRuns
         /\ pc' = "list" /\ runs' = runs + 1
         /\ warned' = FALSE /\ failed' = FALSE
         /\ UNCHANGED <<world, res, monitored>>

\* listClusterResources.  Without Traces, a cluster without containers,
\* network and volumes has nothing to delete.
List == /\ pc = "list"
        /\ res' = [c |-> ctr, n |-> net, v |-> vol]
        /\ pc' = IF ~Traces /\ ctr = {} /\ ~net /\ ~vol THEN "done" ELSE "mon"
        /\ UNCHANGED <<world, monitored, warned, failed, runs>>

\* clusterMonitored, by the containers, or journalsLeft for a cluster
\* without them, which can fail: a warning.
Mon == /\ pc = "mon"
       /\ IF res.c # {}
          THEN monitored' = TRUE /\ warned' = warned
          ELSE IF Traces
               THEN \/ monitored' = journal /\ warned' = warned
                    \/ monitored' = FALSE /\ warned' = TRUE
               ELSE monitored' = FALSE /\ warned' = warned
       /\ pc' = "dereg"
       /\ UNCHANGED <<world, res, failed, runs>>

\* What a deregistration takes out of a set of names: all of the cluster's
\* domain, or without Traces those of the containers found.
Without(names) == IF Traces THEN {} ELSE names \ res.c

\* deregisterClusters.  A cluster without containers is looked at first
\* (HasDomainRecords), which can fail: a warning.  Every other failure is
\* a warning too.
Dereg == /\ pc = "dereg"
         /\ IF res.c = {} /\ (~Traces \/ dns = {})
            THEN UNCHANGED <<known, dns, warned>>
            ELSE \/ known' = Without(known) /\ dns' = Without(dns) /\ warned' = warned
                 \/ UNCHANGED <<known, dns>> /\ warned' = TRUE
                 \/ known' = Without(known) /\ dns' = dns /\ warned' = TRUE
                 \/ /\ ~KnownFirst
                    /\ known' = known /\ dns' = Without(dns) /\ warned' = TRUE
         /\ pc' = "ctrs"
         /\ UNCHANGED <<ctr, net, vol, stack, journal, res, monitored, failed, runs>>

\* deleteContainers: every container is tried; some can fail, which ends
\* the run with an error.
Ctrs == /\ pc = "ctrs"
        /\ \E gone \in SUBSET res.c :
             /\ ctr' = ctr \ gone
             /\ pc' = IF res.c \subseteq gone THEN "net" ELSE "idle"
        /\ UNCHANGED <<net, vol, known, dns, stack, journal, res, monitored, warned, failed, runs>>

\* removeClusterNetwork.  A failure ends the run with an error, after the
\* monitoring stack has forgotten the cluster.
Net == /\ pc = "net"
       /\ \/ net' = (net /\ ~res.n) /\ pc' = "vols" /\ failed' = failed
          \/ res.n /\ net' = net /\ pc' = "forget" /\ failed' = TRUE
       /\ UNCHANGED <<ctr, vol, known, dns, stack, journal, res, monitored, warned, runs>>

\* deleteVolumes, with the same failure.
Vols == /\ pc = "vols"
        /\ \/ vol' = (vol /\ ~res.v) /\ failed' = failed
           \/ res.v /\ vol' = vol /\ failed' = TRUE
        /\ pc' = "forget"
        /\ UNCHANGED <<ctr, net, known, dns, stack, journal, res, monitored, warned, runs>>

\* forgetMonitoredNodes: start the stack, sync Alloy with the containers
\* that exist, remove the journal directory.  Failures are warnings.
Forget == /\ pc = "forget"
          /\ IF monitored
             THEN \/ stack' = stack \cap ctr /\ journal' = FALSE /\ warned' = warned
                  \/ UNCHANGED <<stack, journal>> /\ warned' = TRUE
                  \/ IF JournalLast
                     THEN stack' = stack \cap ctr /\ journal' = journal /\ warned' = TRUE
                     ELSE stack' = stack /\ journal' = FALSE /\ warned' = TRUE
             ELSE UNCHANGED <<stack, journal, warned>>
          /\ pc' = IF failed THEN "idle" ELSE "done"
          /\ UNCHANGED <<ctr, net, vol, known, dns, res, monitored, failed, runs>>

\* The user runs the delete again after a warning.
Retry == /\ pc = "done" /\ warned /\ runs < MaxRuns
         /\ pc' = "idle"
         /\ UNCHANGED <<world, res, monitored, warned, failed, runs>>

Next == Start \/ List \/ Mon \/ Dereg \/ Ctrs \/ Net \/ Vols \/ Forget \/ Kill \/ Retry

Spec == Init /\ [][Next]_vars

\* A run that ends without error and without a warning leaves nothing of X.
CleanAfterSuccess ==
  (pc = "done" /\ ~warned) =>
     /\ ctr = {} /\ ~net /\ ~vol
     /\ known = {} /\ dns = {} /\ stack = {} /\ ~journal
================================================================================
