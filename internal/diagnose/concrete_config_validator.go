package diagnose

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/shirou/gopsutil/host"

	"github.com/newrelic/newrelic-cli/internal/credentials"
	"github.com/newrelic/newrelic-cli/internal/utils/validation"
	"github.com/newrelic/newrelic-client-go/newrelic"
	"github.com/newrelic/newrelic-client-go/pkg/apiaccess"
)

const (
	validationEventType = "NrIntegrationError"
)

type ConcreteConfigValidator struct {
	client *newrelic.NewRelic
	*validation.PollingNRQLValidator
}

type ValidationTracerEvent struct {
	EventType string `json:"eventType"`
	Hostname  string `json:"hostname"`
	Purpose   string `json:"purpose"`
	GUID      string `json:"guid"`
}

func NewConcreteConfigValidator(client *newrelic.NewRelic) *ConcreteConfigValidator {
	v := validation.NewPollingNRQLValidator(&client.Nrdb)
	v.MaxAttempts = 20

	return &ConcreteConfigValidator{
		client:               client,
		PollingNRQLValidator: v,
	}
}

func (c *ConcreteConfigValidator) ValidateConfig(ctx context.Context) error {
	defaultProfile := credentials.DefaultProfile()

	if err := c.validateKeys(defaultProfile); err != nil {
		return err
	}

	i, err := host.InfoWithContext(ctx)
	if err != nil {
		log.Error(err)
		return ErrDiscovery
	}

	evt := ValidationTracerEvent{
		EventType: validationEventType,
		Hostname:  i.Hostname,
		Purpose:   "New Relic CLI configuration validation",
		GUID:      uuid.NewString(),
	}

	if err = c.client.Events.CreateEvent(defaultProfile.AccountID, evt); err != nil {
		log.Error(err)
		return ErrPostEvent
	}

	query := fmt.Sprintf(`
	FROM %s
	SELECT count(*)
	WHERE hostname LIKE '%s%%'
	AND guid = '%s'
	SINCE 10 MINUTES AGO
	`, evt.EventType, evt.Hostname, evt.GUID)

	if _, err = c.Validate(ctx, query); err != nil {
		log.Error(err)
		err = ErrValidation
	}

	return err
}

func (c *ConcreteConfigValidator) validateKeys(profile *credentials.Profile) error {
	if err := c.validateLicenseKey(profile); err != nil {
		return err
	}

	if err := c.validateInsightsInsertKey(profile); err != nil {
		return err
	}

	return nil
}

func (c *ConcreteConfigValidator) validateInsightsInsertKey(profile *credentials.Profile) error {
	insightsInsertKeys, err := c.client.APIAccess.ListInsightsInsertKeys(profile.AccountID)
	if err != nil {
		log.Error(err)
		return ErrConnection
	}

	for _, k := range insightsInsertKeys {
		if k.Key == profile.InsightsInsertKey {
			return nil
		}
	}

	return ErrInsightsInsertKey
}

func (c *ConcreteConfigValidator) validateLicenseKey(profile *credentials.Profile) error {
	params := apiaccess.APIAccessKeySearchQuery{
		Scope: apiaccess.APIAccessKeySearchScope{
			AccountIDs: []int{profile.AccountID},
		},
		Types: []apiaccess.APIAccessKeyType{
			apiaccess.APIAccessKeyTypeTypes.INGEST,
		},
	}

	licenseKeys, err := c.client.APIAccess.SearchAPIAccessKeys(params)
	if err != nil {
		log.Error(err)
		return ErrConnection
	}

	for _, k := range licenseKeys {
		if k.Key == profile.LicenseKey {
			return nil
		}
	}

	return ErrLicenseKey
}