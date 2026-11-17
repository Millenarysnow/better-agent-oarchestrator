package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newLocalReviewDecisionCommand(ctx *commandContext) *cobra.Command {
	var session, delivery, decision, note string
	cmd := &cobra.Command{Use: "decide [worker-session-id]", Short: "Accept, return or request user input for a local delivery", Args: atMostOneArg, RunE: func(cmd *cobra.Command, args []string) error {
		id := reviewTargetSession(args, session)
		if id == "" || strings.TrimSpace(delivery) == "" {
			return usageError{errors.New("worker session and --delivery are required")}
		}
		if decision != "accepted" && decision != "changes_requested" && decision != "needs_input" {
			return usageError{errors.New("--decision must be accepted, changes_requested or needs_input")}
		}
		if strings.TrimSpace(os.Getenv(envReviewSessionID)) != "" {
			return usageError{errors.New("reviewers submit findings; acceptance belongs to the coordinator or user")}
		}
		if decision != "accepted" && strings.TrimSpace(note) == "" {
			return usageError{errors.New("--note is required for a return or input decision")}
		}
		req := struct {
			DeliveryID string `json:"deliveryId"`
			Decision   string `json:"decision"`
			Note       string `json:"note,omitempty"`
			Actor      string `json:"actorSessionId,omitempty"`
		}{delivery, decision, note, os.Getenv("AO_SESSION_ID")}
		var response struct {
			Decision string `json:"decision"`
		}
		if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/reviews/local/decision", req, &response); err != nil {
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "local delivery %s: %s\n", delivery, response.Decision)
		return err
	}}
	cmd.Flags().StringVar(&session, "session", "", "Worker session id")
	cmd.Flags().StringVar(&delivery, "delivery", "", "Completion report / local delivery id")
	cmd.Flags().StringVar(&decision, "decision", "", "accepted, changes_requested or needs_input")
	cmd.Flags().StringVar(&note, "note", "", "Acceptance explanation or blocking rework reason")
	return cmd
}
