package s3

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/env"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/s3secrets-helper/v2/sentinel"
)

type Client struct {
	s3     *s3.Client
	bucket string
	region string
}

func getCurrentRegion(ctx context.Context) (string, error) {
	if region := os.Getenv("AWS_DEFAULT_REGION"); len(region) > 0 {
		return region, nil
	}

	imdsClient := imds.New(imds.Options{})
	if result, err := imdsClient.GetRegion(ctx, nil); err == nil {
		if len(result.Region) > 0 {
			return result.Region, nil
		}
	}

	return "", errors.New("Unknown current region")
}

func New(log *log.Logger, bucket string, regionHint string) (*Client, error) {
	ctx := context.Background()

	var awsConfig aws.Config
	var err error

	if regionHint != "" {
		// If there is a region hint provided, we use it unconditionally
		awsConfig, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(regionHint),
		)
		if err != nil {
			return nil, fmt.Errorf("Could not load the AWS SDK config (%v)", err)
		}
	} else {
		// Otherwise, use the current region (or a guess) to dynamically find
		// where the bucket lives.
		region, err := getCurrentRegion(ctx)
		if err != nil {
			// Ignore error and fallback to us-east-1 for bucket lookup
			region = "us-east-1"
		}

		awsConfig, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
		)
		if err != nil {
			return nil, fmt.Errorf("Could not load the AWS SDK config (%v)", err)
		}

		log.Printf("Discovered current region as %q\n", awsConfig.Region)

		bucketRegion, err := manager.GetBucketRegion(ctx, s3.NewFromConfig(awsConfig), bucket)
		if err == nil && bucketRegion != "" {
			log.Printf("Discovered bucket region as %q\n", bucketRegion)
			awsConfig.Region = bucketRegion
		} else {
			log.Printf("Could not discover region for bucket %q. Using the %q region as a fallback, if this is not correct configure a bucket region using the %q environment variable. (%v)\n", bucket, awsConfig.Region, env.EnvRegion, err)
		}
	}

	return &Client{
		s3:     s3.NewFromConfig(awsConfig),
		bucket: bucket,
		region: awsConfig.Region,
	}, nil
}

func (c *Client) Bucket() string {
	return c.bucket
}

func (c *Client) Region() string {
	return c.region
}

// Get downloads an object from S3.
// Intended for small files; object is fully read into memory.
// sentinel.ErrNotFound and sentinel.ErrForbidden are returned for those cases.
// Other errors are returned verbatim.
func (c *Client) Get(key string) ([]byte, error) {
	out, err := c.s3.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: &c.bucket,
		Key:    &key,
	})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, sentinel.ErrNotFound
		}

		// Possible values can be found at https://docs.aws.amazon.com/AmazonS3/latest/API/API_Error.html
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			code := apiErr.ErrorCode()
			if code == "AccessDenied" {
				return nil, sentinel.ErrForbidden
			}
		}

		return nil, fmt.Errorf("Could not GetObject (%s) in bucket (%s). Ensure your IAM Identity has s3:GetObject permission for this key and bucket. (%v)", key, c.bucket, err)
	}
	defer out.Body.Close()
	// we probably should return io.Reader or io.ReadCloser rather than []byte,
	// maybe somebody should refactor that (and all the tests etc) one day.
	return ioutil.ReadAll(out.Body)
}

// ListSuffix returns a list of keys in the bucket that have the given prefix and suffix.
// This has a maximum of 1000 keys, for now. This can be expanded by using the continuation token.
func (c *Client) ListSuffix(prefix string, suffixes []string) ([]string, error) {
	var resp *s3.ListObjectsV2Output
	var keys []string
	resp, err := c.s3.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
		Bucket: &c.bucket,
		Prefix: &prefix,
	})

	// Iterate over all objects at the prefix and find those who match our suffix
	for i, object := range resp.Contents {
		for _, suffix := range suffixes {
			if strings.HasSuffix(*object.Key, suffix) {
				if i < len(resp.Contents)-1 {
					keys = append(keys, *object.Key)
					resp.Contents = append(resp.Contents[:i], resp.Contents[i+1:]...) //since we have a match, pop it
					break                                                             //... then break out of the suffix loop
				} else {
					keys = append(keys, *object.Key) // No need to pop the object as we are at the end of the list
				} //... then break out of the suffix loop
			}
		}
	}

	if err != nil {
		return nil, fmt.Errorf("Could not ListObjectsV2 (%s) in bucket (%s). Ensure your IAM Identity has s3:ListBucket permission for this bucket. (%v)", prefix, c.bucket, err)
	}
	return keys, nil
}

// BucketExists returns whether the bucket exists.
// 200 OK returns true without error.
// 404 Not Found and 403 Forbidden return false without error.
// Other errors result in false with an error.
func (c *Client) BucketExists() (bool, error) {
	if _, err := c.s3.HeadBucket(context.TODO(), &s3.HeadBucketInput{Bucket: &c.bucket}); err != nil {
		return false, fmt.Errorf("Could not HeadBucket (%s). Ensure your IAM Identity has s3:ListBucket permission for this bucket. (%v)", c.bucket, err)
	}
	return true, nil
}
