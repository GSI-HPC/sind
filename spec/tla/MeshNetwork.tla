------------------------------ MODULE MeshNetwork ------------------------------
(* The mesh network of a realm and its DNS container, as a create makes
   them (pkg/mesh: ensureMeshNetwork, createMeshNetwork, ensureDNS) and as
   a create that fails removes them (pkg/cluster: CreateInRealm, and the
   rollback of Create).

   A client that finds no mesh network creates one without a subnet, reads
   the subnet that Docker picked, removes the network, and creates it again
   with that subnet and the label of the pinned DNS address.  The DNS
   container gets the address that the client read.  A create that fails
   removes the mesh when its own call created the network (created).  Any
   step can fail, and a client can be killed at any step.

   Locked = TRUE: each client holds the realm lock from its first step to
   its last, as the sind command does.  Locked = FALSE: clients that do not
   take the lock, which CreateInRealm asks its caller for.
   ExistsIsCreated = TRUE is the variant in which "already exists" from a
   network create counts as a network that this call created. *)
EXTENDS Naturals

CONSTANTS Procs, Locked, ExistsIsCreated, MaxId

VARIABLES net,      \* the network: [id: 0 (none) or its ID, maker, pin: has the DNS label]
          dns,      \* the DNS container: none | pinned | free (an address that Docker picked)
          nextId,   \* the daemon's next network ID
          lock,     \* the clients that hold the realm lock
          pc,       \* the next step of a client
          created,  \* the created flag of the client's Manager
          pinOf     \* the ID of the network whose pinned address the client read; 0: none

vars == <<net, dns, nextId, lock, pc, created, pinOf>>

None == [id |-> 0, maker |-> CHOOSE p \in Procs : TRUE, pin |-> FALSE]

Init == /\ net = None
        /\ dns = "none"
        /\ nextId = 1
        /\ lock = {}
        /\ pc = [p \in Procs |-> "idle"]
        /\ created = [p \in Procs |-> FALSE]
        /\ pinOf = [p \in Procs |-> 0]

Go(p, to) == pc' = [pc EXCEPT ![p] = to]
Ended(p)  == pc[p] \in {"done", "failed", "dead"}
\* A client that ends lets the lock go; the lock of a killed client is
\* taken over (RealmLock.tla).
Unlock(p) == lock' = lock \ {p}

Start(p) == /\ pc[p] = "idle"
            /\ Locked => lock = {}
            /\ lock' = lock \cup {p}
            /\ Go(p, "check")
            /\ UNCHANGED <<net, dns, nextId, created, pinOf>>

\* ensureMeshNetwork: docker network inspect.
Check(p) == /\ pc[p] = "check"
            /\ IF net.id # 0
               THEN /\ pinOf' = [pinOf EXCEPT ![p] = IF net.pin THEN net.id ELSE 0]
                    /\ Go(p, "dns")
               ELSE /\ Go(p, "plain")
                    /\ UNCHANGED pinOf
            /\ UNCHANGED <<net, dns, nextId, lock, created>>

\* "already exists" from a network create.
Exists(p) == /\ created' = [created EXCEPT ![p] = ExistsIsCreated]
             /\ Go(p, "reread")
             /\ UNCHANGED <<net, nextId>>

\* createMeshNetwork: docker network create, without a subnet.
Plain(p) == /\ pc[p] = "plain"
            /\ IF net.id # 0
               THEN Exists(p)
               ELSE /\ nextId <= MaxId
                    /\ net' = [id |-> nextId, maker |-> p, pin |-> FALSE]
                    /\ nextId' = nextId + 1
                    /\ created' = [created EXCEPT ![p] = TRUE]
                    /\ Go(p, "inspect")
            /\ UNCHANGED <<dns, lock, pinOf>>

\* docker network inspect, then pinnedLayout: a subnet to pin, or one that
\* is too small (or no attempt left), which stays as it is.
Inspect(p) == /\ pc[p] = "inspect"
              /\ IF net.id = 0
                 THEN Go(p, "error")
                 ELSE Go(p, "remove") \/ Go(p, "dns")
              /\ UNCHANGED <<net, dns, nextId, lock, created, pinOf>>

\* docker network rm, by the name.  Docker refuses while a container is on
\* the network.  The flag stays set when the removal fails.
Remove(p) == /\ pc[p] = "remove"
             /\ IF net.id = 0 \/ dns # "none"
                THEN /\ Go(p, "error")
                     /\ UNCHANGED <<net, created>>
                ELSE /\ net' = None
                     /\ created' = [created EXCEPT ![p] = FALSE]
                     /\ Go(p, "pinned")
             /\ UNCHANGED <<dns, nextId, lock, pinOf>>

