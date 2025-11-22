package secrets_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/secrets"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/sentinel"
)

type FakeClient struct {
	t      *testing.T
	data   map[string]FakeObject
	bucket string
}

type FakeObject struct {
	data []byte
	err  error
}

func (c *FakeClient) Get(key string) ([]byte, error) {
	time.Sleep(time.Duration(rand.Int()%100) * time.Millisecond)
	path := c.bucket + "/" + key
	if result, ok := c.data[path]; ok {
		c.t.Logf("FakeClient Get %s: %d bytes, error: %v", path, len(result.data), result.err)
		return result.data, result.err
	}
	c.t.Logf("FakeClient Get %s: Not Found", path)
	return nil, sentinel.ErrNotFound
}

func (c *FakeClient) BucketExists() (bool, error) {
	return true, nil
}

func (c *FakeClient) Bucket() string {
	return c.bucket
}

func (c *FakeClient) ListSuffix(prefix string, suffix []string) ([]string, error) {
	var matches []string

	for file := range c.data {
		if !strings.HasPrefix(file, c.bucket+"/"+prefix) {
			continue
		}
		key := strings.TrimPrefix(file, c.bucket+"/")
		for _, s := range suffix {
			if strings.HasSuffix(key, s) {
				matches = append(matches, key)
				break
			}
		}
	}

	// Sort results to ensure deterministic output
	sort.Strings(matches)
	return matches, nil
}

func (c *FakeClient) Region() string {
	return "us-west-2"
}

type FakeAgent struct {
	t    *testing.T
	keys []string
	run  bool
}

func (a *FakeAgent) Run() (bool, error) {
	if a.run {
		return false, nil
	}
	a.run = true
	return true, nil
}

func (a *FakeAgent) Add(key []byte) error {
	if !a.run {
		return errors.New("Agent must Run() before Add()")
	}
	a.t.Logf("FakeAgent Add (%d bytes)", len(key))
	a.keys = append(a.keys, string(key))
	return nil
}

func (a *FakeAgent) Pid() int {
	return 42
}

func (a *FakeAgent) Stdout() io.Reader {
	if len(a.keys) == 0 {
		return strings.NewReader("")
	}
	return strings.NewReader(`SSH_AUTH_SOCK=/path/to/socket; export SSH_AUTH_SOCK;
SSH_AGENT_PID=42; export SSH_AGENT_PID;
echo Agent pid 42
`)
}

type FakeBuildkiteAgent struct {
	RedactedSecrets []string
}

func (b *FakeBuildkiteAgent) Version() string {
	return "3.73.0"
}

func (b *FakeBuildkiteAgent) SupportsRedactor() bool {
	return true
}

func (b *FakeBuildkiteAgent) RedactorAddSecretsFromJSON(filepath string) error {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return err
	}

	var jsonSecrets map[string]string
	if err := json.Unmarshal(data, &jsonSecrets); err != nil {
		return err
	}

	// Extract all secret values from the JSON map
	for _, secret := range jsonSecrets {
		b.RedactedSecrets = append(b.RedactedSecrets, secret)
	}

	return nil
}

