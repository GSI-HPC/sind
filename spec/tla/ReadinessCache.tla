---------------------------- MODULE ReadinessCache ----------------------------
(* The cache of passed probes of a readiness wait (internal/probe/probe.go,
   untilReady and progress), fed by the events of monitor.Watcher
   (internal/monitor/watcher.go, broadcastLoop).
   - The environment flips the condition of a probe (a unit goes active or
     fails).
   - The systemd monitor sends one event for each flip while it is "up".
     Before it is attached, and after it is dead, it sends nothing.
   - The broadcast loop drops an event when the buffer of the subscriber
     is full.
   - A dead monitor sends one monitor-error event. The broadcast loop
     never drops it: it drops the oldest queued event to make room.
   - The wait runs rounds. It skips a probe that passed while it trusts
     the events, and has seen a unit event, which shows that the monitor
     is attached. A received event clears the probe of its unit; a receive
     that leaves Cap-1 or more events queued clears all (an event may have
     been dropped); a monitor error ends the trust.

   Question: when the wait says "ready", does every probe hold?

   ErrDroppable and UnitEvidence select the code (FALSE, TRUE) or a design
   that the model shows to be wrong:
   - ErrDroppable: the broadcast loop drops a monitor error as it drops
     any other event.
   - UnitEvidence = FALSE: the wait skips a passed probe before it has
     seen a unit event. *)
EXTENDS Naturals, Sequences

CONSTANTS N,            \* number of probes, 1..N
          Cap,          \* subscriber buffer capacity
          MaxFlips,     \* bound on environment flips
          AllowDie,     \* the monitor can stop
          LateAttach,   \* the monitor attaches some time after the wait starts
          ErrDroppable, \* the monitor error can be dropped
          UnitEvidence  \* a passed probe is skipped only after a unit event

Probes == 1..N
VARIABLES cond, mon, buf, passed, trusted, unitEvents, wpc, next, flips, changed

vars == <<cond, mon, buf, passed, trusted, unitEvents, wpc, next, flips, changed>>
None == [p \in Probes |-> FALSE]

Init == /\ cond = None
        /\ mon = IF LateAttach THEN "attaching" ELSE "up"
        /\ buf = <<>>
        /\ passed = None
        /\ trusted = TRUE
        /\ unitEvents = FALSE
        /\ wpc = "round"
        /\ next = 1
        /\ flips = 0
        /\ changed = None

\* Non-blocking send of broadcastLoop: drop on a full buffer.
Deliver(ev) == IF Len(buf) < Cap THEN buf' = Append(buf, ev) ELSE buf' = buf

\* Send of a monitor error: the oldest event makes room on a full buffer.
DeliverErr(ev) == IF Len(buf) < Cap THEN buf' = Append(buf, ev)
                                    ELSE buf' = Append(Tail(buf), ev)

Flip(p) == /\ wpc # "ready" /\ flips < MaxFlips
           /\ cond' = [cond EXCEPT ![p] = ~@]
           /\ flips' = flips + 1
           /\ changed' = [changed EXCEPT ![p] = TRUE]
           /\ IF mon = "up" THEN Deliver([kind |-> "unit", p |-> p]) ELSE buf' = buf
           /\ UNCHANGED <<mon, passed, trusted, unitEvents, wpc, next>>

Attach == /\ mon = "attaching" /\ mon' = "up"
          /\ UNCHANGED <<cond, buf, passed, trusted, unitEvents, wpc, next, flips, changed>>

Die == /\ AllowDie /\ mon = "up" /\ wpc # "ready"
       /\ mon' = "dead"
       /\ IF ErrDroppable THEN Deliver([kind |-> "err", p |-> 0])
                          ELSE DeliverErr([kind |-> "err", p |-> 0])
       /\ UNCHANGED <<cond, passed, trusted, unitEvents, wpc, next, flips, changed>>

\* One probe of a round.
Check == /\ wpc = "round" /\ next <= N
         /\ IF passed[next] THEN /\ next' = next + 1 /\ UNCHANGED <<passed, wpc>>
            ELSE IF cond[next]
                 THEN /\ passed' = [passed EXCEPT ![next] = trusted /\ (unitEvents \/ ~UnitEvidence)]
                      /\ next' = next + 1 /\ wpc' = wpc
                 ELSE /\ wpc' = "wait" /\ UNCHANGED <<passed, next>>
         /\ UNCHANGED <<cond, mon, buf, trusted, unitEvents, flips, changed>>

AllPassed == /\ wpc = "round" /\ next > N /\ wpc' = "ready"
             /\ UNCHANGED <<cond, mon, buf, passed, trusted, unitEvents, next, flips, changed>>

\* progress.take for one event.
Receive == /\ wpc = "wait" /\ buf # <<>>
           /\ LET ev   == Head(buf)
                  rest == Tail(buf)
                  p1   == IF Len(rest) >= Cap - 1 THEN None ELSE passed
              IN /\ buf' = rest
                 /\ IF ev.kind = "err"
                    THEN /\ trusted' = FALSE /\ passed' = None
                         /\ unitEvents' = unitEvents
                    ELSE /\ trusted' = trusted /\ unitEvents' = TRUE
                         /\ passed' = [p1 EXCEPT ![ev.p] = FALSE]
           /\ UNCHANGED <<cond, mon, wpc, next, flips, changed>>

\* The ticker (after takeQueued drained the queue) or a relevant event.
NewRound == /\ wpc = "wait" /\ buf = <<>>
            /\ wpc' = "round" /\ next' = 1 /\ changed' = None
            /\ UNCHANGED <<cond, mon, buf, passed, trusted, unitEvents, flips>>

Next == \/ \E p \in Probes : Flip(p)
        \/ Attach \/ Die \/ Check \/ AllPassed \/ Receive \/ NewRound

Spec == Init /\ [][Next]_vars

\* Safety: when the wait says "ready", each probe holds now, or its
\* condition changed during the last round (a race no poller can close).
\* A probe that went false before the last round began and that the wait
\* skipped is a stale cache entry.
ReadyIsSound == wpc = "ready" => \A p \in Probes : cond[p] \/ changed[p]
================================================================================
