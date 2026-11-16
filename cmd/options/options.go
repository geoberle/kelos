package options

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/go-logr/logr"
	"github.com/google/go-github/v80/github"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/hearthctl/kelos-orchestrator/internal/admission"
	"github.com/hearthctl/kelos-orchestrator/internal/editor"
	"github.com/hearthctl/kelos-orchestrator/internal/implementation"
	merge "github.com/hearthctl/kelos-orchestrator/internal/merge"
	"github.com/hearthctl/kelos-orchestrator/internal/review"
)

// cacheLifetime is how long cached GitHub responses are kept before the cache
// is emptied; refilling it costs about one poll's worth of counted requests.
const cacheLifetime = 24 * time.Hour

type RawOptions struct {
	ConfigFile       string
	AppCredentialDir string
}
type configuration struct {
	Assessment      *merge.AssessmentConfig `json:"assessment,omitempty"`
	MergeEnabled    bool                    `json:"mergeEnabled,omitempty"`
	Editor          *editor.Config          `json:"editor,omitempty"`
	Implementation  *implementation.Config  `json:"implementation,omitempty"`
	Version         string                  `json:"version"`
	Repositories    []string                `json:"repositories"`
	PollInterval    string                  `json:"pollInterval"`
	ControllerLogin string                  `json:"controllerLogin,omitempty"`
}
type validatedOptions struct {
	config           configuration
	interval         time.Duration
	appCredentialDir string
}
type ValidatedOptions struct{ *validatedOptions }
type completedOptions struct {
	config     configuration
	interval   time.Duration
	client     *github.Client
	meter      *meter
	reviews    *review.Cache
	kubernetes dynamic.Interface
}
type Options struct{ *completedOptions }

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if len(o.ConfigFile) == 0 || len(o.AppCredentialDir) == 0 {
		return nil, fmt.Errorf("config-file and app-credential-dir are required")
	}
	data, err := os.ReadFile(o.ConfigFile)
	if err != nil {
		return nil, err
	}
	var config configuration
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}
	if config.Version != "v1" || len(config.Repositories) == 0 {
		return nil, fmt.Errorf("v1 configuration and nonempty repositories required")
	}
	pattern := regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	for _, repo := range config.Repositories {
		if !pattern.MatchString(repo) {
			return nil, fmt.Errorf("invalid repository %q", repo)
		}
	}
	if config.ControllerLogin == "geoberle" || config.ControllerLogin == "github-actions[bot]" {
		return nil, fmt.Errorf("controllerLogin must be a dedicated controller identity")
	}
	if config.MergeEnabled && len(config.ControllerLogin) == 0 {
		return nil, fmt.Errorf("mergeEnabled requires controllerLogin")
	}
	if config.Editor != nil && (len(config.Editor.Namespace) == 0 || len(config.Editor.AgentConfig) == 0 || len(config.Editor.Secret) == 0 || len(config.ControllerLogin) == 0) {
		return nil, fmt.Errorf("editor requires namespace, agentConfig, secret and controllerLogin")
	}
	if config.Assessment != nil && (len(config.Assessment.Namespace) == 0 || len(config.Assessment.AgentConfig) == 0 || len(config.Assessment.Secret) == 0 || len(config.ControllerLogin) == 0) {
		return nil, fmt.Errorf("assessment requires namespace, agentConfig, secret and controllerLogin")
	}
	if config.Implementation != nil && (len(config.Implementation.Namespace) == 0 || len(config.ControllerLogin) == 0) {
		return nil, fmt.Errorf("implementation requires namespace and controllerLogin")
	}
	// A Task started in another repository's Workspace would edit the wrong clone.
	for _, repo := range config.Repositories {
		if config.Editor != nil && len(config.Editor.Workspaces[repo]) == 0 {
			return nil, fmt.Errorf("editor requires a workspace for %s", repo)
		}
		if config.Assessment != nil && len(config.Assessment.Workspaces[repo]) == 0 {
			return nil, fmt.Errorf("assessment requires a read-only workspace for %s", repo)
		}
	}
	interval, err := time.ParseDuration(config.PollInterval)
	if err != nil || interval < time.Second {
		return nil, fmt.Errorf("pollInterval must be at least 1s")
	}
	return &ValidatedOptions{&validatedOptions{config: config, interval: interval, appCredentialDir: o.AppCredentialDir}}, nil
}
func (o *ValidatedOptions) Complete() (*Options, error) {
	ids := map[string]int64{}
	for _, name := range []string{"appID", "installationID"} {
		value, err := os.ReadFile(filepath.Join(o.appCredentialDir, name))
		if err != nil {
			return nil, err
		}
		id, err := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a number: %w", name, err)
		}
		ids[name] = id
	}
	// The transport mints installation tokens and renews them before expiry.
	transport, err := ghinstallation.NewKeyFromFile(http.DefaultTransport, ids["appID"], ids["installationID"], filepath.Join(o.appCredentialDir, "privateKey"))
	if err != nil {
		return nil, err
	}
	httpClient, meter := newHTTPClient(transport)
	client := github.NewClient(httpClient)
	var kube dynamic.Interface
	if o.config.Editor != nil || o.config.Assessment != nil || o.config.Implementation != nil {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
		kube, err = dynamic.NewForConfig(config)
		if err != nil {
			return nil, err
		}
	}
	return &Options{&completedOptions{config: o.config, interval: o.interval, client: client, meter: meter, reviews: review.NewCache(), kubernetes: kube}}, nil
}
func (o *Options) Run(ctx context.Context, logger logr.Logger) error {
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()
	// Admission posts the run command only for TaskSpawners that require it,
	// which is the setup implementation reporting assumes.
	runCommandAuthor := ""
	if o.config.Implementation != nil {
		runCommandAuthor = o.config.ControllerLogin
	}
	forgotten := time.Now()
	for {
		if time.Since(forgotten) >= cacheLifetime {
			o.meter.forget()
			forgotten = time.Now()
		}
		o.reviews.NextPoll()
		if err := admission.Poll(ctx, o.client, o.config.Repositories, runCommandAuthor); err != nil {
			logger.Error(err, "Admission poll failed")
		}
		if o.config.Implementation != nil {
			if err := implementation.Poll(ctx, o.client, o.kubernetes, o.config.Repositories, *o.config.Implementation, o.config.ControllerLogin); err != nil {
				logger.Error(err, "Implementation poll failed")
			}
		}
		if len(o.config.ControllerLogin) > 0 {
			if err := review.Poll(ctx, o.client, o.reviews, o.config.Repositories, o.config.ControllerLogin); err != nil {
				logger.Error(err, "Review poll failed")
			}
		}
		if o.config.Editor != nil {
			if err := editor.Poll(ctx, o.client, o.reviews, o.kubernetes, o.config.Repositories, *o.config.Editor, o.config.ControllerLogin); err != nil {
				logger.Error(err, "Editor poll failed")
			}
		}
		if o.config.Assessment != nil {
			if err := merge.Assess(ctx, o.client, o.reviews, o.kubernetes, o.config.Repositories, *o.config.Assessment, o.config.ControllerLogin); err != nil {
				logger.Error(err, "Assessment poll failed")
			}
		}
		if o.config.MergeEnabled {
			if err := merge.Poll(ctx, o.client, o.reviews, o.config.Repositories, o.config.ControllerLogin); err != nil {
				logger.Error(err, "Merge poll failed")
			}
		}
		requests, notModified, remaining := o.meter.reset()
		logger.Info("Poll finished", "githubRequests", requests, "notModified", notModified, "rateLimitRemaining", remaining)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
