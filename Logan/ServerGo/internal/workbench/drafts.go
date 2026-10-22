package workbench

func (q *request) saveDraft(issue, b Record) (any, int, *apiError) {
	for _, draft := range q.forIssue("editableDrafts", str(issue["id"])) {
		if draft["actor"] == q.actor.Actor && !q.expired(draft) && !q.hasExpiredRefs(draft, map[string]bool{}) && !(q.hasSourceRefs(draft, map[string]bool{}) && q.permission("source") != nil) {
			return nil, 0, fail(409, "DRAFT_EXISTS", "Editable draft already exists; reload it and update its revision")
		}
	}
	d := q.s.state.add("editableDrafts", q.project, Record{"issueId": issue["id"], "actor": q.actor.Actor, "editable": true, "revision": 1, "updatedAt": now(), "expiresAt": earliestExpiry(Record{}, q.s.cfg.Retention)})
	d["evidenceIds"] = stringsOf(issue["evidenceIds"])
	copyDraftFields(d, b)
	d["expiresAt"] = q.dependencyExpiry(d)
	return d, 201, nil
}
func copyDraftFields(d, b Record) {
	for _, field := range []string{"mode", "turnId", "summary", "status", "owner"} {
		if v, ok := b[field]; ok {
			d[field] = v
		}
	}
	d["updatedAt"] = now()
}

func (q *request) refreshDraftDependencies(d Record) {
	issue, _ := q.get("issues", str(d["issueId"]))
	if str(d["summary"]) == "" {
		d["evidenceIds"] = stringsOf(issue["evidenceIds"])
	} else {
		d["evidenceIds"] = unique(append(stringsOf(d["evidenceIds"]), stringsOf(issue["evidenceIds"])...))
	}
	d["expiresAt"] = q.dependencyExpiry(d)
}
