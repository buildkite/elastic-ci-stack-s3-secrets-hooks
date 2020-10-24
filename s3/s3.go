package s3

import (
	"errors"
)

type Client struct{}

func New() *Client {
	return &Client{}
}

func (c *Client) Get(bucket, key string) ([]byte, error) {
	return nil, errors.New("TODO")
}

func (c *Client) BucketExists(bucket string) (bool, error) {
	return false, errors.New("TODO")
}
