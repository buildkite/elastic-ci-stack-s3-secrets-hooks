package s3_test

import (
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/awsdocs/aws-doc-sdk-examples/gov2/testtools"
	s3client "github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/s3"
)

func TestListSuffix(t *testing.T) {
	t.Parallel()

	suffixes := []string{
		"_ACCESS_KEY",
		"_SECRET",
		"_SECRET_KEY",
		"_PASSWORD",
		"_TOKEN",
	}

	stubber := testtools.NewStubber()
	stubListObjectsV2 := func(bucketName string, prefix string, keys []string, raiseErr *testtools.StubError) testtools.Stub {
		var objects []types.Object
		for _, key := range keys {
			objects = append(objects, types.Object{Key: aws.String(key)})
		}
		return testtools.Stub{
			OperationName: "ListObjectsV2",
			Input: &s3.ListObjectsV2Input{
				Bucket: aws.String(bucketName),
				Prefix: aws.String(prefix),
			},
			Output: &s3.ListObjectsV2Output{
				Contents: objects,
			},
			Error: raiseErr,
		}
	}

	t.Run("returns all files with the expected/valid suffixes found", func(t *testing.T) {
		client := s3client.NewFromConfig(*stubber.SdkConfig, "my-bucket")

		not_valid_secret_file := "my-pipeline/secret-files/SOME_OTHER_FILE"
		stubber.Add(stubListObjectsV2("my-bucket", "my-pipeline/secret-files",
			[]string{"my-pipeline/secret-files/BUILDKITE_ACCESS_KEY",
				"my-pipeline/secret-files/DATABASE_SECRET",
				"my-pipeline/secret-files/EXTERNAL_API_SECRET_KEY",
				"my-pipeline/secret-files/PRIVILEGED_PASSWORD",
				"my-pipeline/secret-files/SERVICE_TOKEN",
				not_valid_secret_file}, nil))

		prefix := "my-pipeline/secret-files"
		keys, err := client.ListSuffix(prefix, suffixes)

		if err != nil {
			t.Fatalf("expect no error, got %v", err)
		}

		if len(keys) != 5 {
			t.Fatalf("expect to return all 5 files with valid suffixes,  got %d", len(keys))
		}

		if slices.Contains(keys, not_valid_secret_file) {
			t.Fatalf("expect no file with no valid suffix returned, got %s", not_valid_secret_file)
		}

	})

	t.Run("returns an empty list when no files with valid suffixes are found", func(t *testing.T) {
		client := s3client.NewFromConfig(*stubber.SdkConfig, "my-bucket")

		stubber.Add(stubListObjectsV2("my-bucket", "my-pipeline/secret-files",
			[]string{"my-pipeline/secret-files/SOME_IRRELEVANT_FILE",
				"my-pipeline/secret-files/SOME_OTHER_FILE"}, nil))

		prefix := "my-pipeline/secret-files"
		keys, err := client.ListSuffix(prefix, suffixes)

		if err != nil {
			t.Fatalf("expect no error, got %v", err)
		}

		if len(keys) != 0 {
			t.Fatalf("expect 0 keys, got %d", len(keys))
		}
	})

}
