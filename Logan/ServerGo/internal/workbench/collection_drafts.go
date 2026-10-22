package workbench

import (
	"strings"
	"time"
)

// Drafts preserve operator-entered push parameters without creating a request,
// invoking a provider, or claiming a client response.
func (q *request) collectionDraft(r, b Record) (any, int, *apiError) {
	creating := r == nil
	if !creating {
		if r["actor"] != q.actor.Actor {
			return nil, 0, fail(404, "NOT_FOUND", "Draft not found")
		}
		if q.expired(r) {
			return nil, 0, fail(410, "SOURCE_EXPIRED", "Draft expired; create a new draft")
		}
		if e := q.match(r, "revision"); e != nil {
			return nil, 0, e
		}
	}
	allowed := []string{"app", "targetType", "targetValue", "bindingConfirmed", "issueId", "title", "begin", "end"}
	for k, v := range b {
		if !contains(allowed, k) {
			return nil, 0, fail(422, "DRAFT_FIELD_INVALID", "Unsupported collection draft field")
		}
		if k == "bindingConfirmed" {
			if _, ok := v.(bool); !ok {
				return nil, 0, fail(422, "DRAFT_FIELD_INVALID", "Binding confirmation must be boolean")
			}
		} else if _, ok := v.(string); !ok {
			return nil, 0, fail(422, "DRAFT_FIELD_INVALID", "Draft fields must be strings")
		}
		// Presentation masks are never valid routing parameters. Clients editing
		// another field must omit the masked target to preserve its stored value.
		if k == "targetValue" && strings.Contains(strings.ToUpper(str(v)), "[REDACTED") {
			return nil, 0, fail(422, "DRAFT_TARGET_MASKED", "Omit an unchanged masked target or enter the actual push target")
		}
		if len(str(v)) > 2048 {
			return nil, 0, fail(422, "DRAFT_FIELD_INVALID", "Draft field too long")
		}
	}
	next := Record{"app": "com.hxl.xlb.test", "targetType": "device", "targetValue": "", "bindingConfirmed": false, "issueId": "", "title": "", "begin": "", "end": ""}
	if !creating {
		next = clone(r)
	}
	for _, k := range allowed {
		if v, ok := b[k]; ok {
			next[k] = v
		}
	}
	if next["app"] != "com.hxl.xlb.test" || !contains([]string{"device", "account", "alias"}, str(next["targetType"])) {
		return nil, 0, fail(422, "DRAFT_TARGET_INVALID", "Select configured Android test application and device/account/alias; tags are not configured")
	}
	if old, ok := r["targetType"]; ok && old != next["targetType"] {
		next["bindingConfirmed"] = false
	}
	if strings.TrimSpace(str(next["issueId"])) != "" {
		if _, e := q.get("issues", str(next["issueId"])); e != nil {
			return nil, 0, e
		}
	}
	begin, end := str(next["begin"]), str(next["end"])
	// Drafts may be incomplete; validate each entered endpoint independently.
	// The confirmation step still requires both endpoints before proceeding.
	var a, z time.Time
	for _, endpoint := range []struct {
		value  string
		target *time.Time
	}{{begin, &a}, {end, &z}} {
		if endpoint.value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, endpoint.value)
		if err != nil {
			return nil, 0, fail(422, "DRAFT_TIME_INVALID", "Use an ISO timestamp with time zone for each entered endpoint")
		}
		*endpoint.target = parsed
	}
	if begin != "" && end != "" && !z.After(a) {
		return nil, 0, fail(422, "DRAFT_TIME_INVALID", "End must be later than start")
	}
	next["actor"] = q.actor.Actor
	next["status"] = "draft"
	next["inputSource"] = "operator_entered"
	next["updatedAt"] = now()
	if creating {
		next["revision"] = 1
		next["expiresAt"] = time.Now().Add(q.s.cfg.Retention).UTC().Format(time.RFC3339Nano)
		return q.s.state.add("collectionDrafts", q.project, next), 201, nil
	}
	next["revision"] = num(r["revision"]) + 1
	q.s.state.bucket("collectionDrafts")[str(r["id"])] = next
	return next, 200, nil
}
func (q *request) collectionDraftList() []Record {
	out := []Record{}
	for _, r := range q.list("collectionDrafts") {
		if r["actor"] != q.actor.Actor {
			continue
		}
		if q.expired(r) {
			out = append(out, Record{"id": r["id"], "projectId": q.project, "actor": r["actor"], "status": "draft", "availability": "expired", "revision": r["revision"], "expiresAt": r["expiresAt"], "createdAt": r["createdAt"]})
		} else {
			out = append(out, r)
		}
	}
	return out
}
