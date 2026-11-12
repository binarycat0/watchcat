package github

import (
	"testing"
	"time"
)

// Shaped like the real dremio PR-29919 history (newest first).
const statusLines = `{"id":9,"context":"dremio-pull-requests","state":"success","created_at":"2026-10-16T09:32:39Z","target_url":"https://j/pr/PR-1/7/"}
{"id":8,"context":"bazel","state":"error","created_at":"2026-10-16T08:55:04Z","target_url":"https://j/bazel/PR-1/7/"}
{"id":7,"context":"bazel","state":"pending","created_at":"2026-10-16T08:48:42Z","target_url":"https://j/bazel/PR-1/7/"}
{"id":6,"context":"bazel","state":"pending","created_at":"2026-10-16T08:42:58Z","target_url":"https://j/bazel/PR-1/7/"}
{"id":5,"context":"default","state":"success","created_at":"2026-10-16T08:40:55Z","target_url":"https://j/dispatch/2044/"}
{"id":4,"context":"default","state":"pending","created_at":"2026-10-16T08:40:43Z","target_url":""}
{"id":3,"context":"dremio-pull-requests","state":"pending","created_at":"2026-10-16T08:38:08Z","target_url":"https://j/pr/PR-1/7/"}
{"id":12,"context":"bazel","state":"pending","created_at":"2026-10-16T10:00:00Z","target_url":"https://j/bazel/PR-1/8/"}
{"id":2,"context":"dremio-pull-requests","state":"pending","created_at":"2026-10-16T08:00:00Z","target_url":"https://j/pr/PR-1/6/"}
`

func TestParseStatuses(t *testing.T) {
	runs, err := ParseStatuses([]byte(statusLines), "sha1")
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]int{}
	for i, r := range runs {
		byKey[r.RunKey] = i
	}
	if len(runs) != 5 {
		t.Fatalf("want 5 runs (pr #6 superseded, pr #7, default, bazel #7, bazel #8 rerun), got %+v", runs)
	}
	if old := runs[byKey["status:dremio-pull-requests:2"]]; old.Result != "superseded" || old.CompletedAt == nil {
		t.Fatalf("build #6 never reported and was replaced by #7: %+v", old)
	}
	pr := runs[byKey["status:dremio-pull-requests:3"]]
	if pr.Result != "success" || pr.CompletedAt.Sub(pr.StartedAt) != 54*time.Minute+31*time.Second {
		t.Fatalf("pr run: %+v", pr)
	}
	b7 := runs[byKey["status:bazel:6"]]
	if b7.Result != "error" || b7.CompletedAt.Sub(b7.StartedAt) != 12*time.Minute+6*time.Second || b7.URL != "https://j/bazel/PR-1/7/" {
		t.Fatalf("bazel #7 (two pendings, then error): %+v", b7)
	}
	b8 := runs[byKey["status:bazel:12"]]
	if b8.Result != "" || b8.CompletedAt != nil {
		t.Fatalf("bazel #8 is still running: %+v", b8)
	}
	def := runs[byKey["status:default:4"]]
	if def.URL != "https://j/dispatch/2044/" || def.Result != "success" {
		t.Fatalf("empty URL then URL is the same run: %+v", def)
	}
}

func TestParseCheckRuns(t *testing.T) {
	lines := `{"id":1,"name":"unit","status":"completed","conclusion":"failure","started_at":"2026-11-01T10:00:00Z","completed_at":"2026-11-01T10:07:30Z","html_url":"u1"}
{"id":2,"name":"unit","status":"in_progress","conclusion":null,"started_at":"2026-11-01T10:10:00Z","completed_at":null,"html_url":"u2"}
{"id":3,"name":"lint","status":"queued","conclusion":null,"started_at":null,"completed_at":null,"html_url":"u3"}
`
	runs, err := ParseCheckRuns([]byte(lines), "sha")
	if err != nil || len(runs) != 2 {
		t.Fatalf("queued runs are skipped: %+v %v", runs, err)
	}
	if runs[0].Result != "failure" || runs[0].CompletedAt.Sub(runs[0].StartedAt) != 7*time.Minute+30*time.Second || runs[0].RunKey != "check:1" {
		t.Fatalf("%+v", runs[0])
	}
	if runs[1].Result != "" || runs[1].CompletedAt != nil {
		t.Fatalf("rerun in progress: %+v", runs[1])
	}
}
