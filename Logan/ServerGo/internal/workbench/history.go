package workbench

import "time"

func (q *request) history(issue, before Record, action, reason string) Record {
	return q.s.state.add("issueHistory", q.project, Record{"issueId": issue["id"], "actor": q.actor.Actor, "action": action, "reason": reason, "before": before, "after": clone(issue), "revision": issue["revision"], "expiresAt": time.Now().Add(q.s.cfg.Retention).UTC().Format(time.RFC3339Nano)})
}
