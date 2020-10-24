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
	}
	logbuf := &bytes.Buffer{}
	fakeAgent := &FakeAgent{t: t}

	conf := secrets.Config{
		Repo:     "git@github.com:buildkite/bash-example.git",
		Bucket:   "bkt",
		Prefix:   "pipeline",
		Logger:   log.New(logbuf, "", log.LstdFlags),
		Client:   &FakeClient{t: t, data: fakeData},
		SSHAgent: fakeAgent,
	}
	if err := secrets.Run(conf); err != nil {
		t.Error(err)
	}
	assertDeepEqual(t, []string{"pipeline key", "general key"}, fakeAgent.keys)
	t.Logf("log:\n%s", logbuf.String())
}

func TestNoneFound(t *testing.T) {
	fakeData := map[string]FakeObject{}
	logbuf := &bytes.Buffer{}
	fakeAgent := &FakeAgent{t: t, keys: []string{}}
	conf := secrets.Config{
		Repo:     "git@github.com:buildkite/bash-example.git",
		Bucket:   "bkt",
		Prefix:   "pipeline",
		Logger:   log.New(logbuf, "", log.LstdFlags),
		Client:   &FakeClient{t: t, data: fakeData},
		SSHAgent: fakeAgent,
	}
	if err := secrets.Run(conf); err != nil {
		t.Error(err)
	}
	assertDeepEqual(t, []string{}, fakeAgent.keys)
	expectedWarning := "+++ :warning: Failed to find an SSH key in secret bucket"
	if !strings.Contains(logbuf.String(), expectedWarning) {
		t.Error("expected warning about no SSH keys for git@... repo")
	}
	t.Logf("logbuf:\n%s", logbuf.String())
}

func assertDeepEqual(t *testing.T, expected, actual interface{}) {
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}
