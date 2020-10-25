package s3

import (
	"io/ioutil"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/buildkite/elastic-ci-stack-s3-secrets-hooks/sentinel"
)

type Client struct {
	s3 *s3.S3
}

func New(region string) (*Client, error) {
	sess, err := session.NewSession(&aws.Config{
		Region: &region,
	})
	if err != nil {
		return nil, err
	}
	return &Client{
		s3: s3.New(sess),
	}, nil
}

// Get downloads an object from S3.
// Intended for small files; object is fully read into memory.
// sentinel.ErrNotFound and sentinel.ErrForbidden are returned for those cases.
// Other errors are returned verbatim.
func (c *Client) Get(bucket, key string) ([]byte, error) {
	out, err := c.s3.GetObject(&s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case "NoSuchKey":
				return nil, sentinel.ErrNotFound
			case "Forbidden":
				return nil, sentinel.ErrForbidden
			default:
				return nil, aerr
			}
		} else {
			return nil, err
		}
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
func (c *Client) BucketExists(bucket string) (bool, error) {
	if _, err := c.s3.HeadBucket(&s3.HeadBucketInput{Bucket: &bucket}); err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			// https://github.com/aws/aws-sdk-go/issues/2593#issuecomment-491436818
			case "NoSuchBucket", "NotFound":
				return false, nil
			default: // e.g. NoCredentialProviders, Forbidden
				return false, aerr
			}
		} else {
			return false, err
		}
	}
	return true, nil
}
