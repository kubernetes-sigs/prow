/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package providers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gocloud.dev/blob"
	"gocloud.dev/blob/s3blob"
)

const S3MinUploadPartSize = 5 * 1024 * 1024 // 5 MiB

// getS3Bucket opens a gocloud blob.Bucket based on given credentials in the format the
// struct S3Credentials defines (see documentation of GetBucket for an example)
func getS3Bucket(ctx context.Context, creds []byte, bucketName string) (*blob.Bucket, error) {
	s3Creds := &S3Credentials{}
	if err := json.Unmarshal(creds, s3Creds); err != nil {
		return nil, fmt.Errorf("error getting S3 credentials from JSON: %w", err)
	}
	client, err := newS3Client(ctx, s3Creds)
	if err != nil {
		return nil, fmt.Errorf("error creating s3 client: %w", err)
	}
	bkt, err := s3blob.OpenBucketV2(ctx, client, bucketName, nil)
	if err != nil {
		return nil, fmt.Errorf("error opening S3 bucket: %w", err)
	}
	return bkt, nil
}

func newS3Client(ctx context.Context, creds *S3Credentials) (*s3.Client, error) {
	var opts []func(*config.LoadOptions) error

	// Use the default credential chain if no credentials are specified
	if creds.AccessKey != "" && creds.SecretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds.AccessKey,
			creds.SecretKey,
			creds.SessionToken,
		)))
	}
	opts = append(opts,
		config.WithRegion(creds.Region),
	)

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("error loading AWS SDK config: %w", err)
	}

	s3Opts := []func(o *s3.Options){
		func(o *s3.Options) {
			o.UsePathStyle = creds.S3ForcePathStyle
			o.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(t *http.Transport) {
				if t.TLSClientConfig == nil {
					t.TLSClientConfig = &tls.Config{}
				}
				t.TLSClientConfig.InsecureSkipVerify = creds.Insecure
			})
		},
	}
	if creds.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = &creds.Endpoint
		})
	}

	return s3.NewFromConfig(cfg, s3Opts...), nil
}
