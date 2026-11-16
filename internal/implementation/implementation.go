// Package implementation reports implementation Tasks that ended without
// completing their issue.
package implementation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v80/github"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/hearthctl/kelos-orchestrator/internal/admission"
)

type Config struct {
	// Namespace holds the Tasks that the implementation TaskSpawners create.
	Namespace string `json:"namespace"`
}

// Result is the final message an implementation agent must end with.
type Result struct {
	// State is "completed" or "stopped".
	State string `json:"state"`
	// PullRequest is the URL of the pull request of a completed issue.
	PullRequest string `json:"pullRequest"`
	// Reason says why a stopped Task could not complete the issue.
	Reason string `json:"reason"`
	// Unmet lists the acceptance criteria that are not met or not verified.
	Unmet []string `json:"unmet"`
	// Next says what a human must do before a retry can succeed.
	Next string `json:"next"`
}

const (
	reportMarker = "<!-- kelos-implementation-stopped/v1 "
	// reportedAnnotation marks a Task whose stop is already on its issue, so
	// finished Tasks cost no GitHub requests on later polls.
	reportedAnnotation = "orchestrator.kelos.dev/stop-reported"
	// captureWindow covers the time Kelos may still be reading the final
	// message from the Pod log after it has set the completion time.
	captureWindow = time.Minute
)

var tasks = schema.GroupVersionResource{Group: "kelos.dev", Version: "v1alpha2", Resource: "tasks"}

// Poll comments once on the issue of every finished Task that a TaskSpawner
// created from an issue and that did not complete it. It changes no labels and
// never creates or deletes Tasks.
func Poll(ctx context.Context, c *github.Client, k dynamic.Interface, repositories []string, config Config, controllerLogin string) error {
	list, err := k.Resource(tasks).Namespace(config.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "kelos.dev/taskspawner"})
	if err != nil {
		return err
	}
	var failures []error
	for i := range list.Items {
		task := &list.Items[i]
		repository, _, _ := unstructured.NestedString(task.Object, "spec", "upstreamRepo")
		annotations := task.GetAnnotations()
		if !slices.Contains(repositories, repository) || annotations["kelos.dev/source-kind"] != "issue" || annotations[reportedAnnotation] == "true" {
			continue
		}
		number, err := strconv.Atoi(annotations["kelos.dev/source-number"])
		if err != nil {
			continue
		}
		stop, stopped := outcome(task)
		if !stopped {
			continue
		}
		owner, repo, _ := strings.Cut(repository, "/")
		if err := report(ctx, c, k, config.Namespace, owner, repo, number, task, controllerLogin, stop); err != nil {
			failures = append(failures, fmt.Errorf("%s#%d: %w", repository, number, err))
		}
	}
	return errors.Join(failures...)
}

// outcome reads why a finished Task stopped. Kelos marks a Task Succeeded
// whenever the agent exits cleanly, so only the agent's Result tells whether
// the issue is complete; anything else is reported with what is known.
func outcome(task *unstructured.Unstructured) (Result, bool) {
	phase, _, _ := unstructured.NestedString(task.Object, "status", "phase")
	switch phase {
	case "Failed":
		message, _, _ := unstructured.NestedString(task.Object, "status", "message")
		return Result{Reason: "Kelos reported the Task as failed: " + message, Next: "Inspect the Task log, then retry."}, true
	case "Succeeded":
	default:
		return Result{}, false
	}
	completed, _, _ := unstructured.NestedString(task.Object, "status", "completionTime")
	if at, err := time.Parse(time.RFC3339, completed); err != nil || time.Since(at) < captureWindow {
		return Result{}, false
	}
	encoded, _, _ := unstructured.NestedString(task.Object, "status", "results", "response")
	raw, _ := base64.StdEncoding.DecodeString(encoded)
	var result Result
	if err := json.Unmarshal(raw, &result); err == nil {
		if result.State == "completed" && len(result.PullRequest) > 0 {
			return Result{}, false
		}
		if result.State == "stopped" && len(result.Reason) > 0 {
			return result, true
		}
	}
	return Result{Reason: "The Task ended without a readable final result. Its final message was:\n\n" + string(raw), Next: "Read the final message above, correct the issue or the branch, then retry."}, true
}

func report(ctx context.Context, c *github.Client, k dynamic.Interface, namespace, owner, repo string, number int, task *unstructured.Unstructured, controllerLogin string, stop Result) error {
	// A retry replaces the Task under the same name, so the UID identifies the attempt.
	marker := reportMarker + string(task.GetUID()) + " -->"
	found := false
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for !found {
		comments, response, err := c.Issues.ListComments(ctx, owner, repo, number, opts)
		if err != nil {
			return err
		}
		for _, comment := range comments {
			if comment.GetUser().GetLogin() == controllerLogin && strings.HasPrefix(comment.GetBody(), marker) {
				found = true
			}
		}
		if response.NextPage == 0 {
			break
		}
		opts.Page = response.NextPage
	}
	if !found {
		branch, _, _ := unstructured.NestedString(task.Object, "spec", "branch")
		body := marker + "\nKelos Task `" + task.GetName() + "` stopped without completing this issue.\n\n**Why it stopped**\n\n" + quote(stop.Reason) + "\n\n"
		if len(stop.Unmet) > 0 {
			criteria := make([]string, 0, len(stop.Unmet))
			for _, criterion := range stop.Unmet {
				criteria = append(criteria, "- "+strings.ReplaceAll(criterion, "\n", " "))
			}
			body += "**Unmet acceptance criteria**\n\n" + quote(strings.Join(criteria, "\n")) + "\n\n"
		}
		if len(stop.Next) > 0 {
			body += "**How to proceed**\n\n" + quote(stop.Next) + "\n\n"
		}
		body += "The work so far is on branch `" + branch + "`. After addressing the above, comment `" + admission.RunCommand + "` on this issue to retry. A retry deletes this Task and its log."
		if _, _, err := c.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{Body: github.Ptr(body)}); err != nil {
			return err
		}
	}
	patch := []byte(`{"metadata":{"annotations":{"` + reportedAnnotation + `":"true"}}}`)
	_, err := k.Resource(tasks).Namespace(namespace).Patch(ctx, task.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// quote renders agent or Kelos text as a blockquote. Kelos reads a line equal
// to the run command as a trigger from the controller, which would retry the
// Task forever; a quoted line never equals it. An HTML comment opener is
// defused so the text cannot hide the rest of the report.
func quote(text string) string {
	if runes := []rune(text); len(runes) > 4000 {
		text = string(runes[:4000]) + " …"
	}
	text = strings.ReplaceAll(text, "<!--", "&lt;!--")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight("> "+line, " ")
	}
	return strings.Join(lines, "\n")
}
