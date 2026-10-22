package workbench

import "strings"

// Linking is a relationship change, never an identity correction of the log.
// Only conflicting known values require confirmation; missing identity stays unknown.
func (q *request) linkAsset(issue, b Record) (any, int, *apiError) {
	a, e := q.get("assets", str(b["assetId"]))
	if e != nil {
		return nil, 0, e
	}
	if contains(stringsOf(issue["assetIds"]), str(a["id"])) {
		return issue, 200, nil
	}
	reason := strings.TrimSpace(str(b["reason"]))
	if e = confirmAssetContext(issue, a, b); e != nil {
		return nil, 0, e
	}
	if len(assetContextDifferences(issue, a)) > 0 {
		if e = q.match(issue, "revision"); e != nil {
			return nil, 0, e
		}
	}
	before := clone(issue)
	issue["assetIds"] = unique(append(stringsOf(issue["assetIds"]), str(a["id"])))
	issue["revision"] = num(issue["revision"]) + 1
	issue["updatedAt"] = now()
	if reason == "" {
		reason = "关联已有日志：" + str(a["name"])
	}
	q.history(issue, before, "edited", reason)
	return issue, 200, nil
}

func assetContextDifferences(issue, a Record) []string {
	context := record(issue["context"])
	differences := []string{}
	for _, key := range []string{"app", "platform", "environment", "device", "version", "build"} {
		left, right := strings.TrimSpace(str(context[key])), strings.TrimSpace(str(a[key]))
		if left != "" && right != "" && left != right {
			differences = append(differences, key)
		}
	}
	return differences
}

func confirmAssetContext(issue, a, decision Record) *apiError {
	differences := assetContextDifferences(issue, a)
	if len(differences) == 0 {
		return nil
	}
	if decision["confirmed"] != true {
		return fail(409, "CONTEXT_CONFIRMATION_REQUIRED", "Confirm known log/issue differences before linking: "+strings.Join(differences, ", "))
	}
	if strings.TrimSpace(str(decision["reason"])) == "" {
		return fail(422, "REASON_REQUIRED", "Explain why the different log belongs in this investigation")
	}
	return nil
}
