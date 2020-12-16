package install

import (
	"fmt"
	"net/url"

	"github.com/manifoldco/promptui"
	log "github.com/sirupsen/logrus"

	"github.com/newrelic/newrelic-cli/internal/utils"
)

type recipeInstaller struct {
	installContext
	discoverer        discoverer
	fileFilterer      fileFilterer
	recipeFetcher     recipeFetcher
	recipeExecutor    recipeExecutor
	recipeValidator   recipeValidator
	recipeFileFetcher recipeFileFetcher
}

func newRecipeInstaller(
	ic installContext,
	d discoverer,
	l fileFilterer,
	f recipeFetcher,
	e recipeExecutor,
	v recipeValidator,
	ff recipeFileFetcher,
) *recipeInstaller {
	i := recipeInstaller{
		discoverer:        d,
		fileFilterer:      l,
		recipeFetcher:     f,
		recipeExecutor:    e,
		recipeValidator:   v,
		recipeFileFetcher: ff,
	}

	i.specifyActions = ic.specifyActions
	i.interactiveMode = ic.interactiveMode
	i.installLogging = ic.installLogging
	i.installInfraAgent = ic.installInfraAgent
	i.recipeNames = ic.recipeNames
	i.recipePaths = ic.recipePaths

	return &i
}

const (
	infraAgentRecipeName = "Infrastructure Agent Installer"
	loggingRecipeName    = "Logs integration"
)

func (i *recipeInstaller) install() {
	log.Infoln("Welcome to New Relic. Let's install some instrumentation.")
	log.Infoln("Questions? Read more about our installation process at https://docs.newrelic.com/install-newrelic.")

	// Execute the discovery process, exiting on failure.
	m := i.discoverFatal()

	// Run the infra agent recipe, exiting on failure.
	if i.ShouldInstallInfraAgent() {
		i.installInfraAgentFatal(m)
	}

	// Retrieve a list of recipes to execute.
	var recipes []recipe
	if i.RecipePathsProvided() {
		for _, n := range i.recipePaths {
			recipes = append(recipes, *i.recipeFromPathFatal(n))
		}
	} else if i.RecipeNamesProvided() {
		// Execute the requested recipes.
		for _, n := range i.recipeNames {
			r := i.fetchWarn(m, n)
			recipes = append(recipes, *r)
		}
	} else {
		// Ask the recipe service for recommendations.
		recipes = i.fetchRecommendationsFatal(m)
	}

	// Run the logging recipe if requested, exiting on failure.
	if i.ShouldInstallLogging() {
		i.installLoggingFatal(m, recipes)
	}

	// Execute and validate each of the recipes in the collection.
	for _, r := range recipes {
		i.executeAndValidateWarn(m, &r)
	}

	log.Infoln("Success! Your data is available in New Relic.")
	log.Infoln("Go to New Relic to confirm and start exploring your data.")
}

func (i *recipeInstaller) discoverFatal() *discoveryManifest {
	m, err := i.discoverer.discover(utils.SignalCtx)
	if err != nil {
		log.Fatalf("Could not install New Relic.  There was an error discovering system info: %s", err)
	}

	return m
}

func (i *recipeInstaller) recipeFromPathFatal(recipePath string) *recipe {
	recipeURL, parseErr := url.Parse(recipePath)
	if parseErr == nil && recipeURL.Scheme != "" {
		f, err := i.recipeFileFetcher.fetchRecipeFile(recipeURL)
		if err != nil {
			log.Fatalf("Could not fetch file %s: %s", recipePath, err)
		}
		return finalizeRecipe(f)
	}

	f, err := i.recipeFileFetcher.loadRecipeFile(recipePath)
	if err != nil {
		log.Fatalf("Could not load file %s: %s", recipePath, err)
	}
	return finalizeRecipe(f)
}

func finalizeRecipe(f *recipeFile) *recipe {
	r, err := f.ToRecipe()
	if err != nil {
		log.Fatalf("Could finalize recipe %s: %s", f.Name, err)
	}
	return r
}

func (i *recipeInstaller) installInfraAgentFatal(m *discoveryManifest) {
	i.fetchExecuteAndValidateFatal(m, infraAgentRecipeName)
}

