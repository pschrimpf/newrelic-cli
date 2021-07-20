package validation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/newrelic/newrelic-cli/internal/install/ux"
	"github.com/newrelic/newrelic-cli/internal/utils"
)

// AgentValidator polls NRDB to assert data is being reported for the given query.
type AgentValidator struct {
	httpClient        utils.HTTPClientInterface
	validationURL     string
	MaxAttempts       int
	Interval          time.Duration
	ProgressIndicator ux.ProgressIndicator
}

// TODO: Rename this response per proper domain (e.g. AgentValidationResponse?)
type AgentSuccessResponse struct {
	GUID string `json:"guid"`
}

type AgentStatusResponse struct {
	Checks []AgentEndpoint   `json:"checks"`
	Config AgentStatusConfig `json:"config"`
}

type AgentEndpoint struct {
	Url       string `json:"url"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error"`
}

type AgentStatusConfig struct {
	ReachabilityTimeout string `json:"reachability_timeout"`
}

// NewAgentValidator returns a new instance of AgentValidator.
func NewAgentValidator(c utils.HTTPClientInterface, validationURL string) *AgentValidator {
	v := AgentValidator{
		MaxAttempts:       3,
		Interval:          5 * time.Second,
		ProgressIndicator: ux.NewSpinner(),
		httpClient:        utils.NewHTTPClient(),
		validationURL:     validationURL, // "https://af062943-dc76-45d1-8067-7849cbfe0d98.mock.pstmn.io/v1/status",
		// JUST IDEAS
		// validation: {
		// 	baseURL: "",
		// 	port: "",
		// 	endpoint: "",
		// }
	}

	return &v
}

// Validate
func (v *AgentValidator) Validate(ctx context.Context) (string, error) {
	return v.waitForData(ctx)
}

// TODO: Find repeated code from other `waitForData` methods and
// consider consolidation for better DRY practices.
func (v *AgentValidator) waitForData(ctx context.Context) (string, error) {
	count := 0
	ticker := time.NewTicker(v.Interval)
	defer ticker.Stop()

	progressMsg := "Checking for data in New Relic (this may take a few minutes)..."
	v.ProgressIndicator.Start(progressMsg)
	defer v.ProgressIndicator.Stop()

	for {
		if count == v.MaxAttempts {
			v.ProgressIndicator.Fail("")
			return "", fmt.Errorf("reached max validation attempts")
		}

		entityGUID, err := v.doValidate(ctx)
		if err != nil {
			v.ProgressIndicator.Fail("")
			return "", err
		}

		count++

		if entityGUID != "" {
			v.ProgressIndicator.Success("")
			return entityGUID, nil
		}

		select {
		case <-ticker.C:
			continue

		case <-ctx.Done():
			v.ProgressIndicator.Fail("")
			return "", fmt.Errorf("validation cancelled")
		}
	}
}

func (v *AgentValidator) doValidate(ctx context.Context) (string, error) {
	resp, err := v.httpClient.Get(ctx, v.validationURL)
	if err != nil {
		return "", err
	}

	log.Print("\n\n **************************** \n")
	log.Printf("\n doValidate:  %+v \n", resp)
	log.Print("\n **************************** \n\n")
	time.Sleep(3 * time.Second)

	response := AgentSuccessResponse{}
	err = json.Unmarshal(resp, &response)
	if err != nil {
		return "", err
	}

	return response.GUID, nil
}
