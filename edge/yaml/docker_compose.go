package yaml

import (
	"fmt"
	"strings"

	"github.com/portainer/agent"
	"github.com/portainer/agent/edge/aws"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edge"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type DockerComposeYaml struct {
	FileContent         string
	RegistryCredentials []edge.RegistryCredentials
	awsConfig           *agent.AWSConfig
}

type Compose struct {
	Version  string             `yaml:"version"`
	Services map[string]Service `yaml:"services"`
}

type Service struct {
	Image       string   `yaml:"image"`
	Labels      []string `yaml:"labels,omitempty"`
	Command     []string `yaml:"command,omitempty"`
	Environment []string `yaml:"environment,omitempty"`
	Volumes     []string `yaml:"volumes,omitempty"`
}

func NewDockerComposeYAML(fileContent string, credentials []edge.RegistryCredentials, config *agent.AWSConfig) *DockerComposeYaml {
	return &DockerComposeYaml{
		FileContent:         fileContent,
		RegistryCredentials: credentials,
		awsConfig:           config,
	}
}

func (y *DockerComposeYaml) AddCredentialsAsEnvForSpecificService(serviceName string) ([]portainer.Pair, string, error) {
	var compose Compose

	// Parse file content to the object with yaml struct
	err := yaml.Unmarshal([]byte(y.FileContent), &compose)
	if err != nil {
		return nil, "", errors.Wrap(err, "Error while unmarshalling the docker compose file content")
	}

	if !validateComposeFile(&compose, serviceName) {
		return nil, "", errors.New("Failed to validate the compose file content")
	}

	// Extract registry server url from compose object, the service existence is already validated
	service := compose.Services[serviceName]

	serverUrl, err := extractRegistryServerUrl(service.Image)
	if err != nil {
		return nil, "", err
	}

	envVars := make([]portainer.Pair, 0)
	if y.awsConfig != nil {
		log.Info().Msg("using local AWS config for credential lookup")

		// Exchange ECR credential with ECR certificate
		awsRegistryCredentials, err := aws.DoAWSIAMRolesAnywhereAuthAndGetECRCredentials(serverUrl, y.awsConfig)
		if err != nil {
			// It doesn't need to fallback the registry here, so it is unnecessary to check ErrNoCredential error
			return nil, "", err
		}

		log.Info().Str("registry_server_url", serverUrl).Msg("")

		// hardcode username for aws ecr registry
		// @https://docs.aws.amazon.com/cli/latest/reference/ecr/get-login-password.html#examples
		envVars = append(envVars, portainer.Pair{Name: "REGISTRY_USERNAME", Value: "AWS"})
		envVars = append(envVars, portainer.Pair{Name: "REGISTRY_PASSWORD", Value: awsRegistryCredentials.Secret})
	} else if len(y.RegistryCredentials) > 0 {
		log.Info().Msg("using private registry credential")
		for _, cred := range y.RegistryCredentials {
			if serverUrl != cred.ServerURL {
				continue
			}
			log.Info().Str("registry_server_url", cred.ServerURL).Msg("")
			envVars = append(envVars, portainer.Pair{Name: "REGISTRY_USERNAME", Value: cred.Username})
			envVars = append(envVars, portainer.Pair{Name: "REGISTRY_PASSWORD", Value: cred.Secret})

			break
		}
	}

	// These env vars will be interpolated by the compose library in the `ComposeDeployer`
	composeEnvVars := make(map[string]string)
	if len(envVars) > 0 {
		composeEnvVars["REGISTRY_USED"] = "1"
		for _, env := range envVars {
			composeEnvVars[env.Name] = fmt.Sprintf("${%s}", env.Name)
		}
	}

	updateComposeFile, err := updateServiceWithEnv(compose, serviceName, composeEnvVars)

	return envVars, updateComposeFile, err
}

func updateServiceWithEnv(compose Compose, serviceName string, envs map[string]string) (string, error) {
	log.Info().Int("number", len(envs)).Msg("environment variable")

	service, ok := compose.Services[serviceName]
	if !ok {
		return "", fmt.Errorf("failed to find the service: %s", serviceName)
	}

	if service.Environment == nil {
		service.Environment = make([]string, 0)
	}

	for k, v := range envs {
		service.Environment = append(service.Environment, fmt.Sprintf("%s=%s", k, v))
	}

	compose.Services[serviceName] = service

	// Marshal the Compose object into a byte slice.
	yamlBytes, err := yaml.Marshal(compose)
	if err != nil {
		log.Error().Msg("failed to encode compose to yaml file")

		return "", errors.Wrap(err, "failed to encode compose to yaml file")
	}

	return string(yamlBytes), nil
}

func validateComposeFile(compose *Compose, serviceName string) bool {
	if compose == nil || compose.Version == "" || len(compose.Services) == 0 {
		return false
	}

	_, ok := compose.Services[serviceName]

	return ok
}

func extractRegistryServerUrl(imageName string) (string, error) {
	if imageName == "" {
		return "", errors.New("No image name provided")
	}

	scheme := ""

	pos := strings.Index(imageName, "://")
	if pos != -1 {
		scheme = imageName[:pos+3]
		imageName = imageName[pos+3:]
	}

	parts := strings.Split(imageName, "/")
	registryURL := parts[0]

	if len(parts) > 2 || (len(parts) == 2 && strings.Contains(imageName, ".")) {
		if scheme != "" {
			registryURL = scheme + parts[0]
		}
	} else {
		// possible use cases can be
		// ubuntu:20.04
		// portainerci/portainer-ee:latest
		return "", nil
	}

	return registryURL, nil
}