func TestRun(t *testing.T) {
	fakeData := map[string]FakeObject{
		"bkt/pipeline/private_ssh_key": {nil, sentinel.ErrNotFound},
		"bkt/pipeline/id_rsa_github":   {[]byte("pipeline key"), nil},
		"bkt/private_ssh_key":          {[]byte("general key"), nil},
		"bkt/id_rsa_github":            {nil, sentinel.ErrForbidden},

		"bkt/env":                  {[]byte("A=one\nB=two"), nil},
		"bkt/environment":          {nil, sentinel.ErrForbidden},
		"bkt/pipeline/env":         {nil, sentinel.ErrNotFound},
		"bkt/pipeline/environment": {[]byte("C=three"), nil},

		"bkt/git-credentials":          {[]byte("general git key"), nil},
		"bkt/pipeline/git-credentials": {[]byte("pipeline git key"), nil},

		"bkt/pipeline/secret-files/BUILDKITE_ACCESS_KEY":    {[]byte("buildkite access key"), nil},
		"bkt/pipeline/secret-files/DATABASE_SECRET":         {[]byte("database secret"), nil},
		"bkt/pipeline/secret-files/EXTERNAL_API_SECRET_KEY": {[]byte("external api secret"), nil},
		"bkt/pipeline/secret-files/PRIVILEGED_PASSWORD":     {[]byte("privileged password"), nil},
		"bkt/pipeline/secret-files/SERVICE_TOKEN":           {[]byte("service token"), nil},
		"bkt/secret-files/ORG_SERVICE_TOKEN":                {[]byte("org service token"), nil},
	}
	logbuf := &bytes.Buffer{}
	fakeAgent := &FakeAgent{t: t}
	fakeBuildkiteAgent := &FakeBuildkiteAgent{}
	envSink := &bytes.Buffer{}

	conf := secrets.Config{
		Repo:                "git@github.com:buildkite/bash-example.git",
		Bucket:              "bkt",
		Prefix:              "pipeline",
		Client:              &FakeClient{t: t, data: fakeData, bucket: "bkt"},
		Logger:              log.New(logbuf, "", log.LstdFlags),
		SSHAgent:            fakeAgent,
		BuildkiteAgent:      fakeBuildkiteAgent,
		EnvSink:             envSink,
		GitCredentialHelper: "/path/to/git-credential-s3-secrets",
	}
	if err := secrets.Run(&conf); err != nil {
		t.Error(err)
	}

	// verify ssh-agent
	assertDeepEqual(t, []string{"pipeline key", "general key"}, fakeAgent.keys)

	// verify env
	gitCredentialHelpers := strings.Join([]string{
		`'credential.helper=/path/to/git-credential-s3-secrets bkt us-west-2 git-credentials'`,
		`'credential.helper=/path/to/git-credential-s3-secrets bkt us-west-2 pipeline/git-credentials'`,
	}, " ")
	expected := strings.Join([]string{
		// because an SSH key was found, ssh-agent was started:
		"SSH_AUTH_SOCK=/path/to/socket; export SSH_AUTH_SOCK;",
		"SSH_AGENT_PID=42; export SSH_AGENT_PID;",
		"echo Agent pid 42",
		// combined env files:
		"A=one",
		"B=two",
		"C=three",
		// because git-credentials were found:
		// (wrap in double quotes so that bash eval doesn't consume the inner single quote.
		`GIT_CONFIG_PARAMETERS="` + gitCredentialHelpers + `"`,
		`ORG_SERVICE_TOKEN="org service token"`,
		`BUILDKITE_ACCESS_KEY="buildkite access key"`,
		`DATABASE_SECRET="database secret"`,
		`EXTERNAL_API_SECRET_KEY="external api secret"`,
		`PRIVILEGED_PASSWORD="privileged password"`,
		`SERVICE_TOKEN="service token"`,
	}, "\n") + "\n"

	if actual := envSink.String(); expected != actual {
		t.Errorf("unexpected env written:\n-%q\n+%q", expected, actual)
	}
	t.Logf("hook log:\n%s", logbuf.String())
}

func TestNoneFound(t *testing.T) {
	fakeData := map[string]FakeObject{}
	logbuf := &bytes.Buffer{}
	fakeAgent := &FakeAgent{t: t, keys: []string{}}
	fakeBuildkiteAgent := &FakeBuildkiteAgent{}
	envSink := &bytes.Buffer{}

	conf := secrets.Config{
		Repo:           "git@github.com:buildkite/bash-example.git",
		Bucket:         "bkt",
		Prefix:         "pipeline",
		Logger:         log.New(logbuf, "", log.LstdFlags),
		Client:         &FakeClient{t: t, data: fakeData},
		SSHAgent:       fakeAgent,
		BuildkiteAgent: fakeBuildkiteAgent,
		EnvSink:        envSink,
	}
	if err := secrets.Run(&conf); err != nil {
		t.Error(err)
	}
	assertDeepEqual(t, []string{}, fakeAgent.keys)
	if envSink.Len() != 0 {
		t.Errorf("expected envSink to be empty, got %q", envSink.String())
	}
	expectedWarning := "+++ :warning: Failed to find an SSH key in secret bucket"
	if !strings.Contains(logbuf.String(), expectedWarning) {
		t.Error("expected warning about no SSH keys for git@... repo")
	}
	t.Logf("hook log:\n%s", logbuf.String())
}