func (i *recipeInstaller) installLoggingFatal(m *discoveryManifest, recipes []recipe) {
	r := i.fetchFatal(m, loggingRecipeName)

	logMatches, err := i.fileFilterer.filter(utils.SignalCtx, recipes)
	if err != nil {
		log.Fatal(err)
	}

	var acceptedLogMatches []logMatch
	for _, match := range logMatches {
		if userAcceptLogFile(match) {
			acceptedLogMatches = append(acceptedLogMatches, match)
		}
	}

	// The struct to approximate the logging configuration file of the Infra Agent.
	type loggingConfig struct {
		Logs []logMatch `yaml:"logs"`
	}

	r.AddVar("DISCOVERED_LOG_FILES", loggingConfig{Logs: acceptedLogMatches})

	i.executeAndValidateFatal(m, r)
}

func (i *recipeInstaller) fetchRecommendationsFatal(m *discoveryManifest) []recipe {
	recipes, err := i.recipeFetcher.fetchRecommendations(utils.SignalCtx, m)
	if err != nil {
		log.Fatalf("Could not install New Relic. Error retrieving recipe recommendations: %s", err)
	}

	return recipes
}

func (i *recipeInstaller) fetchExecuteAndValidateFatal(m *discoveryManifest, recipeName string) {
	r := i.fetchFatal(m, recipeName)
	i.executeAndValidateFatal(m, r)
}

func (i *recipeInstaller) fetchWarn(m *discoveryManifest, recipeName string) *recipe {
	r, err := i.recipeFetcher.fetchRecipe(utils.SignalCtx, m, recipeName)
	if err != nil {
		log.Warnf("Could not install %s. Error retrieving recipe: %s", recipeName, err)
		return nil
	}

	if r == nil {
		log.Warnf("Recipe %s not found. Skipping installation.", recipeName)
	}

	return r
}

func (i *recipeInstaller) fetchFatal(m *discoveryManifest, recipeName string) *recipe {
	r, err := i.recipeFetcher.fetchRecipe(utils.SignalCtx, m, recipeName)
	if err != nil {
		log.Fatalf("Could not install %s. Error retrieving recipe: %s", recipeName, err)
	}

	if r == nil {
		log.Fatalf("Recipe %s not found.", recipeName)
	}

	return r
}

func (i *recipeInstaller) executeAndValidate(m *discoveryManifest, r *recipe) (bool, error) {
	// Execute the recipe steps.
	log.Infof("Installing %s...\n", r.Name)
	if err := i.recipeExecutor.execute(utils.SignalCtx, *m, *r); err != nil {
		return false, fmt.Errorf("encountered an error while executing %s: %s", r.Name, err)
	}
	log.Infof("Installing %s...success\n", r.Name)

	if r.ValidationNRQL != "" {
		log.Info("Listening for data...")
		ok, err := i.recipeValidator.validate(utils.SignalCtx, *m, *r)
		if err != nil {
			return false, fmt.Errorf("encountered an error while validating receipt of data for %s: %s", r.Name, err)
		}

		if !ok {
			log.Infoln("failed.")
			return false, nil
		}
	} else {
		log.Warnf("unable to validate using empty recipe ValidationNRQL")
	}

	log.Infoln("success.")
	return true, nil
}

func (i *recipeInstaller) executeAndValidateFatal(m *discoveryManifest, r *recipe) {
	ok, err := i.executeAndValidate(m, r)
	if err != nil {
		log.Fatalf("Could not install %s: %s", r.Name, err)
	}

	if !ok {
		log.Fatalf("Could not detect data from %s.", r.Name)
	}
}

func (i *recipeInstaller) executeAndValidateWarn(m *discoveryManifest, r *recipe) {
	ok, err := i.executeAndValidate(m, r)
	if err != nil {
		log.Warnf("Could not install %s: %s", r.Name, err)
	}

	if !ok {
		log.Warnf("Could not detect data from %s.", r.Name)
	}
}

func userAcceptLogFile(match logMatch) bool {
	msg := fmt.Sprintf("Files have been found at the following pattern: %s\nDo you want to watch them? [Yes/No]", match.File)

	prompt := promptui.Select{
		Label: msg,
		Items: []string{"Yes", "No"},
	}

	_, result, err := prompt.Run()
	if err != nil {
		log.Errorf("prompt failed: %s", err)
		return false
	}

	return result == "Yes"
}