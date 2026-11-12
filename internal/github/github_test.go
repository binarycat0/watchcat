package github

import (
	"testing"

	"github.com/binarycat0/watchcat/internal/core"
)

const sample = `{"data":{"repository":{"pullRequest":{
 "title":"Fix it","state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"CHANGES_REQUESTED","headRefOid":"abc",
 "commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
   {"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS","detailsUrl":"u1"},
   {"__typename":"CheckRun","name":"unit","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"u2"},
   {"__typename":"CheckRun","name":"e2e","status":"IN_PROGRESS","conclusion":null,"detailsUrl":"u3"},
   {"__typename":"CheckRun","name":"docs","status":"COMPLETED","conclusion":"SKIPPED"},
   {"__typename":"StatusContext","context":"jenkins/pr","state":"ERROR","targetUrl":"u4"}
 ]}}}}]},
 "reviewThreads":{"nodes":[{"isResolved":false,"isOutdated":false},{"isResolved":true,"isOutdated":false},{"isResolved":false,"isOutdated":true}]}
}}}}`

func TestParse(t *testing.T) {
	s, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if s.CI != core.CIFailure || len(s.FailedChecks) != 2 || s.FailedChecks[1].Name != "jenkins/pr" || s.FailedChecks[1].URL != "u4" {
		t.Fatalf("checks: %+v", s)
	}
	if len(s.PendingChecks) != 1 || s.Unresolved != 1 || s.HeadSHA != "abc" {
		t.Fatalf("snapshot: %+v", s)
	}
	if s.Status() != core.StatusCIFailed {
		t.Fatalf("status %s", s.Status())
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte(`{"data":{"repository":null},"errors":[{"message":"Could not resolve to a Repository"}]}`)); err == nil {
		t.Fatal("expected GraphQL error")
	}
	if _, err := Parse([]byte(`{"data":{"repository":{"pullRequest":null}}}`)); err == nil {
		t.Fatal("expected not found")
	}
}