func TestNoneFoundWithDisabledWarning(t *testing.T) {
	fakeData := map[string]FakeObject{}
	logbuf := &bytes.Buffer{}
	fakeAgent := &FakeAgent{t: t, keys: []string{}}
	fakeBuildkiteAgent := &FakeBuildkiteAgent{}
	envSink := &bytes.Buffer{}

	conf := secrets.Config{
		Repo:                      "git@github.com:buildkite/bash-example.git",
		Bucket:                    "bkt",
		Prefix:                    "pipeline",
		Logger:                    log.New(logbuf, "", log.LstdFlags),
		Client:                    &FakeClient{t: t, data: fakeData},
		SSHAgent:                  fakeAgent,
		BuildkiteAgent:            fakeBuildkiteAgent,
		EnvSink:                   envSink,
		SkipSSHKeyNotFoundWarning: true,
	}
	if err := secrets.Run(&conf); err != nil {
		t.Error(err)
	}
	assertDeepEqual(t, []string{}, fakeAgent.keys)
	if envSink.Len() != 0 {
		t.Errorf("expected envSink to be empty, got %q", envSink.String())
	}
	unexpectedWarning := "+++ :warning: Failed to find an SSH key in secret bucket"
	if strings.Contains(logbuf.String(), unexpectedWarning) {
		t.Error("unexpected warning about no SSH keys for git@... repo")
	}
	t.Logf("hook log:\n%s", logbuf.String())
}

