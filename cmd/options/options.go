package options

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-github/v80/github"

	"github.com/geoberle/kelos/internal/admission"
	merge "github.com/geoberle/kelos/internal/merge"
	"github.com/geoberle/kelos/internal/review"
)

type RawOptions struct {
	ConfigFile string
	TokenFile  string
}
type configuration struct {
	MergeEnabled    bool     `json:"mergeEnabled,omitempty"`
	Version         string   `json:"version"`
	Repositories    []string `json:"repositories"`
	PollInterval    string   `json:"pollInterval"`
	ControllerLogin string   `json:"controllerLogin,omitempty"`
}
type validatedOptions struct {
	config    configuration
	interval  time.Duration
	tokenFile string
}
type ValidatedOptions struct{ *validatedOptions }
type completedOptions struct {
	config   configuration
	interval time.Duration
	client   *github.Client
}
type Options struct{ *completedOptions }

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if len(o.ConfigFile) == 0 || len(o.TokenFile) == 0 {
		return nil, fmt.Errorf("config-file and token-file are required")
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
	interval, err := time.ParseDuration(config.PollInterval)
	if err != nil || interval < time.Second {
		return nil, fmt.Errorf("pollInterval must be at least 1s")
	}
	return &ValidatedOptions{&validatedOptions{config: config, interval: interval, tokenFile: o.TokenFile}}, nil
}
func (o *ValidatedOptions) Complete() (*Options, error) {
	token, err := os.ReadFile(o.tokenFile)
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(string(token))
	if len(value) == 0 {
		return nil, fmt.Errorf("empty token file")
	}
	client := github.NewClient(&http.Client{Timeout: 30 * time.Second}).WithAuthToken(value)
	return &Options{&completedOptions{config: o.config, interval: o.interval, client: client}}, nil
}
func (o *Options) Run(ctx context.Context, logger logr.Logger) error {
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()
	for {
		if err := admission.Poll(ctx, o.client, o.config.Repositories); err != nil {
			logger.Error(err, "Admission poll failed")
		}
		if len(o.config.ControllerLogin) > 0 {
			if err := review.Poll(ctx, o.client, o.config.Repositories, o.config.ControllerLogin); err != nil {
				logger.Error(err, "Review poll failed")
			}
		}
		if o.config.MergeEnabled {
			if err := merge.Poll(ctx, o.client, o.config.Repositories, o.config.ControllerLogin); err != nil {
				logger.Error(err, "Merge poll failed")
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
