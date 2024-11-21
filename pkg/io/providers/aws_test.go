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
	"testing"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
)

func Test_newS3Client(t *testing.T) {
	tests := []struct {
		name  string
		creds s3Credentials
	}{
		{
			name: "only accesskey and secretkey set",
			creds: s3Credentials{
				AccessKey: "foo",
				SecretKey: "bar",
			},
		},
		{
			name: "all options set ",
			creds: s3Credentials{
				AccessKey:        "foo",
				SecretKey:        "bar",
				Endpoint:         "https://foobar.com",
				Region:           "eu01",
				Insecure:         true,
				S3ForcePathStyle: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := newS3Client(ctx, &tt.creds)
			if err != nil {
				t.Errorf("newS3Client() error = %v,", err)
				return
			}
			s3opts := got.Options()
			awsCreds, err := s3opts.Credentials.Retrieve(ctx)
			if err != nil {
				t.Errorf("get aws credentials error = %v,", err)
				return
			}
			if awsCreds.AccessKeyID != tt.creds.AccessKey {
				t.Errorf("want AccessKey %s, got %s", tt.creds.AccessKey, awsCreds.AccessKeyID)
			}
			if awsCreds.SecretAccessKey != tt.creds.SecretKey {
				t.Errorf("want SecretKey %s, got %s", tt.creds.SecretKey, awsCreds.SecretAccessKey)
			}

			httpClient, ok := s3opts.HTTPClient.(*awshttp.BuildableClient)
			if !ok {
				t.Errorf("s3 HTTPClient is not a awshttp.BuildableClient, got %T", s3opts.HTTPClient)
				return
			}
			tlsConfig := httpClient.GetTransport().TLSClientConfig
			if tlsConfig == nil {
				t.Error("tlsConfig of s3 httpClient transport is nil")
				return
			}
			if tlsConfig.InsecureSkipVerify != tt.creds.Insecure {
				t.Errorf("want tlsConfig.InsecureSkipVerify %v, got %v", tt.creds.Insecure, tlsConfig.InsecureSkipVerify)
			}

			if s3opts.Region != tt.creds.Region {
				t.Errorf("want Region in s3 options %v, got %v", tt.creds.Region, s3opts.Region)
			}
			if s3opts.UsePathStyle != tt.creds.S3ForcePathStyle {
				t.Errorf("want UsePathStyle in s3 options %v, got %v", tt.creds.S3ForcePathStyle, s3opts.UsePathStyle)
			}
			if tt.creds.Endpoint != "" {
				if s3opts.BaseEndpoint == nil {
					t.Error("BaseEndpoint in s3 options should be set, got nil")
					return
				}
				if *s3opts.BaseEndpoint != tt.creds.Endpoint {
					t.Errorf("want BaseEndpoint in s3 options %s, got %s", tt.creds.Endpoint, *s3opts.BaseEndpoint)
				}
			}
		})
	}
}
