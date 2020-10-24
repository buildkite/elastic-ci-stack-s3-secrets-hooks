package secrets_test

import (
	"bytes"
	"errors"
	"log"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/secrets"
)

type FakeClient struct {
	t    *testing.T
	data map[string]FakeObject
}

type FakeObject struct {
	data []byte
	err  error
}

func (c *FakeClient) Get(bucket, key string) ([]byte, error) {
	time.Sleep(time.Duration(rand.Int()%100) * time.Millisecond)
	path := bucket + "/" + key
	if result, ok := c.data[path]; ok {
		c.t.Logf("FakeClient Get %s: %d bytes, error: %v", path, len(result.data), result.err)
		return result.data, result.err
	}
	c.t.Logf("FakeClient Get %s: Not Found", path)
	return nil, errors.New("Not Found") // TODO: error type
}

func (c *FakeClient) BucketExists(bucket string) (bool, error) {
	return true, nil
}

type FakeAgent struct {
	t    *testing.T
	keys []string
}

func (a *FakeAgent) Add(key []byte) error {
	a.t.Logf("FakeAgent Add (%d bytes)", len(key))
	a.keys = append(a.keys, string(key))
	return nil
}

func (a *FakeAgent) Pid() uint {
	return 42
}

func TestRun(t *testing.T) {
	fakeData := map[string]FakeObject{
		"bkt/pipeline/private_ssh_key": {nil, errors.New("NotFound")}, // TODO: error type
		"bkt/pipeline/id_rsa_github":   {[]byte("pipeline key"), nil},
		"bkt/private_ssh_key":          {[]byte("general key"), nil},
		"bkt/id_rsa_github":            {nil, errors.New("Forbidden")}, // TODO: error type

		"bkt/env":                  {[]byte("A=one\nB=two"), nil},
		"bkt/environment":          {nil, errors.New("Forbidden")}, // TODO: error type
		"bkt/pipeline/env":         {nil, errors.New("NotFound")},  // TODO: error type
		"bkt/pipeline/environment": {[]byte("C=three"), nil},

		"bkt/git-credentials":          {[]byte("general git key"), nil},
		"bkt/pipeline/git-credentials": {[]byte("pipeline git key"), nil},
	}
	logbuf := &bytes.Buffer{}
	fakeAgent := &FakeAgent{t: t}
	envSink := &bytes.Buffer{}

	conf := secrets.Config{
		Repo:                "git@github.com:buildkite/bash-example.git",
		Bucket:              "bkt",
		Prefix:              "pipeline",
		Logger:              log.New(logbuf, "", log.LstdFlags),
		Client:              &FakeClient{t: t, data: fakeData},
		SSHAgent:            fakeAgent,
		EnvSink:             envSink,
		GitCredentialHelper: "/path/to/git-credential-s3-secrets",
	}
	if err := secrets.Run(conf); err != nil {
		t.Error(err)
	}

	// verify ssh-agent
	assertDeepEqual(t, []string{"pipeline key", "general key"}, fakeAgent.keys)

	// verify env
	gitCredentialHelpers := strings.Join([]string{
		"'credential.helper=/path/to/git-credential-s3-secrets bkt git-credentials'",
		"'credential.helper=/path/to/git-credential-s3-secrets bkt pipeline/git-credentials'",
	}, " ") + "\n"
	expected := strings.Join([]string{
		"A=one",
		"B=two",
		"C=three",
		"GIT_CONFIG_PARAMETERS=" + gitCredentialHelpers,
	}, "\n")
	if actual := envSink.String(); expected != actual {
		t.Errorf("unexpected env written:\n-%q\n+%q", expected, actual)
	}
	t.Logf("hook log:\n%s", logbuf.String())
}

func TestNoneFound(t *testing.T) {
	fakeData := map[string]FakeObject{}
	logbuf := &bytes.Buffer{}
	fakeAgent := &FakeAgent{t: t, keys: []string{}}
	envSink := &bytes.Buffer{}

	conf := secrets.Config{
		Repo:     "git@github.com:buildkite/bash-example.git",
		Bucket:   "bkt",
		Prefix:   "pipeline",
		Logger:   log.New(logbuf, "", log.LstdFlags),
		Client:   &FakeClient{t: t, data: fakeData},
		SSHAgent: fakeAgent,
		EnvSink:  envSink,
	}
	if err := secrets.Run(conf); err != nil {
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

func assertDeepEqual(t *testing.T, expected, actual interface{}) {
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}
