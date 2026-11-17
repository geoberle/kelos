package feedback

import (
	"slices"
	"strings"
)

const (
	// ApproveCommand is the human approval of a pull request head.
	ApproveCommand = "/lgtm"
	// ReassessCommand requests another merge assessment.
	ReassessCommand = "/reassess"
	// RunCommand starts an implementation Task from an issue. Kelos reads it;
	// on a pull request it has no effect.
	RunCommand = "/kelos run"
)

// HasCommand reports whether a line of the comment starts with the command,
// as its first words. Text after the command, and on other lines, is a remark.
func HasCommand(body, command string) bool {
	want := strings.Fields(command)
	for _, line := range strings.Split(body, "\n") {
		if words := strings.Fields(line); len(words) >= len(want) && slices.Equal(words[:len(want)], want) {
			return true
		}
	}
	return false
}

// Approves reports whether the comment is a human approval. Prow withdraws an
// approval with "/lgtm cancel"; a comment with that line never approves.
func Approves(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if words := strings.Fields(line); len(words) > 1 && words[0] == ApproveCommand && words[1] == "cancel" {
			return false
		}
	}
	return HasCommand(body, ApproveCommand)
}