func assertDeepEqual(t *testing.T, expected, actual interface{}) {
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

// TestSecretRedactionFromEnvFile tests that secrets from env/environment files
// are correctly identified for redaction based on their variable name suffix and length.
func TestSecretRedactionFromEnvFile(t *testing.T) {
	tests := []struct {
		name            string
		envFileKey      string
		envContent      string
		shouldRedact    []string // values that should be in secretsToRedact
		shouldNotRedact []string // values that should NOT be in secretsToRedact
	}{
		{
			name:            "root env file with valid secret suffixes",
			envFileKey:      "env",
			envContent:      "MY_PASSWORD=secret123\nAPI_SECRET=secret456\nAUTH_TOKEN=token789\nAWS_ACCESS_KEY=akia-key123\nDB_SECRET_KEY=dbseckey456",
			shouldRedact:    []string{"secret123", "secret456", "token789", "akia-key123", "dbseckey456"},
			shouldNotRedact: []string{},
		},
		{
			name:            "root env file with false positives in var name",
			envFileKey:      "env",
			envContent:      "DISABLE_PASSWORD_PROMPT=true\nENABLE_SECRETS=only-on-ci\nDISABLE_TOKEN_AUTH=always-disable\nNO_ACCESS_KEY_NEEDED=1",
			shouldRedact:    []string{},
			shouldNotRedact: []string{"true", "only-on-ci", "always-disable", "1"},
		},
		{
			name:            "root env file with false positives in value min length",
			envFileKey:      "env",
			envContent:      "DISABLE_PASSWORD=1\nENABLE_SECRET=true\nACCESS_KEY=no\nGITHUB_TOKEN=none",
			shouldRedact:    []string{},
			shouldNotRedact: []string{"1", "true", "no", "none"},
		},
		{
			name:            "root env file mixed case",
			envFileKey:      "env",
			envContent:      "MY_PASSWORD=secret123\nDISABLE_PASSWORD_PROMPT=only-on-ci\nAPI_SECRET=secret456\nBUCKET_FOR_SECRETS=bucket-for-secrets\nHIDE_PASSWORD=true",
			shouldRedact:    []string{"secret123", "secret456"},
			shouldNotRedact: []string{"only-on-ci", "s3://bucket-for-secrets", "true"},
		},
		{
			name:            "pipeline environment file with valid secret suffixes",
			envFileKey:      "pipeline/environment",
			envContent:      "DB_PASSWORD=dbpass123\nSERVICE_SECRET=svcsecret\nAUTH_TOKEN=authtoken",
			shouldRedact:    []string{"dbpass123", "svcsecret", "authtoken"},
			shouldNotRedact: []string{},
		},
		{
			name:            "pipeline environment file with false positives in var name",
			envFileKey:      "pipeline/environment",
			envContent:      "DISABLE_PASSWORD_PROMPT=only-on-ci\nBUCKET_FOR_SECRETS=bucket-for-secrets",
			shouldRedact:    []string{},
			shouldNotRedact: []string{"only-on-ci", "bucket-for-secrets"},
		},
		{
			name:            "pipeline environment file with false positives in value min length",
			envFileKey:      "pipeline/environment",
			envContent:      "DISABLE_PASSWORD=1\nENABLE_SECRET=true\nACCESS_KEY=no\nGITHUB_TOKEN=none",
			shouldRedact:    []string{},
			shouldNotRedact: []string{"1", "true", "no", "none"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeData := map[string]FakeObject{
				"bkt/" + tt.envFileKey: {[]byte(tt.envContent), nil},
			}
			logbuf := &bytes.Buffer{}
			fakeAgent := &FakeAgent{t: t}
			fakeBuildkiteAgent := &FakeBuildkiteAgent{}
			envSink := &bytes.Buffer{}

			conf := secrets.Config{
				Repo:           "git@github.com:buildkite/bash-example.git",
				Bucket:         "bkt",
				Prefix:         "pipeline",
				Client:         &FakeClient{t: t, data: fakeData, bucket: "bkt"},
				Logger:         log.New(logbuf, "", log.LstdFlags),
				SSHAgent:       fakeAgent,
				BuildkiteAgent: fakeBuildkiteAgent,
				EnvSink:        envSink,
			}

			if err := secrets.Run(&conf); err != nil {
				t.Fatalf("Run() error = %v", err)
			}

			redactedSecrets := fakeBuildkiteAgent.RedactedSecrets

			// Check that all expected secrets are redacted
			for _, expectedSecret := range tt.shouldRedact {
				if !slices.Contains(redactedSecrets, expectedSecret) {
					t.Errorf("Expected secret %q to be in secretsToRedact, but it wasn't. Got: %v", expectedSecret, redactedSecrets)
				}
			}

			// Check that false positives are NOT redacted
			for _, notSecret := range tt.shouldNotRedact {
				if slices.Contains(redactedSecrets, notSecret) {
					t.Errorf("Expected value %q NOT to be in secretsToRedact, but it was. Got: %v", notSecret, redactedSecrets)
				}
			}
		})
	}
}

// TestSecretRedactionFromSecretFiles tests that secrets from secret-files
// are correctly identified for redaction based on their filename suffix.
func TestSecretRedactionFromSecretFiles(t *testing.T) {
	tests := []struct {
		name          string
		secretFileKey string
		secretValue   string
		shouldRedact  bool
	}{
		{
			name:          "root secret-files with valid suffix _PASSWORD",
			secretFileKey: "secret-files/DATABASE_PASSWORD",
			secretValue:   "dbpassword123",
			shouldRedact:  true,
		},
		{
			name:          "root secret-files with value too short DISABLE_PASSWORD",
			secretFileKey: "secret-files/DISABLE_PASSWORD",
			secretValue:   "true",
			shouldRedact:  false, // value < MinSecretSize
		},
		{
			name:          "pipeline secret-files with valid suffix _TOKEN",
			secretFileKey: "pipeline/secret-files/SERVICE_TOKEN",
			secretValue:   "servicetoken123",
			shouldRedact:  true,
		},
		{
			name:          "root secret-files with value too short GITHUB_TOKEN",
			secretFileKey: "secret-files/GITHUB_TOKEN",
			secretValue:   "none",
			shouldRedact:  false, // value < MinSecretSize
		},
		{
			name:          "pipeline secret-files with valid suffix _ACCESS_KEY",
			secretFileKey: "pipeline/secret-files/AWS_ACCESS_KEY",
			secretValue:   "awsaccesskey123",
			shouldRedact:  true,
		},
		{
			name:          "root secret-files with valid suffix _SECRET",
			secretFileKey: "secret-files/API_SECRET",
			secretValue:   "apisecret123",
			shouldRedact:  true,
		},
		{
			name:          "pipeline secret-files with valid suffix _SECRET_KEY",
			secretFileKey: "pipeline/secret-files/DATABASE_SECRET_KEY",
			secretValue:   "dbsecretkey123",
			shouldRedact:  true,
		},
		{
			name:          "pipeline secret-files with false positive filename",
			secretFileKey: "pipeline/secret-files/DISABLE_PASSWORD_PROMPT",
			secretValue:   "true",
			shouldRedact:  false, // name not matching suffix _PASSWORD
		},

	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeData := map[string]FakeObject{
				"bkt/" + tt.secretFileKey: {[]byte(tt.secretValue), nil},
			}
			logbuf := &bytes.Buffer{}
			fakeAgent := &FakeAgent{t: t}
			fakeBuildkiteAgent := &FakeBuildkiteAgent{}
			envSink := &bytes.Buffer{}

			fakeClient := &FakeClient{
				t:      t,
				data:   fakeData,
				bucket: "bkt",
			}

			conf := secrets.Config{
				Repo:           "git@github.com:buildkite/bash-example.git",
				Bucket:         "bkt",
				Prefix:         "pipeline",
				Client:         fakeClient,
				Logger:         log.New(logbuf, "", log.LstdFlags),
				SSHAgent:       fakeAgent,
				BuildkiteAgent: fakeBuildkiteAgent,
				EnvSink:        envSink,
			}

			if err := secrets.Run(&conf); err != nil {
				t.Fatalf("Run() error = %v", err)
			}

			redactedSecrets := fakeBuildkiteAgent.RedactedSecrets

			if tt.shouldRedact {
				if !slices.Contains(redactedSecrets, tt.secretValue) {
					t.Errorf("Expected secret value %q to be in secretsToRedact, but it wasn't. Got: %v", tt.secretValue, redactedSecrets)
				}
			} else {
				if slices.Contains(redactedSecrets, tt.secretValue) {
					t.Errorf("Expected value %q NOT to be in secretsToRedact, but it was. Got: %v", tt.secretValue, redactedSecrets)
				}
			}
		})
	}
}

