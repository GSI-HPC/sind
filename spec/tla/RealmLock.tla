------------------------------- MODULE RealmLock -------------------------------
(* The daemon part of the realm lock (pkg/state/daemonlock.go, lockDaemon).
   The lock is a network named <realm>-lock; the daemon creates a name at
   most once.  A client that finds the lock taken inspects it.  When the
   holder runs on the same host (same boot, same PID namespace) and its PID
   is gone, the client removes the stale lock by its network ID and tries
   again.  Any client can be killed at any time and never releases then.
   ByName = TRUE is the variant that removes the stale lock by name. *)
EXTENDS Naturals, FiniteSets

CONSTANTS Procs, ByName, MaxId

\* c runs on host h2; every other client on h1.
Host == [p \in Procs |-> IF p = "c" THEN "h2" ELSE "h1"]

VARIABLES lock,    \* [held: BOOLEAN, owner: Procs, id: Nat]
          nextId,  \* the daemon's next network ID
          pc,      \* idle | inspect | stale | cs | done
          seen,    \* the holder a client inspected: [owner, id]
          alive

vars == <<lock, nextId, pc, seen, alive>>

Init == /\ lock = [held |-> FALSE, owner |-> CHOOSE p \in Procs : TRUE, id |-> 0]
        /\ nextId = 1
        /\ pc = [p \in Procs |-> "idle"]
        /\ seen = [p \in Procs |-> [owner |-> p, id |-> 0]]
        /\ alive = [p \in Procs |-> TRUE]

Live(p) == alive[p] /\ pc[p] # "done"

\* docker network create: atomic create-if-absent.
Create(p) == /\ Live(p) /\ pc[p] = "idle" /\ nextId <= MaxId
             /\ IF ~lock.held
                THEN /\ lock' = [held |-> TRUE, owner |-> p, id |-> nextId]
                     /\ nextId' = nextId + 1
                     /\ pc' = [pc EXCEPT ![p] = "cs"]
                ELSE /\ pc' = [pc EXCEPT ![p] = "inspect"]
                     /\ UNCHANGED <<lock, nextId>>
             /\ UNCHANGED <<seen, alive>>

\* docker network inspect, then the decision of lockDaemon.
Inspect(p) == /\ Live(p) /\ pc[p] = "inspect"
              /\ IF ~lock.held
                 THEN pc' = [pc EXCEPT ![p] = "idle"] /\ seen' = seen
                 ELSE /\ seen' = [seen EXCEPT ![p] = [owner |-> lock.owner, id |-> lock.id]]
                      /\ IF Host[lock.owner] = Host[p] /\ ~alive[lock.owner]
                         THEN pc' = [pc EXCEPT ![p] = "stale"]
                         ELSE pc' = [pc EXCEPT ![p] = "idle"]   \* wait, try again
              /\ UNCHANGED <<lock, nextId, alive>>

\* Remove the stale lock: by ID (today) or by name (variant).
RemoveStale(p) == /\ Live(p) /\ pc[p] = "stale"
                  /\ IF lock.held /\ (ByName \/ lock.id = seen[p].id)
                     THEN lock' = [lock EXCEPT !.held = FALSE]
                     ELSE lock' = lock                \* not found: ignored
                  /\ pc' = [pc EXCEPT ![p] = "idle"]
                  /\ UNCHANGED <<nextId, seen, alive>>

\* The unlock function: remove by the ID this client created.
Release(p) == /\ Live(p) /\ pc[p] = "cs"
              /\ lock' = IF lock.held /\ lock.owner = p THEN [lock EXCEPT !.held = FALSE] ELSE lock
              /\ pc' = [pc EXCEPT ![p] = "done"]
              /\ UNCHANGED <<nextId, seen, alive>>

Crash(p) == /\ Live(p) /\ alive' = [alive EXCEPT ![p] = FALSE]
            /\ UNCHANGED <<lock, nextId, pc, seen>>

Next == \E p \in Procs : Create(p) \/ Inspect(p) \/ RemoveStale(p) \/ Release(p) \/ Crash(p)
Spec == Init /\ [][Next]_vars

\* No two live clients change the realm at once, and a client that does
\* holds the lock itself.
MutualExclusion == \A p, q \in Procs : (p # q /\ Live(p) /\ Live(q)) => ~(pc[p] = "cs" /\ pc[q] = "cs")
HolderOwnsLock  == \A p \in Procs : (Live(p) /\ pc[p] = "cs") => (lock.held /\ lock.owner = p)
================================================================================
