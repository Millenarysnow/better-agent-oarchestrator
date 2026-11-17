package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestLocalReviewTriggerDoesNotEnablePRInjection(t *testing.T) {
	cfg := setReviewEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{"created":true}`)
	writeRunFileFor(t, cfg, srv)
	if _, _, err := executeCLI(t, aliveDeps(), "review", "trigger", "worker-1", "--local"); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(capture.body), &body); err != nil {
		t.Fatal(err)
	}
	if body["local"] != true || body["enableAutoInject"] == true || body["prUrl"] != nil {
		t.Fatalf("body=%v", body)
	}
}

func TestLocalReviewDecisionCarriesActorAndPreservesDaemonError(t *testing.T) {
	cfg := setReviewEnv(t)
	t.Setenv("AO_SESSION_ID", "orchestrator-1")
	srv, capture := reviewServer(t, http.StatusConflict, `{"error":"conflict","code":"REVIEW_CONFLICT","message":"delivery changed","requestId":"request-1"}`)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, aliveDeps(), "review", "decide", "worker-1", "--delivery", "report-1", "--decision", "accepted")
	if err == nil {
		t.Fatal("daemon error was lost")
	}
	var body map[string]any
	if e := json.Unmarshal([]byte(capture.body), &body); e != nil {
		t.Fatal(e)
	}
	if body["actorSessionId"] != "orchestrator-1" || body["deliveryId"] != "report-1" || capture.path != "/api/v1/sessions/worker-1/reviews/local/decision" {
		t.Fatalf("request=%+v %v", capture, body)
	}
}

func TestLocalReviewCommandValidation(t *testing.T) {
	for _, args := range [][]string{
		{"review", "trigger", "worker-1", "--local", "--pr", "https://github.com/o/r/pull/1"},
		{"review", "decide", "worker-1", "--decision", "accepted"},
		{"review", "decide", "worker-1", "--delivery", "r1", "--decision", "changes_requested"},
	} {
		t.Run(args[1]+args[len(args)-1], func(t *testing.T) {
			setReviewEnv(t)
			_, _, err := executeCLI(t, aliveDeps(), args...)
			var usage usageError
			if !errors.As(err, &usage) {
				t.Fatalf("want usage error, got %v", err)
			}
		})
	}
}