// TestSecretRedactionAllSources tests a comprehensive scenario with secrets
// from all four sources to ensure they're all handled correctly.
func TestSecretRedactionAllSources(t *testing.T) {
	fakeData := map[string]FakeObject{
		// Root env file - mix of valid secrets and false positives by env var name and false positive in value min length
		"bkt/env": {[]byte("ROOT_PASSWORD=rootpass\nNEW_PASSWORD_PROMPT=please-enter-a-new-password\nDISABLE_PASSWORD=1"), nil},
		// Pipeline env file - mix of valid secrets and false positives
		"bkt/pipeline/environment": {[]byte("PIPELINE_SECRET=pipesecret\nREDACT_SECRETS=only-on-ci"), nil},
		// Root secret-files - valid secret
		"bkt/secret-files/API_TOKEN": {[]byte("apitoken123"), nil},
		// Root secret-files - false positive in filename
		"bkt/secret-files/DISABLE_TOKEN_AUTH": {[]byte("always-disable"), nil},
		// Pipeline secret-files - valid secret
		"bkt/pipeline/secret-files/DB_PASSWORD": {[]byte("dbpass123"), nil},
		// Pipeline secret-files - valid file but value too short
		"bkt/pipeline/secret-files/ADMIN_PASSWORD": {[]byte("admin"), nil},
		// Pipeline secret-files - false positive in filename
		"bkt/pipeline/secret-files/ENABLE_SECRETS": {[]byte("always-enable"), nil},
	}

	logbuf := &bytes.Buffer{}
	fakeAgent := &FakeAgent{t: t}
	fakeBuildkiteAgent := &FakeBuildkiteAgent{}
	envSink := &bytes.Buffer{}

	fakeClient := &FakeClient{
		t:      t,
		data:   fakeData,
		bucket: "bkt",
	}

	conf := secrets.Config{
		Repo:           "git@github.com:buildkite/bash-example.git",
		Bucket:         "bkt",
		Prefix:         "pipeline",
		Client:         fakeClient,
		Logger:         log.New(logbuf, "", log.LstdFlags),
		SSHAgent:       fakeAgent,
		BuildkiteAgent: fakeBuildkiteAgent,
		EnvSink:        envSink,
	}

	if err := secrets.Run(&conf); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	redactedSecrets := fakeBuildkiteAgent.RedactedSecrets

	// Values that SHOULD be redacted (end with secret suffix)
	shouldRedact := []string{
		"rootpass",    // from ROOT_PASSWORD in bkt/env
		"pipesecret",  // from PIPELINE_SECRET in bkt/pipeline/environment
		"apitoken123", // from API_TOKEN in secret-files/
		"dbpass123",   // from DB_PASSWORD in pipeline/secret-files/
	}

	// Values that should NOT be redacted (suffix in middle of name)
	shouldNotRedact := []string{
		"please-enter-a-new-password", // from NEW_PASSWORD_PROMPT in bkt/env
		"only-on-ci",                  // from REDACT_SECRETS in bkt/pipeline/environment
		"always-disable",              // from DISABLE_TOKEN_AUTH in secret-files/
		"always-enable",               // from ENABLE_SECRETS in pipeline/secret-files/
		"1",                           // from DISABLE_PASSWORD in bkt/env (name match but value < MinSecretSize)
		"admin",                       // from ADMIN_PASSWORD in pipeline/secret-files/ (value < MinSecretSize)
	}

	// Verify all expected secrets are redacted
	for _, expectedSecret := range shouldRedact {
		if !slices.Contains(redactedSecrets, expectedSecret) {
			t.Errorf("Expected secret %q to be in secretsToRedact, but it wasn't. Got: %v", expectedSecret, redactedSecrets)
		}
	}

	// Verify false positives are NOT redacted
	for _, notSecret := range shouldNotRedact {
		if slices.Contains(redactedSecrets, notSecret) {
			t.Errorf("Expected value %q NOT to be in secretsToRedact, but it was. Got: %v", notSecret, redactedSecrets)
		}
	}
}