\* docker network create with the subnet and the DNS label.  Another
\* network can have taken the subnet: the client starts over.
Pinned(p) == /\ pc[p] = "pinned"
             /\ IF net.id # 0
                THEN Exists(p) /\ UNCHANGED pinOf
                ELSE \/ /\ nextId <= MaxId
                        /\ net' = [id |-> nextId, maker |-> p, pin |-> TRUE]
                        /\ nextId' = nextId + 1
                        /\ created' = [created EXCEPT ![p] = TRUE]
                        /\ pinOf' = [pinOf EXCEPT ![p] = nextId]
                        /\ Go(p, "dns")
                     \/ /\ Go(p, "plain")
                        /\ UNCHANGED <<net, nextId, created, pinOf>>
             /\ UNCHANGED <<dns, lock>>

\* concurrentMeshDNSIP: the labels of the network that exists now.
Reread(p) == /\ pc[p] = "reread"
             /\ pinOf' = [pinOf EXCEPT ![p] = IF net.id # 0 /\ net.pin THEN net.id ELSE 0]
             /\ Go(p, "dns")
             /\ UNCHANGED <<net, dns, nextId, lock, created>>

\* ensureDNS: the container that exists, or a new one at the address that
\* the client read.  Docker refuses a container without its network, and
\* an address that the network of this name does not have.
DNS(p) == /\ pc[p] = "dns"
          /\ IF dns # "none"
             THEN Go(p, "made") /\ dns' = dns
             ELSE IF net.id = 0 \/ (pinOf[p] # 0 /\ pinOf[p] # net.id)
             THEN Go(p, "error") /\ dns' = dns
             ELSE /\ dns' = IF pinOf[p] # 0 THEN "pinned" ELSE "free"
                  /\ Go(p, "made")
          /\ UNCHANGED <<net, nextId, lock, created, pinOf>>

\* Any docker command of EnsureMesh can fail.
Fail(p) == /\ pc[p] \in {"check", "plain", "inspect", "remove", "pinned", "reread", "dns"}
           /\ Go(p, "error")
           /\ UNCHANGED <<net, dns, nextId, lock, created, pinOf>>

\* CreateInRealm: EnsureMesh failed.  It removes the mesh when the call
\* created the network.
Error(p) == /\ pc[p] = "error"
            /\ IF created[p]
               THEN Go(p, "rollback") /\ UNCHANGED lock
               ELSE Go(p, "failed") /\ Unlock(p)
            /\ UNCHANGED <<net, dns, nextId, created, pinOf>>

\* The rest of the create fails.  rollbackRemovesMesh: the mesh goes when
\* the call created the network and no cluster uses it.
FailLate(p) == /\ pc[p] = "made"
               /\ IF created[p] /\ \A q \in Procs \ {p} : pc[q] # "done"
                  THEN Go(p, "rollback") /\ UNCHANGED lock
                  ELSE Go(p, "failed") /\ Unlock(p)
               /\ UNCHANGED <<net, dns, nextId, created, pinOf>>

\* CleanupMesh: the containers, then the network, by their names.
Rollback(p) == /\ pc[p] = "rollback"
               /\ dns' = "none"
               /\ net' = None
               /\ Go(p, "failed")
               /\ Unlock(p)
               /\ UNCHANGED <<nextId, created, pinOf>>

\* The create ends well: the cluster runs on the mesh.
Commit(p) == /\ pc[p] = "made"
             /\ Go(p, "done")
             /\ Unlock(p)
             /\ UNCHANGED <<net, dns, nextId, created, pinOf>>

Kill(p) == /\ pc[p] # "idle" /\ ~Ended(p)
           /\ Go(p, "dead")
           /\ Unlock(p)
           /\ UNCHANGED <<net, dns, nextId, created, pinOf>>

Next == \E p \in Procs :
          \/ Start(p) \/ Check(p) \/ Plain(p) \/ Inspect(p) \/ Remove(p)
          \/ Pinned(p) \/ Reread(p) \/ DNS(p) \/ Fail(p) \/ Error(p)
          \/ FailLate(p) \/ Rollback(p) \/ Commit(p) \/ Kill(p)
Spec == Init /\ [][Next]_vars

\* A client removes only a network that it created itself: to pin its
\* subnet, and when its create fails.
RemovesOwn == \A p \in Procs : pc[p] \in {"remove", "rollback"} => (net.id = 0 \/ net.maker = p)
\* The DNS container of a mesh with a pinned address has that address.
PinnedDNS == (net.id # 0 /\ net.pin /\ dns # "none") => dns = "pinned"
\* A cluster whose create ended well has its mesh: the network and the DNS
\* container.
MeshKept == \A p \in Procs : pc[p] = "done" => (net.id # 0 /\ dns # "none")
================================================================================
