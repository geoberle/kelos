package feedback_test

import (
	"testing"

	"github.com/hearthctl/kelos-orchestrator/internal/feedback"
)

func TestHasCommand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		command string
		want    bool
	}{
		{name: "command alone", body: "/lgtm", command: "/lgtm", want: true},
		{name: "command with surrounding space", body: "  /lgtm \n", command: "/lgtm", want: true},
		{name: "command followed by a remark on its line", body: "/lgtm acceptable", command: "/lgtm", want: true},
		{name: "command followed by a tab", body: "/lgtm\tfine", command: "/lgtm", want: true},
		{name: "command on the first of several lines", body: "/lgtm\nacceptable", command: "/lgtm", want: true},
		{name: "command on a later line", body: "The data loss is acceptable.\n\n/lgtm", command: "/lgtm", want: true},
		{name: "command on a line with Windows line ending", body: "fine\r\n/lgtm\r\nthanks", command: "/lgtm", want: true},
		{name: "indented command", body: "note\n   /reassess now", command: "/reassess", want: true},
		{name: "command inside a line", body: "please /lgtm this", command: "/lgtm", want: false},
		{name: "longer word that starts with the command", body: "/lgtmx", command: "/lgtm", want: false},
		{name: "quoted command", body: "> /lgtm", command: "/lgtm", want: false},
		{name: "another command", body: "/reassess", command: "/lgtm", want: false},
		{name: "empty comment", body: "", command: "/lgtm", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := feedback.HasCommand(tc.body, tc.command); got != tc.want {
				t.Fatalf("HasCommand(%q, %q)=%v want %v", tc.body, tc.command, got, tc.want)
			}
		})
	}
}

func TestApproves(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{name: "approval alone", body: "/lgtm", want: true},
		{name: "approval with a remark", body: "/lgtm\nacceptable", want: true},
		{name: "approval after a remark", body: "Acceptable for now.\n/lgtm", want: true},
		{name: "Prow's withdrawal is not an approval", body: "/lgtm cancel", want: false},
		{name: "withdrawal next to an approval is not an approval", body: "/lgtm\n/lgtm cancel", want: false},
		{name: "remark that starts with cancel is not a withdrawal word", body: "/lgtm cancelling my earlier objection", want: true},
		{name: "reassess is not an approval", body: "/reassess", want: false},
		{name: "plain remark", body: "looks good to me", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := feedback.Approves(tc.body); got != tc.want {
				t.Fatalf("Approves(%q)=%v want %v", tc.body, got, tc.want)
			}
		})
	}
}
