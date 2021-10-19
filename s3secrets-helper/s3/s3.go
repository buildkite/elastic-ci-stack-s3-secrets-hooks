package s3

import (
	"context"
	"errors"
	"log"
	"io/ioutil"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/sentinel"
)

type Client struct {
	s3 *s3.Client
	bucket string
}

func New(log *log.Logger, bucket string) (*Client, error) {
	ctx := context.Background()

	// Using the current region (or a guess) find where the bucket lives
	managerCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(os.Getenv("AWS_DEFAULT_REGION")),
		config.WithEC2IMDSRegion(),
		config.WithDefaultRegion("us-east-1"),
	)
	if err != nil {
		return nil, err
	}

	log.Printf("Discovered current region as %q\n", managerCfg.Region)

	managerClient := s3.NewFromConfig(managerCfg)
	bucketRegion, err := manager.GetBucketRegion(ctx, managerClient, bucket)
	if err != nil {
		return nil, err
	}

	log.Printf("Discovered bucket region as %q\n", bucketRegion)

	s3Cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(bucketRegion))
	if err != nil {
		return nil, err
	}

	return &Client{
		s3: s3.NewFromConfig(s3Cfg),
		bucket: bucket,
	}, nil
}

func (c *Client) Bucket() (string) {
	return c.bucket
}

// Get downloads an object from S3.
// Intended for small files; object is fully read into memory.
// sentinel.ErrNotFound and sentinel.ErrForbidden are returned for those cases.
// Other errors are returned verbatim.
func (c *Client) Get(key string) ([]byte, error) {
	out, err := c.s3.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: &c.bucket,
		Key:    &key,
	})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, sentinel.ErrNotFound
		}

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			code := apiErr.ErrorCode()
			// TODO confirm "Forbidden" is a member of the set of values this can return
			if code == "Forbidden" {
				return nil, sentinel.ErrForbidden
			}
		}

		return nil, err
	}
	defer out.Body.Close()
	// we probably should return io.Reader or io.ReadCloser rather than []byte,
	// maybe somebody should refactor that (and all the tests etc) one day.
	return ioutil.ReadAll(out.Body)
}

// BucketExists returns whether the bucket exists.
// 200 OK returns true without error.
// 404 Not Found and 403 Forbidden return false without error.
// Other errors result in false with an error.
func (c *Client) BucketExists() (bool, error) {
	if _, err := c.s3.HeadBucket(context.Background(), &s3.HeadBucketInput{Bucket: &c.bucket}); err != nil {
		return false, err
	}
	return true, nil
}
